package runner

import (
	"strings"
	"testing"
)

func TestLiveProtocolRejectsDuplicatesInvalidTimelineAndUnboundedEvents(t *testing.T) {
	valid := `{"protocol":"liberty-live-asr","version":1,"sequence":2,"type":"final","segmentId":1,"revision":3,"startMs":100,"endMs":800,"text":"synthetic"}`
	if e, err := ParseLiveEvent([]byte(valid)); err != nil || e.Text != "synthetic" {
		t.Fatal(e, err)
	}
	for _, value := range []string{strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(valid, `"endMs":800`, `"endMs":99`, 1), strings.Replace(valid, `"revision":3`, `"revision":0`, 1), strings.Replace(valid, `"version":1`, `"version":true`, 1), strings.Replace(valid, `"synthetic"`, `"`+strings.Repeat("x", 9000)+`"`, 1), strings.Replace(valid, `"final"`, `"unknown"`, 1)} {
		if _, err := ParseLiveEvent([]byte(value)); err == nil {
			t.Fatal("invalid live event accepted")
		}
	}
}
