package studio

// Business-first project setup: editable, schema-only visual drafts and
// streamed, explicit CSV-to-Driver column mapping. Neither path changes the
// allocation engine, financial inputs, assignments, or published results.
import (
	"allocationcore/internal/engine"
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const maxBusinessDriverCSV int64 = 256 << 20
const visualDraftMarker = "multi-schema-draft/v1"

type visualDraftMetadata struct {
	Kind           string   `json:"kind"`
	Project        string   `json:"project"`
	Template       string   `json:"template"`
	TemplateSHA256 string   `json:"template_sha256"`
	Dimensions     []string `json:"dimensions"`
	Precision      int      `json:"precision"`
}

type driverNeed struct {
	Alias        string   `json:"alias"`
	CurrentFile  string   `json:"current_file"`
	WeightFields []string `json:"weight_fields"`
}

type driverRequirements struct {
	Project    string       `json:"project"`
	Template   string       `json:"template"`
	SHA256     string       `json:"sha256"`
	Dimensions []string     `json:"dimensions"`
	Drivers    []driverNeed `json:"drivers"`
}

func readProjectTemplate(dir, template string) ([]byte, engine.Spec, error) {
	var spec engine.Spec
	if !validMultiTemplateName(template) {
		return nil, spec, errors.New("invalid template filename")
	}
	info, err := os.Lstat(filepath.Join(dir, "templates", template))
	if err != nil || !info.Mode().IsRegular() || info.Size() > 480<<10 {
		return nil, spec, errors.New("template is absent, not regular or too large")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "templates", template))
	if err != nil {
		return nil, spec, err
	}
	if spec, err = engine.ParseSpec(raw); err != nil {
		return nil, spec, err
	}
	if _, err = engine.Compile(spec); err != nil {
		return nil, spec, err
	}
	// The imported visual rule must fit every account in the project.
	if _, _, err = importDesignerRule(raw, d