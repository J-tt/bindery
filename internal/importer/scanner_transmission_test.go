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
	"github.com/vavallee/bindery/internal/models"
)

// TestCheckTransmissionDownloads_UsesContentPathNotDownloadDir is the
// regression test for Bug #14.
//
// Scenario: a Transmission torrent named "Dune" has completed downloading into
// a subdirectory "<downloadDir>/Dune/". A second, unrelated torrent also lives
// in the same parent directory ("<downloadDir>/WheelOfTime/"). Before the fix,
// checkTransmissionDownloads passed torrent.DownloadDir (the shared parent) as
// the download path, causing tryImportInternal to walk the entire parent
// directory and import files from both torrents. After the fix it must resolve
// the content path to "<downloadDir>/Dune" and import only that subtree.
func TestCheckTransmissionDownloads_UsesContentPathNotDownloadDir(t *testing.T) {
	t.Parallel()

	// Parent directory simulating a shared Transmission download root.
	parentDir := t.TempDir()

	// Target torrent content: "Dune" multi-file torrent.
	duneDir := filepath.Join(parentDir, "Dune")
	if err := os.MkdirAll(duneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(duneDir, "Dune.epub"), []byte("dune epub content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unrelated torrent content in the same parent — must NOT be imported.
	// Use .mobi so the renamer assigns a different destination path than the
	// .epub file above; this ensures both would succeed if the parent dir is
	// walked, making the bug detectable as two library files instead of one.
	wotDir := filepath.Join(parentDir, "WheelOfTime")
	if err := os.MkdirAll(wotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wotDir, "WheelOfTime.mobi"), []byte("wot mobi content"), 0o644); err != nil {
		t.Fatal(err)
	}

	libraryDir := t.TempDir()

	// Transmission mock: returns one seeding torrent with name="Dune".
	// The "name" field is included so the fixed code can compute
	// filepath.Join(downloadDir, name) = "<parentDir>/Dune".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transmission/rpc" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": "success",
			"arguments": map[string]any{
				"torrents": []map[string]any{{
					"id":          42,
					"hashString":  "deadbeef",
					"name":        "Dune",
					"status":      3, // seeding (complete)
					"percentDone": 1.0,
					"downloadDir": parentDir,
					"errorString": "",
				}},
			},
		})
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

	s := NewScanner(dlRepo, clientRepo, bookRepo, authorRepo, histRepo, libraryDir, "", "", "", "")

	host, port := scannerTestHostPort(t, srv.URL)
	client := &models.DownloadClient{
		Name:    "transmission-bug14",
		Type:    "transmission",
		Host:    host,
		Port:    port,
		Enabled: true,
	}
	if err := clientRepo.Create(ctx, client); err != nil {
		t.Fatalf("create client: %v", err)
	}

	author := &models.Author{ForeignID: "OL-frank-herbert", Name: "Frank Herbert", SortName: "Herbert, Frank"}
	if err := authorRepo.Create(ctx, author); err != nil {
		t.Fatal(err)
	}
	book := &models.Book{
		ForeignID: "OL-dune",
		AuthorID:  author.ID,
		Title:     "Dune",
		Status:    models.BookStatusWanted,
		MediaType: models.MediaTypeEbook,
	}
	if err := bookRepo.Create(ctx, book); err != nil {
		t.Fatal(err)
	}

	torrentID := "42" // matches fmt.Sprintf("%d", torrent.ID)
	dl := &models.Download{
		GUID:             "guid-transmission-bug14",
		Title:            "Dune",
		NZBURL:           "magnet:?xt=urn:btih:deadbeef",
		Status:           models.StateDownloading,
		Protocol:         "torrent",
		TorrentID:        &torrentID,
		BookID:           &book.ID,
		DownloadClientID: &client.ID,
	}
	if err := dlRepo.Create(ctx, dl); err != nil {
		t.Fatalf("create download: %v", err)
	}

	s.checkTransmissionDownloads(ctx, client)

	// Collect all files imported into the library directory.
	var libFiles []string
	_ = filepath.Walk(libraryDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		libFiles = append(libFiles, filepath.Base(path))
		return nil
	})

	// Bug #14: before the fix, checkTransmissionDownloads passes
	// torrent.DownloadDir (the shared parent) as downloadPath. Walking the
	// parent finds both Dune.epub AND WheelOfTime.mobi; both have different
	// extensions so the renamer produces different destination paths and both
	// succeed — the library ends up with 2 files instead of 1. After the fix,
	// the content path is resolved to parentDir/Dune, so only Dune.epub is
	// imported.
	for _, f := range libFiles {
		if f == "WheelOfTime.mobi" {
			t.Errorf("Bug #14 regression: Transmission import walked the shared download root and imported %q, which belongs to a different torrent", f)
		}
	}
	if len(libFiles) != 1 {
		t.Errorf("expected exactly 1 imported file (Dune.epub), got %d: %v", len(libFiles), libFiles)
	}
}
