# Allocation Core

A rule-driven, multi-dimensional cost allocation engine, distributed here as an
integration kit: the frozen Go compute core plus SQL templates and lightweight
Java/Python HTTP clients.

Start with `README_START_HERE.md` (Chinese orientation). This is the concise
English overview.

## What it is

- Multi-factor, multi-stage allocation over a directed-acyclic-graph topology,
  with integer-amount conservation and largest-remainder tie-breaking.
- Full evidence trail: ledger, lineage, source-to-pool membership, per-pool
  reconciliation.
- A lifecycle Vault that stores completed runs with SHA-256 deduplication and
  gzip compression, plus a bulk DB export projection for `COPY`/`BULK INSERT`.
- Lightweight Java 8 and Python HTTP clients. They are clients only — they never
  reimplement the allocation math.

## Layout

```
core/            frozen Go engine (V20), examples, bats, docs, CORE_SHA256.json
sql/             PostgreSQL / MySQL / SQL Server schema templates (design only)
integration/     Java client JAR + Python HTTP client + SQLite end-to-end demo
docs/            role-based guides (beginner / developer / DB / boundaries)
tests/           integration regression and vault black-box tests
```

The frozen core is pinned by `CORE_SHA256.json` (156 files, SHA-256 verified).
Prebuilt binaries are excluded from this repository and recorded there for
verification after download.

## Quick start

```
# run the two-stage demo (Windows)
core/01_Windows_Run_Two_Stage.bat

# end-to-end SQL -> engine -> SQLite (Python 3 stdlib, no pip)
python integration/python/sqlite_full_run.py --workspace ./demo_workspace --reset-demo
```

## Honest boundaries

- The SQL templates are design references; they are not benchmarked against a
  real PostgreSQL / MySQL / SQL Server instance.
- The Vault archives completed results and evidence, not the raw inputs — it is
  not a from-scratch replay snapshot.
- The engine is single-process. Parallelism is in-process, bounded by cores.

Sample data is fabricated and carries no business meaning.
