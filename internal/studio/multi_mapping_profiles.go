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
			return fmt.Errorf("invalid or ambiguous field mapping: %q -> %q", canonical, source)
		}
		sourceUsed[source] = true
	}
	switch p.Kind {
	case "amount":
		if p.Precision < 0 || p.Precision > 6 || len(p.WeightFields) != 0 {
			return errors.New("amount profile precision must be 0..6; no driver fields allowed")
		}
		for _, dim := range p.Dimensions {
			if dim == "amount" || dim == "row_id" {
				return errors.New("reserved business dimension")
			}
		}
		_, err := checkAmountMapping(p.Columns, p.Dimensions)
		return err
	case "driver":
		if p.Precision != 0 {
			return errors.New("driver profile precision must be 0; weight precision comes from the importer")
		}
		if err := cleanProfileFields(p.WeightFields, 32); err != nil {
			return err
		}
		allowed := map[string]bool{}
		for _, dim := range p.Dimensions {
			allowed[dim] = true
		}
		for _, field := range p.WeightFields {
			if allowed[field] {
				return errors.New("a weight field cannot be a dimension")
			}
			allowed[field] = true
			if p.Columns[field] == "" {
				return fmt.Errorf("missing driver weight mapping for %s", field)
			}
		}
		dimMapped := false
		for key := range p.Columns {
			if !allowed[key] {
				return fmt.Errorf("unknown driver field %s", key)
			}
			for _, dim := range p.Dimensions {
				if dim == key {
					dimMapped = true
				}
			}
		}
		if !dimMapped {
			return errors.New("at least one Driver business dimension must be mapped")
		}
		return nil
	default:
		return errors.New("profile kind must be amount or driver")
	}
}

func (x *MultiApp) mappingProfileDir() (string, error) {
	dir := filepath.Join(filepath.Dir(x.root), "mapping-profiles")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("unsafe mapping profiles directory")
	}
	return dir, nil
}

func (x *MultiApp) mappingProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	dir, err := x.mappingProfileDir()
	if err != nil {
		fail(w, 500, err)
		return
	}
	if r.Method == http.MethodGet {
		items, err := os.ReadDir(dir)
		if err != nil {
			fail(w, 500, err)
			return
		}
		profiles := []mappingProfile{}
		for _, item := range items {
			if !validMultiTemplateName(item.Name()) {
				continue
			}
			path := filepath.Join(dir, item.Name())
			st, err := os.Lstat(path)
			if err != nil || !st.Mode().IsRegular() || st.Size() > maxMappingProfileBytes {
				fail(w, 500, errors.New("invalid mapping profile on disk"))
				return
			}
			data, err := os.ReadFile(path)
			if err != nil {
				fail(w, 500, err)
				return
			}
			var p mappingProfile
			if err = json.Unmarshal(data, &p); err != nil {
				fail(w, 500, err)
				return
			}
			if err = validateMappingProfile(p); err != nil || p.Name+".json" != item.Name() {
				fail(w, 500, errors.New("stored mapping profile is invalid"))
				return
			}
			profiles = append(profiles, p)
		}
		sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
		jsonOut(w, 200, map[string]any{"profiles": profiles})
		return
	}
	var req mappingProfile
	if err := multiRequest(w, r, &req, maxMappingProfileBytes); err != nil {
		fail(w, 400, err)
		return
	}
	if err := validateMappingProfile(req); err != nil {
		fail(w, 422, err)
		return
	}
	// The profile does not contain source rows, project paths or credentials.
	encoded, err := json.MarshalIndent(req, "", "  ")
	if err != nil || len(encoded) > maxMappingProfileBytes {
		fail(w, 422, errors.New("mapping profile is too large"))
		return
	}
	dest := filepath.Join(dir, req.Name+".json")
	if st, err := os.Lstat(dest); err == nil || (err != nil && !os.IsNotExist(err)) {
		if st != nil {
			fail(w, 409, errors.New("mapping profile already exists; select a new name"))
		} else {
			fail(w, 500, err)
		}
		return
	}
	tmp, err := os.CreateTemp(dir, ".mapping-stage-")
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(encoded)
	if err == nil {
		err = tmp.Sync()
	}
	if e := tmp.Close(); err == nil {
		err = e
	}
	if err != nil {
		fail(w, 500, err)
		return
	}
	// Atomic no-replace publication, including across different Studio processes.
	if err = os.Link(tmp.Name(), dest); err != nil {
		if _, stErr := os.Lstat(dest); stErr == nil {
			fail(w, 409, errors.New("mapping profile was created concurrently"))
			return
		}
		fail(w, 500, err)
		return
	}
	jsonOut(w, http.StatusCreated, map[string]any{"saved": true, "profile": req})
}
