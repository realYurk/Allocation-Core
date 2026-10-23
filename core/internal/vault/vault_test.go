package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func file(t *testing.T, dir, name, data string) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T, root, sku string) string {
	t.Helper()
	p := filepath.Join(root, "sample-"+sku)
	if e := os.MkdirAll(p, 0700); e != nil {
		t.Fatal(e)
	}
	file(t, p, "run_summary.json", `{"status":"PASS","input_amount":"100.00","output_amount":"100.00"}`)
	file(t, p, "results.csv", "sku,amount\n"+sku+",100.00\n")
	file(t, p, "ledger.csv", "row,sku\n1,"+sku+"\n")
	file(t, p, "reconciliation.csv", "check,status\n1,PASS\n")
	file(t, p, "lineage_links.csv", "from,to\n1,2\n")
	return p
}
func TestVaultLifecycleAndDedupe(t *testing.T) {
	root := t.TempDir()
	v := filepath.Join(root, "vault")
	a := fixture(t, root, "A")
	r1, e := Register(v, "FC-10+2", "forecast-1", "draft", a)
	if e != nil {
		t.Fatal(e)
	}
	r2, e := Register(v, "FC-10+2", "forecast-2", "draft", a)
	if e != nil {
		t.Fatal(e)
	}
	r3, e := Register(v, "FC-10+2", "official-1", "official", a)
	if e != nil {
		t.Fatal(e)
	}
	st, e := StatsFor(v)
	if e != nil {
		t.Fatal(e)
	}
	if st.Runs != 3 || st.UniqueObjects >= st.Runs*len(r1.Files) || st.StoredBytes >= st.LogicalBytes {
		t.Fatalf("dedup/zip broken: %+v", st)
	}
	if e = Verify(v, r2.ID); e != nil {
		t.Fatal(e)
	}
	restore := filepath.Join(root, "restored")
	if e = Restore(v, r2.ID, restore); e != nil {
		t.Fatal(e)
	}
	for name := range r1.Files {
		a, e := os.ReadFile(filepath.Join(a, name))
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join(restore, name))
		if e != nil || string(a) != string(b) {
			t.Fatalf("restore mismatch %s", name)
		}
	}
	p, e := Prune(v, 1, 0, false)
	if e != nil {
		t.Fatal(e)
	}
	if !p.DryRun || len(p.RemoveIDs) != 1 || p.ProtectedOfficial != 1 {
		t.Fatalf("wrong preview: %+v", p)
	}
	p, e = Prune(v, 1, 0, true)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.RemoveIDs) != 1 {
		t.Fatal("prune did not select one")
	}
	if e = Verify(v, r3.ID); e != nil {
		t.Fatalf("official deleted: %v", e)
	}
	if _, e = StatsFor(v); e != nil {
		t.Fatal(e)
	}
	rel, e := ReleaseSource(v, r2.ID, a, false)
	if e != nil {
		t.Fatal(e)
	}
	if !rel.DryRun || rel.ReclaimableBytes == 0 {
		t.Fatal(rel)
	}
	_, e = ReleaseSource(v, r2.ID, a, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(a, "results.csv")); !os.IsNotExist(e) {
		t.Fatal("source outputs not released")
	}
	if _, e = os.Stat(filepath.Join(a, "run_summary.json")); e != nil {
		t.Fatal("summary should remain")
	}
	gc, e := GarbageCollect(v, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(gc.OrphanObjects) != 0 {
		t.Fatalf("shared objects mistaken for garbage: %+v", gc)
	}
	_ = r1
}
func TestVaultCorruptionFailsClosed(t *testing.T) {
	root := t.TempDir()
	v := filepath.Join(root, "vault")
	a := fixture(t, root, "A")
	r, e := Register(v, "FC-0+12", "x", "official", a)
	if e != nil {
		t.Fatal(e)
	}
	ref := r.Files["results.csv"]
	if e = os.WriteFile(objectPath(v, ref), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = Verify(v, r.ID); e == nil {
		t.Fatal("accepted corrupted object")
	}
	if e = Restore(v, r.ID, filepath.Join(root, "restore")); e == nil {
		t.Fatal("restored corrupted object")
	}
	if _, e = Register(v, "FC-0+12", "x2", "draft", a); e == nil {
		t.Fatal("reused corrupted blob")
	}
}
func TestUnsafeSourceAndIncompleteRejected(t *testing.T) {
	root := t.TempDir()
	a := fixture(t, root, "A")
	if _, e := Register(filepath.Join(root, "vault"), "FC-1+11", "v1", "trial", a); e == nil {
		t.Fatal("bad class accepted")
	}
	file(t, a, "run_summary.json", `{"status":"FAILED"}`)
	if _, e := Register(filepath.Join(root, "vault"), "FC-1+11", "v1", "draft", a); e == nil {
		t.Fatal("failed run accepted")
	}
	file(t, a, "run_summary.json", `{"status":"PASS"}`)
	if e := os.Remove(filepath.Join(a, "ledger.csv")); e != nil {
		t.Fatal(e)
	}
	if _, e := Register(filepath.Join(root, "vault"), "FC-1+11", "v1", "draft", a); e == nil {
		t.Fatal("missing ledger accepted")
	}
}
func TestGCOnlyDeletesUnreferenced(t *testing.T) {
	root := t.TempDir()
	v := filepath.Join(root, "vault")
	a := fixture(t, root, "A")
	r, e := Register(v, "S", "V", "official", a)
	if e != nil {
		t.Fatal(e)
	}
	fake := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	p := filepath.Join(v, "objects", "aa", fake+".raw")
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, []byte("orphan"), 0600); e != nil {
		t.Fatal(e)
	}
	preview, e := GarbageCollect(v, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(preview.OrphanObjects) != 1 {
		t.Fatalf("expected orphan: %+v", preview)
	}
	if _, e = os.Stat(p); e != nil {
		t.Fatal(e)
	}
	_, e = GarbageCollect(v, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("orphan not removed")
	}
	if e = Verify(v, r.ID); e != nil {
		t.Fatal(e)
	}
}
func TestTamperedSourceRejectedBeforeAnyDelete(t *testing.T) {
	root := t.TempDir()
	v := filepath.Join(root, "vault")
	a := fixture(t, root, "A")
	r, e := Register(v, "scenario", "version", "draft", a)
	if e != nil {
		t.Fatal(e)
	}
	file(t, a, "results.csv", "changed results")
	if _, e = ReleaseSource(v, r.ID, a, true); e == nil {
		t.Fatal("tampered source deleted")
	}
	if _, e = os.Stat(filepath.Join(a, "ledger.csv")); e != nil {
		t.Fatal("partial delete on verification failure")
	}
}
func TestCompressedObjectRoundTripExact(t *testing.T) {
	root := t.TempDir()
	v := filepath.Join(root, "vault")
	a := fixture(t, root, "A")
	big := strings.Repeat("记录A,100元,商品A\n", 1000)
	file(t, a, "ledger.csv.gz", big)
	r, e := Register(v, "S", "V", "draft", a)
	if e != nil {
		t.Fatal(e)
	}
	if r.Files["ledger.csv.gz"].Encoding != "raw" {
		t.Fatal("precompressed bytes reencoded")
	}
	if e = Restore(v, r.ID, filepath.Join(root, "recover")); e != nil {
		t.Fatal(e)
	}
	src, e := os.ReadFile(filepath.Join(a, "ledger.csv.gz"))
	if e != nil {
		t.Fatal(e)
	}
	dst, e := os.ReadFile(filepath.Join(root, "recover", "ledger.csv.gz"))
	if e != nil {
		t.Fatal(e)
	}
	h1 := sha256.Sum256(src)
	h2 := sha256.Sum256(dst)
	if hex.EncodeToString(h1[:]) != hex.EncodeToString(h2[:]) {
		t.Fatal("roundtrip failed")
	}
}
func TestExportDB(t *testing.T) {
	root := t.TempDir()
	v := filepath.Join(root, "vault")
	a := fixture(t, root, "A")
	file(t, a, "results.csv", "\ufeffterminal_stage,row_id,region,sku,amount\n最后一步,abc,华北,商品A,100.00\n")
	r, e := Register(v, "FC-12+0", "official", "official", a)
	if e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(root, "bulk.csv")
	n, e := ExportDB(v, r.ID, out)
	if e != nil {
		t.Fatal(e)
	}
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	b, e := os.ReadFile(out)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), `""region"":""华北""`) {
		t.Fatalf("missing JSON dimensions: %s", b)
	}
	if _, e = ExportDB(v, r.ID, out); e == nil {
		t.Fatal("overwrote existing CSV")
	}
}
func TestRejectFailedReconciliationEvenIfSummaryPass(t *testing.T) {
	root := t.TempDir()
	a := fixture(t, root, "A")
	file(t, a, "reconciliation.csv", "stage,delta,status\nS,1.00,FAIL\n")
	if _, e := Register(filepath.Join(root, "vault"), "S", "V", "official", a); e == nil {
		t.Fatal("failed reconciliation accepted")
	}
}
