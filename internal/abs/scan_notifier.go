package abs

import (
	"context"
	"log/slog"

	"github.com/vavallee/bindery/internal/db"
)

// ScanNotifier triggers an ABS library scan after a successful audiobook
// import (Bug #10). It reads ABS credentials from the settings DB at call
// time so that config changes take effect without restarting Bindery.
type ScanNotifier struct {
	settings *db.SettingsRepo
}

// NewScanNotifier returns a ScanNotifier backed by the given settings repo.
func NewScanNotifier(settings *db.SettingsRepo) *ScanNotifier {
	return &ScanNotifier{settings: settings}
}

// ScanLibrary triggers an ABS folder scan for the given library. If ABS is
// not configured (empty base_url or api_key), the call is a no-op.
func (n *ScanNotifier) ScanLibrary(ctx context.Context, libraryID string) error {
	baseURL := n.getSetting(ctx, "abs.base_url")
	apiKey := n.getSetting(ctx, "abs.api_key")
	if baseURL == "" || apiKey == "" {
		slog.Debug("abs: ScanNotifier skipped — ABS not configured")
		return nil
	}
	client, err := NewClient(baseURL, apiKey)
	if err != nil {
		return err
	}
	return client.ScanLibrary(ctx, libraryID)
}

func (n *ScanNotifier) getSetting(ctx context.Context, key string) string {
	s, err := n.settings.Get(ctx, key)
	if err != nil || s == nil {
		return ""
	}
	return s.Value
}
