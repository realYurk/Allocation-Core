package multi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExcessPrecisionReturnsPolicyAdviceWithoutRounding(t *testing.T) {
	dir := preflightFixture(t)
	file := filepath.Join(dir, "amounts.csv")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	record := strings.Split(lines[1], ",")
	record[len(record)-2] = "1.123456789"
	lines[1] = strings.Join(record, ",")
	changed := strings.Join(lines, "\n")
	if err = os.WriteFile(file, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	report := InspectProject(context.Background(), dir)
	if report.Status != "BLOCK" {
		t.Fatal("excess precision accepted")
	}
	found := false
	for _, f := range report.Findings {
		if strings.Contains(f.Title, "小数位") {
			found = true
			if !strings.Contains(f.NextStep, "不能直接截断") || !strings.Contains(f.NextStep, "保留原件") {
				t.Fatal("missing lossless policy advice")
			}
		}
	}
	if !found {
		t.Fatalf("precision advice missing: %+v", report.Findings)
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != changed {
		t.Fatal("inspection altered source to pass validation")
	}
}

func TestOverflowAdviceDoesNotSuggestChangingOriginalMoney(t *testing.T) {
	title, next := businessAdvice("amount overflow")
	if !strings.Contains(title, "容量") || !strings.Contains(next, "不能") || !strings.Contains(next, "数据契约") {
		t.Fatal("overflow advice does not explain the contract")
	}
}
