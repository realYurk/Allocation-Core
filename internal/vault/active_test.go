package vault

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func officialFixture(t *testing.T, base, vaultRoot, scenario, version, sku string) Record {
	t.Helper()
	dir := fixture(t, base, sku)
	r, err := Register(vaultRoot, scenario, version, "official", dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestActivePublishCASAndRollback(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "vault")
	a := officialFixture(t, base, root, "Scenario_A", "Version_A", "A")
	b := officialFixture(t, base, root, "Scenario_A", "Version_A", "B")
	draftDir := fixture(t, base, "D")
	draft, e := Register(root, "Scenario_A", "Version_A", "draft", draftDir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Active(root, "Scenario_A", "Version_A"); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("expected missing pointer: %v", e)
	}
	if _, e = Publish(root, "Scenario_A", "Version_A", a.ID, ""); e == nil {
		t.Fatal("missing lease accepted")
	}
	if _, e = Publish(root, "Scenario_A", "Version_A", draft.ID, "none"); e == nil {
		t.Fatal("draft published")
	}
	current, e := Publish(root, "Scenario_A", "Version_A", a.ID, "none")
	if e != nil {
		t.Fatal(e)
	}
	if current.RunID != a.ID || current.PreviousRunID != "" {
		t.Fatalf("wrong initial pointer %+v", current)
	}
	if _, e = Publish(root, "Scenario_A", "Version_A", b.ID, "none"); e == nil {
		t.Fatal("stale none lease succeeded")
	}
	if _, e = Publish(root, "Scenario_A", "Version_A", b.ID, "incorrect"); e == nil {
		t.Fatal("stale wrong lease succeeded")
	}
	next, e := Publish(root, "Scenario_A", "Version_A", b.ID, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if next.RunID != b.ID || next.PreviousRunID != a.ID {
		t.Fatalf("wrong newer pointer %+v", next)
	}
	got, e := Active(root, "Scenario_A", "Version_A")
	if e != nil || got.RunID != b.ID {
		t.Fatalf("bad active %+v / %v", got, e)
	}
	back, e := Publish(root, "Scenario_A", "Version_A", a.ID, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	if back.RunID != a.ID || back.PreviousRunID != b.ID {
		t.Fatalf("rollback not supported %+v", back)
	}
	other := officialFixture(t, base, root, "Scenario_B", "Version_A", "C")
	if _, e = Publish(root, "Scenario_A", "Version_A", other.ID, a.ID); e == nil {
		t.Fatal("cross-scenario publish succeeded")
	}
}
func TestOfficialPruneProtectsCurrentHistoryDraftAndSharedObjects(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "vault")
	a := officialFixture(t, base, root, "S", "V", "A")
	b := officialFixture(t, base, root, "S", "V", "B")
	c := officialFixture(t, base, root, "S", "V", "C")
	d, err := Register(root, "S", "V", "draft", fixture(t, base, "D"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PruneOfficials(root, 1, 0, true); err == nil {
		t.Fatal("deleted officials without explicit active pointer")
	}
	if _, err = Publish(root, "S", "V", c.ID, "none"); err != nil {
		t.Fatal(err)
	}
	preview, err := PruneOfficials(root, 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.DryRun || preview.ProtectedActive != 1 || preview.ProtectedHistory != 1 || preview.ProtectedDrafts != 1 || len(preview.RemoveIDs) != 1 || preview.RemoveIDs[0] != a.ID {
		t.Fatalf("bad prune preview %+v", preview)
	}
	if err = Verify(root, a.ID); err != nil {
		t.Fatalf("dry-run deleted %s: %v", a.ID, err)
	}
	plan, err := PruneOfficials(root, 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.DryRun || len(plan.RemoveIDs) != 1 {
		t.Fatalf("bad apply %+v", plan)
	}
	if _, err = getRecord(root, a.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pruned historical record still present: %v", err)
	}
	for _, id := range []string{b.ID, c.ID, d.ID} {
		if err = Verify(root, id); err != nil {
			t.Fatalf("retained %s broken: %v", id, err)
		}
	}
	if _, err = Publish(root, "S", "V", b.ID, c.ID); err != nil {
		t.Fatalf("previous kept for rollback: %v", err)
	}
	// A new official record reusing common immutable objects does not inflate stores.
	same := officialFixture(t, base, root, "S", "V", "B")
	if err = Verify(root, same.ID); err != nil {
		t.Fatal(err)
	}
}
func TestOfficialPruneRefusesTamperedPointerAndPreservesAllRuns(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "vault")
	a := officialFixture(t, base, root, "S", "V", "A")
	b := officialFixture(t, base, root, "S", "V", "B")
	if _, e := Publish(root, "S", "V", b.ID, "none"); e != nil {
		t.Fatal(e)
	}
	path := activePath(root, "S", "V")
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var p ActivePointer
	if e = json.Unmarshal(original, &p); e != nil {
		t.Fatal(e)
	}
	p.RunID = "nonexistent"
	corrupt, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, corrupt, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = PruneOfficials(root, 0, 0, true); e == nil {
		t.Fatal("tampered pointer allowed prune")
	}
	if e = Verify(root, a.ID); e != nil {
		t.Fatalf("failed closed, but deleted old run: %v", e)
	}
	if e = os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = PruneOfficials(root, 0, 0, false); e != nil {
		t.Fatal(e)
	}
}
func TestOfficialPruneNeverDeletesActiveEvenKeepZero(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "vault")
	a := officialFixture(t, base, root, "S", "V", "A")
	b := officialFixture(t, base, root, "S", "V", "B")
	if _, e := Publish(root, "S", "V", a.ID, "none"); e != nil {
		t.Fatal(e)
	}
	plan, e := PruneOfficials(root, 0, 0, true)
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.RemoveIDs) != 1 || plan.RemoveIDs[0] != b.ID {
		t.Fatalf("unexpected %+v", plan)
	}
	if e = Verify(root, a.ID); e != nil {
		t.Fatal(e)
	}
}
func TestConcurrentCompareAndSwapOneWinner(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "vault")
	a := officialFixture(t, base, root, "S", "V", "A")
	b := officialFixture(t, base, root, "S", "V", "B")
	c := officialFixture(t, base, root, "S", "V", "C")
	if _, e := Publish(root, "S", "V", a.ID, "none"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{b.ID, c.ID} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); _, e := Publish(root, "S", "V", id, a.ID); results <- e }(id)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("CAS must allow exactly one winner, got %d", success)
	}
}
