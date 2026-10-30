// Generates synthetic DEMO data locally. No data files are committed to source control.
package demo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type example struct{ Path, Text string }

var examples = []example{
	{"examples/cosmetics/amounts.csv", "period,region,brand,sku,account,currency,amount\n2026-01,华东,品牌甲,Unassigned,Cost-B,CUR,1000.00\n"},
	{"examples/cosmetics/sales.csv", "period,region,brand,sku,units\n2026-01,华东,品牌甲,Item-A,30\n2026-01,华东,品牌甲,Item-B,70\n2026-01,华东,品牌甲,试用品,900\n"},
	{"examples/split_merge/revenue.csv", "period,department,sku,revenue\n2026-01,SharedPool,Item-A,5\n2026-01,SharedPool,Item-B,5\n"},
	{"examples/split_merge/material.csv", "period,department,sku,material\n2026-01,SharedPool,Item-A,8\n2026-01,SharedPool,Item-B,2\n"},
	{"examples/split_merge/amounts.csv", "period,department,sku,account,currency,amount\n2026-01,SharedPool,Unassigned,Cost-C,CUR,1000.00\n"},
	{"examples/triple_driver/revenue.csv", "period,department,sku,revenue\n2026-01,SharedPool,Item-A,5\n2026-01,SharedPool,Item-B,5\n"},
	{"examples/triple_driver/material.csv", "period,department,sku,material\n2026-01,SharedPool,Item-A,8\n2026-01,SharedPool,Item-B,2\n"},
	{"examples/triple_driver/orders.csv", "period,department,sku,orders\n2026-01,SharedPool,Item-A,1\n2026-01,SharedPool,Item-B,4\n"},
	{"examples/triple_driver/amounts.csv", "period,department,sku,account,currency,amount\n2026-01,SharedPool,Unassigned,Cost-C,CUR,1000.00\n"},
	{"examples/manufacturing/labor.csv", "period,plant,family,hours\n2026-01,工厂甲,系列甲,1\n2026-01,工厂甲,系列乙,2\n"},
	{"examples/manufacturing/amounts.csv", "period,plant,brand,family,sku,account,currency,amount,row_id\n2026-01,工厂甲,Unassigned,Unassigned,Unassigned,Cost-A,CUR,1200.00,EXP-001\n"},
	{"examples/manufacturing/sales.csv", "period,plant,family,sku,units\n2026-01,工厂甲,系列甲,Item-A,1\n2026-01,工厂甲,系列甲,Item-B,3\n2026-01,工厂甲,系列乙,商品C,2\n2026-01,工厂甲,系列乙,商品D,2\n"},
	{"examples/dual_driver/revenue.csv", "period,department,sku,revenue\n2026-01,SharedPool,Item-A,5\n2026-01,SharedPool,Item-B,5\n"},
	{"examples/dual_driver/material.csv", "period,department,sku,material\n2026-01,SharedPool,Item-A,8\n2026-01,SharedPool,Item-B,2\n"},
	{"examples/dual_driver/amounts.csv", "period,department,sku,account,currency,amount\n2026-01,SharedPool,Unassigned,Cost-C,CUR,1000.00\n"},
	{"examples/cosmetics/amounts.csv", "period,region,brand,sku,account,currency,amount\n2026-01,华东,品牌甲,Unassigned,营销费,CUR,1000.00\n"},
	{"examples/cosmetics/sales.csv", "period,region,brand,sku,units\n2026-01,华东,品牌甲,Item-A,30\n2026-01,华东,品牌甲,Item-B,70\n2026-01,华东,品牌甲,试用品,900\n"},
	{"examples/split_merge/revenue.csv", "period,department,sku,revenue\n2026-01,SharedPool,Item-A,5\n2026-01,SharedPool,Item-B,5\n"},
	{"examples/split_merge/material.csv", "period,department,sku,material\n2026-01,SharedPool,Item-A,8\n2026-01,SharedPool,Item-B,2\n"},
	{"examples/split_merge/amounts.csv", "period,department,sku,account,currency,amount\n2026-01,SharedPool,Unassigned,Cost-C,CUR,1000.00\n"},
	{"examples/triple_driver/revenue.csv", "period,department,sku,revenue\n2026-01,SharedPool,Item-A,5\n2026-01,SharedPool,Item-B,5\n"},
	{"examples/triple_driver/material.csv", "period,department,sku,material\n2026-01,SharedPool,Item-A,8\n2026-01,SharedPool,Item-B,2\n"},
	{"examples/triple_driver/orders.csv", "period,department,sku,orders\n2026-01,SharedPool,Item-A,1\n2026-01,SharedPool,Item-B,4\n"},
	{"examples/triple_driver/amounts.csv", "period,department,sku,account,currency,amount\n2026-01,SharedPool,Unassigned,Cost-C,CUR,1000.00\n"},
	{"examples/multi_account/labor.csv", "period,plant,family,hours\n2026-01,工厂甲,系列甲,1\n2026-01,工厂甲,系列乙,2\n"},
	{"examples/multi_account/amounts.csv", "period,plant,brand,family,sku,account,currency,amount,row_id\n2026-01,工厂甲,Unassigned,Unassigned,Unassigned,Cost-A,CUR,1200.00,SRC-ENERGY\n2026-01,工厂甲,Unassigned,Unassigned,Unassigned,Cost-C,CUR,800.00,SRC-ADMIN\n2026-01,工厂甲,Unassigned,Unassigned,Unassigned,Cost-D,CUR,600.00,SRC-MAINT\n2026-01,工厂甲,Unassigned,Unassigned,Unassigned,Cost-B,CUR,400.00,SRC-LOGISTIC\n"},
	{"examples/multi_account/sales.csv", "period,plant,family,sku,units\n2026-01,工厂甲,系列甲,Item-A,1\n2026-01,工厂甲,系列甲,Item-B,3\n2026-01,工厂甲,系列乙,商品C,2\n2026-01,工厂甲,系列乙,商品D,2\n"},
	{"examples/manufacturing/labor.csv", "period,plant,family,hours\n2026-01,工厂甲,系列甲,1\n2026-01,工厂甲,系列乙,2\n"},
	{"examples/manufacturing/amounts.csv", "period,plant,brand,family,sku,account,currency,amount,row_id\n2026-01,工厂甲,Unassigned,Unassigned,Unassigned,Cost-A,CUR,1200.00,EXP-001\n"},
	{"examples/manufacturing/sales.csv", "period,plant,family,sku,units\n2026-01,工厂甲,系列甲,Item-A,1\n2026-01,工厂甲,系列甲,Item-B,3\n2026-01,工厂甲,系列乙,商品C,2\n2026-01,工厂甲,系列乙,商品D,2\n"},
	{"examples/dual_driver/revenue.csv", "period,department,sku,revenue\n2026-01,SharedPool,Item-A,5\n2026-01,SharedPool,Item-B,5\n"},
	{"examples/dual_driver/material.csv", "period,department,sku,material\n2026-01,SharedPool,Item-A,8\n2026-01,SharedPool,Item-B,2\n"},
	{"examples/dual_driver/amounts.csv", "period,department,sku,account,currency,amount\n2026-01,SharedPool,Unassigned,Cost-C,CUR,1000.00\n"},
	{"testdata/multi_sample/overrides.csv", "account,stage,allocation,driver_dataset,weight_field,share\nCost-D,双因子分摊,material,,,0.50\nCost-D,双因子分摊,revenue,,,0.50\n"},
	{"testdata/multi_sample/assignments.csv", "account,template\nCost-A,material.json\nCost-B,revenue.json\nCost-C,dual.json\nCost-D,dual.json\n"},
	{"testdata/multi_sample/amounts.csv", "period,department,sku,account,currency,amount,row_id\n2026-01,SharedPool,Unassigned,Cost-A,CUR,1200.00,E1\n2026-01,SharedPool,Unassigned,Cost-B,CUR,800.00,L1\n2026-01,SharedPool,Unassigned,Cost-C,CUR,600.00,M1\n2026-01,SharedPool,Unassigned,Cost-D,CUR,400.00,D1\n"},
	{"testdata/multi_sample/drivers/revenue.csv", "period,department,sku,revenue\n2026-01,SharedPool,Item-A,5\n2026-01,SharedPool,Item-B,5\n"},
	{"testdata/multi_sample/drivers/material.csv", "period,department,sku,material\n2026-01,SharedPool,Item-A,8\n2026-01,SharedPool,Item-B,2\n"},
}

func Generate(root string) (int, error) {
	count := 0
	for _, x := range examples {
		p := filepath.Join(root, filepath.FromSlash(x.Path))
		if _, e := os.Stat(p); e == nil {
			continue
		}
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			return count, e
		}
		if e := os.WriteFile(p, []byte(x.Text), 0600); e != nil {
			return count, e
		}
		count++
	}
	if strings.TrimSpace(root) == "" {
		return count, fmt.Errorf("empty root")
	}
	return count, nil
}

// GenerateScale creates distinct synthetic per-account rule configurations on demand.
// It avoids shipping 1,000 generated rule files in the repository.
func GenerateScale(root string, count int) error {
	if count < 1 || count > 10000 {
		return fmt.Errorf("accounts must be 1..10000")
	}
	base := filepath.Join(root, "testdata", "scale_1000")
	if err := os.MkdirAll(filepath.Join(base, "templates"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(base, "drivers"), 0700); err != nil {
		return err
	}
	sampleDir := filepath.Join(root, "testdata", "multi_sample")
	for _, name := range []string{"material.csv", "revenue.csv"} {
		b, err := os.ReadFile(filepath.Join(sampleDir, "drivers", name))
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(base, "drivers", name), b, 0600); err != nil {
			return err
		}
	}
	b, err := os.ReadFile(filepath.Join(sampleDir, "templates", "dual.json"))
	if err != nil {
		return err
	}
	var rule map[string]any
	if err = json.Unmarshal(b, &rule); err != nil {
		return err
	}
	stages := rule["stages"].([]any)
	allocations := stages[0].(map[string]any)["allocations"].([]any)
	var assignments, amounts strings.Builder
	assignments.WriteString("account,template\n")
	amounts.WriteString("period,department,sku,account,currency,amount,row_id\n")
	for i := 0; i < count; i++ {
		account := fmt.Sprintf("ACC%04d", i)
		template := fmt.Sprintf("rule%04d.json", i)
		// Shares are distinct for every account while still summing to 1 exactly.
		first := 1000 + i // 0.1000 through 0.1999 for 1,000 accounts
		if first > 9999 {
			first = i%9000 + 1000
		}
		second := 10000 - first
		allocations[0].(map[string]any)["share"] = fmt.Sprintf("0.%04d", first)
		allocations[1].(map[string]any)["share"] = fmt.Sprintf("0.%04d", second)
		rule["name"] = account
		data, err := json.MarshalIndent(rule, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(base, "templates", template), data, 0600); err != nil {
			return err
		}
		fmt.Fprintf(&assignments, "%s,%s\n", account, template)
		fmt.Fprintf(&amounts, "2026-01,SharedPool,Unassigned,%s,CUR,1000.00,ROW%04d\n", account, i)
	}
	for name, content := range map[string]string{
		"assignments.csv": assignments.String(),
		"amounts.csv":     amounts.String(),
		"overrides.csv":   "account,stage,allocation,driver_dataset,weight_field,share\n",
	} {
		if err = os.WriteFile(filepath.Join(base, name), []byte(content), 0600); err != nil {
			return err
		}
	}
	config := map[string]any{"amounts": "amounts.csv", "assignments": "assignments.csv", "overrides": "overrides.csv", "templates": "templates", "drivers": "drivers", "workers": 2, "jobs": 4, "disk_partitions": 8, "driver_disk_index": true, "evidence_gzip": true, "keep_group_outputs": true, "timeout_seconds": 600}
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(base, "project.json"), content, 0600)
}
