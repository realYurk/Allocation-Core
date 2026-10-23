# Allocation Core

A rule-driven, multi-dimensional, multi-stage cost allocation engine. It takes a set of source amounts, a set of driver datasets, and a rule file, then produces allocated results together with the evidence needed to audit them.

The engine is industry-neutral. Retail, manufacturing, cosmetics, or any business that needs to spread shared costs across products, brands, regions, or accounts uses the same core; what changes between industries is the rule file and the input CSVs, not the calculation logic.

All sample data in this repository is fabricated. No real business data is included.

## What it does

- **Dynamic dimensions.** Period, account, region, brand, SKU, currency — names and counts are defined by the rule, not hardcoded.
- **Multi-factor allocation.** Split a cost across several drivers, e.g. 75% by material usage and 25% by revenue.
- **Multi-stage allocation.** Feed one stage's output into the next, e.g. "factory cost → product line → product".
- **DAG topology.** Stages can fork and merge. Cycles are rejected.
- **Conservation checks.** Every amount pool is verified to sum to its input; the result goes into `reconciliation.csv`.
- **Audit trail.** `ledger.csv` records the driver, denominator, ratio, and result per row. `lineage_links.csv` records how merged rows trace back to their upstream rows.
- **Integer arithmetic.** Amounts use the smallest currency unit (int64) with deterministic largest-remainder distribution, avoiding floating-point drift.
- **Concurrency.** Multi-threaded, with disk partitioning and an external driver index for larger datasets.
- **Checkpoint / resume.** Long runs can be checkpointed and resumed.
- **HTTP API.** The same engine runs behind a local HTTP service for Java, Python, or any other client.

### V17: audit archive and indexed tracing

V17 keeps the allocation math and rule protocol unchanged, and adds two optional tools on top of a completed run:

- **`trace-index`** builds a persistent, sharded reverse index over `ledger.csv`, `lineage_links.csv`, and `source_to_pool.csv`. A single-row trace that previously scanned the whole ledger now reads only the relevant shard. The original CSVs are untouched; a stale or corrupted index is rejected.
- **`audit-pack`** writes the full run output (results, ledger, reconciliation, source-to-pool, lineage, summary) into one lossless ZIP with a SHA-256 manifest. `audit-verify` checks every byte; `audit-extract` restores to a fresh directory. Original files are not deleted.

These are post-run steps. They do not make the allocation itself faster, and the ZIP is an integrity check, not a digital signature.

## Repository layout

```
cmd/alloc/main.go        CLI entry point
internal/engine/         parsing, compile, run, doctor, trace, compare, generate,
                         trace index, audit archive
internal/batch/          batch manifest runner
internal/service/        HTTP service
internal/workspace/      persistent job workspace (publish/submit/verify/diff)
examples/                worked scenarios with rule + amount + driver CSVs
adapters/                Java and Python client examples
tools/                   offline rule generator (HTML)
docs/                    guides, audit notes, benchmark notes
alloc_*                  prebuilt binaries (Windows / Linux / macOS)
```

## Requirements

- **Windows / Linux / macOS:** use the prebuilt binary in the release package. No Go or Python needed.
- **Build from source:** Go 1.21 or newer.

## Quick start

Pick an example scenario and run it. On Windows, the `.bat` files in the project root wrap these commands.

```bash
# Validate rule and data files (header contract only, no computation).
./alloc_windows_amd64.exe doctor examples/split_merge/rule.json

# Run the allocation.
./alloc_windows_amd64.exe run examples/split_merge/rule.json output_demo --workers 4 --disk-partitions 4

# Trace the first result row back to its sources.
./alloc_windows_amd64.exe trace-first examples/split_merge/rule.json output_demo

# Build a persistent trace index, then trace a specific row.
./alloc_windows_amd64.exe trace-index output_demo
./alloc_windows_amd64.exe trace-index-verify output_demo
./alloc_windows_amd64.exe trace examples/split_merge/rule.json output_demo <ROW_ID>

# Archive the run and verify it.
./alloc_windows_amd64.exe audit-pack output_demo output_demo_audit.zip
./alloc_windows_amd64.exe audit-verify output_demo_audit.zip
./alloc_windows_amd64.exe audit-extract output_demo_audit.zip restored_demo
```

On Linux or macOS, substitute the matching binary (`alloc_linux_amd64` or `alloc_darwin_arm64`).

## Input and output

A scenario is three things:

- **Amount CSV** — one row per source amount, with dimension columns and an amount column.
- **Driver CSV** — one row per allocation target, with the weight field and any target dimensions needed to select it.
- **Rule JSON** — dimensions, source grouping, target selection, drivers, shares, stage order, conservation keys.

The run writes:

| File | Purpose |
|---|---|
| `results.csv` | Final allocated amounts. A product can span multiple rows when several drivers contribute to it. |
| `ledger.csv` | Driver, denominator, ratio, and result per row. |
| `reconciliation.csv` | Conservation check per amount pool (PASS / FAIL). |
| `source_to_pool.csv` | Which source amount went into which pool. |
| `lineage_links.csv` | Parent → child links for merged rows, so no upstream row is lost. |
| `comparison.csv` | Before / after / delta per grouping (written by `compare`). |

CSV output is UTF-8 with BOM so Excel opens Chinese headers correctly. If a file still looks garbled, import it in Excel as "Data → From Text/CSV" with UTF-8.

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

# Evidence tools (V17)
alloc trace-index OUTPUT_DIR
alloc trace-index-verify OUTPUT_DIR
alloc audit-pack OUTPUT_DIR ARCHIVE.zip
alloc audit-verify ARCHIVE.zip
alloc audit-extract ARCHIVE.zip NEW_OUTPUT_DIR

# HTTP service
alloc serve --root ./examples --store ./service_data --listen 127.0.0.1:8787 --jobs 2 [--token SECRET]
```

## Key concepts

- **AS_IS / TOTAL / SOURCE** are the three modes that trip people up most. `AS_IS` keeps each source member as its own pool, `TOTAL` merges the dimension into one pool, and `SOURCE` leaves the target dimension unchanged. See `docs/` for a plain explanation.
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

Measured on Linux amd64 (Go 1.23.2) and independently on Windows (i5-12490F, 6c/12t, 16 GB), synthetic two-stage data:

| Final rows | Windows time | Peak RAM |
|---:|---:|---:|
| 240,000 | 0.93 s | 39 MB |
| 1,200,000 | 3.60 s | 48 MB |
| 6,000,000 | 16.4 s | 135 MB |
| 12,000,000 | 31.8 s | 250 MB |

Throughput is roughly linear. The main cost at multi-million-row scale is writing the evidence files, not the math: `ledger.csv` is around 264 MB per million terminal rows, so a 12M-row run produces a ~3.2 GB ledger. V17's `audit-pack` compresses that audit set by ~90% (lossless), and `trace-index` turns a ~2.7 s full scan into a ~4 ms indexed lookup (after a one-time index build). See `docs/V17_PERFORMANCE_AND_RISKS_CN.md` and `bench/STRESS_TEST_REPORT.md` for details and caveats.

## What this is not

- It is not an approvals system and does not write back to your ERP or database. That belongs to the integrating project.
- It does not claim to cover every corporate finance rule. Missing-driver fallback, currency conversion, and period carry-over need explicit configuration semantics, not silent defaults.
- `trace-index` is a query accelerator, not the lineage itself; it must be rebuilt if the evidence changes.
- `audit-pack` is an integrity check, not a tamper-proof credential or third-party attestation.
- The offline HTML tool is a rule starter, not a production DAG editor.

## Documentation

- `docs/` — beginner guide, performance and risks, integration guide, new-company onboarding, HTTP service, driver cache, audit and benchmark notes.
- `README_CN.md` — this document in Chinese.

## License

No license file is included in this repository yet. Sample data is fabricated and carries no business meaning.
