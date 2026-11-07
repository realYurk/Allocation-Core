package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExternalFacadeUsesCore(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "examples", "manufacturing", "rule.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSpec(b)
	if err != nil {
		t.Fatal(err)
	}
	if err = Validate(s); err != nil {
		t.Fatal(err)
	}
	if _, err = ParseSpec([]byte(`{"unknown":true}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
}
