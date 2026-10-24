# Allocation Core Integration Kit (V21.1)

Rule-driven, multi-dimensional cost allocation engine with a frozen, SHA-verified Go core and thin Java / Python integration clients.

- **Financial engine**: full recalculation, multi-stage DAG, multi-factor drivers, largest-remainder rounding, conservation and lineage — unchanged since V19, frozen at V20 (156 files, SHA-verified).
- **Integration layer**: HTTP service, Java 8 JAR, Python HTTP client, SQL templates, a Vault result archive, capacity preflight, and input replay snapshots.

## Start here

| Role | First document |
|---|---|
| No coding background | `docs/01_Beginner_CN.md` |
| Java / Spring Boot | `docs/02_Developer_Integration_CN.md` |
| Python | `docs/02_Developer_Integration_CN.md` |
| Database / ETL | `docs/03_SQL_Database_Integration_CN.md` |
| Technical lead / deploy | `docs/04_Boundaries_Security_Acceptance_CN.md` |

Full bilingual overview: [`README_START_HERE.md`](README_START_HERE.md).

## Repository layout

```
core/           frozen Go engine (alloc_* binaries are gitignored, rebuild from source)
integration/    Java 8 JAR, Python HTTP client, SQLite end-to-end demo, preflight/replay tools
sql/            MySQL / PostgreSQL / SQL Server schema templates
docs/           role-based guides + independent audit reports
tests/          regression + independent attack tests
```

## Version identity

The canonical distribution version is **V21.1.0** (see `RELEASE_IDENTITY.json`). The frozen core still self-reports historical internal strings (`help=V20`, `run_summary=0.18.0`, `/healthz=0.14`); those are not the distribution version and are intentionally left untouched to preserve the 156-file SHA freeze.

## Build

The core is a Go program with no third-party dependencies. Build a binary with:

```bash
cd core && go build -o alloc ./cmd/alloc
```

Prebuilt binaries are excluded from git; download a release and verify it against `CORE_SHA256.json` / `RELEASE_SHA256.json`.
