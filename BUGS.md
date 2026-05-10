# Bindery Bug Log

Bugs discovered while running Bindery v1.7.0 (commit 94a5103) on NixOS.

---

## Bug 1: CheckDownloads only polls the first-priority download client

**File:** `internal/importer/scanner.go` — `CheckDownloads()`

**Description:** `CheckDownloads()` calls `GetFirstEnabled()` which returns a single download
client ordered by the `priority` column. With two clients at the same priority (e.g. both
at 0), only the first by DB row order is ever polled. The second client's completed downloads
sit at 100% indefinitely and are never imported.

**Workaround:** Set the desired client to a lower priority number:
```sql
UPDATE download_clients SET priority=-1 WHERE type='qbittorrent';
```
This prevents the other client from being monitored automatically.

**Fix:** `CheckDownloads` should iterate all enabled clients, not just the first.

---

## Bug 2: Torrent hash race condition — multiple downloads get the same wrong hash

**File:** `internal/downloader/qbittorrent/` (send / hash-fetch logic)

**Description:** When Bindery sends several torrents to qBittorrent in rapid succession (e.g.
from a bulk search result), the "get last-added torrent hash" call races. All downloads in
the batch end up with the same `torrent_id` value (whichever torrent settled last), so
Bindery can never match any of those completed torrents back to their download records.

**Symptoms:**
- `importFailed` on multiple downloads after a bulk grab
- All affected downloads share an identical `torrent_id` in the DB
- Manual hash lookup against the qBittorrent API shows different hashes

**Workaround:** After any mass-grab, query qBittorrent's `/api/v2/torrents/info` and
manually update `torrent_id` in the `downloads` table:
```sql
UPDATE downloads SET torrent_id='<real-hash>' WHERE id=<id>;
UPDATE downloads SET status='downloading' WHERE id=<id>;
```

**Fix:** After each `AddTorrent` call, poll until a torrent with a new hash appears rather
than reading a shared "last hash" field.

---

## Bug 3: Fallback to save_path root causes entire download directory to be imported ✓ Fixed in 62c9874

**File:** `internal/importer/scanner.go` lines 478–483

**Description:** When `filepath.Join(torrent.SavePath, torrent.Name)` doesn't exist on disk
(because qBittorrent sanitises characters in the torrent name — e.g. `:` → `_` — that the
API still reports in their original form), Bindery falls back to
`downloadPath = torrent.SavePath`. If that path is a shared download root
(e.g. `/mnt/media/downloads/complete`), Bindery walks the **entire directory** looking for
book files and then moves or hardlinks the whole root into the audiobook library as a single
audiobook folder.

**Impact:** Destructive. Every file in the download root (movies, TV, other audiobooks, etc.)
gets moved into a single audiobook destination directory. Files from other torrent clients
that were not imported by `*arr` applications have no other copy, so they are effectively
lost when the stray directory is cleaned up.

**Workaround:** Configure qBittorrent to use per-torrent save paths (unique subdirectory per
torrent) so that `torrent.SavePath` is never shared. Rename directories on disk to match the
sanitised torrent name before Bindery processes them.

**Fix:** When `filepath.Join(SavePath, Name)` doesn't exist, bail out with a retryable error
rather than silently falling back to the parent directory. The `SavePath` fallback is only
safe for single-file torrents where `SavePath` IS the content path; for multi-file torrents
it is always a shared root.

---

## Bug 4: All mutating API endpoints silently require `X-Requested-With: bindery-ui` header

**File:** `internal/auth/middleware.go` — `RequireXRequestedWith`

**Description:** Every non-safe (POST/PUT/PATCH/DELETE) request without an API key is
rejected with `403 {"error":"forbidden"}` unless the header `X-Requested-With: bindery-ui`
is present. This is a CSRF-guard measure, but it is entirely undocumented. Scripts, curl
commands, and any third-party integration that calls the API with session-cookie auth will
silently receive 403 with no indication of what is missing.

Additionally, the login endpoint (`POST /api/v1/auth/login`) requires this header before a
session even exists, making the first login attempt from any non-browser client also fail.

**Workaround:** Add `-H 'X-Requested-With: bindery-ui'` to all POST/PUT/DELETE requests,
AND fetch a CSRF token (`GET /api/v1/auth/csrf` → `csrfToken`) and include it as
`X-CSRF-Token` on all mutating requests after login.

**Fix:** Document the required headers in the API docs. Consider relaxing the requirement for
the login endpoint specifically (there is no session to protect at that point).

---

## Bug 5: Prowlarr client search timeout hardcoded at 15 s — too short for Usenet indexers

**File:** `internal/prowlarr/client.go`

**Description:** `http.Client{Timeout: 15 * time.Second}` is hardcoded. Usenet indexers like
NZBgeek routinely take >15 s to respond, especially when Bindery goes through a reverse
proxy. All Usenet searches time out silently.

**Workaround:** Route Prowlarr requests directly to its internal address (not via Caddy)
to reduce per-hop latency. No fix available without recompiling.

**Fix:** Expose a `prowlarr.search_timeout_seconds` (or global `http.timeout_seconds`)
setting, defaulting to 60 s.

---

## Bug 6: Default import mode is `move`, which destroys seeding

**File:** `internal/importer/scanner.go` — `importMode()`

**Description:** The default import mode when no `import.mode` setting exists is `"move"`.
Files are physically moved out of the torrent client's download directory, breaking seeding
immediately. The `hardlink` mode exists (free disk cost, preserves seeding) but is not
the default and is not suggested during setup.

**Workaround:**
```sql
UPDATE settings SET value='hardlink' WHERE key='import.mode';
-- or INSERT if key doesn't exist yet:
INSERT INTO settings(key,value) VALUES('import.mode','hardlink') ON CONFLICT(key) DO UPDATE SET value='hardlink';
```

**Fix:** Default to `hardlink` when source and destination are on the same filesystem.
Fall back to `copy` (with a warning) when cross-filesystem import is detected.

---

## Bug 7: `importFailed` downloads are never automatically retried

**File:** `internal/importer/scanner.go` — `CheckDownloads()`

**Description:** `CheckDownloads` only processes downloads whose `status` is `'downloading'`.
Once a download is marked `importFailed` (e.g. due to a path mismatch, stale hash, or a
transient filesystem error), it stays in that state permanently. There is no retry queue,
no configurable retry count, and no UI action to bulk-reset failed imports.

**Impact:** Silent data loss — downloaded files exist in qBittorrent but are never imported.
Discovery requires querying the SQLite database directly.

**Workaround:**
```sql
UPDATE downloads SET status='downloading' WHERE status='importFailed';
```
Re-run manually after fixing the underlying cause.

**Fix:** Add a `importFailed` → `downloading` retry with exponential back-off (cap at e.g.
3 retries), or expose a "retry failed imports" button in the UI.

---

## Bug 8: No per-author audiobook root folder — acknowledged as TODO in source

**File:** `internal/importer/scanner.go` lines 600–606 (comment references issue #421)

**Description:** There is a single global `BINDERY_AUDIOBOOK_DIR` environment variable for
all audiobooks. The `root_folder_id` per-author override only affects ebook routing, not
audiobook routing (this is intentional — the comment explains it — but the feature gap is
real). A user who wants different authors' audiobooks in different directories (e.g. matching
separate ABS library folder structure) has no way to configure this.

**Workaround:** None. All audiobooks go to a single directory.

**Fix:** Add a per-author `audiobook_root_folder_id` column (parallel to the existing
`root_folder_id`). The code comment at line 605 explicitly calls this out.

---

## Bug 9: Bindery supports only a single ABS library connection

**File:** `internal/abs/` — settings keys `abs.base_url`, `abs.library_id`

**Description:** Only one ABS instance and one library ID can be configured. A typical ABS
setup has two separate libraries — `Books` (ebooks, path `/media/books`) and `Audiobooks`
(audiobooks, path `/media/audiobooks`). Bindery's ABS import reads from only one of them.
If the `abs.library_id` points to `Books`, audiobook items in the `Audiobooks` library are
never reconciled; if it points to `Audiobooks`, ebook items are missed.

**Workaround:** Point `abs.library_id` at whichever library is more important. Manually
reconcile the other library's items if needed.

**Fix:** Support a list of `(base_url, library_id)` pairs, or at minimum add a second
`abs.audiobooks_library_id` setting.

---

## Bug 10: ABS integration is import-only — no metadata push after import

**File:** `internal/abs/` — `importer.go`, `internal/importer/scanner.go`

**Description:** Bindery reads FROM ABS (to populate its book catalog) but never writes TO
ABS after importing a file. After a successful import, ABS must independently discover the
new file via its next scheduled scan and then separately fetch metadata from Audible/etc.
Bindery has the ASIN, series name, series sequence, and cover information in its own DB at
import time (sourced from Audnex, Hardcover, etc.) and could trigger an ABS metadata refresh
(`POST /api/items/{id}/match` or `POST /api/items/{id}/scan`) immediately, but does not.

**Impact:** Newly imported audiobooks appear in ABS without series grouping, correct title,
cover, or narrator until the user manually triggers a metadata match in the ABS UI.

**Fix:** After a successful audiobook import, call the ABS API to:
1. Trigger a folder scan to surface the new item
2. Once the item exists in ABS, call `/api/items/{id}/match` with the known ASIN

---

## Bug 11: `X-Requested-With` bypass via API key not applied consistently

**File:** `internal/auth/middleware.go` — `RequireXRequestedWith`

**Description:** The `X-Requested-With: bindery-ui` requirement is bypassed when an API key
is present in `X-Api-Key` header or `?apikey=` query param. However, some endpoints (noted
in Bug 4 context) return admin-role errors for API key auth even when the key is valid and
the user has admin role. This creates a situation where API key auth bypasses CSRF/XRW checks
but then fails at the role check, giving a misleading error path.

**Workaround:** Use session-cookie auth with the `X-Requested-With` and `X-CSRF-Token`
headers for all mutating operations.

**Fix:** Audit which endpoints require admin session vs. admin API key, and document clearly.

---

## Bug 12: Downloaded audiobooks treated as MISSING in ABS when import mode is `move`

**Context:** Bindery imports audiobooks by moving or hardlinking files from the download
client's save directory into the configured library directory. The ABS integration reads
FROM ABS (import-only, Bug 10), so Bindery has no mechanism to tell ABS that a new item
just landed in the library.

**Description:** After a successful import, ABS must independently discover the new file
via its next scheduled library scan (default: every 24 hours or manually triggered). Until
then, the item does not appear in ABS at all. If Bindery also moves the file from the
download directory (import mode `move`), and ABS was previously tracking the file from
that download directory path (because it was placed in a path ABS happened to scan), the
item will appear as MISSING in ABS immediately after import rather than simply updating to
the new path.

**Workaround:** Trigger a manual ABS library scan after any batch of imports:
```bash
curl -X POST -H "Authorization: Bearer $ABS_TOKEN" \
  http://localhost:8000/api/libraries/{library_id}/scan
```

**Fix:** Bindery should call the ABS scan or item-add API immediately after a successful
import (see also Bug 10).

---

## Bug 13: Bindery imports files to the ebook library path for `media_type='both'` books when an ebook torrent is grabbed

**Description:** When a book has `media_type='both'` (Bindery is configured to manage both
formats), and a torrent that happens to contain an ebook (epub/azw3) is grabbed, the file
is correctly placed in the ebook library (`BINDERY_LIBRARY_DIR`). However, from the user's
perspective who expects audiobooks, this is a silent mismatch: the book's status changes to
`imported`, the audiobook library is empty, and there is no indication that what was
imported was an ebook rather than an audiobook.

**Workaround:** Check the file extension of imported items in the `downloads` table path,
or inspect the library directory directly.

**Fix:** Show the detected format (audiobook/ebook) in the import history UI. Optionally,
allow configuring per-book `media_type` preference so that searches prioritise one format.

---

## Notes

- NZBgeek consistently times out when routed through `get.jett.sh/prowlarr` (Caddy reverse
  proxy). Direct internal address works. Cause: proxy latency pushes total round-trip over
  the hardcoded 15 s Prowlarr client timeout (Bug 5).
- 1337x was rate-limited until 2026-05-10 after the mass search session.
- `auth.mode=enabled` in settings; login requires `X-Requested-With: bindery-ui` AND a
  CSRF token fetched from `GET /api/v1/auth/csrf` (field: `csrfToken`).
