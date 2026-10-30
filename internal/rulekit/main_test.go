package rulekit

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
)

func fixture() ([]byte, []byte) {
	m := []byte(`{"schema":"generic-allocation-table/1","name":"generic","precision":2,"dimensions":["period","account","item"],"immutable_dimensions":["period","account"],"datasets":{"amounts":{"expenses":"amounts.csv"},"drivers":{"units":"units.csv"}}}`)
	r := []string{"s1", `[{"from":"expenses","share":"1"}]`, `{"period":"AS_IS","account":"AS_IS","item":"TOTAL"}`, `{}`, `{"period":{"mode":"SOURCE"},"account":{"mode":"SOURCE"},"item":{"mode":"ALL"}}`, `["period","account"]`, "units", "1", "units", "units", "REJECT", `{}`}
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Write([]string{"stage_id", "inputs_json", "grouping_json", "filters_json", "target_json", "conservation_keys_json", "allocation_id", "share", "driver_dataset", "weight_field", "negative_driver_policy", "driver_filters_json"})
	w.Write(r)
	w.Flush()
	return m, []byte(b.String())
}
func TestValidGenericTable(t *testing.T) {
	m, s := fixture()
	out, e := Convert(m, s)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if json.Unmarshal(out, &v) != nil || v["schema"] != "allocation-ir/2.0" {
		t.Fatal(string(out))
	}
}
func TestRejectUnknownDriver(t *testing.T) {
	m, s := fixture()
	s = []byte(strings.Replace(string(s), ",units,units,REJECT,", ",missing,units,REJECT,", 1))
	if _, e := Convert(m, s); e == nil {
		t.Fatal("unknown driver accepted")
	}
}
func TestRejectWrongShare(t *testing.T) {
	m, s := fixture()
	s = []byte(strings.Replace(string(s), ",1,units,", ",0.9,units,", 1))
	if _, e := Convert(m, s); e == nil {
		t.Fatal("non-conserving fractions accepted")
	}
}
func TestRejectPathEscape(t *testing.T) {
	m, s := fixture()
	m = []byte(strings.Replace(string(m), "units.csv", "../outside.csv", 1))
	if _, e := Convert(m, s); e == nil {
		t.Fatal("path escape accepted")
	}
}

func TestExtractLosslessStageJSON(t *testing.T) {
	input := []byte(`{"schema":"allocation-ir/2.0","name":"sample","precision":2,"dimensions":["period","account","item"],"immutable_dimensions":["period","account"],"datasets":{"amounts":{"expenses":"amounts.csv"},"drivers":{}},"stages":[{"id":"pass","operation":"PASS","enabled":false,"inputs":[{"from":"expenses","share":"1"}],"source":{"grouping":{"period":"AS_IS","account":"AS_IS","item":"TOTAL"},"filters":{}},"target":{"period":{"mode":"SOURCE"},"account":{"mode":"SOURCE"},"item":{"mode":"ELEMENT","value":"X"}},"conservation_keys":["period","account"]}]}`)
	m, s, e := Extract(input)
	if e != nil {
		t.Fatal(e)
	}
	out, e := Convert(m, s)
	if e != nil {
		t.Fatal(e)
	}
	var a, b map[string]any
	json.Unmarshal(input, &a)
	json.Unmarshal(out, &b)
	x, _ := json.Marshal(a["stages"])
	y, _ := json.Marshal(b["stages"])
	if string(x) != string(y) {
		t.Fatalf("stage changed %s => %s", x, y)
	}
}
