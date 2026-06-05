package llm

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wellivea1/noobboard/internal/models"
)

type Diagnosis struct {
	Severity            models.Severity     `json:"severity"`
	Confidence          float64             `json:"confidence"`
	IncidentType        models.IncidentType `json:"incident_type"`
	AffectedServices    []string            `json:"affected_services"`
	Diagnosis           string              `json:"diagnosis"`
	Evidence            []string            `json:"evidence"`
	GeneralUserSummary  string              `json:"general_user_summary"`
	AdminMessage        string              `json:"admin_message"`
	RecommendedActionID string              `json:"recommended_action_id"`
	RecommendedTarget   ActionTarget        `json:"recommended_action_target"`
	ShouldNotifyAdmin   bool                `json:"should_notify_admin"`
}

type ActionTarget struct {
	Kind     string `json:"kind"`
	IDOrName string `json:"id_or_name"`
}

func ValidateDiagnosis(data []byte) (Diagnosis, error) {
	var diagnosis Diagnosis
	if err := json.Unmarshal(data, &diagnosis); err != nil {
		return Diagnosis{}, err
	}
	diagnosis.inferMissingActionTarget()
	return diagnosis, diagnosis.Validate()
}

func (d *Diagnosis) inferMissingActionTarget() {
	if !actionRequiresAppTarget(d.RecommendedActionID) {
		return
	}
	if d.RecommendedTarget.Kind == "app" && d.RecommendedTarget.IDOrName != "" {
		return
	}
	if len(d.AffectedServices) != 1 {
		return
	}
	d.RecommendedTarget = ActionTarget{Kind: "app", IDOrName: d.AffectedServices[0]}
}

func (d Diagnosis) Validate() error {
	if !validSeverity(d.Severity) {
		return fmt.Errorf("invalid severity %q", d.Severity)
	}
	if d.Confidence < 0 || d.Confidence > 1 {
		return errors.New("confidence must be between 0 and 1")
	}
	if !validIncidentType(d.IncidentType) {
		return fmt.Errorf("invalid incident_type %q", d.IncidentType)
	}
	if d.Diagnosis == "" {
		return errors.New("diagnosis is required")
	}
	if d.GeneralUserSummary == "" {
		return errors.New("general_user_summary is required")
	}
	if d.AdminMessage == "" {
		return errors.New("admin_message is required")
	}
	if !validAction(d.RecommendedActionID) {
		return fmt.Errorf("invalid recommended_action_id %q", d.RecommendedActionID)
	}
	if !validActionTargetKind(d.RecommendedTarget.Kind) {
		return fmt.Errorf("invalid recommended_action_target.kind %q", d.RecommendedTarget.Kind)
	}
	if actionRequiresAppTarget(d.RecommendedActionID) {
		if d.RecommendedTarget.Kind != "app" || d.RecommendedTarget.IDOrName == "" {
			return fmt.Errorf("recommended_action_target must identify one app for %s", d.RecommendedActionID)
		}
	}
	return nil
}

func JSONSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"severity": map[string]interface{}{
				"type": "string",
				"enum": []string{"none", "low", "medium", "high", "critical"},
			},
			"confidence": map[string]interface{}{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
			"incident_type": map[string]interface{}{
				"type": "string",
				"enum": []string{"internet_down", "nas_unreachable", "unraid_api_unavailable", "array_stopped", "docker_service_down", "app_down", "app_degraded", "storage_warning", "unifi_issue", "dns_issue", "unknown"},
			},
			"affected_services": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"diagnosis": map[string]interface{}{"type": "string"},
			"evidence": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"general_user_summary":  map[string]interface{}{"type": "string"},
			"admin_message":         map[string]interface{}{"type": "string"},
			"recommended_action_id": map[string]interface{}{"type": "string", "enum": []string{"none", "ask_admin_to_check", "ask_admin_to_restart_container", "ask_admin_to_check_unifi", "ask_admin_to_check_storage", "unknown"}},
			"recommended_action_target": map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"kind":       map[string]interface{}{"type": "string", "enum": []string{"none", "app", "server", "network", "storage", "manual"}},
					"id_or_name": map[string]interface{}{"type": "string"},
				},
				"required": []string{"kind", "id_or_name"},
			},
			"should_notify_admin": map[string]interface{}{"type": "boolean"},
		},
		"required": []string{"severity", "confidence", "incident_type", "affected_services", "diagnosis", "evidence", "general_user_summary", "admin_message", "recommended_action_id", "recommended_action_target", "should_notify_admin"},
	}
}

func validSeverity(value models.Severity) bool {
	switch value {
	case models.SeverityNone, models.SeverityLow, models.SeverityMedium, models.SeverityHigh, models.SeverityCritical:
		return true
	default:
		return false
	}
}

func validIncidentType(value models.IncidentType) bool {
	switch value {
	case models.IncidentInternetDown, models.IncidentNASUnreachable, models.IncidentUnraidAPIUnavailable, models.IncidentArrayStopped, models.IncidentDockerServiceDown, models.IncidentAppDown, models.IncidentAppDegraded, models.IncidentStorageWarning, models.IncidentUnifiIssue, models.IncidentDNSIssue, models.IncidentUnknown:
		return true
	default:
		return false
	}
}

func validAction(value string) bool {
	switch value {
	case "none", "ask_admin_to_check", "ask_admin_to_restart_container", "ask_admin_to_check_unifi", "ask_admin_to_check_storage", "unknown":
		return true
	default:
		return false
	}
}

func validActionTargetKind(value string) bool {
	switch value {
	case "none", "app", "server", "network", "storage", "manual":
		return true
	default:
		return false
	}
}

func actionRequiresAppTarget(actionID string) bool {
	switch actionID {
	case "ask_admin_to_restart_container":
		return true
	default:
		return false
	}
}
