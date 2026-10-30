package multi

import (
	"allocationcore/internal/multi/batchvault"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchVaultCompleteReleaseRollbackAndRetention(t *testing.T) {
	project, engine := fixture(t)
	base := t.TempDir()
	vault := filepath.Join(base, "batch-vault")
	first := filepath.Join(base, "run-1")
	second := filepath.Join(base, "run-2")
	for _, out := range []string{first, second} {
		res, err := Run(project, out, engine)
		if err != nil {
			t.Fatal(err)
		}
		if res.Accounts != 4 || res.Groups != 4 {
			t.Fatalf("bad run %v", res)
		}
	}
	a, err := batchvault.Register(vault, "Scenario_A", "Version_A", first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := batchvault.Register(vault, "Scenario_A", "Version_A", second)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Files) < 15 {
		t.Fatalf("archive does not include group evidence: %d", len(a.Files))
	}
	if _, err = batchvault.Publish(vault, "Scenario_A", "Version_A", a.ID, "none"); err != nil {
		t.Fatal(err)
	}
	if _, err = batchvault.Publish(vault, "Scenario_A", "Version_A", b.ID, "none"); err == nil {
		t.Fatal("accepted stale initial publication")
	}
	if _, err = batchvault.Publish(vault, "Scenario_A", "Version_A", b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	p, err := batchvault.Active(vault, "Scenario_A", "Version_A")
	if err != nil || p.RunID != b.ID {
		t.Fatalf("active mismatch %+v %v", p, err)
	}
	if _, err = batchvault.Publish(vault, "Scenario_A", "Version_A", a.ID, b.ID); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	restore := filepath.Join(base, "restored")
	if err = batchvault.Restore(vault, a.ID, restore); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"results.csv", "run_summary.json", "groups", "snapshots", "configuration"} {
		if _, err = os.Stat(filepath.Join(restore, key)); err != nil {
			t.Fatalf("missing restored %s: %v", key, err)
		}
	}
	plan, err := batchvault.Prune(vault, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RemoveIDs) != 1 || plan.RemoveIDs[0] != b.ID {
		t.Fatalf("expected only historical version prune: %+v", plan)
	}
	if err = batchvault.Verify(vault, b.ID); err != nil {
		t.Fatal(err)
	} // dry-run must not delete
	plan, err = batchvault.Prune(vault, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = batchvault.Verify(vault, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = batchvault.Verify(vault, b.ID); err == nil {
		t.Fatal("historical pruned batch remains")
	}
	if _, err = batchvault.Active(vault, "Scenario_A", "Version_A"); err != nil {
		t.Fatal(err)
	}
}
func TestBatchVaultCorruptEvidenceFailsClosed(t *testing.T) {
	project, engine := fixture(t)
	base := t.TempDir()
	run := filepath.Join(base, "run")
	_, err := Run(project, run, engine)
	if err != nil {
		t.Fatal(err)
	}
	v := filepath.Join(base, "vault")
	r, err := batchvault.Register(v, "Scenario_A", "Version_A", run)
	if err != nil {
		t.Fatal(err)
	}
	ref := r.Files["results.csv"]
	p := filepath.Join(v, "objects", ref.SHA256[:2], ref.SHA256+".raw")
	if err = os.WriteFile(p, []byte("CORRUPTED"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = batchvault.Publish(v, "Scenario_A", "Version_A", r.ID, "none"); err == nil {
		t.Fatal("corrupt archive published")
	}
	if _, err = batchvault.Active(v, "Scenario_A", "Version_A"); !os.IsNotExist(err) {
		t.Fatalf("corrupted archive changed pointer: %v", err)
	}
}
func TestBatchVaultRefusesPartialOrTamperedBusinessOutcome(t *testing.T) {
	project, engine := fixture(t)
	base := t.TempDir()
	run := filepath.Join(base, "run")
	_, err := Run(project, run, engine)
	if err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(run, "results.csv")
	b, err := os.ReadFile(evidence)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(b), ",160.00", ",161.00", 1)
	if changed == string(b) {
		t.Fatal("golden sample amount changed unexpectedly")
	}
	if err = os.WriteFile(evidence, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = batchvault.Register(filepath.Join(base, "vault"), "Scenario_A", "Version_A", run); err == nil {
		t.Fatal("changed per-account result was archived")
	}
}
func TestBatchVaultRejectsUnexpectedFileAndSymlink(t *testing.T) {
	project, engine := fixture(t)
	base := t.TempDir()
	run := filepath.Join(base, "run")
	_, err := Run(project, run, engine)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(run, "unexpected.exe"), []byte("hi"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = batchvault.Register(filepath.Join(base, "vault"), "S", "V", run); err == nil {
		t.Fatal("unexpected file archived")
	}
}

func TestBatchVaultGCPreviewAndFailClosedCorruptPointer(t *testing.T) {
	project, engine := fixture(t)
	base := t.TempDir()
	out := filepath.Join(base, "run")
	if _, err := Run(project, out, engine); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "vault")
	record, err := batchvault.Register(root, "F", "V", out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = batchvault.Publish(root, "F", "V", record.ID, "none"); err != nil {
		t.Fatal(err)
	}
	bytes := []byte("orphaned-blob")
	h := sha256.Sum256(bytes)
	sha := hex.EncodeToString(h[:])
	path := filepath.Join(root, "objects", sha[:2], sha+".raw")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := batchvault.GC(root, false)
	if err != nil || plan.OrphanObjects != 1 || !plan.DryRun {
		t.Fatalf("dry-run GC: %+v %v", plan, err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("dry-run removed object")
	}
	entries, err := os.ReadDir(filepath.Join(root, "active"))
	if err != nil {
		t.Fatal(err)
	}
	pointer := filepath.Join(root, "active", entries[0].Name())
	raw, err := os.ReadFile(pointer)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(pointer, []byte("{bad-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = batchvault.GC(root, true); err == nil {
		t.Fatal("GC accepted malformed active pointer")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("unsafe GC removed orphan while pointer malformed")
	}
	if err = os.WriteFile(pointer, raw, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err = batchvault.GC(root, true)
	if err != nil || plan.OrphanObjects != 1 {
		t.Fatalf("gc: %+v %v", plan, err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("GC failed to remove orphan")
	}
	if err = batchvault.Verify(root, record.ID); err != nil {
		t.Fatal("GC damaged registered batch", err)
	}
}
func TestBatchVaultConcurrentCASOnlyOneWinner(t *testing.T) {
	project, engine := fixture(t)
	base := t.TempDir()
	out := filepath.Join(base, "run")
	if _, err := Run(project, out, engine); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "vault")
	a, err := batchvault.Register(root, "FC", "V", out)
	if err != nil {
		t.Fatal(err)
	}
	b, err := batchvault.Register(root, "FC", "V", out)
	if err != nil {
		t.Fatal(err)
	}
	c, err := batchvault.Register(root, "FC", "V", out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = batchvault.Publish(root, "FC", "V", a.ID, "none"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, id := range []string{b.ID, c.ID} {
		go func(id string) { _, err := batchvault.Publish(root, "FC", "V", id, a.ID); results <- err }(id)
	}
	x, y := <-results, <-results
	if (x == nil) == (y == nil) {
		t.Fatalf("expected exactly one CAS winner, got %v / %v", x, y)
	}
	p, err := batchvault.Active(root, "FC", "V")
	if err != nil {
		t.Fatal(err)
	}
	if p.RunID != b.ID && p.RunID != c.ID {
		t.Fatalf("unexpected winner %s", p.RunID)
	}
}
