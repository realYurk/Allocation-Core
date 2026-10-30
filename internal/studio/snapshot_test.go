package studio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyStudioSnapshotFrozenInputs(t *testing.T) {
	src := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(src, 0700); err != nil {
		t.Fatal(err)
	}
	rule := `{"name":"example","dimensions":["account","sku"],"precision":2,"datasets":{"amounts":{"base":"data.csv"},"drivers":{"d":"driver.csv"}},"stages":[{"id":"one"}]}`
	for name, value := range map[string]string{"rule.json": rule, "data.csv": "account,sku,amount\na,x,100.00\n", "driver.csv": "sku,weight\nx,1\n"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	out, saved, manifest, err := snapshotLegacyCase(context.Background(), src, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(out)
	if string(saved) != rule || len(manifest) != 3 {
		t.Fatalf("bad snapshot: %#v", manifest)
	}
	if err = os.WriteFile(filepath.Join(src, "data.csv"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshotData, err := os.ReadFile(filepath.Join(out, "data.csv"))
	if err != nil || !strings.Contains(string(snapshotData), "100.00") {
		t.Fatalf("live edit leaked into snapshot: %s: %v", snapshotData, err)
	}
}
func TestLegacyStudioSnapshotCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := snapshotLegacyCase(ctx, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("invalid project unexpectedly accepted")
	}
}
