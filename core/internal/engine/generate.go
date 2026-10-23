package engine

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Generate a new-company integration starter, NOT a replacement calculation engine.
func Generate(configPath, outdir string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	spec, err := ParseSpec(data)
	if err != nil {
		return err
	}
	if _, err = Compile(spec); err != nil {
		return err
	}
	if _, err = os.Stat(outdir); err == nil {
		return fail("generator destination exists: %s", outdir)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Join(outdir, "data"), 0755); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(outdir, "integrations"), 0755); err != nil {
		return err
	}
	newSpec := spec
	newSpec.Datasets.Amounts = map[string]string{}
	newSpec.Datasets.Drivers = map[string]string{}
	mapping := map[string]interface{}{"amounts": map[string]interface{}{}, "drivers": map[string]interface{}{}}
	for name := range spec.Datasets.Amounts {
		fn := "amount_" + name + ".csv"
		newSpec.Datasets.Amounts[name] = filepath.ToSlash(filepath.Join("data", fn))
		cols := append(append([]string{}, spec.Dimensions...), "amount", "row_id")
		if err := writeTemplate(filepath.Join(outdir, "data", fn), cols); err != nil {
			return err
		}
		fields := map[string]string{}
		for _, d := range cols {
			fields[d] = d
		}
		mapping["amounts"].(map[string]interface{})[name] = map[string]interface{}{"input_csv": "CHANGE_ME_SOURCE.csv", "output_csv": newSpec.Datasets.Amounts[name], "fields": fields}
	}
	for name := range spec.Datasets.Drivers {
		fn := "driver_" + name + ".csv"
		newSpec.Datasets.Drivers[name] = filepath.ToSlash(filepath.Join("data", fn))
		cols := append([]string{}, spec.Dimensions...)
		weights := map[string]bool{}
		for _, st := range spec.Stages {
			for _, a := range st.Allocations {
				if a.DriverDataset == name {
					weights[a.WeightField] = true
				}
			}
		}
		ws := make([]string, 0, len(weights))
		for w := range weights {
			ws = append(ws, w)
		}
		sort.Strings(ws)
		cols = append(cols, ws...)
		if err := writeTemplate(filepath.Join(outdir, "data", fn), cols); err != nil {
			return err
		}
		fields := map[string]string{}
		for _, d := range cols {
			fields[d] = d
		}
		mapping["drivers"].(map[string]interface{})[name] = map[string]interface{}{"input_csv": "CHANGE_ME_SOURCE.csv", "output_csv": newSpec.Datasets.Drivers[name], "fields": fields}
	}
	conf, _ := json.MarshalIndent(newSpec, "", "  ")
	if err = os.WriteFile(filepath.Join(outdir, "rule.json"), conf, 0644); err != nil {
		return err
	}
	mapJson, _ := json.MarshalIndent(mapping, "", "  ")
	if err = os.WriteFile(filepath.Join(outdir, "integrations", "column_mapping.json"), mapJson, 0644); err != nil {
		return err
	}
	bat := `@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
pushd "%~dp0"
if errorlevel 1 (echo ERROR: Cannot access the project folder. & pause & exit /b 1)
if not exist "logs" mkdir "logs"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" (echo ERROR: Copy the compiled Windows EXE into this folder. & pause & exit /b 1)
"%EXE%" validate "rule.json"
if errorlevel 1 (echo RULE VALIDATION FAILED & pause & exit /b 1)
set "OUT=runs\run_%RANDOM%_%RANDOM%"
"%EXE%" run "rule.json" "%OUT%" --workers 4 --disk-partitions 8 --driver-disk-index --driver-cache-dir "cache"
if errorlevel 1 (echo RUN FAILED & pause & exit /b 1)
echo.
echo PASS. Result folder: %OUT%
echo Excel CSV files: results.csv, ledger.csv, reconciliation.csv
pause
`
	// Keep generated file NAMES ASCII-only: Windows ZIP extractors may misdecode
	// UTF-8 Unicode entry names even though Chinese CSV contents are supported.
	bat = strings.ReplaceAll(bat, "\n", "\r\n")
	if err = os.WriteFile(filepath.Join(outdir, "Windows_Run_Allocation.bat"), []byte(bat), 0644); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(outdir, "integrations", "csv_mapping_adapter.py"), []byte(pythonTemplate), 0644); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(outdir, "integrations", "AllocationCLIClient.java"), []byte(javaTemplate), 0644); err != nil {
		return err
	}
	desc := fmt.Sprintf("# 新企业接入清单（自动生成）\n\n维度：%s\n\n节点：\n", strings.Join(spec.Dimensions, ", "))
	for _, st := range spec.Stages {
		desc += "\n- " + st.ID
		for _, e := range st.Inputs {
			desc += fmt.Sprintf(" ← %s（%s）", e.From, e.Share)
		}
	}
	desc += "\n\n## 操作\n\n1. 把 `data/` 下每个 CSV 的表头作为导出模板，填入真实的或已脱敏的业务数据。数据缺失时不能留空。\n2. `rule.json` 是唯一规则定义。编辑后运行 `alloc_windows_amd64.exe validate rule.json`。\n3. 把本软件包的 `alloc_windows_amd64.exe` 复制到此目录，双击 `Windows_Run_Allocation.bat`。\n4. `runs/run_*/results.csv` 是分摊结果，`runs/run_*/reconciliation.csv` 每个金额池守恒，`runs/run_*/ledger.csv` 是逐笔金额分配证据。\n5. 不要把输出直接覆盖正式财务系统。先在测试系统对账，确认科目、期间、商品口径正确。\n6. 如果上游字段名不同，编辑 `integrations/column_mapping.json`，运行 `python integrations/csv_mapping_adapter.py integrations/column_mapping.json` 进行本地 CSV 列名转换；不使用 Python 时也可让 ETL/SQL/Java 输出同样的 CSV。\n\n## 驱动数据特别说明\n\n`driver_*.csv` 模板默认包含所有维度，但引擎允许省略只需要从来源金额继承的维度。例如全科目共享同一个销量驱动时，驱动 CSV 可以不包含 `account`，避免复制 100 遍销量。凡是目标配置为 INCLUDE 的维度，则必须在驱动 CSV 中提供对应成员。\n"
	return os.WriteFile(filepath.Join(outdir, "NEW_COMPANY_START_HERE.md"), []byte(desc), 0644)
}
func writeTemplate(file string, cols []string) error {
	f, e := os.Create(file)
	if e != nil {
		return e
	}
	if _, e := f.Write([]byte{0xEF, 0xBB, 0xBF}); e != nil {
		f.Close()
		return e
	}
	w := csv.NewWriter(f)
	err := w.Write(cols)
	w.Flush()
	if err == nil {
		err = w.Error()
	}
	cerr := f.Close()
	if err != nil {
		return err
	}
	return cerr
}

const pythonTemplate = `# -*- coding: utf-8 -*-
# Python 3.6+ standard-library adapter. Reorders/renames exported CSV columns.
# Usage: python csv_mapping_adapter.py column_mapping.json
from __future__ import print_function
import csv, json, os, sys

def run(config_path):
    base=os.path.dirname(os.path.abspath(config_path))
    project=os.path.dirname(base)
    with open(config_path,'r',encoding='utf-8') as f: cfg=json.load(f)
    for group in ('amounts','drivers'):
        for name,setting in sorted(cfg.get(group,{}).items()):
            input_path=setting['input_csv']
            if not os.path.isabs(input_path): input_path=os.path.join(project,input_path)
            output_path=os.path.join(project,setting['output_csv'])
            if not os.path.isfile(input_path):
                raise IOError('Please set input_csv for '+group+'/'+name+': '+input_path)
            columns=setting['fields']  # {canonical_name: upstream_export_column}
            if os.path.abspath(input_path)==os.path.abspath(output_path):
                raise ValueError('input and output must differ')
            with open(input_path,'r',newline='',encoding='utf-8-sig') as fin:
                reader=csv.DictReader(fin)
                for field in columns.values():
                    if field not in (reader.fieldnames or []): raise ValueError('missing field '+field)
                with open(output_path,'w',newline='',encoding='utf-8-sig') as fout:
                    writer=csv.DictWriter(fout,fieldnames=list(columns.keys()))
                    writer.writeheader()
                    for row in reader:
                        writer.writerow({canonical:row[upstream] for canonical,upstream in columns.items()})
            print('Wrote',output_path)

if __name__=='__main__':
    if len(sys.argv)!=2: raise SystemExit('usage: python csv_mapping_adapter.py column_mapping.json')
    run(sys.argv[1])
`
const javaTemplate = `import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
// Java 8+ client to invoke the independent allocation executable. No Maven dependency.
// Compile: javac AllocationCLIClient.java
// Usage: java AllocationCLIClient alloc_windows_amd64.exe rule.json output_java
public final class AllocationCLIClient {
  public static void main(String[] args) throws Exception {
    if(args.length!=3) throw new IllegalArgumentException("Usage: executable config.json output_dir");
    List<String> cmd=Arrays.asList(args[0],"run",args[1],args[2]);
    ProcessBuilder builder=new ProcessBuilder(cmd);
    builder.redirectErrorStream(true);
    Process process=builder.start();
    try(BufferedReader r=new BufferedReader(new InputStreamReader(process.getInputStream(),StandardCharsets.UTF_8))){
      String line;while((line=r.readLine())!=null) System.out.println(line);
    }
    int code=process.waitFor();
    if(code!=0) throw new IllegalStateException("Allocation failed with exit status "+code);
    System.out.println("Verified output: "+args[2]);
  }
}
`
