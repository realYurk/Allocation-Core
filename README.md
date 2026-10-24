# Allocation Core

Rule-driven, multi-dimensional cost allocation engine. Full recalculation (no incremental), multi-stage DAG, multi-factor drivers, largest-remainder rounding, conservation and lineage. One Go binary is the only calculation engine; the Java and Python artifacts are thin HTTP clients that contain no allocation logic.

## What it is

- A single, self-contained executable (`core/alloc_<os>_<arch>`) that reads rule + CSV inputs and writes results, reconciliation, ledger and evidence.
- Callable three ways: an HTTP service (`serve`, recommended for integration), a CLI subprocess (`run`, for batch/cron), or — not provided — an in-process native library.
- Version strings are no longer hard-coded into the product; the `allocation-ir/2.0` schema and `/v1/jobs` API remain stable compatibility markers.

## Start here

| Role | First document |
|---|---|
| Non-developer, "how do I use it" | `docs/USER_START_CN.md` |
| Developer integrating into my system | `docs/ACCESS_GUIDE_CN.md` (data prep + how to call it + failure modes) |
| Java / Spring Boot | `docs/02_Developer_Integration_CN.md` |
| Database / ETL | `docs/03_SQL_Database_Integration_CN.md` |
| Production capacity & admission | `docs/PRODUCTION_ADMISSION_CN.md` |
| Technical lead / deploy | `docs/04_Boundaries_Security_Acceptance_CN.md` |

Bilingual overview: [`README_START_HERE.md`](README_START_HERE.md).

## Quick run (CLI)

```bash
core/alloc_windows_amd64.exe run core/examples/manufacturing/rule.json output --workers 4
```

Then check `output/run_summary.json` for `status=PASS` and `input_amount == output_amount`.

## Quick run (HTTP)

```bash
core/alloc_windows_amd64.exe serve --root core/examples --store ./service_data --listen 127.0.0.1:8787
curl -X POST http://127.0.0.1:8787/v1/jobs -H "Content-Type: application/json" \
  -d '{"rule":"manufacturing/rule.json","workers":2,"evidence_gzip":true}'
```

The service listens on loopback only; put an authenticated reverse proxy in front for remote access.

## Repository layout

```
core/           the Go engine (binaries gitignored; rebuild from source)
integration/    Java 8 JAR + Python HTTP client + SQLite demo + capacity/replay tools
sql/            MySQL / PostgreSQL / SQL Server schema templates
docs/           role-based guides + independent audit reports
tests/          regression + independent attack tests
```

## Build

```bash
cd core && go build -o alloc ./cmd/alloc
```

Prebuilt binaries are excluded from git. Download a release and verify against `CORE_SHA256.json` / `RELEASE_SHA256.json`.
