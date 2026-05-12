package metadata_test

import (
	"strings"
	"testing"
)

const (
	binderyIntegrationEnv       = "BINDERY_INTEGRATION"
	binderyHardcoverAPITokenEnv = "BINDERY_HARDCOVER_API_TOKEN"
	googleBooksAPIKeyEnv        = "GOOGLE_BOOKS_API_KEY"
)

func skipIfLiveProviderUnavailableError(t *testing.T, provider string, err error) {
	t.Helper()
	if isLiveProviderUnavailableError(err) {
		t.Skipf("skipping %s live lookup; provider quota, rate limit, or access is unavailable", provider)
	}
}

func isLiveProviderUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "http 429") ||
		strings.Contains(msg, "quota exceeded") ||
		strings.Contains(msg, "ratelimitexceeded") ||
		strings.Contains(msg, "resource_exhausted") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate limit") ||
		(strings.Contains(msg, "http 403") && (strings.Contains(msg, "permission_denied") ||
			strings.Contains(msg, "api_key_service_blocked") ||
			strings.Contains(msg, "requests to this api") ||
			strings.Contains(msg, "accessnotconfigured") ||
			strings.Contains(msg, "api key not valid")))
}
