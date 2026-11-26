package desktop

import (
	"fmt"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
)

func BenchmarkResultWorkbenchFrame(benchmark *testing.B) {
	for _, segmentCount := range []int{400, 10000} {
		for _, scenario := range []struct {
			name, query, speaker string
			legacy               bool
		}{
			{name: "all"},
			{name: "speaker", speaker: "Speaker 0"},
			{name: "search", query: "TRANSCRIPT"},
			{name: "speaker-search", query: "TRANSCRIPT", speaker: "Speaker 0"},
			{name: "legacy-search", query: "TRANSCRIPT", speaker: "Speaker 0", legacy: true},
		} {
			benchmark.Run(fmt.Sprintf("%d/%s", segmentCount, scenario.name), func(benchmark *testing.B) {
				view := resultParityView("en-US", segmentCount)
				view.Actions = &client.ResultActionController{}
				if scenario.legacy {
					result := view.Client.Result
					result.Job.DiarizationStatus = "legacy_unverified"
					result.Transcript = slices.Clone(result.Speakers)
					for index := range result.Transcript {
						result.Transcript[index].Speaker = nil
					}
				}
				tester := ui.NewTester(view.View, 1280, 900)
				view.query = scenario.query
				view.speaker = scenario.speaker
				tester.Frame()
				if !tester.HasText("Synthetic transcript 0000") {
					benchmark.Fatal("expected the first filtered row to render")
				}
				benchmark.ReportAllocs()
				for benchmark.Loop() {
					tester.Frame()
				}
			})
		}
	}
}

// Same scenario as the performance audit: a new result wrapper with unchanged
// transcript storage (for example, an updated summary or export timestamp).
func BenchmarkResultSnapshotRefresh(b *testing.B) {
	view := resultParityView("en-US", 10000)
	view.Actions = &client.ResultActionController{}
	tester := ui.NewTester(view.View, 1280, 900)
	view.query = "TRANSCRIPT"
	tester.Frame()
	if !tester.HasText("Synthetic transcript 0000") {
		b.Fatal("missing transcript")
	}
	b.ReportAllocs()
	for b.Loop() {
		snapshot := *view.Client.Result
		view.Client.Result = &snapshot
		tester.Frame()
	}
}
