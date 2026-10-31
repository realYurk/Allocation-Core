package studio

// Mapping profiles describe business-column names, not financial values.
// They are private local runtime data and intentionally independent of a
// project/template name so that a compatible second scenario can reuse them.
import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const mappingProfileSchema = "allocation-field-map/v1"
const maxMappingProfileBytes = 16 << 10

type mappingProfile struct {
	Schema       string            `json:"schema"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"` // amount or driver
	Dimensions   []string          `json:"dimensions"`
	Precision    int               `json:"precision"` // amount only; driver weight scale is fixed by the importer
	WeightFields []string          `json:"weight_fields,omitempty"`
	Columns      map[string]string `json:"columns"` // canonical -> source CSV column
}

func cleanProfileFields(fields []string, max int) error {
	if len(fields) == 0 || len(fields) > max {
		return errors.New("invalid field contract length")
	}
	used := map[string]bool{}
	for _, v := range fields {
		if v == "" || len(v) > 128 || strings.TrimSpace(v) != v || strings.ContainsAny(v, "\x00\r\n") || used[v] {
			return fmt.Errorf("empty, repeated or unsafe contract field %q", v)
		}
		used[v] = true
	}
	return nil
}

func validateMappingProfile(p mappingProfile) error {
	if p.Schema != mappingProfileSchema || !multiSafe.MatchString(p.Name) {
		return errors.New("invalid profile schema or name")
	}
	if err := cleanProfileFields(p.Dimensions, 30); err != nil {
		return err
	}
	if len(p.Columns) == 0 || len(p.Columns) > 64 {
		return errors.New("profile must have 1..64 explicit field mappings")
	}
	sourceUsed := map[string]bool{}
	for canonical, source := range p.Columns {
		if canonical == "" || len(canonical) > 128 || source == "" || len(source) > 256 || strings.TrimSpace(source) != source || strings.ContainsAny(source, "\x00\r\n") || sourceUsed[source] {
			return fmt.Errorf("in