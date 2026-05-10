package prowlarr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNew_DefaultTimeout(t *testing.T) {
	c := New("http://prowlarr.local:9696", "key")
	if c.http.Timeout != 60*time.Second {
		t.Errorf("default timeout = %v, want 60s", c.http.Timeout)
	}
}

func TestNewWithTimeout(t *testing.T) {
	c := NewWithTimeout("http://prowlarr.local:9696", "key", 30*time.Second)
	if c.http.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", c.http.Timeout)
	}
}

func TestNew_StripTrailingSlash(t *testing.T) {
	c := New("http://prowlarr.local:9696/", "key")
	if strings.HasSuffix(c.baseURL, "/") {
		t.Errorf("baseURL should have trailing slash stripped, got %q", c.baseURL)
	}
}

func TestFetchIndexers_HappyPath(t *testing.T) {
	body := `[
		{"id":1,"name":"Tracker1","protocol":"torrent","supportsSearch":true,"categories":[{"id":7000},{"id":7020}]},
		{"id":2,"name":"NZBHydra","protocol":"usenet","supportsSearch":false,"categories":[]}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/indexer" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != "secret" {
			t.Errorf("expected X-Api-Key header, got %q", r.Header.Get("X-Api-Key"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret")
	infos, err := c.FetchIndexers(context.Background())
	if err != nil {
		t.Fatalf("FetchIndexers: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 infos, got %d", len(infos))
	}

	t1 := infos[0]
	if t1.ProwlarrID != 1 {
		t.Errorf("ProwlarrID = %d, want 1", t1.ProwlarrID)
	}
	if t1.Name != "Tracker1" {
		t.Errorf("Name = %q, want Tracker1", t1.Name)
	}
	if t1.Protocol != "torrent" {
		t.Errorf("Protocol = %q, want torrent", t1.Protocol)
	}
	if !t1.SupportsSearch {
		t.Errorf("SupportsSearch = false, want true")
	}
	if t1.TorznabURL != srv.URL+"/1/api" {
		t.Errorf("TorznabURL = %q, want %q", t1.TorznabURL, srv.URL+"/1/api")
	}
	if t1.APIKey != "secret" {
		t.Errorf("APIKey = %q, want secret", t1.APIKey)
	}
	if len(t1.Categories) != 2 || t1.Categories[0] != 7000 || t1.Categories[1] != 7020 {
		t.Errorf("Categories = %v, want [7000 7020]", t1.Categories)
	}

	// Second indexer: no categories → empty slice
	t2 := infos[1]
	if t2.Name != "NZBHydra" {
		t.Errorf("Name = %q, want NZBHydra", t2.Name)
	}
	if len(t2.Categories) != 0 {
		t.Errorf("Categories = %v, want []", t2.Categories)
	}
}

func TestFetchIndexers_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "key")
	infos, err := c.FetchIndexers(context.Background())
	if err != nil {
		t.Fatalf("FetchIndexers: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected empty slice, got %d", len(infos))
	}
}

func TestFetchIndexers_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, "wrong")
	_, err := c.FetchIndexers(context.Background())
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
	if !strings.Contains(err.Error(), "invalid Prowlarr API key") {
		t.Errorf("unexpected error %v", err)
	}
}

func TestFetchIndexers_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	c := New(srv.URL, "key")
	_, err := c.FetchIndexers(context.Background())
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got %v", err)
	}
}

func TestFetchIndexers_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := New(srv.URL, "key")
	_, err := c.FetchIndexers(context.Background())
	if err == nil {
		t.Fatal("expected error on bad JSON, got nil")
	}
	if !strings.Contains(err.Error(), "decode prowlarr indexers") {
		t.Errorf("expected decode error, got %v", err)
	}
}

func TestTest_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/system/status" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":"1.2.3"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "key")
	version, err := c.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", version)
	}
}

func TestTest_BadJSON_ReturnsEmptyVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not valid json`))
	}))
	defer srv.Close()

	c := New(srv.URL, "key")
	version, err := c.Test(context.Background())
	if err != nil {
		t.Fatalf("expected nil error on bad JSON, got %v", err)
	}
	if version != "" {
		t.Errorf("expected empty version on bad JSON, got %q", version)
	}
}

func TestTest_NetworkError(t *testing.T) {
	// Start and immediately close a server so the port is unreachable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()

	c := New(addr, "key")
	_, err := c.Test(context.Background())
	if err == nil {
		t.Fatal("expected error on connection refused, got nil")
	}
	if !strings.Contains(err.Error(), "could not reach Prowlarr") {
		t.Errorf("expected 'could not reach Prowlarr' in error, got %v", err)
	}
}
