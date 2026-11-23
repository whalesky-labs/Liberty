package domain

import "time"

func PersistedUTC(instant time.Time) string {
	const base = "2006-01-02T15:04:05"
	var fraction string
	switch nanos := instant.Nanosecond(); {
	case nanos == 0:
	case nanos%1_000_000 == 0:
		fraction = ".000"
	case nanos%1_000 == 0:
		fraction = ".000000"
	default:
		fraction = ".000000000"
	}
	return instant.UTC().Format(base + fraction + "+00:00")
}
