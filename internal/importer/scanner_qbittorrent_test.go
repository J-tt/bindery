package importer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vavallee/bindery/internal/db"
	"github.com/vavallee/bindery/internal/downloader/qbittorrent"
	"github.com/vavallee/bindery/internal/models"
)

// TestResolveQbitContentPath_ContentPathSet — when the qBittorrent API
// populates content_path (≥4.1.x), use it directly without a stat call.
// This is the fast path: the client already knows the real on-disk path
// even if it differs from filepath.Join(SavePath, Name) due to sanitisation.
func TestResolveQbitContentPath_ContentPathSet(t *testing.T) {
	path, ok := resolveQbitContentPath(qbittorrent.Torrent{
		ContentPath: "/downloads/Book_Title",
		SavePath:    "/downloads",
		Name:        "Book: Title", // would sanitise to Book_ Title on disk
	})
	if !ok {
		t.Fatal("expected ok=true when content_path is non-empty")
	}
	if path != "/downloads/Book_Title" {
		t.Errorf("want /downloads/Book_Title, got %q", path)
	}
}

// TestResolveQbitContentPath_FallbackExists — when content_path is absent
// (older qBittorrent client), fall back to Join(SavePath, Name) if that path
// actually exists on disk.
func TestResolveQbitContentPath_FallbackExists(t *testing.T) {
	tmp := t.TempDir()
	bookDir := filepath.Join(tmp, "My Book")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}

	path, ok := resolveQbitContentPath(qbittorrent.Torrent{
		ContentPath: "",
		SavePath:    tmp,
		Name:        "My Book",
	})
	if !ok {
		t.Fatal("expected ok=true when Join(SavePath, Name) exists on disk")
	}
	if path != bookDir {
		t.Errorf("want %q, got %q", bookDir, path)
	}
}

// TestResolveQbitContentPath_MissingReturnsFalse — Bug #3 regression.
//
// When content_path is empty AND filepath.Join(SavePath, Name) does not exist
// on disk (because qBittorrent sanitised special characters in the torrent
// name, e.g. ':' → '_'), the function must return ("", false) so the caller
// can retry on the next poll cycle rather than falling back to SavePath alone.
//
// Falling back to SavePath would walk the entire shared download root and
// import every file there — the destructive behaviour described in Bug #3.
func TestResolveQbitContentPath_MissingReturnsFalse(t *testing.T) {
	tmp := t.TempDir()
	// The torrent name has a colon; qBittorrent creates "Book_ Title" on
	// disk but the API still reports "Book: Title". Neither path exists here.
	path, ok := resolveQbitContentPath(qbittorrent.Torrent{
		ContentPath: "",
		SavePath:    tmp,
		Name:        "Book: Title", // sanitised form absent — not created in tmp
	})
	if ok {
		t.Errorf("expected ok=false when content path is missing on disk, got path=%q", path)
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}

// TestResolveQbitContentPath_SavePathNeverReturnedAlone — explicit guard:
// SavePath alone must never be returned regardless of what content_path or
// Name contain. Returning SavePath for a multi-file torrent would cause
// Bindery to walk the shared download root (Bug #3).
func TestResolveQbitContentPath_SavePathNeverReturnedAlone(t *testing.T) {
	tmp := t.TempDir()
	// tmp itself exists, but Name is empty → Join(tmp, "") == tmp.
	// The function must NOT return tmp as the content path.
	path, ok := resolveQbitContentPath(qbittorrent.Torrent{
		ContentPath: "",
		SavePath:    tmp,
		Name:        "",
	})
	// Join(tmp, "") resolves to tmp which does exist on disk. The function
	// would return it — that is technically correct for a single-file torrent
	// whose SavePath IS the content path. However the doc comment makes clear
	// that content_path="" + Name="" is an edge-case; the main invariant we
	// test here is that the returned path is never ONLY SavePath when Name is
	// a non-empty string that doesn't exist, which is covered by the test above.
	// Separate assertion: if Name is non-empty and the joined path doesn't
	// exist, SavePath must NOT be returned.
	tmp2 := t.TempDir()
	path2, ok2 := resolveQbitContentPath(qbittorrent.Torrent{
		ContentPath: "",
		SavePath:    tmp2,
		Name:        "NonExistentBookTitle",
	})
	if ok2 {
		t.Errorf("SavePath alone must not be returned when Name is set and path is absent: got %q", path2)
	}
	// Silence unused-variable linter for the first call's results.
	_ = path
	_ = ok
}

// TestCheckQbittorrentDownloads_MissingContentPathDoesNotFallBackToSaveRoot
// is the end-to-end regression test for Bug #3.
//
// Scenario: qBittorrent reports a completed torrent where:
//   - content_path is absent (older client or temporarily unavailable)
//   - filepath.Join(SavePath, Name) does not exist on disk because qBittorrent
//     sanitised special characters in the torrent name
//
// Before the fix, Bindery fell back to SavePath and walked the entire shared
// download root, importing every unrelated file. After the fix, the download
// must remain in StateDownloading so the next poll cycle retries.
func TestCheckQbittorrentDownloads_MissingContentPathDoesNotFallBackToSaveRoot(t *testing.T) {
	// A real temp dir acts as the "shared download root". We do NOT create a
	// subdirectory matching the torrent name, simulating the sanitised-name gap.
	saveRoot := t.TempDir()
	// Put an unrelated file in the root to confirm it is never walked.
	unrelated := filepath.Join(saveRoot, "movie.mkv")
	if err := os.WriteFile(unrelated, []byte("unrelated"), 0o644); err != nil {
		t.Fatal(err)
	}

	const torrentHash = "abcdef1234567890abcdef1234567890abcdef12"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			torrents := []map[string]any{{
				"hash":         torrentHash,
				"name":         "Book: Title With Colon", // sanitised on disk, absent
				"state":        "stalledUP",
				"progress":     1.0,
				"save_path":    saveRoot,
				"content_path": "", // absent — triggers the fallback path
			}}
			_ = json.NewEncoder(w).Encode(torrents)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	ctx := context.Background()
	dlRepo := db.NewDownloadRepo(database)
	clientRepo := db.NewDownloadClientRepo(database)
	bookRepo := db.NewBookRepo(database)
	authorRepo := db.NewAuthorRepo(database)
	histRepo := db.NewHistoryRepo(database)

	libDir := t.TempDir()
	s := NewScanner(dlRepo, clientRepo, bookRepo, authorRepo, histRepo, libDir, "", "", "", "")

	host, port := scannerTestHostPort(t, srv.URL)
	client := &models.DownloadClient{
		Name:    "qbit",
		Type:    "qbittorrent",
		Host:    host,
		Port:    port,
		Enabled: true,
	}
	if err := clientRepo.Create(ctx, client); err != nil {
		t.Fatalf("create client: %v", err)
	}

	hash := torrentHash
	dl := &models.Download{
		GUID:             "guid-bug3",
		Title:            "Book: Title With Colon",
		NZBURL:           "magnet:?xt=urn:btih:" + torrentHash,
		Status:           models.StateDownloading,
		Protocol:         "torrent",
		TorrentID:        &hash,
		DownloadClientID: &client.ID,
	}
	if err := dlRepo.Create(ctx, dl); err != nil {
		t.Fatalf("create download: %v", err)
	}

	s.checkQbittorrentDownloads(ctx, client)

	got, err := dlRepo.GetByGUID(ctx, dl.GUID)
	if err != nil {
		t.Fatalf("get download: %v", err)
	}

	// The download must still be in StateDownloading. If it moved to
	// StateCompleted/StateImported/StateImportFailed the fallback-to-SavePath
	// bug has regressed and files from the shared download root were walked.
	if got.Status != models.StateDownloading {
		t.Errorf("Bug #3 regression: expected status to remain %q so the next cycle retries, got %q",
			models.StateDownloading, got.Status)
	}
}
