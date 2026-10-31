package main

import (
	"strings"
	"testing"
)

func TestVersionInfoShowsBuildAndRealConnectorCapability(t *testing.T) {
	info := versionInfo()
	for _, required := range []string{"Allocation Core", "Revision:", "MySQL driver:"} {
		if !strings.Contains(info, required) {
			t.Fatalf("version missing %s: %q", required, info)
		}
	}
}
