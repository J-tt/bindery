package metadata

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
)

func (a *Aggregator) SearchSeries(ctx context.Context, query string, limit int) ([]SeriesSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	key := "series-search:" + strings.ToLower(query) + ":" + strconv.Itoa(limit)
	if cached, ok := a.cache.get(key); ok {
		return cached.([]SeriesSearchResult), nil
	}
	var lastErr error
	for _, provider := range a.seriesCatalogProviders() {
		results, err := provider.SearchSeries(ctx, query, limit)
		if err != nil {
			lastErr = err
			slog.Debug("series search failed", "error", err)
			continue
		}
		if results == nil {
			results = []SeriesSearchResult{}
		}
		a.cache.set(key, results)
		return results, nil
	}
	return nil, lastErr
}

// GetSeriesCatalog fetches the ordered book catalog for a provider series.
func (a *Aggregator) GetSeriesCatalog(ctx context.Context, foreignID string) (*SeriesCatalog, error) {
	foreignID = strings.TrimSpace(foreignID)
	if foreignID == "" {
		return nil, nil
	}
	key := "series-catalog:" + foreignID
	if cached, ok := a.cache.get(key); ok {
		return cached.(*SeriesCatalog), nil
	}
	var lastErr error
	for _, provider := range a.seriesCatalogProviders() {
		catalog, err := provider.GetSeriesCatalog(ctx, foreignID)
		if err != nil {
			lastErr = err
			slog.Debug("series catalog failed", "foreignID", foreignID, "error", err)
			continue
		}
		if catalog != nil {
			a.cache.set(key, catalog)
		}
		return catalog, nil
	}
	return nil, lastErr
}

func (a *Aggregator) seriesCatalogProviders() []SeriesCatalogProvider {
	if a == nil {
		return nil
	}
	providers := make([]SeriesCatalogProvider, 0, len(a.enrichers)+1)
	if provider, ok := a.primary.(SeriesCatalogProvider); ok {
		providers = append(providers, provider)
	}
	for _, enricher := range a.enrichers {
		if provider, ok := enricher.(SeriesCatalogProvider); ok {
			providers = append(providers, provider)
		}
	}
	return providers
}
