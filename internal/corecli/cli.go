package corecli

import (
	"allocationcore/internal/batch"
	"allocationcore/internal/capacity"
	"allocationcore/internal/engine"
	"allocationcore/internal/service"
	"allocationcore/internal/vault"
	"allocationcore/internal/workspace"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func printJSON(v any) {
	j, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		stop(e)
	}
	fmt.Println(string(j))
}
func optionInt(args []string, flag string, defaultValue int) (int, error) {
	if len(args) == 0 {
		return defaultValue, nil
	}
	if len(args) != 2 || args[0] != flag {
		return 0, fmt.Errorf("expected %s N", flag)
	}
	n, e := strconv.Atoi(args[1])
	if e != nil || n < 1 || n > 128 {
		return 0, fmt.Errorf("%s expects integer 1..128", flag)
	}
	return n, nil
}
func runOptions(args []string) (engine.RunOptions, error) {
	o := engine.RunOptions{Workers: 1}
	used := map[string]bool{}
	for i := 0; i < len(args); {
		flag := args[i]
		if used[flag] {
			return o, fmt.Errorf("duplicate option %s", flag)
		}
		used[flag] = true
		if flag == "--evidence-gzip" {
			o.EvidenceGzip = true
			i++
			continue
		}
		if flag == "--driver-disk-index" {
			o.DriverDiskIndex = true
			i++
			continue
		}
		if flag == "--resume" {
			o.Resume = true
			i++
			continue
		}
		if i+1 >= len(args) {
			return o, fmt.Errorf("%s needs a value", flag)
		}
		value := args[i+1]
		switch flag {
		case "--workers", "--disk-partitions":
			n, e := strconv.Atoi(value)
			if e != nil {
				return o, fmt.Errorf("%s expects an integer", flag)
			}
			if flag == "--workers" {
				if n < 1 || n > 128 {
					return o, fmt.Errorf("workers must be 1..128")
				}
				o.Workers = n
			} else {
				if n < 2 || n > 64 {
					return o, fmt.Errorf("disk partitions must be 2..64")
				}
				o.DiskPartitions = n
			}
		case "--driver-cache-dir":
			if strings.TrimSpace(value) == "" {
				return o, fmt.Errorf("driver cache directory must not be empty")
			}
			o.DriverCacheDir = value
		case "--checkpoint-dir":
			if strings.TrimSpace(value) == "" {
				return o, fmt.Errorf("checkpoint directory must not be empty")
			}
			o.CheckpointDir = value
		default:
			return o, fmt.Errorf("unknown option %s", flag)
		}
		i += 2
	}
	if o.DriverCacheDir != "" && !o.DriverDiskIndex {
		return o, fmt.Errorf("--driver-cache-dir requires --driver-disk-index")
	}
	if o.DriverDiskIndex && o.DiskPartitions == 0 {
		return o, fmt.Errorf("--driver-disk-index requires --disk-partitions 2..64")
	}
	if o.Resume && o.CheckpointDir == "" {
		return o, fmt.Errorf("--resume requires --checkpoint-dir")
	}
	if o.CheckpointDir != "" && o.DiskPartitions == 0 {
		return o, fmt.Errorf("--checkpoint-dir requires --disk-partitions 2..64")
	}
	return o, nil
}
func batchOptions(args []string) (int, int, int, error) {
	jobs, workers, disk := 2, 1, 0
	if len(args)%2 != 0 {
		return 0, 0, 0, fmt.Errorf("expected --jobs N --workers N")
	}
	used := map[string]bool{}
	for i := 0; i < len(args); i += 2 {
		flag := args[i]
		if used[flag] {
			return 0, 0, 0, fmt.Errorf("duplicate option %s", flag)
		}
		used[flag] = true
		n, e := strconv.Atoi(args[i+1])
		if e != nil || n < 1 {
			return 0, 0, 0, fmt.Errorf("invalid option %s", flag)
		}
		switch flag {
		case "--jobs":
			jobs = n
		case "--workers":
			workers = n
		case "--disk-partitions":
			disk = n
		default:
			return 0, 0, 0, fmt.Errorf("unknown option %s", flag)
		}
	}
	if jobs > 32 || workers > 128 || (disk != 0 && (disk < 2 || disk > 64)) {
		return 0, 0, 0, fmt.Errorf("jobs<=32 workers<=128 required")
	}
	return jobs, workers, disk, nil
}
func Run() {
	args := os.Args[1:]
	if len(args) < 1 {
		help()
	}
	if args[0] == "capacity" {
		if len(args) != 3 {
			stop(fmt.Errorf("usage: alloc capacity BUDGET.json OUTPUT_DIR"))
		}
		r, e := capacity.Check(args[1], args[2])
		if e != nil {
			stop(e)
		}
		printJSON(r)
		if r.Status != "PASS" {
			stop(fmt.Errorf("capacity preflight BLOCK"))
		}
		return
	}
	// Lifecycle vault commands are additive and never modify the financial engine.
	if args[0] == "vault" {
		if len(args) < 3 {
			stop(fmt.Errorf("vault usage: add/list/stats/verify/restore/prune"))
		}
		cmd, root := args[1], args[2]
		switch cmd {
		case "add":
			if len(args) != 7 {
				stop(fmt.Errorf("vault add VAULT SCENARIO VERSION draft|official RUN_DIR"))
			}
			r, e := vault.Register(root, args[3], args[4], args[5], args[6])
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "list":
			if len(args) != 3 {
				help()
			}
			r, e := vault.List(root)
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "stats":
			if len(args) != 3 {
				help()
			}
			r, e := vault.StatsFor(root)
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "verify":
			if len(args) != 4 {
				help()
			}
			if e := vault.Verify(root, args[3]); e != nil {
				stop(e)
			}
			fmt.Println("VAULT: VERIFIED")
		case "restore":
			if len(args) != 5 {
				help()
			}
			if e := vault.Restore(root, args[3], args[4]); e != nil {
				stop(e)
			}
			fmt.Println("VAULT: RESTORED", args[4])
		case "export-db":
			if len(args) != 5 {
				help()
			}
			n, e := vault.ExportDB(root, args[3], args[4])
			if e != nil {
				stop(e)
			}
			printJSON(map[string]any{"rows": n, "csv": args[4], "note": "For bulk COPY; no SQL write performed"})
		case "gc":
			if len(args) != 3 && len(args) != 4 {
				help()
			}
			apply := len(args) == 4 && args[3] == "--apply"
			if len(args) == 4 && !apply {
				help()
			}
			r, e := vault.GarbageCollect(root, apply)
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "active":
			if len(args) != 5 {
				stop(fmt.Errorf("vault active VAULT SCENARIO VERSION"))
			}
			r, e := vault.Active(root, args[3], args[4])
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "publish":
			if len(args) != 8 || args[6] != "--expect" {
				stop(fmt.Errorf("vault publish VAULT SCENARIO VERSION RUN_ID --expect none|CURRENT_RUN_ID"))
			}
			r, e := vault.Publish(root, args[3], args[4], args[5], args[7])
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "prune-official":
			keep, days, apply := 1, 30, false
			seen := map[string]bool{}
			for i := 3; i < len(args); {
				key := args[i]
				if seen[key] {
					stop(fmt.Errorf("duplicate %s", key))
				}
				seen[key] = true
				if key == "--apply" {
					apply = true
					i++
					continue
				}
				if i+1 >= len(args) {
					help()
				}
				num, e := strconv.Atoi(args[i+1])
				if e != nil {
					stop(e)
				}
				switch key {
				case "--keep-previous":
					keep = num
				case "--older-than-days":
					days = num
				default:
					help()
				}
				i += 2
			}
			r, e := vault.PruneOfficials(root, keep, days, apply)
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "release-source":
			if len(args) != 5 && len(args) != 6 {
				help()
			}
			apply := len(args) == 6 && args[5] == "--apply"
			if len(args) == 6 && !apply {
				help()
			}
			r, e := vault.ReleaseSource(root, args[3], args[4], apply)
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "prune":
			if len(args) < 3 {
				help()
			}
			keep, days, apply := 2, 0, false
			seen := map[string]bool{}
			for i := 3; i < len(args); {
				key := args[i]
				if seen[key] {
					stop(fmt.Errorf("duplicate %s", key))
				}
				seen[key] = true
				if key == "--apply" {
					apply = true
					i++
					continue
				}
				if i+1 >= len(args) {
					help()
				}
				num, e := strconv.Atoi(args[i+1])
				if e != nil {
					stop(e)
				}
				switch key {
				case "--keep-drafts":
					keep = num
				case "--older-than-days":
					days = num
				default:
					help()
				}
				i += 2
			}
			r, e := vault.Prune(root, keep, days, apply)
			if e != nil {
				stop(e)
			}
			printJSON(r)
		default:
			help()
		}
		return
	}
	// Evidence tools operate on an already completed run and do not parse rules.
	if args[0] == "trace-pack-index" || args[0] == "trace-pack-verify" || args[0] == "trace-index" || args[0] == "trace-index-verify" || args[0] == "audit-pack" || args[0] == "audit-verify" || args[0] == "audit-extract" || args[0] == "evidence-verify" || args[0] == "evidence-expand" {
		switch args[0] {
		case "evidence-verify":
			if len(args) != 2 {
				help()
			}
			result, e := engine.VerifyEvidenceStream(args[1])
			if e != nil {
				stop(e)
			}
			printJSON(result)
		case "evidence-expand":
			if len(args) != 3 {
				help()
			}
			if e := engine.ExpandEvidenceStream(args[1], args[2]); e != nil {
				stop(e)
			}
			fmt.Println("LOSSLESS EVIDENCE RESTORED:", args[2])
		case "trace-pack-index":
			if len(args) != 2 {
				help()
			}
			receipt, e := engine.BuildPackedTraceIndex(args[1])
			if e != nil {
				stop(e)
			}
			printJSON(receipt)
		case "trace-pack-verify":
			if len(args) != 2 {
				help()
			}
			if e := engine.VerifyPackedTraceIndex(args[1]); e != nil {
				stop(e)
			}
			fmt.Println("PACKED TRACE INDEX VERIFIED")
		case "trace-index":
			if len(args) != 2 {
				help()
			}
			receipt, e := engine.BuildTraceIndex(args[1])
			if e != nil {
				stop(e)
			}
			printJSON(receipt)
		case "trace-index-verify":
			if len(args) != 2 {
				help()
			}
			if e := engine.VerifyTraceIndex(args[1]); e != nil {
				stop(e)
			}
			fmt.Println("TRACE INDEX VERIFIED")
		case "audit-pack":
			if len(args) != 3 {
				help()
			}
			report, e := engine.PackEvidence(args[1], args[2])
			if e != nil {
				stop(e)
			}
			printJSON(report)
		case "audit-verify":
			if len(args) != 2 {
				help()
			}
			report, e := engine.VerifyEvidencePack(args[1])
			if e != nil {
				stop(e)
			}
			printJSON(report)
		case "audit-extract":
			if len(args) != 3 {
				help()
			}
			if e := engine.ExtractEvidencePack(args[1], args[2]); e != nil {
				stop(e)
			}
			fmt.Println("EXTRACTED AND VERIFIED:", args[2])
		}
		return
	}
	if args[0] == "serve" {
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		root := fs.String("root", ".", "directory containing allowed rules and datasets")
		store := fs.String("store", "./service_data", "service output directory")
		listen := fs.String("listen", "127.0.0.1:8787", "local-only listener")
		maxJobs := fs.Int("jobs", 2, "maximum concurrent jobs")
		q := fs.Int("queue", 16, "maximum waiting jobs")
		token := fs.String("token", "", "optional Bearer token for local API")
		budgetFile := fs.String("budget-file", "", "trusted capacity budget; checked before accepting jobs")
		if e := fs.Parse(args[1:]); e != nil {
			stop(e)
		}
		host, _, e := net.SplitHostPort(*listen)
		if e != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
			stop(fmt.Errorf("This service only permits loopback --listen; use authenticated reverse proxy for remote access"))
		}
		app, e := service.New(service.Config{Root: *root, Store: *store, Token: *token, Concurrent: *maxJobs, Queue: *q, BudgetFile: *budgetFile})
		if e != nil {
			stop(e)
		}
		server := &http.Server{Addr: *listen, Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
		fmt.Println("Allocation Core HTTP API listening on", *listen)
		fmt.Println("GET /healthz | POST /v1/validate | POST /v1/jobs | GET /v1/jobs/{id}")
		stop(server.ListenAndServe())
	}
	if args[0] == "batch" {
		if len(args) < 3 {
			help()
		}
		batchArgs := args[3:]
		evidenceGzip := false
		filtered := make([]string, 0, len(batchArgs))
		for _, v := range batchArgs {
			if v == "--evidence-gzip" {
				if evidenceGzip {
					stop(fmt.Errorf("duplicate --evidence-gzip"))
				}
				evidenceGzip = true
			} else {
				filtered = append(filtered, v)
			}
		}
		jobs, workers, disk, e := batchOptions(filtered)
		if e != nil {
			stop(e)
		}
		report, e := batch.RunWithCompression(args[1], args[2], jobs, workers, disk, evidenceGzip)
		printJSON(report)
		if e != nil {
			stop(e)
		}
		return
	}
	if args[0] == "workspace" {
		if len(args) < 3 {
			help()
		}
		command := args[1]
		switch command {
		case "publish":
			if len(args) != 4 {
				help()
			}
			r, e := workspace.Publish(args[2], args[3])
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "submit":
			if len(args) < 4 {
				help()
			}
			workers, e := optionInt(args[4:], "--workers", 1)
			if e != nil {
				stop(e)
			}
			j, e := workspace.SubmitWithOptions(args[2], args[3], engine.RunOptions{Workers: workers})
			if e != nil {
				stop(e)
			}
			printJSON(j)
		case "list":
			if len(args) != 3 {
				help()
			}
			j, e := workspace.List(args[2])
			if e != nil {
				stop(e)
			}
			printJSON(j)
		case "history":
			if len(args) != 3 {
				help()
			}
			jobs, e := workspace.List(args[2])
			if e != nil {
				stop(e)
			}
			fmt.Printf("%-45s %-14s %-18s %-18s\n", "JOB_ID", "STATUS", "SOURCE_AMOUNT", "RESULT_AMOUNT")
			for _, j := range jobs {
				before, after := "-", "-"
				if j.Summary != nil {
					before = j.Summary.InputAmount
					after = j.Summary.OutputAmount
				}
				fmt.Printf("%-45s %-14s %-18s %-18s\n", j.ID, j.State, before, after)
			}
		case "verify-latest":
			if len(args) != 3 {
				help()
			}
			jobs, e := workspace.List(args[2])
			if e != nil {
				stop(e)
			}
			if len(jobs) == 0 {
				stop(fmt.Errorf("no jobs found"))
			}
			if e = workspace.Verify(args[2], jobs[0].ID); e != nil {
				stop(e)
			}
			fmt.Println("VERIFIED latest job:", jobs[0].ID)
		case "rules":
			if len(args) != 3 {
				help()
			}
			r, e := workspace.ListRules(args[2])
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "inspect":
			if len(args) != 4 {
				help()
			}
			j, e := workspace.Load(args[2], args[3])
			if e != nil {
				stop(e)
			}
			printJSON(j)
		case "verify":
			if len(args) != 4 {
				help()
			}
			if e := workspace.Verify(args[2], args[3]); e != nil {
				stop(e)
			}
			fmt.Println("VERIFIED: rule snapshot, inputs, result and evidence SHA-256 match")
		case "recover":
			if len(args) != 3 {
				help()
			}
			ids, e := workspace.Recover(args[2])
			if e != nil {
				stop(e)
			}
			printJSON(ids)
		case "diff":
			if len(args) != 6 {
				help()
			}
			r, e := workspace.DiffPublishedRules(args[2], args[3], args[4], args[5])
			if e != nil {
				stop(e)
			}
			printJSON(r)
		case "retry":
			if len(args) < 4 {
				help()
			}
			workers, e := optionInt(args[4:], "--workers", 0)
			if e != nil {
				stop(e)
			}
			j, e := workspace.RetryWithOptions(args[2], args[3], workers)
			if e != nil {
				stop(e)
			}
			printJSON(j)
		default:
			help()
		}
		return
	}
	if len(args) < 2 {
		help()
	}
	cmd, file := args[0], args[1]
	data, err := os.ReadFile(file)
	if err != nil {
		stop(err)
	}
	spec, err := engine.ParseSpec(data)
	if err != nil {
		stop(err)
	}
	plan, err := engine.Compile(spec)
	if err != nil {
		stop(err)
	}
	switch cmd {
	case "doctor":
		lines, e := engine.Doctor(file, spec)
		if e != nil {
			stop(e)
		}
		for _, line := range lines {
			fmt.Println(line)
		}
		fmt.Println("DOCTOR: PASS")
	case "trace-first":
		if len(args) != 3 {
			stop(fmt.Errorf("usage: alloc trace-first RULE.json OUTPUT_DIR"))
		}
		id, e := engine.FirstResultID(args[2])
		if e != nil {
			stop(e)
		}
		report, e := engine.TraceRow(args[2], id, 500)
		if e != nil {
			stop(e)
		}
		printJSON(report)
	case "trace":
		if len(args) != 4 {
			stop(fmt.Errorf("usage: alloc trace RULE.json OUTPUT_DIR ROW_ID"))
		}
		report, e := engine.TraceRow(args[2], args[3], 500)
		if e != nil {
			stop(e)
		}
		printJSON(report)
	case "compare":
		if len(args) != 4 {
			stop(fmt.Errorf("usage: alloc compare RULE.json RUN_OUTPUT_DIR period,account"))
		}
		by := strings.Split(args[3], ",")
		for i := range by {
			by[i] = strings.TrimSpace(by[i])
		}
		out := args[2] + "/comparison.csv"
		if err := engine.CompareBeforeAfter(file, args[2], out, by); err != nil {
			stop(err)
		}
		fmt.Println("COMPARISON READY:", out)
	case "explain":
		fmt.Printf("%d stages, %d dimensions\n", len(plan.Ordered), len(spec.Dimensions))
		for _, stage := range plan.Ordered {
			fmt.Println("STAGE:", stage.ID)
			for _, edge := range stage.Inputs {
				fmt.Printf("  INPUT %s x %s\n", edge.From, edge.Share)
			}
			for _, a := range stage.Allocations {
				fmt.Printf("  DRIVER %s SHARE %s DATASET %s FIELD %s\n", a.ID, a.Share, a.DriverDataset, a.WeightField)
			}
			for _, d := range spec.Dimensions {
				fmt.Printf("  DIM %-18s SOURCE %-6s TARGET %-16s\n", d, stage.Source.Grouping[d], stage.Target[d].Mode)
			}
			fmt.Println("  CONSERVATION", strings.Join(stage.ConservationKeys, ","))
		}
	case "validate":
		fmt.Printf("VALID: %d dimensions, %d stages, %d sources\n", len(spec.Dimensions), len(plan.Ordered), len(spec.Datasets.Amounts))
	case "graph":
		for _, s := range plan.Ordered {
			fmt.Printf("%s", s.ID)
			for _, in := range s.Inputs {
				fmt.Printf(" <- %s (%s)", in.From, in.Share)
			}
			fmt.Println()
		}
	case "generate":
		if len(args) != 3 {
			help()
		}
		if err := engine.Generate(file, args[2]); err != nil {
			stop(err)
		}
		fmt.Println("Generated integration starter:", args[2])
	case "run":
		out := ""
		flags := []string{}
		if len(args) > 2 {
			if strings.HasPrefix(args[2], "--") {
				flags = args[2:]
			} else {
				out = args[2]
				flags = args[3:]
			}
		}
		options, e := runOptions(flags)
		if e != nil {
			stop(e)
		}
		var result engine.Summary
		if options.DiskPartitions > 0 {
			result, err = engine.RunDiskWithOptions(file, out, options)
		} else {
			result, err = engine.RunWithOptions(file, out, options)
		}
		if err != nil {
			stop(err)
		}
		printJSON(result)
	default:
		help()
	}
}
func help() {
	fmt.Fprintln(os.Stderr, `Allocation Core
Capacity admission:
  alloc capacity BUDGET.json OUTPUT_DIR
  Set ALLOC_CAPACITY_POLICY to a trusted budget path to enforce on ALL engine paths.
Vault lifecycle:
  alloc vault add VAULT SCENARIO VERSION draft|official RUN_DIR
  alloc vault list VAULT
  alloc vault stats VAULT
  alloc vault verify VAULT RUN_ID
  alloc vault restore VAULT RUN_ID NEW_DIR
  alloc vault publish VAULT SCENARIO VERSION RUN_ID --expect none|CURRENT_RUN_ID
  alloc vault active VAULT SCENARIO VERSION
  alloc vault prune VAULT --keep-drafts 2 --older-than-days 7 [--apply]
  alloc vault prune-official VAULT --keep-previous 1 --older-than-days 30 [--apply]
  alloc vault release-source VAULT RUN_ID ORIGINAL_RUN_DIR [--apply]
  alloc vault gc VAULT [--apply]
  alloc vault export-db VAULT RUN_ID NEW_BULK_CSV
Evidence and indexed tracing:
  alloc evidence-verify OUTPUT_DIR
  alloc evidence-expand OUTPUT_DIR RESTORED_DIR
  alloc trace-pack-index GZIP_RUN_DIR
  alloc trace-pack-verify GZIP_RUN_DIR
  alloc trace RULE.json GZIP_RUN_DIR ROW_ID (auto-uses packed index)
  alloc trace-index OUTPUT_DIR
  alloc trace-index-verify OUTPUT_DIR
  alloc audit-pack OUTPUT_DIR ARCHIVE.zip
  alloc audit-verify ARCHIVE.zip
  alloc audit-extract ARCHIVE.zip NEW_OUTPUT_DIR
HTTP service:
  alloc serve --root ./examples --store ./service_data --listen 127.0.0.1:8787 --jobs 2 [--token SECRET] [--budget-file BUDGET.json]
Original commands:
  alloc validate RULE.json
  alloc explain RULE.json
  alloc doctor RULE.json
  alloc compare RULE.json OUTPUT_DIR period,account
  alloc trace RULE.json OUTPUT_DIR ROW_ID
  alloc trace-first RULE.json OUTPUT_DIR
  alloc graph RULE.json
  alloc run RULE.json OUTPUT_DIR [--workers 4] [--disk-partitions 8] [--checkpoint-dir CKPT_DIR] [--resume] [--driver-disk-index] [--driver-cache-dir CACHE_DIR] [--evidence-gzip]
  alloc batch MANIFEST.json OUTPUT_DIR [--jobs 3] [--workers 2] [--disk-partitions 8] [--evidence-gzip]
  alloc generate RULE.json NEW_COMPANY_DIR
Persistent workspace commands:
  alloc workspace publish RULE.json WORKSPACE
  alloc workspace rules WORKSPACE
  alloc workspace diff WORKSPACE RULE_ID LEFT_REVISION RIGHT_REVISION
  alloc workspace submit RULE.json WORKSPACE [--workers 4]
  alloc workspace list WORKSPACE
  alloc workspace history WORKSPACE
  alloc workspace verify-latest WORKSPACE
  alloc workspace inspect WORKSPACE JOB_ID
  alloc workspace verify WORKSPACE JOB_ID
  alloc workspace recover WORKSPACE
  alloc workspace retry WORKSPACE JOB_ID [--workers 4]`)
	os.Exit(2)
}
func stop(err error) { fmt.Fprintln(os.Stderr, "ERROR:", err); os.Exit(1) }
