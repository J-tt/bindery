package metadata

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

const (
	binderyIntegrationEnv    = "BINDERY_INTEGRATION"
	binderyTestLoadDotenvEnv = "BINDERY_TEST_LOAD_DOTENV"
	googleBooksAPIKeyEnv     = "GOOGLE_BOOKS_API_KEY"
)

func TestMain(m *testing.M) {
	loadLiveTestDotenv()
	os.Exit(m.Run())
}

func loadLiveTestDotenv() {
	if os.Getenv(binderyIntegrationEnv) != "1" || os.Getenv(binderyTestLoadDotenvEnv) != "1" {
		return
	}
	path, ok := findLiveTestDotenv()
	if !ok {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := parseLiveDotenvLine(scanner.Text())
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
	}
}

func findLiveTestDotenv() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			candidate := filepath.Join(dir, ".env")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, true
			}
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func parseLiveDotenvLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if !validLiveDotenvKey(key) {
		return "", "", false
	}
	return key, parseLiveDotenvValue(value), true
}

func validLiveDotenvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func parseLiveDotenvValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if value[0] == '"' || value[0] == '\'' {
		return parseLiveDotenvQuotedValue(value, rune(value[0]))
	}
	return stripLiveDotenvComment(value)
}

func parseLiveDotenvQuotedValue(value string, quote rune) string {
	var b strings.Builder
	escaped := false
	for _, r := range value[1:] {
		if quote == '"' && escaped {
			switch r {
			case 'n':
				b.WriteRune('\n')
			case 'r':
				b.WriteRune('\r')
			case 't':
				b.WriteRune('\t')
			default:
				b.WriteRune(r)
			}
			escaped = false
			continue
		}
		if quote == '"' && r == '\\' {
			escaped = true
			continue
		}
		if r == quote {
			return b.String()
		}
		b.WriteRune(r)
	}
	return b.String()
}

func stripLiveDotenvComment(value string) string {
	previousSpace := true
	for i, r := range value {
		if r == '#' && previousSpace {
			return strings.TrimSpace(value[:i])
		}
		previousSpace = unicode.IsSpace(r)
	}
	return strings.TrimSpace(value)
}

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
