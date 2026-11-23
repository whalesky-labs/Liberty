package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/domain"
)

func BenchmarkAssemblyPracticeFrame(b *testing.B) {
	view := assemblyGameView{Locale: "en-US"}
	tester := ui.NewTester(view.View, 960, 560)
	if err := tester.Click("Start Assembly"); err != nil {
		b.Fatal(err)
	}
	tester.Frame()
	if view.round.Snapshot().Phase != domain.AssemblyPlaying {
		b.Fatal("practice round did not start")
	}
	step := time.Second / 60
	framesPerRound := int((60*time.Second + step - 1) / step)
	frames := 0
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if frames == framesPerRound {
			if state := view.round.Snapshot(); state.Phase != domain.AssemblyTimedOut {
				b.Fatalf("practice round did not reach timeout: %+v", state)
			}
			// Advance may finish on the deadline without a pending repaint. Render
			// the terminal state before querying its replay control.
			tester.Frame()
			if err := tester.Click("Play Again"); err != nil {
				b.Fatal(err)
			}
			tester.Frame()
			frames = 0
		}
		tester.Advance(step)
		frames++
	}
}
