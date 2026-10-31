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
	if _, _, err = importDesignerRule(raw, dir, nil); err != nil {
		return nil, spec, err
	}
	for _, file := range spec.Datasets.Drivers {
		if !multiCSV.MatchString(file) || file == "amounts.csv" {
			return nil, spec, errors.New("driver filename must be a simple project-local CSV, distinct from amounts.csv")
		}
	}
	return raw, spec, nil
}

func ruleDriverRequirements(spec engine.Spec) []driverNeed {
	byAlias := map[string]map[string]bool{}
	for alias := range spec.Datasets.Drivers {
		byAlias[alias] = map[string]bool{}
	}
	for _, stage := range spec.Stages {
		if !engine.Enabled(stage.Enabled) || (stage.Operation != "" && stage.Operation != "ALLOCATE") {
			continue
		}
		for _, alloc := range stage.Allocations {
			if !engine.Enabled(alloc.Enabled) {
				continue
			}
			if _, ok := byAlias[alloc.DriverDataset]; ok {
				byAlias[alloc.DriverDataset][alloc.WeightField] = true
			}
		}
	}
	aliases := make([]string, 0, len(byAlias))
	for alias := range byAlias {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	out := make([]driverNeed, 0, len(aliases))
	for _, alias := range aliases {
		fields := make([]string, 0, len(byAlias[alias]))
		for field := range byAlias[alias] {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		out = append(out, driverNeed{Alias: alias, CurrentFile: spec.Datasets.Drivers[alias], WeightFields: fields})
	}
	return out
}

func draftHeaderFile(path string, columns []string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	err = w.Write(columns)
	w.Flush()
	if err == nil {
		err = w.Error()
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// This flow intentionally copies ZERO amount/driver rows. A draft is a
// structural design surface, not a financial sample or a standalone run.
func (x *MultiApp) createVisualDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Name     string `json:"name"`
		Template string `json:"template"`
		Draft    string `json:"draft"`
	}
	if err := multiRequest(w, r, &req, 4096); err != nil {
		fail(w, 400, err)
		return
	}
	if !multiSafe.MatchString(req.Draft) {
		fail(w, 422, errors.New("draft name must be 1..48 ASCII letters, numbers, _ or -"))
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.busy[req.Name] {
		fail(w, 409, errors.New("project is calculating; cannot create an editor draft"))
		return
	}
	projectDir, err := x.projectDir(req.Name)
	if err != nil {
		fail(w, 404, err)
		return
	}
	if x.parent == nil || x.parent.custom == "" {
		fail(w, 500, errors.New("visual rule designer is not configured"))
		return
	}
	raw, spec, err := readProjectTemplate(projectDir, req.Template)
	if err != nil {
		fail(w, 422, err)
		return
	}
	x.parent.editMu.Lock()
	defer x.parent.editMu.Unlock()
	dest := filepath.Join(x.parent.custom, req.Draft)
	if _, err = os.Lstat(dest); err == nil {
		fail(w, 409, errors.New("visual draft already exists; never overwrite"))
		return
	} else if !os.IsNotExist(err) {
		fail(w, 500, err)
		return
	}
	stage, err := os.MkdirTemp(x.parent.custom, ".visual-draft-")
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer os.RemoveAll(stage)
	// Header-only files let the existing designer read its data contract without
	// touching or copying a single private financial data row.
	if err = draftHeaderFile(filepath.Join(stage, "amounts.csv"), append(append([]string{}, spec.Dimensions...), "amount")); err != nil {
		fail(w, 500, err)
		return
	}
	requirements := ruleDriverRequirements(spec)
	byFile := map[string]map[string]bool{}
	for _, need := range requirements {
		fields := byFile[need.CurrentFile]
		if fields == nil {
			fields = map[string]bool{}
			byFile[need.CurrentFile] = fields
		}
		for _, field := range need.WeightFields {
			fields[field] = true
		}
	}
	for filename, weightFields := range byFile {
		columns := append([]string{}, spec.Dimensions...)
		for field := range weightFields {
			found := false
			for _, dim := range columns {
				if dim == field {
					found = true
					break
				}
			}
			if !found {
				columns = append(columns, field)
			}
		}
		// Preserve the original Driver header when it is readable, so the
		// designer shows actual column names, not inferred business values.
		existing := filepath.Join(projectDir, "drivers", filename)
		if fi, e := os.Lstat(existing); e == nil && fi.Mode().IsRegular() {
			if file, e := os.Open(existing); e == nil {
				header, readErr := csv.NewReader(bufio.NewReaderSize(file, 64<<10)).Read()
				_ = file.Close()
				if readErr == nil && len(header) > 0 {
					columns = header
					columns[0] = strings.TrimPrefix(columns[0], "\ufeff")
				}
			}
		}
		if err = draftHeaderFile(filepath.Join(stage, filename), columns); err != nil {
			fail(w, 500, err)
			return
		}
	}
	// No rewrite of the financial semantics; only redirect dataset basenames
	// into the private schema-only designer case.
	for alias := range spec.Datasets.Amounts {
		spec.Datasets.Amounts[alias] = "amounts.csv"
	}
	saved, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		fail(w, 500, err)
		return
	}
	if err = os.WriteFile(filepath.Join(stage, "rule.json"), saved, 0600); err != nil {
		fail(w, 500, err)
		return
	}
	hash := sha256.Sum256(raw)
	meta := visualDraftMetadata{Kind: visualDraftMarker, Project: req.Name, Template: req.Template, TemplateSHA256: hex.EncodeToString(hash[:]), Dimensions: spec.Dimensions, Precision: spec.Precision}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		fail(w, 500, err)
		return
	}
	if err = os.WriteFile(filepath.Join(stage, "editor_draft.json"), data, 0600); err != nil {
		fail(w, 500, err)
		return
	}
	if err = os.Rename(stage, dest); err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 201, map[string]any{"case": "user_" + req.Draft, "source_template": req.Template,
		"dimensions": spec.Dimensions, "precision": spec.Precision, "stages": len(spec.Stages),
		"note": "STRUCTURE_ONLY: the editor has headers but no financial data rows. Saving validates the rule graph, not actual Driver coverage or allocation results. Import as a NEW multi template, Inspect, Run and reconcile."})
}

func readVisualDraft(dir string) (visualDraftMetadata, bool, error) {
	var meta visualDraftMetadata
	info, err := os.Lstat(filepath.Join(dir, "editor_draft.json"))
	if os.IsNotExist(err) {
		return meta, false, nil
	}
	if err != nil {
		return meta, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8192 {
		return meta, false, errors.New("invalid draft metadata")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "editor_draft.json"))
	if err != nil {
		return meta, false, err
	}
	if err = json.Unmarshal(raw, &meta); err != nil {
		return meta, false, err
	}
	if meta.Kind != visualDraftMarker || len(meta.Dimensions) == 0 || meta.Precision < 0 || meta.Precision > 6 {
		return meta, false, errors.New("unrecognized rule draft metadata")
	}
	return meta, true, nil
}

func validateStructuralDraft(raw []byte, meta visualDraftMetadata) error {
	spec, err := engine.ParseSpec(raw)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(spec.Dimensions, meta.Dimensions) || spec.Precision != meta.Precision {
		return errors.New("draft cannot alter the source business project's dimension order or amount precision")
	}
	if _, err := engine.Compile(spec); err != nil {
		return err
	}
	haveAccount := false
	for _, dim := range spec.Immutable {
		if dim == "account" {
			haveAccount = true
		}
	}
	if !haveAccount {
		return errors.New("account must remain an immutable dimension")
	}
	if len(spec.Datasets.Amounts) != 1 {
		return errors.New("multi template requires exactly one amount dataset")
	}
	for _, stage := range spec.Stages {
		conserved := false
		for _, key := range stage.ConservationKeys {
			if key == "account" {
				conserved = true
			}
		}
		if !conserved || stage.Source.Grouping["account"] != "AS_IS" || stage.Target["account"].Mode != "SOURCE" {
			return fmt.Errorf("stage %s cannot combine or reassign accounts", stage.ID)
		}
	}
	for _, group := range []map[string]string{spec.Datasets.Amounts, spec.Datasets.Drivers} {
		for _, file := range group {
			if !multiCSV.MatchString(file) {
				return fmt.Errorf("invalid schema-only dataset file %q", file)
			}
		}
	}
	return nil
}

func (x *MultiApp) driverNeeds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	dir, err := x.projectDir(r.URL.Query().Get("name"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	template := r.URL.Query().Get("template")
	raw, spec, err := readProjectTemplate(dir, template)
	if err != nil {
		fail(w, 422, err)
		return
	}
	sum := sha256.Sum256(raw)
	jsonOut(w, 200, driverRequirements{Project: r.URL.Query().Get("name"), Template: template, SHA256: hex.EncodeToString(sum[:]), Dimensions: spec.Dimensions, Drivers: ruleDriverRequirements(spec)})
}

func (x *MultiApp) importMappedDriver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBusinessDriverCSV+(32<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, err)
		return
	}
	// Strict field sequence keeps multipart handling bounded and predictable.
	form := map[string]string{}
	for _, field := range []string{"name", "template", "alias", "file", "base_sha256", "mapping"} {
		part, e := reader.NextPart()
		if e != nil {
			fail(w, 400, fmt.Errorf("missing field %s: %w", field, e))
			return
		}
		if part.FormName() != field || part.FileName() != "" {
			fail(w, 400, fmt.Errorf("unexpected field, expected %s", field))
			return
		}
		raw, e := io.ReadAll(io.LimitReader(part, 8193))
		_ = part.Close()
		if e != nil || len(raw) > 8192 {
			fail(w, 422, fmt.Errorf("field %s too long", field))
			return
		}
		form[field] = string(raw)
	}
	if !multiCSV.MatchString(form["file"]) || form["file"] == "amounts.csv" {
		fail(w, 422, errors.New("new driver must have a simple .csv filename and cannot be amounts.csv"))
		return
	}
	dir, err := x.projectDir(form["name"])
	if err != nil {
		fail(w, 404, err)
		return
	}
	raw, spec, err := readProjectTemplate(dir, form["template"])
	if err != nil {
		fail(w, 422, err)
		return
	}
	digest := sha256.Sum256(raw)
	if form["base_sha256"] != hex.EncodeToString(digest[:]) {
		fail(w, 409, errors.New("business rule changed since mapping preview; refresh and map again"))
		return
	}
	var mapping map[string]string
	if err = json.Unmarshal([]byte(form["mapping"]), &mapping); err != nil {
		fail(w, 422, err)
		return
	}
	needs := ruleDriverRequirements(spec)
	var selected *driverNeed
	for i := range needs {
		if needs[i].Alias == form["alias"] {
			selected = &needs[i]
			break
		}
	}
	if selected == nil || len(selected.WeightFields) == 0 {
		fail(w, 422, errors.New("selected template does not use that Driver alias"))
		return
	}
	desired := map[string]bool{}
	for _, dim := range spec.Dimensions {
		desired[dim] = true
	}
	for _, field := range selected.WeightFields {
		desired[field] = true
	}
	positions := map[string]string{}
	dimCount := 0
	sourceSeen := map[string]bool{}
	for field, source := range mapping {
		if !desired[field] || source == "" || sourceSeen[source] {
			fail(w, 422, fmt.Errorf("unexpected or ambiguous column mapping: %s -> %s", field, source))
			return
		}
		positions[field] = source
		sourceSeen[source] = true
		for _, dim := range spec.Dimensions {
			if dim == field {
				dimCount++
				break
			}
		}
	}
	if dimCount == 0 {
		fail(w, 422, errors.New("map at least one business dimension in Driver CSV"))
		return
	}
	for _, field := range selected.WeightFields {
		if positions[field] == "" {
			fail(w, 422, fmt.Errorf("missing weight-field mapping %s", field))
			return
		}
	}
	// The raw uploaded file is never published or retained, even on failure.
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "csv" || part.FileName() == "" || !strings.EqualFold(filepath.Ext(part.FileName()), ".csv") {
		fail(w, 400, errors.New("expected the source .csv upload"))
		return
	}
	outputNames := make([]string, 0, len(positions))
	for _, dim := range spec.Dimensions {
		if positions[dim] != "" {
			outputNames = append(outputNames, dim)
		}
	}
	for _, field := range selected.WeightFields {
		found := false
		for _, v := range outputNames {
			if v == field {
				found = true
				break
			}
		}
		if !found {
			outputNames = append(outputNames, field)
		}
	}
	csvReader := csv.NewReader(bufio.NewReaderSize(part, 256<<10))
	csvReader.FieldsPerRecord = -1
	header, err := csvReader.Read()
	if err != nil {
		fail(w, 422, fmt.Errorf("source CSV header: %w", err))
		return
	}
	sourceIndex := map[string]int{}
	for i, name := range header {
		if i == 0 {
			name = strings.TrimPrefix(name, "\ufeff")
		}
		if name == "" {
			fail(w, 422, errors.New("source header has blank column"))
			return
		}
		if _, ok := sourceIndex[name]; ok {
			fail(w, 422, fmt.Errorf("duplicate source column %q", name))
			return
		}
		sourceIndex[name] = i
	}
	for _, field := range outputNames {
		if _, ok := sourceIndex[positions[field]]; !ok {
			fail(w, 422, fmt.Errorf("source CSV missing mapped column %q for %q", positions[field], field))
			return
		}
	}
	driverDir := filepath.Join(dir, "drivers")
	fi, err := os.Lstat(driverDir)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		fail(w, 422, errors.New("drivers directory is invalid"))
		return
	}
	tmp, err := os.CreateTemp(driverDir, ".driver-import-")
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer os.Remove(tmp.Name())
	out := csv.NewWriter(tmp)
	err = out.Write(outputNames)
	var rows int64
	for err == nil {
		if e := r.Context().Err(); e != nil {
			err = e
			break
		}
		record, e := csvReader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			err = fmt.Errorf("source row %d: %w", rows+2, e)
			break
		}
		if len(record) != len(header) {
			err = fmt.Errorf("source row %d: columns %d expected %d", rows+2, len(record), len(header))
			break
		}
		normalized := make([]string, 0, len(outputNames))
		for _, field := range outputNames {
			value := strings.TrimSpace(record[sourceIndex[positions[field]]])
			if value == "" {
				err = fmt.Errorf("source row %d: %s is blank", rows+2, field)
				break
			}
			for _, weight := range selected.WeightFields {
				if field == weight {
					if _, e := engine.ParseScaled(value, 6); e != nil {
						err = fmt.Errorf("source row %d: weight %s: %w", rows+2, field, e)
					}
				}
			}
			if err != nil {
				break
			}
			normalized = append(normalized, value)
		}
		if err != nil {
			break
		}
		err = out.Write(normalized)
		rows++
	}
	if err == nil && rows == 0 {
		err = errors.New("source Driver CSV is empty")
	}
	if err == nil {
		_, err = reader.NextPart()
		if err == nil {
			err = errors.New("unexpected multipart data after CSV")
		} else if errors.Is(err, io.EOF) {
			err = nil
		}
	}
	out.Flush()
	if err == nil {
		err = out.Error()
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		fail(w, 422, err)
		return
	}
	// Hash and stat the completed stage BEFORE publication. No read or IO
	// that can report a failure remains after the no-overwrite installation.
	staged, e := os.Open(tmp.Name())
	if e != nil {
		fail(w, 500, e)
		return
	}
	stageInfo, e := staged.Stat()
	if e != nil {
		_ = staged.Close()
		fail(w, 500, e)
		return
	}
	h := sha256.New()
	_, e = io.Copy(h, staged)
	ce := staged.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		fail(w, 500, e)
		return
	}

	// Re-check business rule and busy state at the point of publication.
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.busy[form["name"]] {
		fail(w, 409, errors.New("project started running before Driver publication"))
		return
	}
	now, e := os.ReadFile(filepath.Join(dir, "templates", form["template"]))
	if e != nil || sha256.Sum256(now) != digest {
		fail(w, 409, errors.New("template changed during Driver upload"))
		return
	}
	dest := filepath.Join(driverDir, form["file"])
	if e = os.Link(tmp.Name(), dest); e != nil {
		if _, statErr := os.Lstat(dest); statErr == nil {
			fail(w, 409, errors.New("driver filename already exists; never overwrite"))
			return
		}
		fail(w, 500, e)
		return
	}
	jsonOut(w, 201, map[string]any{"created": form["file"], "rows": rows, "bytes": stageInfo.Size(), "sha256": hex.EncodeToString(h.Sum(nil)), "columns": outputNames,
		"note": "New Driver file only. Existing template mapping and formal results are unchanged. Select it when importing an edited rule, then Inspect and Run."})
}
