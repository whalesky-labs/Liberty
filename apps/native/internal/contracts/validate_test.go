package contracts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSharedGoldenFixtures(t *testing.T) {
	v, err := Shared()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"runner/v2/result":           {"result-completed", "result-unavailable"},
		"runner/v2/progress":         {"progress-transcribing"},
		"runner/v2/event":            {"event-progress"},
		"runtime/v1/runtime":         {"current"},
		"settings/v1/settings":       {"current"},
		"meeting-job/v1/meeting-job": {"current"},
		"ai/v1/ai":                   {"current"},
	}
	for schema, fixtures := range cases {
		for _, fixture := range fixtures {
			t.Run(schema+"/"+fixture, func(t *testing.T) {
				data, err := os.ReadFile(filepath.Join("../../../../packages/shared-types/fixtures", filepath.Dir(schema), fixture+".json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := v.Validate("https://liberty.local/schemas/"+schema+".schema.json", data); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	if v.Validate("https://example.invalid/unknown", []byte(`{}`)) != ErrInvalid {
		t.Fatal("unknown schema accepted")
	}
}
