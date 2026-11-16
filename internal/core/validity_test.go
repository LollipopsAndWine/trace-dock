package core

import (
	"path/filepath"
	"testing"
)

func TestValidityDiagnosticsAreIndependentFromSensitiveInformation(t *testing.T) {
	for _, tc := range []struct{ name, tool, content, reason, validity string }{
		{"Codex completed", "codex", `{"type":"event_msg","payload":{"type":"task_complete"}}`, "completion_observed", "valid"},
		{"Codex missing end", "codex", `{"type":"event_msg","payload":{"type":"task_started"}}`, "completion_not_observed", "invalid"},
		{"Codex cancelled", "codex", `{"type":"event_msg","payload":{"type":"turn_aborted"}}`, "error_or_interruption_recorded", "invalid"},
		{"Codex resumed", "codex", "{\"type\":\"event_msg\",\"payload\":{\"type\":\"turn_aborted\"}}\n{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}", "completed_with_recorded_errors", "invalid"},
		{"Codex damaged", "codex", "not JSON\n{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}", "trajectory_parse_error", "invalid"},
		{"Claude completed", "claude", `{"type":"assistant","message":{"stop_reason":"end_turn"}}`, "completion_observed", "valid"},
		{"Claude API error", "claude", `{"type":"assistant","error":"authentication_failed","message":{"stop_reason":"stop_sequence"}}`, "error_or_interruption_recorded", "invalid"},
		{"Claude resumed", "claude", "{\"type\":\"assistant\",\"error\":\"authentication_failed\"}\n{\"type\":\"assistant\",\"message\":{\"stop_reason\":\"end_turn\"}}", "completed_with_recorded_errors", "invalid"},
		{"Claude truncated", "claude", `{"type":"assistant","message":{"stop_reason":"max_tokens"}}`, "error_or_interruption_recorded", "invalid"},
		{"secret only affects later scan", "claude", `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"password=UnitPass9#fixture"}]}}`, "completion_observed", "valid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			p := filepath.Join(home, "rollout-11111111-1111-1111-1111-111111111111.jsonl")
			fixture(t, p, tc.content+"\n")
			s := parseSession(tc.tool, p, int64(len(tc.content)+1), home)
			if s.ValidityReason != tc.reason || s.Validity != tc.validity || s.Eligibility == "excluded" {
				t.Fatalf("validity diagnostics confused completion with collection or secrets: %+v", s)
			}
		})
	}
}
