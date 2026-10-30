package multi

import (
	"allocationcore/internal/multi/batchvault"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
)

func batchOut(v any) { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
func batchCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("batch commands: add/list/verify/active/publish/restore/prune")
	}
	switch args[0] {
	case "add":
		if len(args) != 5 {
			return fmt.Errorf("batch add VAULT SCENARIO VERSION COMPLETE_RUN_DIR")
		}
		r, e := batchvault.Register(args[1], args[2], args[3], args[4])
		if e == nil {
			batchOut(map[string]any{"id": r.ID, "scenario": r.Scenario, "version": r.Version, "accounts": r.Accounts, "groups": r.Groups, "files": len(r.Files), "input_amount": r.InputAmount, "output_amount": r.OutputAmount})
		}
		return e
	case "status":
		if len(args) != 4 {
			return fmt.Errorf("batch status VAULT SCENARIO VERSION")
		}
		p, e := batchvault.Active(args[1], args[2], args[3])
		if errors.Is(e, os.ErrNotExist) {
			batchOut(map[string]string{"scenario": args[2], "version": args[3], "run_id": "none"})
			return nil
		}
		if e == nil {
			batchOut(p)
		}
		return e
	case "list":
		if len(args) != 2 {
			return fmt.Errorf("batch list VAULT")
		}
		v, e := batchvault.List(args[1])
		if e == nil {
			summary := []map[string]any{}
			for _, r := range v {
				summary = append(summary, map[string]any{"id": r.ID, "scenario": r.Scenario, "version": r.Version, "accounts": r.Accounts, "groups": r.Groups, "files": len(r.Files), "created_utc": r.CreatedUTC})
			}
			batchOut(summary)
		}
		return e
	case "verify":
		if len(args) != 3 {
			return fmt.Errorf("batch verify VAULT BATCH_ID")
		}
		e := batchvault.Verify(args[1], args[2])
		if e == nil {
			batchOut(map[string]any{"verified": true, "id": args[2]})
		}
		return e
	case "active":
		if len(args) != 4 {
			return fmt.Errorf("batch active VAULT SCENARIO VERSION")
		}
		v, e := batchvault.Active(args[1], args[2], args[3])
		if e == nil {
			batchOut(v)
		}
		return e
	case "publish":
		if len(args) != 7 || args[5] != "--expect" {
			return fmt.Errorf("batch publish VAULT SCENARIO VERSION BATCH_ID --expect none|OLD_BATCH_ID")
		}
		v, e := batchvault.Publish(args[1], args[2], args[3], args[4], args[6])
		if e == nil {
			batchOut(v)
		}
		return e
	case "restore":
		if len(args) != 4 {
			return fmt.Errorf("batch restore VAULT BATCH_ID NEW_OUTPUT_DIR")
		}
		e := batchvault.Restore(args[1], args[2], args[3])
		if e == nil {
			batchOut(map[string]any{"restored": args[2], "destination": args[3]})
		}
		return e
	case "gc":
		if len(args) < 2 {
			return fmt.Errorf("batch gc VAULT [--apply]")
		}
		f := flag.NewFlagSet("batch gc", flag.ContinueOnError)
		apply := f.Bool("apply", false, "remove orphan objects")
		if e := f.Parse(args[2:]); e != nil {
			return e
		}
		if f.NArg() != 0 {
			return fmt.Errorf("unexpected gc arguments")
		}
		v, e := batchvault.GC(args[1], *apply)
		if e == nil {
			batchOut(v)
		}
		return e
	case "prune":
		if len(args) < 2 {
			return fmt.Errorf("batch prune VAULT [--keep-previous 1] [--older-than-days 30] [--apply]")
		}
		f := flag.NewFlagSet("batch prune", flag.ContinueOnError)
		previous := f.Int("keep-previous", 1, "rollback versions per scenario/version")
		days := f.Int("older-than-days", 30, "oldest eligible age")
		apply := f.Bool("apply", false, "actually remove historical records and unreferenced archive objects")
		if e := f.Parse(args[2:]); e != nil {
			return e
		}
		if f.NArg() != 0 {
			return fmt.Errorf("unexpected prune arguments")
		}
		v, e := batchvault.Prune(args[1], *previous, *days, *apply)
		if e == nil {
			batchOut(v)
		}
		return e
	default:
		return fmt.Errorf("unknown batch command %s", args[0])
	}
}
func runBatchCLI() {
	if err := batchCommand(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "BLOCK:", err)
		os.Exit(1)
	}
	os.Exit(0)
}
