# U14 on `dev-mac`

- Date: 2026-04-20
- Host: `dev-mac` (local macOS 15.3, Apple M4)
- AIMA repo: `HEAD=44bc4c7e362d`
- Central repo: `aima-central-knowledge HEAD=ec76e78ecfe7`

## Verdict

`KNOWN ISSUE`

The Central store contract itself is consistent across SQLite and Postgres, but
the edge-facing raw response shape is not fully identical across both backends:
`scenario list-central` returns different timestamp string formats.

## What Was Verified

1. Central store contract tests passed on both backends.
   - SQLite: `go test ./internal/central -run TestSQLiteCentralStore -v`
   - Postgres: `go test ./internal/central -run TestPostgresCentralStore -v`
   - Both ran the same `storeTestSuite` and passed all subtests.

2. Edge client calls were replayed against two local Central servers with the
   same API key and the same mock OpenAI-compatible LLM.
   - SQLite Central: `127.0.0.1:18083`
   - Postgres Central: `127.0.0.1:18084`
   - Edge side used isolated `AIMA_DATA_DIR` values and seeded only the minimum
     canonical `device.id` directly into SQLite because CLI config whitelist
     does not expose `device.*` keys.

3. `knowledge sync`, `knowledge advise`, and `scenario generate` matched across
   both backends after stripping expected volatile fields.
   - `knowledge sync`: same payload shape after ignoring `endpoint` and `device_id`
   - `knowledge advise`: same payload shape after ignoring advisory `id` and `created_at`
   - `scenario generate`: same payload shape after ignoring stored scenario `id` and timestamps

4. `scenario list-central` exposed a backend-specific timestamp serialization
   difference in raw output.
   - SQLite example: `2026-04-20T10:50:20Z`
   - Postgres example: `2026-04-20 18:51:20+08`
   - The field set is the same, but the raw JSON is not fully consistent, so
     this UAT item does not meet its acceptance bar yet.

## Evidence

- `00-mock-openai.log`
- `01-store-sqlite.txt`
- `02-store-postgres.txt`
- `10-central-sqlite.log`
- `14-sqlite-sync.txt`
- `15-sqlite-advise.json`
- `16-sqlite-scenario.json`
- `17-sqlite-list-central.json`
- `20-central-postgres.log`
- `23-postgres-sync.txt`
- `24-postgres-advise.json`
- `25-postgres-scenario.json`
- `26-postgres-list-central.json`
- `27-backend-compare.txt`
