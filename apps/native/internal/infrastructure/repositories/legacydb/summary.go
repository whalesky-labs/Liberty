package legacydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"

	"liberty.local/liberty/internal/domain"
)

func readSummaryRuns(ctx context.Context, tx *sql.Tx, id string) ([]domain.SummaryRun, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,job_id,COALESCE(model_config_id,''),COALESCE(template_id,''),
 include_speaker,include_timestamp,extra_instructions,status,error_message,prompt_preview,raw_response,
 result_json,minutes_payload_json,created_at,updated_at FROM ai_summary_runs WHERE job_id=?
 ORDER BY datetime(updated_at) DESC,updated_at DESC,id DESC`, id)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	result := []domain.SummaryRun{}
	for rows.Next() {
		var run domain.SummaryRun
		var summary, minutes *string
		if rows.Scan(&run.ID, &run.JobID, &run.ModelConfigID, &run.TemplateID, &run.IncludeSpeaker, &run.IncludeTimestamp, &run.ExtraInstructions, &run.Status, &run.ErrorMessage, &run.PromptPreview, &run.RawResponse, &summary, &minutes, &run.CreatedAt, &run.UpdatedAt) != nil {
			return nil, ErrDatabase
		}
		if summary != nil {
			run.Result = DecodeSummaryResult(*summary)
		}
		if minutes != nil {
			run.MinutesPayload = DecodeMinutesPayload(*minutes)
		}
		result = append(result, run)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return result, nil
}

func DecodeSummaryResult(text string) *domain.SummaryResult {
	var value domain.SummaryResult
	if decodeDefaultObject(text, &value) {
		return &value
	}
	return nil
}

// DecodeMinutesPayload is shared by reads and transactional backfill so they
// agree whether a persisted value is already valid and must be preserved.
func DecodeMinutesPayload(text string) *domain.MinutesPayload {
	value := domain.MinutesPayload{SchemaVersion: 1, MeetingInfoSource: "empty"}
	if decodeDefaultObject(text, &value) {
		switch value.MeetingInfoSource {
		case "empty", "ai", "user":
			return &value
		}
	}
	return nil
}

// serde(default) allows missing fields, but rejects explicit null for non-Option
// fields. encoding/json alone silently accepts null strings/slices, which would
// make corrupt persisted runs appear valid and change active-result selection.
func decodeDefaultObject[T any](text string, target *T) bool {
	raw := json.RawMessage(text)
	if !compatibleJSON(raw, reflect.TypeOf(*target)) || json.Unmarshal(raw, target) != nil {
		return false
	}
	emptySlices(reflect.ValueOf(target).Elem())
	return true
}
func compatibleJSON(raw json.RawMessage, t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		if strings.TrimSpace(string(raw)) == "null" {
			return true
		}
		return compatibleJSON(raw, t.Elem())
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return false
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.Anonymous {
				if !compatibleJSON(raw, field.Type) {
					return false
				}
				continue
			}
			key := strings.Split(field.Tag.Get("json"), ",")[0]
			if value, ok := object[key]; ok && !compatibleJSON(value, field.Type) {
				return false
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil || values == nil {
			return false
		}
		for _, value := range values {
			if !compatibleJSON(value, t.Elem()) {
				return false
			}
		}
	}
	return true
}
func emptySlices(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			emptySlices(v.Field(i))
		}
	case reflect.Slice:
		if v.IsNil() {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
		}
		for i := 0; i < v.Len(); i++ {
			emptySlices(v.Index(i))
		}
	case reflect.Pointer:
		if !v.IsNil() {
			emptySlices(v.Elem())
		}
	}
}
