package multi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preflightFixture(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "multi_sample")
	dst := filepath.Join(t.TempDir(), "project")
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, e := filepath.Rel(src, path)
		if e != nil {
			return e
		}
		dest := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(dest, raw, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}
func TestPreflightStreamingSuccessNoOutput(t *testing.T) {
	dir := preflightFixture(t)
	got := InspectProject(context.Background(), dir)
	if got.Status != "PASS" || got.Accounts != 4 || got.RuleGroups != 4 || got.InputAmount != "3000.00" || got.UniqueDriverFiles != 2 || got.TotalRows != 4 {
		t.Fatalf("unexpected report: %+v", got)
	}
	for _, d := range got.Drivers {
		if d.Error != "" || d.Rows == 0 || d.ReferencedBy == 0 {
			t.Fatalf("bad driver: %+v", d)
		}
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 6 {
		t.Fatalf("preflight wrote into the project: %v", items)
	}
}
func TestPreflightFailsOnMissingWeightColumn(t *testing.T) {
	dir := preflightFixture(t)
	path := filepath.Join(dir, "drivers", "material.csv")
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	data = []byte(strings.Replace(string(data), "material", "REMOVED_COLUMN", 1))
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	got := InspectProject(context.Background(), dir)
	if got.Status != "BLOCK" || len(got.Problems) == 0 || !strings.Contains(strings.Join(got.Problems, " "), "missing CSV column") {
		t.Fatalf("must detect invalid driver: %+v", got)
	}
}
func TestPreflightFailsOnMissingAssignedAccount(t *testing.T) {
	dir := preflightFixture(t)
	path := filepath.Join(dir, "amounts.csv")
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	data = []byte(strings.Replace(string(data), "Cost-D,", "Unassigned-99,", 1))
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	got := InspectProject(context.Background(), dir)
	if got.Status != "BLOCK" || !strings.Contains(strings.Join(got.Problems, " "), "no assigned rule") {
		t.Fatalf("must detect unknown account: %+v", got)
	}
}
func TestPreflightCancel(t *testing.T) {
	dir := preflightFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := InspectProject(ctx, dir)
	if got.Status != "BLOCK" {
		t.Fatalf("canceled preflight must block: %+v", got)
	}
}
