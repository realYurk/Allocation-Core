package studio

// CSV-first project onboarding is a user-facing workflow, not a new financial
// engine. It makes a NEW project from an existing rule-template project, so
// importing data can never overwrite historical data or published results.
import (
	"allocationcore/internal/engine"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const maxOnboardingUpload = 256 << 20 // 256 MiB; larger files stay on local disk.
const maxOnboardingAccounts = 50000

// Clone only reusable rule/driver configuration, never source amounts,
// assignments, overrides, run history, or generated caches. This deliberately
// uses streaming copies to avoid retaining large driver files in Go heap.
func cloneBusinessAssets(source, stage string) error {
	for _, folder := range []string{"templates", "drivers"} {
		fromRoot := filepath.Join(source, folder)
		st, err := os.Lstat(fromRoot)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source %s folder is not a regular directory", folder)
		}
		if err := filepath.WalkDir(fromRoot, func(path string, item os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if item.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("source contains symlink: %s", item.Name())
			}
			rel, e := filepath.Rel(fromRoot, path)
			if e != nil {
				return e
			}
			out := filepath.Join(stage, folder, rel)
			if item.IsDir() {
				return os.MkdirAll(out, 0700)
			}
			if !item.Type().IsRegular() {
				return fmt.Errorf("source contains nonregular file: %s", item.Name())
			}
			in, e := os.Open(path)
			if e != nil {
				return e
			}
			f, e := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				_ = in.Close()
				return e
			}
			_, e = io.Copy(f, in)
			if e == nil {
				e = f.Sync()
			}
			ce := f.Close()
			ie := in.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
			return ie
		}); err != nil {
			return err
		}
	}
	config, err := os.ReadFile(filepath.Join(source, "project.json"))
	if err != nil {
		return err
	}
	if len(config) > 1<<20 {
		return errors.New("project config too large")
	}
	var project map[string]any
	if err := json.Unmarshal(config, &project); err != nil {
		return err
	}
	if project["templates"] != "templates" || project["drivers"] != "drivers" {
		return errors.New("source project uses unsupported templates/drivers folders")
	}
	project["amounts"] = "amounts.csv"
	project["assignments"] = "assignments.csv"
	project["overrides"] = "overrides.csv"
	encoded, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stage, "project.json"), encoded, 0600)
}

func readOnboardingTemplate(source, template string) (int, []string, error) {
	if !strings.HasSuffix(template, ".json") || !multiSafe.MatchString(strings.TrimSuffix(template, ".json")) {
		return 0, nil, errors.New("invalid rule template name")
	}
	path := filepath.Join(source, "templates", template)
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return 0, nil, errors.New("rule template not found or not regular")
	}
	if st.Size() > 1<<20 {
		return 0, nil, errors.New("rule template too large")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, err
	}
	var cfg struct {
		Schema     string   `json:"schema"`
		Precision  int      `json:"precision"`
		Dimensions []string `json:"dimensions"`
		Immutable  []string `json:"immutable_dimensions"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return 0, nil, err
	}
	if cfg.Schema != "allocation-ir/2.0" || cfg.Precision < 0 || cfg.Precision > 6 {
		return 0, nil, errors.New("unsupported template schema/precision")
	}
	seen := map[string]bool{}
	for _, d := range cfg.Dimensions {
		if d == "" || seen[d] {
			return 0, nil, errors.New("template contains invalid dimensions")
		}
		seen[d] = true
	}
	if !seen["account"] {
		return 0, nil, errors.New("selected template has no account dimension")
	}
	if seen["amount"] || seen["row_id"] {
		return 0, nil, errors.New("amount and row_id are reserved CSV fields, not business dimensions")
	}
	return cfg.Precision, cfg.Dimensions, nil
}

type onboardPreview struct {
	Accounts     int    `json:"accounts"`
	Rows         int64  `json:"amount_rows"`
	Total        string `json:"input_amount"`
	Precision    int    `json:"precision"`
	Template     string `json:"default_template"`
	SHA256       string `json:"amounts_sha256"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	Bytes        int64  `json:"amounts_bytes"`
}

func streamOnboardingAmounts(ctx context.Context, source io.Reader, dst string, precision int, dims []string, template string) (onboardPreview, []string, error) {
	var report onboardPreview
	report.Precision, report.Template = precision, template
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return report, nil, err
	}
	defer f.Close()
	hash := sha256.New()
	rd := csv.NewReader(bufio.NewReaderSize(io.TeeReader(source, io.MultiWriter(f, hash)), 256<<10))
	rd.FieldsPerRecord = -1
	header, err := rd.Read()
	if err != nil {
		return report, nil, fmt.Errorf("CSV header: %w", err)
	}
	positions := map[string]int{}
	for i, h := range header {
		h = strings.TrimPrefix(h, "\ufeff")
		if h == "" {
			return report, nil, errors.New("CSV header has an empty name")
		}
		if _, ok := positions[h]; ok {
			return report, nil, fmt.Errorf("duplicate CSV column %q", h)
		}
		positions[h] = i
	}
	for _, d := range append(append([]string{}, dims...), "amount") {
		if _, ok := positions[d]; !ok {
			return report, nil, fmt.Errorf("missing required CSV column %q (selected rule needs it)", d)
		}
	}
	accounts := map[string]struct{}{}
	total := new(big.Int)
	for {
		if err := ctx.Err(); err != nil {
			return report, nil, err
		}
		row, e := rd.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return report, nil, fmt.Errorf("CSV row %d: %w", report.Rows+2, e)
		}
		if len(row) != len(header) {
			return report, nil, fmt.Errorf("CSV row %d: expected %d columns, got %d", report.Rows+2, len(header), len(row))
		}
		account := strings.TrimSpace(row[positions["account"]])
		if account == "" || len(account) > 256 || strings.ContainsAny(account, "\x00\r\n") {
			return report, nil, fmt.Errorf("CSV row %d has invalid account", report.Rows+2)
		}
		for _, d := range dims {
			if strings.TrimSpace(row[positions[d]]) == "" {
				return report, nil, fmt.Errorf("CSV row %d has empty dimension %s", report.Rows+2, d)
			}
		}
		amt, e := engine.ParseScaled(strings.TrimSpace(row[positions["amount"]]), precision)
		if e != nil {
			return report, nil, fmt.Errorf("CSV row %d has invalid amount: %w", report.Rows+2, e)
		}
		total.Add(total, big.NewInt(amt))
		accounts[account] = struct{}{}
		if len(accounts) > maxOnboardingAccounts {
			return report, nil, fmt.Errorf("more than %d distinct accounts; split project first", maxOnboardingAccounts)
		}
		report.Rows++
	}
	if report.Rows == 0 {
		return report, nil, errors.New("CSV contains no amount rows")
	}
	if e := f.Sync(); e != nil {
		return report, nil, e
	}
	stat, e := f.Stat()
	if e != nil {
		return report, nil, e
	}
	report.Bytes = stat.Size()
	report.SHA256 = hex.EncodeToString(hash.Sum(nil))
	report.Accounts = len(accounts)
	// A summed import can exceed int64 even when every individual record is valid.
	sum := new(big.Int).Set(total)
	negative := sum.Sign() < 0
	sum.Abs(sum)
	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	whole, frac := new(big.Int).QuoRem(sum, base, new(big.Int))
	if precision == 0 {
		report.Total = whole.String()
	} else {
		report.Total = fmt.Sprintf("%s.%0*s", whole, precision, frac.String())
	}
	if negative {
		report.Total = "-" + report.Total
	}
	names := make([]string, 0, len(accounts))
	for v := range accounts {
		names = append(names, v)
	}
	sort.Strings(names)
	return report, names, nil
}

func writeOnboardingAssignments(path, template string, accounts []string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	err = w.Write([]string{"account", "template"})
	for _, a := range accounts {
		if err != nil {
			break
		}
		err = w.Write([]string{a, template})
	}
	w.Flush()
	if err == nil {
		err = w.Error()
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	return ce
}

// A project-local normalization receipt retains the exact mapping and both
// content digests. It never includes source rows or claims business correctness.
type onboardingReceipt struct {
	Kind                             string            `json:"kind"`
	Project                          string            `json:"project"`
	SourceProject                    string            `json:"source_project"`
	SourceFilename                   string            `json:"source_filename"`
	RuleTemplate                     string            `json:"rule_template"`
	RuleTemplateSHA256               string            `json:"rule_template_sha256"`
	Mapping                          map[string]string `json:"column_mapping,omitempty"`
	SourceSHA256                     string            `json:"source_sha256"`
	AmountsSHA256                    string            `json:"normalized_amounts_sha256"`
	AmountRows                       int64             `json:"amount_rows"`
	Accounts                         int               `json:"accounts"`
	Total                            string            `json:"input_amount"`
	Precision                        int               `json:"precision"`
	Dimensions                       []string          `json:"dimensions"`
	DriversCopiedFromTemplateProject bool              `json:"drivers_copied_from_template_project"`
	Status                           string            `json:"status"`
}

// FormData fields in order: name, source, template, csv. Destination is new.
// Strict streaming keeps the browser and the server from materializing the
// entire data file in RAM; it is intentionally limited to 256 MiB per upload.
func (x *MultiApp) createFromAmountsCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxOnboardingUpload+(32<<10))
	mr, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, err)
		return
	}
	names := []string{"name", "source", "template"}
	form := make(map[string]string, 3)
	for _, name := range names {
		part, e := mr.NextPart()
		if e != nil {
			fail(w, 400, fmt.Errorf("missing multipart field %s: %w", name, e))
			return
		}
		if part.FormName() != name || part.FileName() != "" {
			fail(w, 400, fmt.Errorf("expected field %s", name))
			return
		}
		raw, e := io.ReadAll(io.LimitReader(part, 513))
		if e != nil || len(raw) > 512 {
			fail(w, 422, fmt.Errorf("invalid field %s", name))
			return
		}
		form[name] = string(raw)
		part.Close()
	}
	newName, sourceName, template := form["name"], form["source"], form["template"]
	if !multiSafe.MatchString(newName) || !multiSafe.MatchString(sourceName) || newName == sourceName {
		fail(w, 422, errors.New("select a valid NEW project name, different from the source project"))
		return
	}
	sourceDir, err := x.projectDir(sourceName)
	if err != nil {
		fail(w, 404, err)
		return
	}
	precision, dimensions, err := readOnboardingTemplate(sourceDir, template)
	if err != nil {
		fail(w, 422, err)
		return
	}
	upload, err := mr.NextPart()
	var mapping map[string]string
	if err == nil && upload.FormName() == "mapping" && upload.FileName() == "" {
		raw, e := io.ReadAll(io.LimitReader(upload, 8193))
		_ = upload.Close()
		if e != nil || len(raw) > 8192 {
			fail(w, 422, errors.New("column mapping exceeds 8 KiB"))
			return
		}
		if e = json.Unmarshal(raw, &mapping); e != nil {
			fail(w, 422, fmt.Errorf("invalid column mapping: %w", e))
			return
		}
		if _, e = checkAmountMapping(mapping, dimensions); e != nil {
			fail(w, 422, e)
			return
		}
		upload, err = mr.NextPart()
	}
	if err != nil || upload.FormName() != "csv" || upload.FileName() == "" || !strings.EqualFold(filepath.Ext(upload.FileName()), ".csv") {
		fail(w, 400, errors.New("expected uploaded .csv file in csv field"))
		return
	}
	if err := os.MkdirAll(x.root, 0700); err != nil {
		fail(w, 500, err)
		return
	}
	stage, err := os.MkdirTemp(x.root, ".onboard-")
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer os.RemoveAll(stage)
	if err := cloneBusinessAssets(sourceDir, stage); err != nil {
		fail(w, 422, err)
		return
	}
	// A concurrent edit to the source template must not make the copied
	// project's financial coordinate contract disagree with the chosen one.
	stagePrecision, stageDimensions, e := readOnboardingTemplate(stage, template)
	if e != nil || stagePrecision != precision || !reflect.DeepEqual(stageDimensions, dimensions) {
		fail(w, 409, errors.New("source rule template changed while preparing project; retry"))
		return
	}
	var report onboardPreview
	var accounts []string
	if mapping == nil {
		report, accounts, err = streamOnboardingAmounts(r.Context(), upload, filepath.Join(stage, "amounts.csv"), precision, dimensions, template)
	} else {
		report, accounts, err = streamMappedOnboardingAmounts(r.Context(), upload, filepath.Join(stage, "amounts.csv"), precision, dimensions, template, mapping)
	}
	if err != nil {
		fail(w, 422, err)
		return
	}
	if err := upload.Close(); err != nil {
		fail(w, 422, err)
		return
	}
	next, err := mr.NextPart()
	if err == nil {
		next.Close()
		fail(w, 400, errors.New("unexpected additional multipart fields"))
		return
	}
	if !errors.Is(err, io.EOF) {
		fail(w, 400, err)
		return
	}
	if err := writeOnboardingAssignments(filepath.Join(stage, "assignments.csv"), template, accounts); err != nil {
		fail(w, 500, err)
		return
	}
	if err := os.WriteFile(filepath.Join(stage, "overrides.csv"), []byte("account,stage,allocation,driver_dataset,weight_field,share\n"), 0600); err != nil {
		fail(w, 500, err)
		return
	}
	rule, err := os.ReadFile(filepath.Join(stage, "templates", template))
	if err != nil {
		fail(w, 500, err)
		return
	}
	digest := sha256.Sum256(rule)
	inputSHA := report.SourceSHA256
	if inputSHA == "" {
		inputSHA = report.SHA256
	}
	receipt := onboardingReceipt{
		Kind: "business-onboarding/v1", Project: newName, SourceProject: sourceName,
		SourceFilename: upload.FileName(), RuleTemplate: template,
		RuleTemplateSHA256: hex.EncodeToString(digest[:]), Mapping: mapping,
		SourceSHA256: inputSHA, AmountsSHA256: report.SHA256,
		AmountRows: report.Rows, Accounts: report.Accounts,
		Total: report.Total, Precision: precision, Dimensions: dimensions,
		DriversCopiedFromTemplateProject: true, Status: "INPUT_ONLY_NOT_BUSINESS_VERIFIED",
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(stage, "onboarding_manifest.json"), receiptJSON, 0600)
	}
	if err != nil {
		fail(w, 500, err)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	destination := filepath.Join(x.root, newName)
	if _, err := os.Lstat(destination); err == nil {
		fail(w, 409, errors.New("project already exists; never overwrite data"))
		return
	} else if !os.IsNotExist(err) {
		fail(w, 500, err)
		return
	}
	if err := os.Rename(stage, destination); err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, http.StatusCreated, map[string]any{"project": newName, "import": report, "manifest": "onboarding_manifest.json", "note": "Amounts and assignments validated. Rule eligibility and replacement driver data must be checked with Inspect before Run; existing projects remain untouched."})
}
