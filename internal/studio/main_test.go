package studio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaseIDRejectsTraversal(t *testing.T) {
	a := &Application{home: t.TempDir(), custom: t.TempDir()}
	for _, id := range []string{"../secret", "..\\secret", "/etc/passwd", "user_../../etc", "space name", "", "a/b", "a\\b"} {
		if _, _, err := a.locate(id); err == nil {
			t.Fatalf("unsafe id accepted: %q", id)
		}
	}
}
func TestSourceCSVUTF8AndLimit(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "data.csv")
	text := "\ufeffaccount,amount,row_id\nCost-A,12.30,ONE\nCost-C,1.20,TWO\n"
	if err := os.WriteFile(f, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := readCSV(f, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["account"] != "Cost-A" || rows[0]["amount"] != "12.30" {
		t.Fatalf("unexpected csv: %#v", rows)
	}
	rows, err = readCSV(f, 4)
	if err != nil || len(rows) != 2 {
		t.Fatalf("incorrect line count %v %v", len(rows), err)
	}
}
func TestInputsCannotEscapeCaseDirectory(t *testing.T) {
	root := t.TempDir()
	cases := filepath.Join(root, "examples")
	if err := os.MkdirAll(filepath.Join(cases, "safe"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "private.csv"), []byte("a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rule := `{"name":"example","datasets":{"amounts":{"data":"../../private.csv"}},"stages":[{"id":"step"}]}`
	if err := os.WriteFile(filepath.Join(cases, "safe", "rule.json"), []byte(rule), 0600); err != nil {
		t.Fatal(err)
	}
	a := &Application{home: root, data: root, custom: t.TempDir()}
	_, _, _, err := a.readCase("safe")
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected dataset containment failure: %v", err)
	}
}
