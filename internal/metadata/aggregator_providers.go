package metadata

import "strings"

func (a *Aggregator) providerForForeignID(foreignID string) Provider {
	if a == nil {
		return nil
	}
	want := providerNameForForeignID(foreignID)
	if want == "" {
		return a.primary
	}
	for _, provider := range a.providers() {
		if provider == nil {
			continue
		}
		if normalizedProviderName(provider.Name()) == want {
			return provider
		}
	}
	if want == "openlibrary" || want == normalizedProviderName(providerName(a.primary)) {
		return a.primary
	}
	return nil
}

func providerName(provider Provider) string {
	if provider == nil {
		return ""
	}
	return provider.Name()
}

func sameProvider(a, b Provider) bool {
	return normalizedProviderName(providerName(a)) == normalizedProviderName(providerName(b))
}

func providerNameForForeignID(foreignID string) string {
	foreignID = strings.TrimSpace(foreignID)
	switch {
	case strings.HasPrefix(foreignID, "gb:"):
		return "googlebooks"
	case strings.HasPrefix(foreignID, "hc:"):
		return "hardcover"
	case strings.HasPrefix(foreignID, "dnb:"):
		return "dnb"
	default:
		return "openlibrary"
	}
}

func normalizedProviderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ol", "openlibrary", "open_library":
		return "openlibrary"
	case "gb", "googlebooks", "google_books":
		return "googlebooks"
	case "hc", "hardcover":
		return "hardcover"
	case "dnb":
		return "dnb"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

func (a *Aggregator) providers() []Provider {
	if a == nil {
		return nil
	}
	providers := make([]Provider, 0, len(a.enrichers)+1)
	if a.primary != nil {
		providers = append(providers, a.primary)
	}
	providers = append(providers, a.enrichers...)
	return providers
}
