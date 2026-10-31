package multi

// Read-only project preflight. It validates the assigned business-rule plan and
// scans each distinct driver CSV once. No calculation or published result is
// produced; execution must still create verified snapshots and run Core.
import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"allocationcore/internal/engine"
)

type PreflightDataset struct {
	Name         string   `json:"name"`
	Rows         int64    `json:"rows"`
	Bytes        int64    `json:"bytes"`
	WeightFields []string `json:"weight_fields"`
	ReferencedBy int      `json:"referenced_by_rule_groups"`
	Error        string   `json:"error,omitempty"`
    WeightSignals []DriverWeightSignal `json:"weight_signals"`
    allAffected map[string]map[string]bool // transient only; excluded from JSON
}
type PreflightReport struct {
	Status            string             `json:"status"`
	Accounts          int                `json:"accounts"`
	RuleGroups        int                `json:"rule_groups"`
	TotalRows         int64              `json:"amount_rows"`
	InputAmount       string             `json:"input_amount"`
	UniqueDriverFiles int                `json:"unique_driver_files"`
	DriverReferences  int                `json:"driver_references"`
	Drivers           []PreflightDataset `json:"drivers"`
	Problems          []string           `json:"problems"`
	ElapsedMS         int64              `json:"elapsed_ms"`
	Scope             string             `json:"scope"`
    Findings []BusinessFinding `json:"findings"`
    AccountDetails []AccountReadiness `json:"account_details"`
    ReviewAccounts int `json:"review_accounts"`
    BlockedAccounts int `json:"blocked_accounts"`
    CheckCount int `json:"check_count"`
    DataMatchVerified bool `json:"data_match_verified"`
}

func (r *PreflightReport) block(format string, args ...any) {
	r.Status = "BLOCK"
	if len(r.Problems) < 25 {
		r.Problems = append(r.Problems, fmt.Sprintf(format, args...))
	}
}

// InspectProject is safe to invoke before a batch. It neither creates staging
// files nor writes cache entries. PASS covers configuration/data shape only.
func InspectProject(ctx context.Context, projectDir string) (report PreflightReport) {
	begin := time.Now()
	report = PreflightReport{Status: "PASS", InputAmount: "0", Drivers: []PreflightDataset{}, Problems: []string{},
		Scope: "Configuration, account coverage and input numeric checks ONLY; no driver eligibility, allocation accuracy or reference-system equivalence verified."}
	var working *project
    var inputCounts map[string]int64
    amountsComplete := false
    defer func() {
        addBusinessDiagnostics(&report,working,inputCounts,amountsComplete)
        report.ElapsedMS = time.Since(begin).Milliseconds()
    }()
	dir, err := filepath.Abs(projectDir)
	if err != nil {
		report.block("invalid project directory: %v", err)
		return
	}
	p := &project{dir: dir, ctx: ctx}
    working=p
	if err = p.initialize(); err != nil {
		report.block("rule/assignment configuration: %v", err)
		return
	}
	report.Accounts, report.RuleGroups = len(p.assign), len(p.groups)
	if err = p.verifyControls(); err != nil {
		report.block("configuration changed: %v", err)
		return
	}
	source, err := regularWithin(dir, p.cfg.Amounts)
	if err != nil {
		report.block("amounts file: %v", err)
		return
	}
	f, err := os.Open(source)
	if err != nil {
		report.block("open amounts: %v", err)
		return
	}
	rd := csv.NewReader(bufio.NewReaderSize(f, 256*1024))
	rd.FieldsPerRecord = -1
	header, err := rd.Read()
	if err != nil {
		f.Close()
		report.block("amounts header: %v", err)
		return
	}
	pos, err := requiredColumns(header, "account", "amount")
	if err == nil {
		for _, d := range p.canonicalHeader {
			if _, ok := pos[d]; !ok {
				err = fmt.Errorf("missing dimension %q", d)
				break
			}
		}
	}
	if err != nil {
		f.Close()
		report.block("amounts: %v", err)
		return
	}
	counts := make(map[string]int64, len(p.assign))
    inputCounts=counts
	total := new(big.Int)
	for {
		if e := ctx.Err(); e != nil {
			report.block("amount scan canceled: %v", e)
			break
		}
		row, e := rd.Read()
		if e == io.EOF {
            amountsComplete = true
            break
        }
		if e != nil {
			report.block("amount row %d: %v", report.TotalRows+2, e)
			break
		}
		report.TotalRows++
		if len(row) != len(header) {
			report.block("amount row %d: wrong column count", report.TotalRows+1)
			break
		}
		account := get(row, pos["account"])
		if _, ok := p.assign[account]; !ok {
			report.block("amount row %d: account %q has no assigned rule", report.TotalRows+1, account)
			break
		}
		valid := true
		for _, dim := range p.canonicalHeader {
			if get(row, pos[dim]) == "" {
				report.block("amount row %d: empty coordinate %q", report.TotalRows+1, dim)
				valid = false
				break
			}
		}
		if !valid {
			break
		}
		value, e := engine.ParseScaled(get(row, pos["amount"]), p.precision)
		if e != nil {
			report.block("amount row %d: %v", report.TotalRows+1, e)
			break
		}
		total.Add(total, big.NewInt(value))
		counts[account]++
	}
	if e := f.Close(); e != nil {
		report.block("close amounts: %v", e)
	}
	if report.Status == "PASS" {
		for _, account := range sortedAccounts(p.assign) {
			if counts[account] == 0 {
				report.block("account %q has a rule but no input amount rows", account)
			}
		}
	}
	report.InputAmount = money(total, p.precision)
	// A driver may be referenced by hundreds of accounts/stages; combine all
	// requested weight columns and inspect the physical file only once.
	type demand struct {
		fields map[string]bool
		groups map[string]bool
		name   string
        accounts map[string]map[string]bool
    }
	demands := map[string]*demand{}
	for _, g := range p.groups {
		var spec engine.Spec
		if err := json.Unmarshal([]byte(g.Rule), &spec); err != nil {
			report.block("invalid effective rule %s: %v", g.ID, err)
			continue
		}
		for _, st := range spec.Stages {
			if !engine.Enabled(st.Enabled) || st.Operation == "MERGE" || st.Operation == "PASS" {
				continue
			}
			for _, alloc := range st.Allocations {
				if !engine.Enabled(alloc.Enabled) {
					continue
				}
				path := spec.Datasets.Drivers[alloc.DriverDataset]
				if path == "" {
					report.block("rule group %s references unknown driver %s", g.ID, alloc.DriverDataset)
					continue
				}
				// initialize() has already validated every referenced path.
				d := demands[path]
				if d == nil {
					d = &demand{fields: map[string]bool{}, groups: map[string]bool{}, name: filepath.Base(path), accounts: map[string]map[string]bool{}}
					demands[path] = d
				}
				d.fields[alloc.WeightField] = true
                if d.accounts[alloc.WeightField]==nil {d.accounts[alloc.WeightField]=map[string]bool{}}
                for _,account:=range g.Accounts {d.accounts[alloc.WeightField][account]=true}
                d.groups[g.ID] = true
				report.DriverReferences++
			}
		}
	}
	paths := make([]string, 0, len(demands))
	for path := range demands {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	report.UniqueDriverFiles = len(paths)
	for _, path := range paths {
		d := demands[path]
		fields := make([]string, 0, len(d.fields))
		for field := range d.fields {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		check := PreflightDataset{Name: d.name, WeightFields: fields, ReferencedBy: len(d.groups), allAffected:d.accounts}
		if e := scanDriverForPreflight(ctx, path, fields, p.canonicalHeader, &check); e != nil {
			check.Error = e.Error()
			report.block("driver %s: %v", d.name, e)
		}
		report.Drivers = append(report.Drivers, check)
	}
	if e := p.verifyControls(); e != nil {
		report.block("configuration changed during scan: %v", e)
	}
	return
}
func sortedAccounts(assign map[string]string) []string {
	result := make([]string, 0, len(assign))
	for a := range assign {
		result = append(result, a)
	}
	sort.Strings(result)
	return result
}
func scanDriverForPreflight(ctx context.Context, path string, fields, dimensions []string, out *PreflightDataset) error {
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular driver file")
	}
	out.Bytes = info.Size()
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReaderSize(f, 256*1024))
	r.FieldsPerRecord = -1
	header, e := r.Read()
	if e != nil {
		return fmt.Errorf("CSV header: %w", e)
	}
	ix, e := requiredColumns(header, fields...)
	if e != nil {
		return e
	}
	found := 0
	dimPositions := []int{}
	for _, dim := range dimensions {
		if i, ok := ix[dim]; ok {
			found++
			dimPositions = append(dimPositions, i)
		}
	}
	if found == 0 {
		return fmt.Errorf("driver has no matching dimension columns")
	}
	signals:=make(map[string]*DriverWeightSignal,len(fields))
    for _,field:=range fields {signals[field]=&DriverWeightSignal{Field:field}}
    for {
        if e := ctx.Err(); e != nil {return e}
        row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return fmt.Errorf("row %d: %w", out.Rows+2, e)
		}
		out.Rows++
		if len(row) != len(header) {
			return fmt.Errorf("row %d: expected %d columns, got %d", out.Rows+1, len(header), len(row))
		}
		for _, i := range dimPositions {
			if strings.TrimSpace(row[i]) == "" {
				return fmt.Errorf("row %d: empty dimension %s", out.Rows+1, header[i])
			}
		}
		for _, field := range fields {
			value, e := engine.ParseScaled(get(row, ix[field]), 6)
            if e != nil {return fmt.Errorf("row %d field %s: %w",out.Rows+1,field,e)}
            sig:=signals[field]
            if value>0 {sig.PositiveRows++} else if value<0 {sig.NegativeRows++} else {sig.ZeroRows++}
		}
	}
	if out.Rows == 0 {return fmt.Errorf("no driver rows")}
    for _,field:=range fields {
        sig:=signals[field]
        sig.AccountExamples,sig.AffectedAccounts=sortedSamples(out.allAffected[field],8)
        out.WeightSignals=append(out.WeightSignals,*sig)
    }
    return nil
}

// runInspectCLI produces a stable, machine-readable quality gate suitable for
// automated integrations. It does not execute any allocations.
func runInspectCLI(args []string) {
	fs := flag.NewFlagSet("multi inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("project", "", "multi-account project folder")
	jsonMode := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil || *dir == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: AllocationCore multi inspect -project DIR [-json]")
		os.Exit(2)
	}
	report := InspectProject(context.Background(), *dir)
	if *jsonMode {
		_ = json.NewEncoder(os.Stdout).Encode(report)
	} else {
		fmt.Printf("INPUT PREFLIGHT: %s | accounts=%d | effective rule groups=%d | amount rows=%d | amount=%s | unique drivers=%d | driver references=%d | elapsed=%dms\n",
			report.Status, report.Accounts, report.RuleGroups, report.TotalRows, report.InputAmount, report.UniqueDriverFiles, report.DriverReferences, report.ElapsedMS)
		for _, item := range report.Drivers {
			fmt.Printf("DRIVER %s: rows=%d groups=%d fields=%v %s\n", item.Name, item.Rows, item.ReferencedBy, item.WeightFields, item.Error)
		}
		for _, issue := range report.Problems {
			fmt.Fprintln(os.Stderr, "BLOCK:", issue)
		}
		fmt.Println("Scope: input shape and numeric checks only; run Core to verify allocation and reference results.")
	}
	if report.Status != "PASS" {
		os.Exit(1)
	}
}
