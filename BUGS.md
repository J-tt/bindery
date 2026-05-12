# Bindery Bug Log

Bugs discovered while running Bindery v1.7.0 (commit 94a5103) on NixOS.

---

## Bug 1: CheckDownloads only polls the first-priority download client ✓ Fixed in 2f9451d

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

## Bug 2: Torrent hash race condition — multiple downloads get the same wrong hash ✓ Fixed

**File:** `internal/downloader/qbittorrent/client.go` — `AddTorrent()`

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

**Fix:** `AddTorrent` now holds `addMu` (a new `sync.Mutex` on `Client`) for the entire
before-snapshot → submit → poll sequence. Concurrent calls are serialised, so each call's
before/after hash-set diff only ever sees the single torrent it submitted.

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

## Bug 4: All mutating API endpoints silently require `X-Requested-With: bindery-ui` header ✓ Fixed

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

**Fix:** `RequireXRequestedWith` now exempts `AllowUnauthPath` routes (login, setup, logout,
csrf, status) — the same set already exempted by `RequireCSRFToken`. There is no session
cookie to protect against CSRF on these endpoints, so requiring the header was pure friction
for non-browser clients with no security benefit.

---

## Bug 5: Prowlarr client search timeout hardcoded at 15 s — too short for Usenet indexers ✓ Fixed

**File:** `internal/prowlarr/client.go`

**Description:** `http.Client{Timeout: 15 * time.Second}` is hardcoded. Usenet indexers like
NZBgeek routinely take >15 s to respond, especially when Bindery goes through a reverse
proxy. All Usenet searches time out silently.

**Workaround:** Route Prowlarr requests directly to its internal address (not via Caddy)
to reduce per-hop latency. No fix available without recompiling.

**Fix:** Default raised from 15 s → 60 s. A new `prowlarr.NewWithTimeout` constructor lets
callers override the timeout. `ProwlarrHandler` and the startup-sync goroutine both read the
`prowlarr.search_timeout_seconds` setting (stored in SQLite) and pass it through; the setting
defaults to 60 s when absent.

---

## Bug 6: Default import mode is `move`, which destroys seeding ✓ Fixed

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

## Bug 7: `importFailed` downloads are never automatically retried ✓ Fixed (migration 036, IncrementImportRetryCount)

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

**Fix:** All four download client check loops (`checkQbittorrentDownloads`,
`checkTransmissionDownloads`, `checkSABnzbdDownloads`, `checkNZBGetDownloads`) now retry
downloads in `importFailed` state up to `importRetryLimit` (3) times before giving up.
A new `import_retry_count` column (migration 036) tracks the attempt count so retries
are capped and do not loop forever on persistently broken imports.

---

## Bug 8: No per-author audiobook root folder ✓ Fixed (migration 038, AudiobookRootFolderID)

**File:** `internal/importer/scanner.go` lines 600–606 (comment references issue #421)

**Description:** There is a single global `BINDERY_AUDIOBOOK_DIR` environment variable for
all audiobooks. The `root_folder_id` per-author override only affects ebook routing, not
audiobook routing (this is intentional — the comment explains it — but the feature gap is
real). A user who wants different authors' audiobooks in different directories (e.g. matching
separate ABS library folder structure) has no way to configure this.

**Workaround:** None. All audiobooks go to a single directory.

**Fix:** Added `audiobook_root_folder_id` column to `authors` (migration 038) and
`AudiobookRootFolderID *int64` field to `models.Author`. A new `effectiveAudiobookDir()`
method on Scanner resolves the per-author folder when set, falling back to the global
`BINDERY_AUDIOBOOK_DIR`. The ebook `effectiveLibraryDir()` path is unchanged so assigning
an ebook root folder never accidentally redirects audiobooks (#421). API Create and Update
handlers accept `audiobookRootFolderId` in the request body.

---

## Bug 9: Bindery supports only a single ABS library connection ✓ Fixed

**File:** `internal/abs/` — settings keys `abs.base_url`, `abs.library_id`

**Description:** Only one ABS instance and one library ID can be configured. A typical ABS
setup has two separate libraries — `Books` (ebooks, path `/media/books`) and `Audiobooks`
(audiobooks, path `/media/audiobooks`). Bindery's ABS import reads from only one of them.
If the `abs.library_id` points to `Books`, audiobook items in the `Audiobooks` library are
never reconciled; if it points to `Audiobooks`, ebook items are missed.

**Workaround:** Point `abs.library_id` at whichever library is more important. Manually
reconcile the other library's items if needed.

**Fix:** Added `abs.audiobooks_library_id` setting (`SettingABSAudiobooksLibraryID`). When
set to a different library ID than `abs.library_id`, the importer enumerates both libraries
in sequence within a single import run — each library gets its own `abs_import_runs` record.
The new field is exposed in `GET`/`PUT /api/v1/abs/config` as `audiobooksLibraryId`.

---

## Bug 10: ABS integration is import-only — no metadata push after import ✓ Fixed (scan trigger)

**File:** `internal/abs/` — `importer.go`, `internal/importer/scanner.go`

**Description:** Bindery reads FROM ABS (to populate its book catalog) but never writes TO
ABS after importing a file. After a successful import, ABS must independently discover the
new file via its next scheduled scan and then separately fetch metadata from Audible/etc.
Bindery has the ASIN, series name, series sequence, and cover information in its own DB at
import time (sourced from Audnex, Hardcover, etc.) and could trigger an ABS metadata refresh
(`POST /api/items/{id}/match` or `POST /api/items/{id}/scan`) immediately, but does not.

**Impact:** Newly imported audiobooks appear in ABS without series grouping, correct title,
cover, or narrator until the user manually triggers a metadata match in the ABS UI.

**Fix (partial):** After a successful audiobook import, `tryImportInternal` now calls
`abs.ScanNotifier.ScanLibrary` → `POST /api/libraries/{id}/scan` so ABS discovers the new
item within seconds rather than waiting up to 24 h for its next scheduled scan. The ABS
library ID is resolved at import time from the `abs.audiobooks_library_id` setting (falling
back to `abs.library_id`) so config changes take effect without restart. The follow-up
`/api/items/{id}/match` ASIN call (step 2) is a future improvement — it requires polling for
the item to appear after the scan completes.

---

## Bug 11: `X-Requested-With` bypass via API key not applied consistently ✓ Fixed

**File:** `internal/auth/middleware.go` — `Middleware`

**Description:** The `X-Requested-With: bindery-ui` requirement is bypassed when an API key
is present in `X-Api-Key` header or `?apikey=` query param. However, `Middleware` passed the
request to the next handler without setting any role in context when API key auth succeeded.
`RequireAdmin` subsequently saw role `""` instead of `"admin"` and returned 403
`{"error":"admin role required"}`, making API key auth silently unusable for any admin-protected
endpoint regardless of whether the key was valid.

**Workaround:** Use session-cookie auth with the `X-Requested-With` and `X-CSRF-Token`
headers for all mutating operations.

**Fix:** `Middleware` now sets `userRoleCtxKey = "admin"` in the request context when a
valid API key is accepted. API keys are admin credentials by definition (there is only one
global key, provisioned by the admin). `RequireAdmin`-guarded routes are now reachable via
API key without a session cookie.

---

## Bug 12: Downloaded audiobooks treated as MISSING in ABS when import mode is `move` ✓ Fixed

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

**Fix:** Resolved by the Bug #10 library scan trigger — `tryImportInternal` now calls
`POST /api/libraries/{id}/scan` immediately after a successful audiobook import, so ABS
rescans and updates the item path before it can appear as MISSING.

---

## Bug 13: Bindery imports files to the ebook library path for `media_type='both'` books when an ebook torrent is grabbed ✓ Fixed

**Description:** When a book has `media_type='both'` (Bindery is configured to manage both
formats), and a torrent that happens to contain an ebook (epub/azw3) is grabbed, the file
is correctly placed in the ebook library (`BINDERY_LIBRARY_DIR`). However, from the user's
perspective who expects audiobooks, this is a silent mismatch: the book's status changes to
`imported`, the audiobook library is empty, and there is no indication that what was
imported was an ebook rather than an audiobook.

**Workaround:** Check the file extension of imported items in the `downloads` table path,
or inspect the library directory directly.

**Fix:** `createHistoryEvent` now includes a `"format"` key (`"ebook"` or `"audiobook"`) in
the `bookImported` history event data for both the ebook and audiobook import paths. The queue
and history UI can surface this field so the user knows which format was imported without
inspecting the file path.

---

## Bug 14: Transmission import walks shared download root instead of torrent content path ✓ Fixed

**File:** `internal/importer/scanner.go` — `checkTransmissionDownloads()`

**Description:** `checkTransmissionDownloads` passed `torrent.DownloadDir` directly as
`downloadPath` to `tryImportInternal`. `DownloadDir` is Transmission's *parent* save
directory (e.g. `/downloads/books/`), not the individual torrent's content directory
(`/downloads/books/Dune/`). `tryImportInternal` then calls `filepath.Walk(downloadPath)`
which recursively finds book files across *all* subdirectories in that root — not just
the torrent being processed.

This is identical in impact to Bug #3 (qBittorrent SavePath fallback) but affects
Transmission. All completed torrents that share a common download root are at risk.

**Symptoms:**
- Multiple book files imported for a single download record (one per torrent in the root)
- Incorrect `.mobi` or `.azw3` stubs created in the library for unrelated torrents
- Download status shows `imported` even though wrong files were used

**Workaround:** Configure Transmission to download each book torrent to a unique
subdirectory (e.g. using Transmission's per-torrent download path). This makes
`DownloadDir` unique per torrent and equivalent to the content path.

**Fix:** Added `resolveTransmissionContentPath(t transmission.Torrent)` which computes
`filepath.Join(DownloadDir, Name)` and verifies the path exists with `os.Stat` before
returning it. `checkTransmissionDownloads` now uses this resolved path for both the
initial import and the Bug #7 retry path. If the content path is absent (torrent still
flushing to disk), the download is left in its current state so the next poll cycle
retries — matching qBittorrent's behaviour.

---

## Bug 15: `importBlocked` is a terminal state with no retry path ✓ Fixed

**File:** `internal/models/download_state.go`, `internal/importer/scanner.go`

**Description:** The state machine defines `StateImportBlocked: {}` — no valid transitions
out of this state. The retry logic in all four `check*Downloads` loops only looks for
`StateImportFailed`; items that land in `StateImportBlocked` are silently abandoned and
never retried, regardless of whether the underlying cause has been resolved.

`StateImportBlocked` is used for transient infrastructure failures (e.g. destination
directory not writable, cross-filesystem hardlink when bind-mount sandboxing creates
separate device namespaces) as well as genuinely permanent blocks (e.g. no library
directory configured). The current design treats both the same way — permanent abandonment.

**Impact:** Any item that hits a transient import error and lands in `importBlocked` is
permanently stuck. The only recovery is a manual SQL update:
```sql
UPDATE downloads
SET status = 'importFailed', import_retry_count = 0
WHERE status = 'importBlocked';
```
Then restart the service to trigger the scanner. There is no UI action, no API endpoint,
and no automatic retry.

**Observed trigger:** Upgrading from a hand-rolled systemd unit (no `ProtectSystem`) to the
flake's NixOS module (adds `ProtectSystem=strict` + per-path `ReadWritePaths` bind mounts).
The separate bind mounts give distinct `st_dev` values even for directories on the same
underlying ZFS pool, causing `link()` to return `EXDEV`. Fixed by collapsing all
`/mnt/media` paths into a single `ReadWritePaths=/mnt/media` entry so one bind mount covers
the entire tree.

**Fix:** `StateImportBlocked` now has a valid transition to `StateImportPending`
(`download_state.go`). All four `check*Downloads` loops (`checkQbittorrentDownloads`,
`checkTransmissionDownloads`, `checkSABnzbdDownloads`, `checkNZBGetDownloads`) now treat
`StateImportBlocked` identically to `StateImportFailed` for retry purposes — downloads in
either state are retried up to `importRetryLimit` (3) times using the same
`IncrementImportRetryCount` cap, so a resolved transient block is automatically recovered
on the next poll cycle.

---

## Bug 16: Author name token matching fires across different people in multi-author releases (false positive grabs)

**Upstream:** vavallee/bindery#563 (open, no fix yet)

**File:** search/grab relevance pipeline — author matching logic

**Description:** The author relevance check tokenises the monitored author's name and
checks whether all tokens appear anywhere in the release's author field. It does not verify
that the tokens belong to the same individual. In a multi-author release this causes false
positives: monitored author "Rachel Reid" matches a release credited to "Rachel Larsen,
Adam Reid, and Ozi Akturk" because the tokens "Rachel" and "Reid" both appear — but on
different people.

The same mechanism triggers on shared surnames in single-author releases. Observed locally:
monitored "Ryan Cahill" auto-grabbed "How the Irish Saved Civilization - Thomas Cahill
[MP3] [64 Kbps]" (grabbed 2026-05-11 18:24) — "Cahill" matched the surname, and the
shared token was enough to clear the auto-grab threshold.

**Observed instance (2026-05-11):**
- Monitored author: `Ryan Cahill` — wanted book: `The Fall`
- Release grabbed: `How the Irish Saved Civilization: The Untold Story of Irelands Heroic Role from the Fall of Rome to the Rise of Medieval Europe - Thomas Cahill [MP3] [64 Kbps]`
- Author field in release: `Thomas Cahill` (single author, not multi-author)
- Imported to: `Ryan Cahill/The Fall (2021)/`
- Token `Cahill` matched the surname; `Ryan` likely matched via `"from the Fall of Rome"` in the title string

Relevant log lines:
```
{"msg":"auto-grabbing book","book":"The Fall","author":"Ryan Cahill","format":"audiobook","result":"How the Irish Saved Civilization: The Untold Story of Irelands Heroic Role from the Fall of Rome to the Rise of Medieval Europe - Thomas Cahill [MP3] [64 Kbps]","indexer":"Audiobookbay","protocol":"torrent","client":"qBittorrent","size":236778944}
{"msg":"download completed","title":"How the Irish Saved Civilization: The Untold Story of Irelands Heroic Role from the Fall of Rome to the Rise of Medieval Europe - Thomas Cahill [MP3] [64 Kbps]","path":"/mnt/media/downloads/complete/Thomas Cahill - How the Irish Saved Civilization"}
{"msg":"importing audiobook folder","src":"/mnt/media/downloads/complete/Thomas Cahill - How the Irish Saved Civilization","dst":"/mnt/media/audiobooks/Ryan Cahill/The Fall (2021)","mode":"hardlink"}
```

**Impact:** Unwanted downloads consume bandwidth, disk space, and indexer request quota.
With 1337x already rate-limited from bulk searches, false positive grabs compound the
quota problem.

**Fix needed:** Author token matching should require all name tokens to appear within a
single author entry (i.e. after splitting the author field on commas/semicolons), not
anywhere in the combined string. Upstream issue vavallee/bindery#563 is open; no fix
merged as of 2026-05-12.

---

## Bug 17: Queue UI shows no timestamps — impossible to distinguish new failures from old ones ✓ Fixed

**Files:** Frontend queue view; `internal/db/downloads.go`; migrations

**Description:** Two compounding problems:

**17a — Timestamps not stored.** The `downloads` table has `added_at`, `grabbed_at`,
`completed_at`, and `imported_at` columns but all are NULL for every row — none of the
write paths populate them. Confirmed via direct DB inspection: `SELECT added_at, grabbed_at
FROM downloads ORDER BY rowid DESC LIMIT 5` returns NULL for all rows. The schema defaults
`added_at` to `CURRENT_TIMESTAMP` but the INSERT must be overriding it with NULL explicitly,
or the ORM is not relying on the default.

**17b — Timestamps not surfaced in the UI.** Even if timestamps were populated, the queue
interface shows no temporal information — no grabbed time, no last-attempted time, no age.
All items look identical regardless of whether they were grabbed 2 minutes ago or 6 months
ago.

**Impact:**
- After a service restart or config change that resolves an underlying cause, there is no
  way to tell which failures are newly occurring vs. which were already present before the
  fix — the queue appears unchanged even when it isn't.
- The only way to get any temporal context is raw SQL, and even that returns NULLs.
- Makes triage slow and error-prone; a long-standing unresolvable failure looks identical
  to a brand-new one that should be investigated immediately.

**Fix:**
1. `DownloadRepo.Create` now explicitly passes `now` for `added_at`; `Transition` sets
   `grabbed_at`, `completed_at`, and `imported_at` at the appropriate state changes.
2. Frontend: `Download` interface gains `addedAt`, `grabbedAt`, and `importRetryCount`
   fields. Queue rows now display a relative timestamp (`3h ago`) and retry count
   (`attempt 2 of 3`) so operators can tell new failures from stale ones at a glance.

---

## Notes

- NZBgeek consistently times out when routed through `get.jett.sh/prowlarr` (Caddy reverse
  proxy). Direct internal address works. Cause: proxy latency pushes total round-trip over
  the hardcoded 15 s Prowlarr client timeout (Bug 5).
- 1337x was rate-limited until 2026-05-10 after the mass search session.
- `auth.mode=enabled` in settings; login requires `X-Requested-With: bindery-ui` AND a
  CSRF token fetched from `GET /api/v1/auth/csrf` (field: `csrfToken`).
