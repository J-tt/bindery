package metadata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vavallee/bindery/internal/metadata/audnex"
	"github.com/vavallee/bindery/internal/models"
)

type stubAudnexClient struct {
	books map[string]*audnex.Book
	calls int
}

func (s *stubAudnexClient) GetBook(_ context.Context, asin string) (*audnex.Book, error) {
	s.calls++
	if s.books == nil {
		return nil, nil
	}
	return s.books[asin], nil
}

func TestAggregator_EnrichAudiobook_NonAudiobook(t *testing.T) {
	agg := newTestAggregator(&mockProvider{name: "ol"})
	book := &models.Book{Title: "Ebook", MediaType: models.MediaTypeEbook, ASIN: "B001"}
	if err := agg.EnrichAudiobook(context.Background(), book); err != nil {
		t.Fatalf("EnrichAudiobook (ebook): %v", err)
	}
}

func TestAggregator_EnrichAudiobook_NilBook(t *testing.T) {
	agg := newTestAggregator(&mockProvider{name: "ol"})
	if err := agg.EnrichAudiobook(context.Background(), nil); err != nil {
		t.Fatalf("EnrichAudiobook(nil): %v", err)
	}
}

func TestAggregator_EnrichAudiobook_NoASIN(t *testing.T) {
	agg := newTestAggregator(&mockProvider{name: "ol"})
	book := &models.Book{Title: "Audiobook", MediaType: models.MediaTypeAudiobook, ASIN: ""}
	if err := agg.EnrichAudiobook(context.Background(), book); err != nil {
		t.Fatalf("EnrichAudiobook (no ASIN): %v", err)
	}
}

// TestAggregator_GetAuthorAudiobooks_Unconfigured verifies the nil-audible
// path used by every test aggregator returns an empty result instead of
// panicking — the aggregator is constructed without an audible.Client in
// unit tests, and callers rely on a safe fallback.
func TestAggregator_GetAuthorAudiobooks_Unconfigured(t *testing.T) {
	agg := newTestAggregator(&mockProvider{name: "ol"})
	books, err := agg.GetAuthorAudiobooks(context.Background(), "Frank Herbert")
	if err != nil {
		t.Fatalf("GetAuthorAudiobooks (nil client): %v", err)
	}
	if books != nil {
		t.Errorf("want nil, got %v", books)
	}
}

// TestAggregator_GetAuthorAudiobooks_EmptyName guards against the trivial
// case where an unnamed author triggers an unfiltered Audible browse.
func TestAggregator_GetAuthorAudiobooks_EmptyName(t *testing.T) {
	agg := newTestAggregator(&mockProvider{name: "ol"})
	books, err := agg.GetAuthorAudiobooks(context.Background(), "   ")
	if err != nil {
		t.Fatalf("GetAuthorAudiobooks (empty): %v", err)
	}
	if books != nil {
		t.Errorf("want nil, got %v", books)
	}
}

func TestAggregator_GetCanonicalBookByASIN_CanonicalizesAudnexHit(t *testing.T) {
	primary := &mockProvider{
		name: "openlibrary",
		searchBooksByQuery: map[string][]models.Book{
			"Iron Flame Rebecca Yarros": {{
				ForeignID:    "OL-IRON",
				Title:        "Iron Flame",
				EditionCount: 42,
				Author:       &models.Author{Name: "Rebecca Yarros"},
			}},
		},
		getBookByID: map[string]*models.Book{
			"OL-IRON": {
				ForeignID:        "OL-IRON",
				Title:            "Iron Flame",
				Description:      "Canonical OpenLibrary description for Iron Flame.",
				MetadataProvider: "openlibrary",
				Author:           &models.Author{Name: "Rebecca Yarros"},
			},
		},
	}
	audnexClient := &stubAudnexClient{books: map[string]*audnex.Book{
		"B0D3R3MTLM": {
			ASIN:     "B0D3R3MTLM",
			Title:    "Iron Flame (Part 2 of 2) (Dramatized Adaptation)",
			Authors:  []audnex.Person{{Name: "Rebecca Yarros"}},
			Language: "English",
		},
	}}
	agg := newTestAggregator(primary).WithAudnexClient(audnexClient)

	got, err := agg.GetCanonicalBookByASIN(context.Background(), " b0d3r3mtlm ")
	if err != nil {
		t.Fatalf("GetCanonicalBookByASIN: %v", err)
	}
	if got == nil || got.ForeignID != "OL-IRON" {
		t.Fatalf("GetCanonicalBookByASIN = %+v, want OL-IRON", got)
	}

	got, err = agg.GetCanonicalBookByASIN(context.Background(), "B0D3R3MTLM")
	if err != nil {
		t.Fatalf("cached GetCanonicalBookByASIN: %v", err)
	}
	if got == nil || got.ForeignID != "OL-IRON" {
		t.Fatalf("cached GetCanonicalBookByASIN = %+v, want OL-IRON", got)
	}
	if audnexClient.calls != 1 {
		t.Fatalf("audnex calls = %d, want 1", audnexClient.calls)
	}
}

func TestAggregator_GetCanonicalBookByASIN_NoAudnexHit(t *testing.T) {
	agg := newTestAggregator(&mockProvider{name: "openlibrary"}).WithAudnexClient(&stubAudnexClient{})

	got, err := agg.GetCanonicalBookByASIN(context.Background(), "B0NONE")
	if err != nil {
		t.Fatalf("GetCanonicalBookByASIN: %v", err)
	}
	if got != nil {
		t.Fatalf("GetCanonicalBookByASIN = %+v, want nil", got)
	}
}

func TestAggregator_GetCanonicalBookByASIN_AmbiguousPrimaryMatch(t *testing.T) {
	primary := &mockProvider{
		name: "openlibrary",
		searchBooksByQuery: map[string][]models.Book{
			"Dune Frank Herbert": {
				{ForeignID: "OL-DUNE-1", Title: "Dune", EditionCount: 20, Author: &models.Author{Name: "Frank Herbert"}},
				{ForeignID: "OL-DUNE-2", Title: "Dune", EditionCount: 20, Author: &models.Author{Name: "Frank Herbert"}},
			},
		},
	}
	audnexClient := &stubAudnexClient{books: map[string]*audnex.Book{
		"B0036S4B2G": {
			ASIN:    "B0036S4B2G",
			Title:   "Dune",
			Authors: []audnex.Person{{Name: "Frank Herbert"}},
		},
	}}
	agg := newTestAggregator(primary).WithAudnexClient(audnexClient)

	got, err := agg.GetCanonicalBookByASIN(context.Background(), "B0036S4B2G")
	if err != nil {
		t.Fatalf("GetCanonicalBookByASIN: %v", err)
	}
	if got != nil {
		t.Fatalf("GetCanonicalBookByASIN = %+v, want nil for ambiguous primary match", got)
	}
}

func TestAggregator_EnrichBook_SkipsOnSearchError(t *testing.T) {
	primary := &mockProvider{
		name:    "ol",
		getBook: &models.Book{Title: "Error Test", Description: "x"},
	}
	enricher := &mockProvider{
		name:          "hc",
		searchBookErr: errors.New("hardcover unavailable"),
	}
	agg := &Aggregator{
		primary:   primary,
		enrichers: []Provider{enricher},
		cache:     newTTLCache(time.Minute),
	}

	got, err := agg.GetBook(context.Background(), "OL002W")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	// Description should remain unchanged since enricher errored.
	if got.Description != "x" {
		t.Errorf("description changed unexpectedly: %q", got.Description)
	}
}

func TestAggregator_enrichBook_FillsCoverWhenMissing(t *testing.T) {
	enricher := &mockProvider{
		name:        "gb",
		searchBooks: []models.Book{{Description: "A description.", ImageURL: "https://books.google.com/cover.jpg"}},
	}
	agg := &Aggregator{enrichers: []Provider{enricher}, cache: newTTLCache(time.Minute)}

	book := &models.Book{Title: "Sapiens", ImageURL: ""}
	agg.enrichBook(context.Background(), book)
	if book.ImageURL != "https://books.google.com/cover.jpg" {
		t.Errorf("expected cover to be filled from enricher, got %q", book.ImageURL)
	}
}

func TestAggregator_enrichBook_KeepsExistingCover(t *testing.T) {
	enricher := &mockProvider{
		name:        "gb",
		searchBooks: []models.Book{{ImageURL: "https://books.google.com/other.jpg"}},
	}
	agg := &Aggregator{enrichers: []Provider{enricher}, cache: newTTLCache(time.Minute)}

	existing := "https://covers.openlibrary.org/b/id/123-L.jpg"
	book := &models.Book{Title: "Dune", ImageURL: existing}
	agg.enrichBook(context.Background(), book)
	if book.ImageURL != existing {
		t.Errorf("existing cover should not be replaced, got %q", book.ImageURL)
	}
}

func TestAggregator_GetBook_NoCover_EnrichedFromProvider(t *testing.T) {
	// Book has a long description so the old trigger wouldn't fire — but
	// ImageURL is empty so enrichment must still run.
	primary := &mockProvider{
		name: "ol",
		getBook: &models.Book{
			Title:       "21 Lessons for the 21st Century",
			Description: "A sufficiently long description that would previously have skipped enrichment entirely.",
			ImageURL:    "",
		},
	}
	enricher := &mockProvider{
		name:        "gb",
		searchBooks: []models.Book{{ImageURL: "https://books.google.com/cover-en.jpg"}},
	}
	agg := &Aggregator{primary: primary, enrichers: []Provider{enricher}, cache: newTTLCache(time.Minute)}

	got, err := agg.GetBook(context.Background(), "OL123W")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if got.ImageURL != "https://books.google.com/cover-en.jpg" {
		t.Errorf("expected cover from enricher, got %q", got.ImageURL)
	}
}

func TestAggregator_GetAuthorWorks_CoversEnrichedForMissingOnes(t *testing.T) {
	primary := &mockWorksProvider{
		mockProvider: mockProvider{
			name: "ol",
			authorWorks: []models.Book{
				{ForeignID: "OL1W", Title: "Sapiens", ImageURL: ""},
				{ForeignID: "OL2W", Title: "Homo Deus", ImageURL: "https://covers.openlibrary.org/b/id/999-L.jpg"},
			},
		},
	}
	enricher := &mockProvider{
		name:        "gb",
		searchBooks: []models.Book{{ImageURL: "https://books.google.com/sapiens.jpg"}},
	}
	agg := &Aggregator{primary: primary, enrichers: []Provider{enricher}, cache: newTTLCache(time.Minute)}

	got, err := agg.GetAuthorWorks(context.Background(), "OL123A")
	if err != nil {
		t.Fatalf("GetAuthorWorks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 works, got %d", len(got))
	}
	// Book without OL cover should get enriched cover
	if got[0].ImageURL != "https://books.google.com/sapiens.jpg" {
		t.Errorf("Sapiens: expected enriched cover, got %q", got[0].ImageURL)
	}
	// Book with OL cover should keep it
	if got[1].ImageURL != "https://covers.openlibrary.org/b/id/999-L.jpg" {
		t.Errorf("Homo Deus: expected OL cover preserved, got %q", got[1].ImageURL)
	}
}

func TestAggregator_GetAuthorWorks_NoEnrichersNoCovers(t *testing.T) {
	// With no enrichers, works without covers stay coverless — no panic.
	primary := &mockWorksProvider{
		mockProvider: mockProvider{
			name:        "ol",
			authorWorks: []models.Book{{ForeignID: "OL1W", Title: "No Cover Book", ImageURL: ""}},
		},
	}
	agg := &Aggregator{primary: primary, cache: newTTLCache(time.Minute)}

	got, err := agg.GetAuthorWorks(context.Background(), "OL456A")
	if err != nil {
		t.Fatalf("GetAuthorWorks: %v", err)
	}
	if got[0].ImageURL != "" {
		t.Errorf("expected empty cover with no enrichers, got %q", got[0].ImageURL)
	}
}
