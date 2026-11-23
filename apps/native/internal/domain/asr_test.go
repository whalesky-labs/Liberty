package domain

import "testing"

func TestRunnerDuration(t *testing.T) {
	for _, tc := range []struct {
		end  uint64
		want uint32
	}{{0, 7}, {1, 1}, {60000, 1}, {60001, 2}, {^uint64(0), ^uint32(0)}} {
		r := RunnerResult{Transcript: []Segment{{EndMS: tc.end}}}
		if got := r.Duration(7); got != tc.want {
			t.Fatalf("end %d: %d != %d", tc.end, got, tc.want)
		}
	}
	minutes := uint32(5)
	if got := (RunnerResult{DurationMinutes: &minutes, Transcript: []Segment{{EndMS: 60001}}}).Duration(7); got != 5 {
		t.Fatal("explicit duration lost")
	}
}
