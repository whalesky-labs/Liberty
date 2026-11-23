package domain

import (
	"testing"
	"time"
)

func TestPersistedUTCMatchesLegacyFractionWidthsAndSortOrder(t *testing.T) {
	base := time.Date(2024, 3, 1, 8, 0, 0, 0, time.FixedZone("test", 8*60*60))
	var previous string
	for _, sample := range []struct {
		nanos    time.Duration
		fraction string
	}{
		{0, ""}, {1, ".000000001"}, {1000, ".000001"}, {time.Millisecond, ".001"}, {100 * time.Millisecond, ".100"},
	} {
		got := PersistedUTC(base.Add(sample.nanos))
		if got != "2024-03-01T00:00:00"+sample.fraction+"+00:00" || got <= previous {
			t.Fatalf("unexpected legacy timestamp %q after %q", got, previous)
		}
		previous = got
	}
}
