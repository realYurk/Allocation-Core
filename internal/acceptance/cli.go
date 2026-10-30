package acceptance

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// CLI compares a previously published result file against an independently
// supplied reference. No automatic column guesses beyond amount/value and
// the documented engine provenance columns are made.
func CLI() {
	flags := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	actual := flags.String("actual", "", "published results.csv")
	reference := flags.String("reference", "", "reference CSV with identical business coordinate dimensions")
	out := flags.String("out", "", "new local private report directory")
	precision := flags.Int("precision", 2, "fixed decimal precision 0..6")
	tolerance := flags.String("tolerance", "0", "absolute difference tolerance")
	if e := flags.Parse(os.Args[2:]); e != nil {
		os.Exit(2)
	}
	if *actual == "" || *reference == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: AllocationCore reconcile --actual results.csv --reference reference.csv --out report_dir [--precision 2] [--tolerance 0]")
		os.Exit(2)
	}
	r, e := Compare(context.Background(), *actual, *reference, *out, Options{Precision: *precision, Tolerance: *tolerance})
	if e != nil {
		fmt.Fprintln(os.Stderr, "RECONCILIATION BLOCK:", e)
		os.Exit(1)
	}
	raw, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(raw))
	if r.Status != "MATCH" {
		os.Exit(3)
	}
}
