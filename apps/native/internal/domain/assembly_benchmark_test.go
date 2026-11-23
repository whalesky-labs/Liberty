package domain

import (
	"testing"
	"time"
)

func BenchmarkAssemblyRoundAdvance(b *testing.B) {
	var round AssemblyRound
	round.Start(AssemblySteady, 42)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if round.phase != AssemblyPlaying {
			round.Start(AssemblySteady, uint64(index+43))
		}
		round.Advance(time.Second / 60)
	}
}
