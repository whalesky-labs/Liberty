package domain

import (
	"math/rand/v2"
	"slices"
	"time"
)

type AssemblyMode uint8

const (
	AssemblySteady AssemblyMode = iota
	AssemblyRush
)

type AssemblyPhase uint8

const (
	AssemblyReady AssemblyPhase = iota
	AssemblyPlaying
	AssemblyPaused
	AssemblyCompleted
	AssemblyTimedOut
)

type AssemblyPart uint8

const (
	AssemblyBolt AssemblyPart = iota + 1
	AssemblyWasher
	AssemblyGear
)

type AssemblyHit uint8

const (
	AssemblyIgnored AssemblyHit = iota
	AssemblyCorrect
	AssemblyWrong
)

// AssemblyRound is a local practice round. Its caller supplies the seed and
// elapsed play time. The zero value is ready to start.
type AssemblyRound struct {
	phase    AssemblyPhase
	mode     AssemblyMode
	order    []AssemblyPart
	position int
	mistakes int
	limit    time.Duration
	penalty  time.Duration
	elapsed  time.Duration
}

type AssemblySnapshot struct {
	Phase           AssemblyPhase
	Mode            AssemblyMode
	Order           []AssemblyPart
	Position        int // Number of correctly placed parts.
	Mistakes        int
	AccuracyPercent int // Rounded share of correct attempts, or zero before the first attempt.
	TimeLimit       time.Duration
	Penalty         time.Duration
	Elapsed         time.Duration // Includes time penalties.
	Remaining       time.Duration
}

// Start replaces any prior round. An unknown mode uses the steady rules.
func (r *AssemblyRound) Start(mode AssemblyMode, seed uint64) {
	steps, limit, penalty := 6, 60*time.Second, 3*time.Second
	if mode == AssemblyRush {
		steps, limit, penalty = 10, 45*time.Second, 5*time.Second
	} else {
		mode = AssemblySteady
	}
	*r = AssemblyRound{
		phase:   AssemblyPlaying,
		mode:    mode,
		order:   assemblyOrder(steps, seed, mode),
		limit:   limit,
		penalty: penalty,
	}
}

func assemblyOrder(steps int, seed uint64, mode AssemblyMode) []AssemblyPart {
	rng := rand.New(rand.NewPCG(seed, uint64(mode)))
	order := make([]AssemblyPart, steps)
	var seen [AssemblyGear + 1]bool
	unseen := int(AssemblyGear)
	for i := range order {
		var choices [AssemblyGear]AssemblyPart
		count := 0
		for part := AssemblyBolt; part <= AssemblyGear; part++ {
			// Reserve the last slots for any missing parts, and avoid runs
			// of three matching parts so every order requires switching.
			neededSlot := steps-i == unseen && seen[part]
			threeInARow := i >= 2 && order[i-1] == part && order[i-2] == part
			if neededSlot || threeInARow {
				continue
			}
			choices[count] = part
			count++
		}
		part := choices[rng.IntN(count)]
		order[i] = part
		if !seen[part] {
			seen[part] = true
			unseen--
		}
	}
	return order
}

// Advance spends active play time and stops at the deadline. It reports whether
// the phase or the displayed remaining whole seconds changed.
func (r *AssemblyRound) Advance(delta time.Duration) bool {
	if r.phase != AssemblyPlaying || delta <= 0 {
		return false
	}
	beforePhase := r.phase
	beforeRemaining := r.limit - r.elapsed
	if delta >= r.limit-r.elapsed {
		r.elapsed = r.limit
		r.phase = AssemblyTimedOut
		return beforePhase != r.phase || assemblyRemainingSeconds(beforeRemaining) != assemblyRemainingSeconds(0)
	}
	r.elapsed += delta
	return beforePhase != r.phase || assemblyRemainingSeconds(beforeRemaining) != assemblyRemainingSeconds(r.limit-r.elapsed)
}

func assemblyRemainingSeconds(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	return int64((value-time.Nanosecond)/time.Second) + 1
}

// Place attempts the next step. A wrong part may spend the last remaining time.
func (r *AssemblyRound) Place(part AssemblyPart) AssemblyHit {
	if r.phase != AssemblyPlaying || part < AssemblyBolt || part > AssemblyGear {
		return AssemblyIgnored
	}
	if r.order[r.position] != part {
		r.mistakes++
		r.Advance(r.penalty)
		return AssemblyWrong
	}
	r.position++
	if r.position == len(r.order) {
		r.phase = AssemblyCompleted
	}
	return AssemblyCorrect
}

func (r *AssemblyRound) Pause() {
	if r.phase == AssemblyPlaying {
		r.phase = AssemblyPaused
	}
}

func (r *AssemblyRound) Resume() {
	if r.phase == AssemblyPaused {
		r.phase = AssemblyPlaying
	}
}

func (r *AssemblyRound) Snapshot() AssemblySnapshot {
	accuracy := 0
	if attempts := r.position + r.mistakes; attempts > 0 {
		accuracy = (100*r.position + attempts/2) / attempts
	}
	return AssemblySnapshot{
		Phase:           r.phase,
		Mode:            r.mode,
		Order:           slices.Clone(r.order),
		Position:        r.position,
		Mistakes:        r.mistakes,
		AccuracyPercent: accuracy,
		TimeLimit:       r.limit,
		Penalty:         r.penalty,
		Elapsed:         r.elapsed,
		Remaining:       r.limit - r.elapsed,
	}
}
