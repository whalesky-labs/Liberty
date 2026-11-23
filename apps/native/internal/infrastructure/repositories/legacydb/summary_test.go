package legacydb

import (
	"liberty.local/liberty/internal/domain"
	"testing"
)

func TestPersistedSummaryDefaultsAndCorruptNulls(t *testing.T) {
	for _, text := range []string{`null`, `[]`, `{"overview":null}`, `{"topics":null}`, `{"topics":[null]}`, `{"actionItems":[{"task":null}]}`, `{"actionItems":[null]}`, `{"actionItems":[{"dueDate":12}]}`} {
		var result domain.SummaryResult
		if decodeDefaultObject(text, &result) {
			t.Fatalf("invalid persisted summary accepted: %s", text)
		}
	}
	var result domain.SummaryResult
	if !decodeDefaultObject(`{"overview":"Saved","unknown":null,"actionItems":[{}]}`, &result) || result.Topics == nil || len(result.ActionItems) != 1 {
		t.Fatal("valid serde defaults lost")
	}
	var payload domain.MinutesPayload
	if decodeDefaultObject(`{"speakerReports":[{"resolvedName":null}]}`, &payload) {
		t.Fatal("embedded participant validation bypassed")
	}
	if decodeDefaultObject(`{"schemaVersion":4294967296}`, &payload) {
		t.Fatal("u32 overflow accepted")
	}
	if !decodeDefaultObject(`{"sourceSummaryRunId":null,"participants":[],"speakerReports":[{}]}`, &payload) || payload.SpeakerReports[0].Summary == nil {
		t.Fatal("optional reference or nested defaults lost")
	}
}
