# Allocation Core

A rule-driven, multi-dimensional cost allocation engine. It takes money in one
set of accounting dimensions and distributes it across another, following
explicit allocation rules with integer-amount conservation and a full lineage
trail.

Read `00_START_HERE_CN.md` first for a quick orientation (Chinese). This file
is the concise English overview.

## What it does

- Multi-factor, multi-stage allocation with directed-acyclic-graph (DAG)
  topologies: split, merge, and re-split money across stages.
- Integer arithmetic in the smallest currency unit, with largest-remainder
  tie-breaking. Every pool, every stage, and the final total must balance
  exactly — the engine refuses to publish a run that does not conserve money.
- Full evidence: a ledger of every allocation decision, source-to-pool
  membership, lineage links, and a per-pool reconciliation report.
- Disk-index execution for large inputs, checkpoint/resume for long runs, and
  an HTTP API for service use.

## What it is not

- Not a general ETL or accounting ledger. It allocates amounts according to
  rules; it does not post journals or maintain account balances.
- Not a distributed engine. It runs as a single process. Parallelism is
  in-process, bounded by CPU cores.
- The rules are explicit and deterministic. There is no learned or
  probabilistic allocation.

## Quick start

```
# validate a rule
alloc validate examples/manufacturing/rule.json

# run an allocation
alloc run examples/manufacturing/rule.json output/ --workers 4

# inspect a single result row's upstream path
alloc trace-first examples/manufacturing/rule.json output/
```

Output files: `results.csv` (final allocation), `ledger.csv` (every decision),
`reconciliation.csv` (per-pool conservation check), `source_to_pool.csv` and
`lineage_links.csv` (provenance).

## CLI commands

```
alloc run RULE.json OUTPUT_DIR [--workers 4] [--disk-partitions 8]
        [--checkpoint-dir CKPT] [--resume] [--driver-disk-index]
        [--evidence-gzip]
alloc batch MANIFEST.json OUTPUT_DIR [--jobs 3] [--workers 2] [--evidence-gzip]
alloc validate RULE.json
alloc explain RULE.json
alloc doctor RULE.json
alloc compare RULE.json OUTPUT_DIR period,account
alloc graph RULE.json
alloc trace RULE.json OUTPUT_DIR ROW_ID
alloc trace-first RULE.json OUTPUT_DIR
alloc generate RULE.json NEW_COMPANY_DIR
alloc serve --root ./examples --listen 127.0.0.1:8787
```

Evidence and indexed tracing (V17+):

```
alloc trace-index OUTPUT_DIR          # persistent reverse index (plaintext)
alloc trace-index-verify OUTPUT_DIR
alloc audit-pack OUTPUT_DIR ARCHIVE.zip
alloc audit-verify ARCHIVE.zip
alloc audit-extract ARCHIVE.zip NEW_OUTPUT_DIR
alloc evidence-verify OUTPUT_DIR      # V18 gzip evidence
alloc evidence-expand OUTPUT_DIR RESTORED_DIR
alloc trace-pack-index GZIP_RUN_DIR   # V19 compressed trace index
alloc trace-pack-verify GZIP_RUN_DIR
```

## V18 / V19 additions

- **V18 — streaming gzip evidence.** `--evidence-gzip` writes `ledger.csv`,
  `source_to_pool.csv`, and `lineage_links.csv` as lossless gzip with a
  SHA-256 manifest, never staging a full plaintext ledger. `evidence-verify`
  checks integrity; `evidence-expand` restores exact bytes.
- **V19 — compressed trace index.** `trace-pack-index` builds a sharded,
  chunked index over the gzip evidence so a single-row lineage lookup reads
  only the matching chunks instead of decompressing multi-GB files.
  `trace`/`trace-first` auto-discover the index.

## Performance (measured)

On a 6-core/12-thread desktop, memory executor, 4 workers:

| Terminal rows | Wall time | Peak RSS |
|---:|---:|---:|
| 1.2 million | 3.6 s | 48 MB |
| 6 million | 16.4 s | 135 MB |
| 12 million | 31.8 s | 250 MB |

`--evidence-gzip` adds ~24% time but cuts the ledger to ~7% of its plaintext
size (1.58 GB → 115 MB at 6M rows). See `bench/STRESS_TEST_REPORT.md` and
`bench/V18_V19_STRICT_TEST_REPORT.md` for full data.

## Documentation

- `00_START_HERE_CN.md` — orientation (Chinese)
- `docs/V19_TECH_AND_PERFORMANCE_CN.md` — V19 technical details and limits
- `docs/` — older version history and integration guides
- `bench/` — reproducible stress and correctness scripts

Sample data is fabricated and carries no business meaning.
