package studio

// A bridge to the existing visual rule designer. It imports *rule definitions*
// into an independent multi-account project; it never transfers financial
// amounts, private driver data, existing assignments, or published results.
import (
    "allocationcore/internal/engine"
    "crypto/sha256"
    "encoding/csv"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "os"
    "path/filepath"
    "reflect"
    "sort"
    "strings"
)

type importedDriver struct {
    Alias string `json:"alias"`
    File string `json:"file"`
    WeightFields []string `json:"weight_fields"`
    Ready bool `json:"ready"`
    Issue string `json:"issue,omitempty"`
}

type designerImportPlan struct {
    SourceSHA256 string `json:"source_sha256"`
    SourceName string `json:"source_name"`
    Stages int `json:"stages"`
    Precision int `json:"precision"`
    Dimensions []string `json:"dimensions"`
    Drivers []importedDriver `json:"drivers"`
    AvailableDrivers []string `json:"available_drivers"`
    Ready bool `json:"ready"`
    Note string `json:"note"`
}

func validMultiTemplateName(name string) bool {
    return strings.HasSuffix(name, ".json") &&
        multiSafe.MatchString(strings.TrimSuffix(name, ".json"))
}

func safeDriverFiles(dir string) ([]string, error) {
    base := filepath.Join(dir, "drivers")
    info, err := os.Lstat(base)
    if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
        return nil, errors.New("drivers must be a regular directory")
    }
    items, err := os.ReadDir(base)
    if err != nil { return nil, err }
    files := []string{}
    for _, item := range items {
        if !multiCSV.MatchString(item.Name()) || !item.Type().IsRegular() {
            continue
        }
        files = append(files, item.Name())
    }
    sort.Strings(files)
    return files, nil
}

func importedDriverReport(base string, alias string, file string, fields []string) importedDriver {
    d := importedDriver{Alias: alias, File: file, WeightFields: fields}
    if !multiCSV.MatchString(file) {
        d.Issue = "Driver file name must be a simple .csv basename"
        return d
    }
    path := filepath.Join(base, "drivers", file)
    st, err := os.Lstat(path)
    if err != nil || !st.Mode().IsRegular() {
        d.Issue = "Driver file does not exist; copy actual business data before running"
        return d
    }
    f, err := os.Open(path)
    if err != nil {d.Issue = "Cannot read driver header: " + err.Error(); return d}
    defer f.Close()
    reader := csv.NewReader(f)
    header, err := reader.Read()
    if err != nil {d.Issue = "Cannot read driver CSV header: " + err.Error();return d}
    cols := map[string]bool{}
    for i, name := range header {
        if i == 0 { name = strings.TrimPrefix(name, "\ufeff") }
        if name == "" || cols[name] {d.Issue = "Duplicate or empty driver header";return d}
        cols[name] = true
    }
    missing := []string{}
    for _, field := range fields {
        if !cols[field] {missing = append(missing, field)}
    }
    if len(missing) > 0 {
        d.Issue = "Missing weight fields: " + strings.Join(missing, ", ")
        return d
    }
    d.Ready = true
    return d
}

func importDesignerRule(source []byte, projectDir string, mapping map[string]string) ([]byte, designerImportPlan, error) {
    var plan designerImportPlan
    if len(source) == 0 || len(source) > 480*1024 {
        return nil, plan, errors.New("source rule is empty or exceeds 480 KiB")
    }
    sum := sha256.Sum256(source)
    plan.SourceSHA256 = hex.EncodeToString(sum[:])
    spec, err := engine.ParseSpec(source)
    if err != nil {return nil, plan, fmt.Errorf("invalid visual designer rule: %w", err)}
    if spec.Schema != engine.Schema || spec.Precision < 0 || spec.Precision > 6 {
        return nil, plan, errors.New("source must use allocation-ir/2.0 with precision 0..6")
    }
    if len(spec.Datasets.Amounts) != 1 {
        return nil, plan, errors.New("multi-account templates require exactly one amounts dataset")
    }
    // A business account must never be merged with another one, not even in
    // an intermediate stage. Never silently 'fix' an unsafe designer rule.
    hasAccount := false
    immutableAccount := false
    for _, dim := range spec.Dimensions { if dim == "account" {hasAccount = true} }
    for _, dim := range spec.Immutable { if dim == "account" {immutableAccount = true} }
    if !hasAccount || !immutableAccount {
        return nil, plan, errors.New("designer rule must declare account as a dimension and immutable_dimension")
    }
    for _, stage := range spec.Stages {
        preserved := false
        for _, key := range stage.ConservationKeys {if key == "account" {preserved = true}}
        if stage.Source.Grouping["account"] != "AS_IS" || stage.Target["account"].Mode != "SOURCE" || !preserved {
            return nil, plan, fmt.Errorf("stage %q must keep account AS_IS, SOURCE and conservation_key", stage.ID)
        }
    }
    if _, err := engine.Compile(spec); err != nil {
        return nil, plan, fmt.Errorf("engine rejected designer graph: %w", err)
    }

    // The current business project's assigned template defines the immutable
    // coordinate contract. An import must not change the monetary precision or
    // the order of dimensions for even one account.
    assignments, err := readAssignments(filepath.Join(projectDir, "assignments.csv"))
    if err != nil {return nil, plan, err}
    if len(assignments) == 0 {return nil, plan, errors.New("project has no account assignments")}
    canonical, err := os.ReadFile(filepath.Join(projectDir, "templates", assignments[0]["template"]))
    if err != nil {return nil, plan, err}
    var contract struct {
        Dimensions []string `json:"dimensions"`
        Precision int `json:"precision"`
    }
    if err := json.Unmarshal(canonical, &contract); err != nil {return nil, plan, err}
    if contract.Precision != spec.Precision || !reflect.DeepEqual(contract.Dimensions, spec.Dimensions) {
        return nil, plan, fmt.Errorf("designer rule dimensions/precision do not match project contract; project=%v precision=%d, designer=%v precision=%d",
            contract.Dimensions, contract.Precision, spec.Dimensions, spec.Precision)
    }

    // Dataset paths are reconstructed, never trusted from the designer.
    // Amounts point at this project's own amounts.csv. Drivers must use an
    // explicit project-local CSV basename, not an absolute or parent path.
    for alias := range spec.Datasets.Amounts {spec.Datasets.Amounts[alias] = "amounts.csv"}
    needed := map[string]map[string]bool{}
    for _, stage := range spec.Stages {
        for _, alloc := range stage.Allocations {
            if alloc.DriverDataset == "" {continue}
            if needed[alloc.DriverDataset] == nil {needed[alloc.DriverDataset] = map[string]bool{}}
            needed[alloc.DriverDataset][alloc.WeightField] = true
        }
    }
    available, err := safeDriverFiles(projectDir)
    if err != nil {return nil, plan, err}
    plan.AvailableDrivers = available
    plan.Ready = true
    plan.SourceName = spec.Name
    plan.Stages = len(spec.Stages)
    plan.Precision = spec.Precision
    plan.Dimensions = spec.Dimensions
    aliases := make([]string,0,len(spec.Datasets.Drivers))
    for alias := range spec.Datasets.Drivers {aliases = append(aliases,alias)}
    sort.Strings(aliases)
    for alias := range mapping {
        if _, ok := spec.Datasets.Drivers[alias]; !ok {
            return nil, plan, fmt.Errorf("unknown driver alias in mapping: %q", alias)
        }
    }
    for _, alias := range aliases {
        old := spec.Datasets.Drivers[alias]
        file := filepath.Base(old)
        if explicit, ok := mapping[alias]; ok {file = explicit}
        if !multiCSV.MatchString(file) {
            return nil, plan, fmt.Errorf("driver %s must map to a project-local .csv filename", alias)
        }
        spec.Datasets.Drivers[alias] = file
        fields := []string{}
        for col := range needed[alias] {fields = append(fields,col)}
        sort.Strings(fields)
        d := importedDriverReport(projectDir,alias,file,fields)
        plan.Drivers = append(plan.Drivers,d)
        if !d.Ready {plan.Ready = false}
    }
    plan.Note = "Only the rule template is imported. Source amounts and driver files are never copied; missing or incompatible drivers block preflight until replaced with verified business data."
    encoded, err := json.MarshalIndent(spec,"","  ")
    if err != nil {return nil,plan,err}
    // Compiled once more after the project-local dataset mapping.
    normalized, err := engine.ParseSpec(encoded)
    if err != nil {return nil,plan,err}
    if _, err := engine.Compile(normalized);err!=nil {return nil,plan,err}
    return encoded,plan,nil
}

func (x *MultiApp) importTemplateFromDesigner(w http.ResponseWriter,r *http.Request) {
    if r.Method != http.MethodPost {w.WriteHeader(http.StatusMethodNotAllowed);return}
    var req struct {
        Name string `json:"name"`
        Case string `json:"case"`
        Template string `json:"template"`
        Preview bool `json:"preview"`
        BaseSHA256 string `json:"base_sha256"`
        DriverMapping map[string]string `json:"driver_mapping"`
    }
    if err := multiRequest(w,r,&req,32<<10);err!=nil {fail(w,400,err);return}
    if !validMultiTemplateName(req.Template) {
        fail(w,422,errors.New("new template name must be 1..48 English letters, digits, _ or -, followed by .json"));return
    }
    x.mu.Lock()
    defer x.mu.Unlock()
    if x.busy[req.Name] {fail(w,409,errors.New("cannot change templates during allocation run"));return}
    dir,err := x.projectDir(req.Name)
    if err!=nil {fail(w,404,err);return}
    if x.parent == nil {fail(w,500,errors.New("designer is unavailable"));return}
    _,designerDir,_,err := x.parent.readCase(req.Case)
    if err!=nil {fail(w,422,fmt.Errorf("visual designer case unavailable: %w",err));return}
    raw,err := os.ReadFile(filepath.Join(designerDir,"rule.json"))
    if err!=nil {fail(w,500,err);return}
    prepared,plan,err := importDesignerRule(raw,dir,req.DriverMapping)
    if err!=nil {fail(w,422,err);return}
    if req.Preview {
        jsonOut(w,200,plan)
        return
    }
    if req.BaseSHA256=="" || req.BaseSHA256!=plan.SourceSHA256 {
        fail(w,409,errors.New("designer rule changed after preview; preview it again before import"));return
    }
    targetDir := filepath.Join(dir,"templates")
    st,err:=os.Lstat(targetDir)
    if err!=nil || !st.IsDir() || st.Mode()&os.ModeSymlink!=0 {
        fail(w,422,errors.New("templates must be a regular directory"));return
    }
    dest := filepath.Join(targetDir,req.Template)
    tmp,err:=os.CreateTemp(targetDir,".rule-import-")
    if err!=nil {fail(w,500,err);return}
    defer os.Remove(tmp.Name())
    _,err=tmp.Write(prepared)
    if err==nil {err=tmp.Sync()}
    if e:=tmp.Close();err==nil {err=e}
    if err!=nil {fail(w,500,err);return}
    // A hard link installs the completed bytes atomically only when the
    // destination does not exist. Unlike Rename, it cannot overwrite a
    // different rule even if another process wrote it concurrently.
    if err:=os.Link(tmp.Name(),dest);err!=nil {
        if _,statErr:=os.Lstat(dest);statErr==nil {
            fail(w,409,errors.New("template already exists: never overwrite a saved business rule"));return
        }
        fail(w,500,fmt.Errorf("cannot atomically install template on this filesystem: %w",err));return
    }
    jsonOut(w,http.StatusCreated,map[string]any{
        "template":req.Template,"import":plan,"assigned":false,
        "note":"Template created without changing account mappings. Assign it to selected accounts, replace unready drivers and run Inspect before calculating.",
    })
}
