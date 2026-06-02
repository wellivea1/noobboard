package audit

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellivea1/server-status/internal/config"
	"github.com/wellivea1/server-status/internal/db"
	"github.com/wellivea1/server-status/internal/privacy"
)

func TestRecordRedactsNestedDetails(t *testing.T) {
	store, err := db.OpenFileStore(filepath.Join(t.TempDir(), "audit.db.json"))
	if err != nil {
		t.Fatal(err)
	}
	auditor := New(store, privacy.NewRedactor(config.PrivacyConfig{}))

	auditor.Record("user-1", "test.nested", map[string]interface{}{
		"top": "api_key=secret-token",
		"nested": map[string]interface{}{
			"password": "password=hunter2",
			"items": []interface{}{
				"authorization: Bearer abc123",
				map[string]interface{}{"cookie": "cookie=session-id"},
			},
		},
	})

	entries, err := store.AuditTail(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d", len(entries))
	}
	if !entries[0].Redacted {
		t.Fatal("audit entry did not report redaction")
	}
	text := strings.ToLower(mustJSON(t, entries[0].Details))
	for _, leaked := range []string{"secret-token", "hunter2", "abc123", "session-id"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("audit details leaked %q: %s", leaked, text)
		}
	}
}

func mustJSON(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
