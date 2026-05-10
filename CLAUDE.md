# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Is Bindery

Bindery is a clean-room replacement for Readarr — an automated book download manager for Usenet & Torrents. It ships as a **single Go binary with an embedded React SPA**. The backend serves the frontend from its own HTTP port; no separate static server is needed.

## Tech Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.25+ |
| Frontend | React 19, TypeScript 6 (strict), Tailwind CSS 4 |
| HTTP router | chi v5 |
| Database | SQLite WAL, pure-Go driver (`modernc.org/sqlite`, no CGO) |
| Frontend build | Vite 8, npm |
| Container | distroless/static-debian12:nonroot |

## Common Commands

All primary tasks go through `make`. Run from the repo root.

```bash
# Backend
make build          # compile Go binary (needs web-build first)
make dev            # go run ./cmd/bindery (live reload via Air or just re-run)
make test           # go test ./... with race detector, in-memory SQLite
make lint           # golangci-lint + frontend lint
make lint-go        # golangci-lint only

# Frontend
make web-dev        # Vite dev server on :5173 (proxies /api to :8787)
make web-build      # Vite production build → web/dist/ (embedded by Go)
cd web && npm ci    # install deps from lockfile
cd web && npm run lint       # Biome lint
cd web && npm run lint:eslint  # ESLint only (fallback)
cd web && npm run typecheck  # tsc --noEmit
cd web && npm test           # Vitest

# Integration & security
make smoke          # boot real binary, run HTTP golden-path suite
make security       # gosec, govulncheck, gitleaks, trivy
make docker-build   # multi-arch image (amd64 + arm64)
make helm-lint      # Helm chart validation
```

### Running a single Go test

```bash
go test ./internal/indexer/... -run TestRankResults -v
```

### Running a single Vitest test

```bash
cd web && npx vitest run src/components/BookCard.test.tsx
```

## Architecture

```
cmd/bindery/          ← Entry point, CLI, server wiring
internal/
  api/                ← chi HTTP handlers (grouped by resource)
  auth/               ← Argon2id passwords, HMAC sessions, OIDC, forward-auth
  db/                 ← SQLite pool, repository interfaces, tx helpers
  migrate/            ← Embedded SQL migrations (NNN_description.sql)
  models/             ← Domain types: Author, Book, Edition, Series, …
  metadata/           ← Fetchers: OpenLibrary, Google Books, Hardcover, DNB, Audnex
  indexer/            ← Newznab/Torznab clients, query builder, dedup, ranking
  decision/           ← Quality profiles, language filter, custom formats, blocklist
  downloader/         ← SABnzbd, NZBGet, qBittorrent, Transmission, Deluge clients
  importer/           ← NZO-ID matching, filename parser, renamer, scanner
  scheduler/          ← Cron loops (auto-grab, refresh, recommendations)
  recommender/        ← Discover engine (taste profiles, multi-source signals)
  seriesmatch/        ← Four-tier series reconciliation
  calibre/            ← calibredb CLI integration, metadata.db ingestion
  abs/                ← Audiobookshelf import pipeline
  opds/               ← OPDS 1.2 catalogue feeds
  notifier/           ← Webhook dispatcher with retry
  httpsec/            ← SSRF guards, hardened response headers
  logbuf/             ← Ring-buffer log store (shown in Settings → Logs)
  webui/              ← go:embed wrapper for web/dist
web/
  src/
    api/              ← HTTP client wrapper
    auth/             ← Auth guards, OIDC context
    components/       ← Reusable UI components
    pages/            ← Routed pages (/book/:id, /author/:id, …)
    settings/         ← Settings UI
    i18n/             ← i18next translations (7 languages)
tests/
  smoke/              ← HTTP-level golden-path tests (real binary)
  security/           ← SSRF, header injection, cookie, SQLi
  abscontract/        ← Audiobookshelf fixture contract tests
charts/bindery/       ← Helm chart
```

### Key design decisions

- **Single binary + embedded UI** — `web/dist` is baked in via `go:embed`; the Go server serves the SPA at `/` and API at `/api/v1/*`.
- **No CGO** — The pure-Go SQLite driver means cross-compilation works for amd64/arm64/armv7/armv6 + macOS/Windows without a C toolchain.
- **Domain-driven packages** — Code is organised by feature (`auth`, `metadata`, `indexer`), not by layer (`models`, `handlers`, `repos`).
- **SQLite WAL** — Single writer, concurrent readers; adequate for typical library workloads.
- **No Goodreads** — Only stable public APIs: OpenLibrary (primary), Google Books, Hardcover, DNB, Audnex, Audible.

## Configuration

Bootstrap config comes from environment variables only:

```
BINDERY_PORT, BINDERY_DB_PATH, BINDERY_DATA_DIR,
BINDERY_LIBRARY_DIR, BINDERY_AUDIOBOOK_DIR, BINDERY_DOWNLOAD_DIR,
BINDERY_URL_BASE, BINDERY_PUID, BINDERY_PGID, BINDERY_LOG_LEVEL
```

Everything else (indexers, download clients, quality profiles, auth mode, notifications) is stored in SQLite and configured through the UI.

## Testing Strategy

| Type | Location | Mechanism |
|---|---|---|
| Unit | `internal/**/*_test.go` | In-memory SQLite (`db.OpenMemory()`), `httptest` |
| Smoke | `tests/smoke/` | Boots real binary, exercises HTTP endpoints |
| Security | `tests/security/` | SSRF guards, header injection, cookie tests |
| ABS contract | `tests/abscontract/` | Fixture server, Audiobookshelf import contract |
| Frontend | `web/src/**/*.test.{ts,tsx}` | Vitest + @testing-library/react + jsdom |

## Linting & Quality Gates

**Go**: golangci-lint v2 run as a Go tool (`go tool golangci-lint run ./...`; config: `.golangci.yml`) — errcheck, staticcheck, gosec, errorlint, bodyclose, sqlclosecheck, noctx, revive, gocritic, misspell and more. Also: `govulncheck` (transitive CVE scan), `gofmt`. Note: `unparam` / `revive.unused-parameter` are intentionally disabled — avoid the `_ type` pattern.

**Frontend**: Biome (`cd web && npm run lint`) replaces ESLint as the primary linter. Config: `web/biome.json`. ESLint remains available via `npm run lint:eslint`. TypeScript strict-mode check via `tsc --noEmit`.

**Security**: gosec, gitleaks, Trivy, Grype, Dockle, ZAP baseline, Semgrep — run weekly via `.github/workflows/security.yml`.

## Database Migrations

Add migrations as `internal/migrate/NNN_short_description.sql`. They run automatically at startup. Migrations must be idempotent (use `IF NOT EXISTS`, `IF EXISTS`, etc.).

## Helm Chart

`charts/bindery/` — lint with `make helm-lint`. The chart is ArgoCD/Flux compatible and uses digest-pinned images.
