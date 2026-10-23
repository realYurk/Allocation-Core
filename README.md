# Allocation Core

A rule-driven, multi-dimensional, multi-stage cost allocation engine. It takes a set of source amounts, a set of driver datasets, and a rule file, then produces allocated results together with the evidence needed to audit them.

The engine is industry-neutral. The same core handles retail, manufacturing, cosmetics, or any other business that needs to spread shared costs across products, brands, regions, or accounts. What changes between industries is the rule file and the input CSVs, not the calculation logic.

All sample data in this repository is fabricated. No real business data is included.

## What it does

- **Dynamic dimensions.** Period, account, region, brand, SKU, currency — the names and the number of dimensions are defined by the rule, not hardcoded.
- **Multi-factor allocation.** Split a cost across several drivers, for example 75% by material usage and 25% by revenue.
- **Multi-stage allocation.** Feed the output of one stage into the next, e.g. "factory cost → product line → product".
- **DAG topology.** Stages can fork and merge. Cycles are rejected.
- **Conservation checks.** Every amount pool is verified to sum to its input. Output is written to `reconciliation.csv`.
- **Audit trail.** `ledger.csv` records which driver, denominator, ratio, and result produced each row. `lineage_links.csv` records how merged rows trace back to their upstream rows.
- **Integer arithmetic.** Amounts are computed in the smallest currency unit (int64) with deterministic remainder distribution, avoiding floating-point rounding drift.
- **Concurrency.** Multi-threaded, with disk partitioning and an external driver index for larger datasets.
- **Checkpoint / resume.** Long runs can be checkpointed and resumed.
- **HTTP API.** The same engine can run behind a local HTTP service for Java, Python, or any other client.

## Repository layout

```
cmd/alloc/main.go        CLI entry point
internal/engine/         parsing, compilation, run, doctor, trace, compare, generate
internal/batch/          batch manifest runner
internal/service/        HTTP service
internal/workspace/      persistent job workspace (publish/submit/verify/diff)
examples/                five worked scenarios with rule + amount + driver CSVs
adapters/                Java and Python client examples
tools/                   offline rule generator (HTML)
docs/                    guides, audit notes, benchmark notes
alloc_*                   prebuilt binaries for Windows / Linux / macOS
```

## Requirements

- **Windows / Linux / macOS:** use the prebuilt binary in the release package. No Go or Python needed.
- **Build from source:** Go 1.21 or newer.

## Quick start

Pick one of the example scenarios and run it. On Windows, the `.bat` files in the project root wrap these commands.

```bash
# Validate the rule and data files (header contract only, does not compute).
./alloc_windows_amd64.exe doctor examples/split_merge/rule.json

# Run the allocation.
./alloc_windows_amd64.exe run examples/split_merge/rule.json output_demo --workers 4 --disk-partitions 4

# Trace the first result row back to its sources.
./alloc_windows_amd64.exe trace-first examples/split_merge/rule.json output_demo

# Trace a specific row.
./alloc_windows_amd64.exe trace examples/split_merge/rule.json output_demo <ROW_ID>

# Compare before/after per account and period.
./alloc_windows_amd64.exe compare examples/split_merge/rule.json output_demo account,period
```

On Linux or macOS, substitute the matching binary (`alloc_linux_amd64` or `alloc_darwin_arm64`).

### Windows double-click

1. Unzip the release package fully. Do not run the `.bat` files from inside the archive.
2. Double-click `00_Windows_Diagnostic.bat` to check the environment.
3. Double-click `16_Windows_Run_Trace_Demo.bat`. This runs the 75% + 25% two-path merge example and opens the output folder.
4. Double-click `17_Windows_Two_Stage_Demo.bat` for the "cost → product line → product" example, which also writes `comparison.csv`.

## Input and output

A scenario is three things:

- **Amount CSV** — one row per source amount, with dimension columns and an amount column.
- **Driver CSV** — one row per allocation target, with the weight field and any target dimensions needed to select it.
- **Rule JSON** — dimensions, source grouping, target selection, drivers, shares, stage order, conservation keys.

The run writes these files:

| File | Purpose |
|---|---|
| `results.csv` | Final allocated amounts. A product can appear on more than one row if multiple drivers contributed to it. |
| `ledger.csv` | Driver, denominator, ratio, and result per row. |
| `reconciliation.csv` | Conservation check per amount pool (PASS / FAIL). |
| `source_to_pool.csv` | Which source amount went into which pool. |
| `lineage_links.csv` | Parent → child links for merged rows, so no upstream row is lost. |
| `comparison.csv` | Before / after / delta per grouping (written by `compare`). |

CSV output is UTF-8 with BOM, so Excel opens Chinese headers correctly. If a file still looks garbled, import it in Excel as "Data → From Text/CSV" with UTF-8.

## CLI reference

```
alloc validate RULE.json
alloc explain RULE.json
alloc doctor RULE.json
alloc compare RULE.json OUTPUT_DIR period,account
alloc trace RULE.json OUTPUT_DIR ROW_ID
alloc trace-first RULE.json OUTPUT_DIR
alloc graph RULE.json
alloc run RULE.json OUTPUT_DIR [--workers N] [--disk-partitions N]
           [--checkpoint-dir DIR] [--resume] [--driver-disk-index] [--driver-cache-dir DIR]
alloc batch MANIFEST.json OUTPUT_DIR [--jobs N] [--workers N] [--disk-partitions N]
alloc generate RULE.json NEW_COMPANY_DIR
alloc serve --root ./examples --store ./service_data --listen 127.0.0.1:8787 --jobs 2 [--token SECRET]
```

### Persistent workspace

```
alloc workspace publish RULE.json WORKSPACE
alloc workspace rules WORKSPACE
alloc workspace diff WORKSPACE RULE_ID LEFT_REVISION RIGHT_REVISION
alloc workspace submit RULE.json WORKSPACE [--workers N]
alloc workspace list WORKSPACE
alloc workspace history WORKSPACE
alloc workspace verify-latest WORKSPACE
alloc workspace inspect WORKSPACE JOB_ID
alloc workspace verify WORKSPACE JOB_ID
alloc workspace recover WORKSPACE
alloc workspace retry WORKSPACE JOB_ID [--workers N]
```

## Key concepts

- **AS_IS / TOTAL / SOURCE** are the three modes that trip people up most. See `docs/` for a plain explanation. In short: `AS_IS` keeps each source member as its own pool, `TOTAL` merges the dimension into one pool, and `SOURCE` leaves the target dimension unchanged.
- **Two different kinds of share.** `stages[].inputs[].share` routes an edge between stages; `stages[].allocations[].share` splits a cost across drivers within one stage. Both must sum to 1 for their active entries, and they are not interchangeable.
- **Multi-factor is not multi-stage.** Within one stage, driver shares must total 100%. Between stages, edge shares must also total 100%.

## Integration

Java, Python, and other systems call the same backend rather than reimplementing the allocation formulas:

- **CLI** — run `alloc run ...` and read the output CSVs.
- **HTTP** — start `alloc serve`, then `POST /v1/jobs`, poll `GET /v1/jobs/{id}`, download `results.csv` etc. See `docs/02_HTTP_Service.md`.
- **Java** — see `adapters/AllocationHTTPClient.java` (Java 8+ standard library).
- **Python** — see `adapters/python_http_client.py` (standard library only).

The HTTP service binds to loopback only. It is not an internet-facing service and has no RBAC, TLS, or full identity management.

## Performance

Measured on Linux amd64, Go 1.23.2, synthetic data (100 accounts, 12 periods, 1000 products, cost → line → product, 4 workers, 16 disk partitions):

| Variant | Final records | Two-stage pools | Time | Peak RSS |
|---|---:|---:|---:|---:|
| V15 baseline | 1,200,000 | 3,600 | 5.52 s | ~39 MB |
| V16 (lineage + validation) | 1,200,000 | 3,600 | 5.33 s | ~45 MB |

`results.csv`, `ledger.csv`, and `reconciliation.csv` are SHA-256 identical between the two versions. The small time difference is run-to-run noise, not a real speedup. Heavily skewed multi-stage data, extreme multi-factor cases, and 10M+ driver rows still need their own stress testing.

## What this is not

- It is not an approvals system and does not write back to your ERP or database. That belongs to the integrating project.
- It does not claim to cover every corporate finance rule. Missing-driver fallback, currency conversion, and period carry-over need explicit configuration semantics, not silent defaults.
- `trace` scans files sequentially for a single row. For high-frequency full-lineage queries, a persistent lineage index is the next step.
- The offline HTML tool is a rule starter, not a production DAG editor.

## Documentation

- `docs/` — quickstart, integration guide, new-company onboarding, HTTP service, driver cache, audit and benchmark notes.
- `README_CN.md` — this document in Chinese.

## License

No license file is included in this repository yet. Sample data is fabricated and carries no business meaning.
