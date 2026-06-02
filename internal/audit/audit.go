package audit

import (
	"fmt"
	"time"

	"github.com/wellivea1/server-status/internal/db"
	"github.com/wellivea1/server-status/internal/models"
	"github.com/wellivea1/server-status/internal/privacy"
)

type Auditor struct {
	store    db.Store
	redactor *privacy.Redactor
}

func New(store db.Store, redactor *privacy.Redactor) *Auditor {
	return &Auditor{store: store, redactor: redactor}
}

func (a *Auditor) Record(actor, action string, details map[string]interface{}) {
	redacted := false
	for key, value := range details {
		var changed bool
		details[key], changed = a.redactValue(value)
		redacted = redacted || changed
	}
	_ = a.store.AppendAudit(models.AuditEntry{
		ID:       fmt.Sprintf("audit-%d", time.Now().UnixNano()),
		Time:     time.Now().UTC(),
		Actor:    actor,
		Action:   action,
		Redacted: redacted,
		Details:  details,
	})
}

func (a *Auditor) redactValue(value interface{}) (interface{}, bool) {
	switch typed := value.(type) {
	case string:
		result := a.redactor.RedactString(typed)
		return result.Text, result.Changed
	case []string:
		changed := false
		out := make([]string, len(typed))
		for i, item := range typed {
			result := a.redactor.RedactString(item)
			out[i] = result.Text
			changed = changed || result.Changed
		}
		return out, changed
	case []interface{}:
		changed := false
		out := make([]interface{}, len(typed))
		for i, item := range typed {
			var itemChanged bool
			out[i], itemChanged = a.redactValue(item)
			changed = changed || itemChanged
		}
		return out, changed
	case map[string]interface{}:
		changed := false
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			var itemChanged bool
			out[key], itemChanged = a.redactValue(item)
			changed = changed || itemChanged
		}
		return out, changed
	default:
		return value, false
	}
}
