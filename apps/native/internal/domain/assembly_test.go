package domain

import (
	"math"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestAssemblyOrdersUseSeedAndMode(t *testing.T) {
	for _, tc := range []struct {
		name           string
		mode           AssemblyMode
		steps          int
		limit, penalty time.Duration
	}{
		{"steady", AssemblySteady, 6, 60 * time.Second, 3 * time.Second},
		{"rush", AssemblyRush, 10, 45 * time.Second, 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var round AssemblyRound
			unique := make(map[[10]AssemblyPart]bool)
			for seed := uint64(0); seed < 64; seed++ {
				round.Start(tc.mode, seed)
				start := round.Snapshot()
				if start.Phase != AssemblyPlaying || start.Mode != tc.mode || len(start.Order) != tc.steps || start.TimeLimit != tc.limit || start.Remaining != tc.limit || start.Penalty != tc.penalty {
					t.Fatalf("unexpected start for seed %d: %+v", seed, start)
				}
				var key [10]AssemblyPart
				copy(key[:], start.Order)
				unique[key] = true
				for _, part := range []AssemblyPart{AssemblyBolt, AssemblyWasher, AssemblyGear} {
					if !slices.Contains(start.Order, part) {
						t.Fatalf("seed %d missing part %d: %v", seed, part, start.Order)
					}
				}
				for i, part := range start.Order {
					if part < AssemblyBolt || part > AssemblyGear || i >= 2 && part == start.Order[i-1] && part == start.Order[i-2] {
						t.Fatalf("seed %d generated invalid order: %v", seed, start.Order)
					}
				}
				for i := range start.Order {
					start.Order[i] = 0
				}
				if !slices.Equal(round.Snapshot().Order, key[:tc.steps]) {
					t.Fatal("modifying the snapshot changed the round")
				}
				round.Start(tc.mode, seed)
				if !slices.Equal(round.Snapshot().Order, key[:tc.steps]) {
					t.Fatalf("seed %d produced a different order on restart", seed)
				}
			}
			if len(unique) < 48 {
				t.Fatalf("64 seeds yielded only %d orders", len(unique))
			}
		})
	}
}

func TestAssemblyOrderCompletesWithCorrectedMistake(t *testing.T) {
	var round AssemblyRound
	if snapshot := round.Snapshot(); snapshot.Phase != AssemblyReady || len(snapshot.Order) != 0 || snapshot.Remaining != 0 {
		t.Fatalf("zero state cannot initialize: %+v", snapshot)
	}
	if round.Place(AssemblyBolt) != AssemblyIgnored {
		t.Fatal("accepted a part in ready state")
	}
	round.Start(AssemblySteady, 42)
	order := round.Snapshot().Order
	round.Advance(2 * time.Second)
	wrongPart := AssemblyBolt
	if wrongPart == order[0] {
		wrongPart = AssemblyWasher
	}
	if round.Place(0) != AssemblyIgnored || round.Place(AssemblyPart(255)) != AssemblyIgnored || round.Place(wrongPart) != AssemblyWrong {
		t.Fatal("invalid and wrong parts must be distinguished")
	}
	if snapshot := round.Snapshot(); snapshot.Position != 0 || snapshot.Mistakes != 1 || snapshot.AccuracyPercent != 0 || snapshot.Elapsed != 5*time.Second || snapshot.Remaining != 55*time.Second {
		t.Fatalf("wrong part advanced progress or triggered incorrect penalty: %+v", snapshot)
	}
	for i, part := range order {
		if round.Place(part) != AssemblyCorrect {
			t.Fatalf("rejected part %d at step %d", part, i)
		}
		if snapshot := round.Snapshot(); snapshot.Position != i+1 || i < len(order)-1 && snapshot.Phase != AssemblyPlaying || i == 1 && snapshot.AccuracyPercent != 67 {
			t.Fatalf("incorrect progress or accuracy after step %d: %+v", i, snapshot)
		}
	}
	completed := round.Snapshot()
	if completed.Phase != AssemblyCompleted || completed.Position != len(order) || completed.AccuracyPercent != 86 || completed.Remaining != 55*time.Second {
		t.Fatalf("last correct part did not complete the round: %+v", completed)
	}
	if round.Place(order[0]) != AssemblyIgnored {
		t.Fatal("accepted a part after completion")
	}
	round.Advance(time.Hour)
	round.Pause()
	round.Resume()
	if !reflect.DeepEqual(round.Snapshot(), completed) {
		t.Fatal("completed round changed without restart")
	}
	round.Start(AssemblyRush, 42)
	if restarted := round.Snapshot(); restarted.Phase != AssemblyPlaying || restarted.Position != 0 || restarted.Mistakes != 0 || restarted.Elapsed != 0 || restarted.AccuracyPercent != 0 || restarted.Mode != AssemblyRush {
		t.Fatalf("restart did not initialize a fresh rush round: %+v", restarted)
	}
}

func TestAssemblyPauseAndDeadline(t *testing.T) {
	var round AssemblyRound
	round.Start(AssemblyRush, 9)
	order := round.Snapshot().Order
	round.Advance(45*time.Second - time.Nanosecond)
	round.Pause()
	round.Pause()
	paused := round.Snapshot()
	round.Advance(time.Duration(math.MaxInt64))
	if round.Place(order[0]) != AssemblyIgnored || !reflect.DeepEqual(round.Snapshot(), paused) || paused.Phase != AssemblyPaused || paused.Remaining != time.Nanosecond {
		t.Fatal("pause did not freeze input and deadline")
	}
	round.Resume()
	round.Resume()
	round.Advance(-time.Hour)
	round.Advance(0)
	if snapshot := round.Snapshot(); snapshot.Phase != AssemblyPlaying || snapshot.Remaining != time.Nanosecond {
		t.Fatalf("resume or nonpositive time altered the deadline: %+v", snapshot)
	}
	round.Advance(time.Nanosecond)
	if snapshot := round.Snapshot(); snapshot.Phase != AssemblyTimedOut || snapshot.Remaining != 0 || snapshot.Elapsed != snapshot.TimeLimit || round.Place(order[0]) != AssemblyIgnored {
		t.Fatalf("exact deadline did not freeze the round: %+v", snapshot)
	}
	round.Start(AssemblyRush, 9)
	round.Advance(time.Duration(math.MaxInt64))
	if snapshot := round.Snapshot(); snapshot.Phase != AssemblyTimedOut || snapshot.Elapsed != snapshot.TimeLimit {
		t.Fatalf("large delta did not stop at the deadline: %+v", snapshot)
	}
}

func TestAssemblyAdvanceReportsVisibleChangesWithoutChangingRoundState(t *testing.T) {
	var round AssemblyRound
	if round.Advance(time.Second) {
		t.Fatal("ready round reported a visible change")
	}
	round.Start(AssemblySteady, 17)
	if round.Advance(500 * time.Millisecond) {
		t.Fatal("sub-second progress changed the displayed remaining seconds")
	}
	if round.Advance(499 * time.Millisecond) {
		t.Fatal("progress before the next whole second changed the display")
	}
	if !round.Advance(time.Millisecond) {
		t.Fatal("crossing a whole-second boundary did not report a visible change")
	}

	start := round.Snapshot()
	wrong := AssemblyBolt
	if wrong == start.Order[0] {
		wrong = AssemblyWasher
	}
	if round.Place(wrong) != AssemblyWrong {
		t.Fatal("failed to establish a penalized playing round")
	}
	before := round.Snapshot()
	if round.Advance(500 * time.Millisecond) {
		t.Fatal("sub-second penalty follow-up changed the displayed seconds")
	}
	after := round.Snapshot()
	if after.Phase != before.Phase || after.Mode != before.Mode || after.Position != before.Position || after.Mistakes != before.Mistakes || after.AccuracyPercent != before.AccuracyPercent || after.TimeLimit != before.TimeLimit || after.Penalty != before.Penalty || !slices.Equal(after.Order, before.Order) || after.Elapsed != before.Elapsed+500*time.Millisecond {
		t.Fatalf("advance changed non-clock state: before=%+v after=%+v", before, after)
	}

	round.Pause()
	paused := round.Snapshot()
	if round.Advance(time.Second) {
		t.Fatal("paused round reported a visible change")
	}
	if !reflect.DeepEqual(paused, round.Snapshot()) {
		t.Fatal("paused round changed while advancing")
	}
	round.Start(AssemblySteady, 18)
	if !round.Advance(60 * time.Second) {
		t.Fatal("deadline transition did not report a visible change")
	}
	if snapshot := round.Snapshot(); snapshot.Phase != AssemblyTimedOut || snapshot.Remaining != 0 {
		t.Fatalf("deadline transition produced the wrong state: %+v", snapshot)
	}
	if round.Advance(time.Second) {
		t.Fatal("timed-out round reported a later visible change")
	}
	round.Start(AssemblySteady, 19)
	for _, part := range round.Snapshot().Order {
		round.Place(part)
	}
	completed := round.Snapshot()
	if completed.Phase != AssemblyCompleted || round.Advance(time.Second) || !reflect.DeepEqual(completed, round.Snapshot()) {
		t.Fatal("completed round reported a visible change or mutated")
	}
}

func TestAssemblyPenaltyCanEndRound(t *testing.T) {
	var round AssemblyRound
	round.Start(AssemblyRush, 1)
	start := round.Snapshot()
	round.Advance(start.TimeLimit - 2*time.Second)
	wrongPart := AssemblyBolt
	if wrongPart == start.Order[0] {
		wrongPart = AssemblyWasher
	}
	if round.Place(wrongPart) != AssemblyWrong || round.Place(wrongPart) != AssemblyIgnored {
		t.Fatal("the first error should end the round and reject further input")
	}
	if snapshot := round.Snapshot(); snapshot.Phase != AssemblyTimedOut || snapshot.Elapsed != start.TimeLimit || snapshot.Remaining != 0 || snapshot.Mistakes != 1 || snapshot.Position != 0 {
		t.Fatalf("penalty passed the deadline or changed progress: %+v", snapshot)
	}
}

func TestAssemblyResultsAreIndependentOfFrameSteps(t *testing.T) {
	for _, mode := range []AssemblyMode{AssemblySteady, AssemblyRush} {
		play := func(frame time.Duration) AssemblySnapshot {
			var round AssemblyRound
			round.Start(mode, math.MaxUint64)
			for i, part := range round.Snapshot().Order {
				for spent := time.Duration(0); spent < time.Second; spent += frame {
					round.Advance(min(frame, time.Second-spent))
				}
				if i == 1 {
					round.Place(AssemblyPart(int(part)%int(AssemblyGear) + 1))
				}
				round.Place(part)
			}
			return round.Snapshot()
		}
		whole, stepped := play(time.Second), play(17*time.Millisecond)
		if whole.Phase != AssemblyCompleted || !reflect.DeepEqual(whole, stepped) {
			t.Fatalf("frame partition changed outcome for mode %d: %+v / %+v", mode, whole, stepped)
		}
	}
}
