package domain

import (
	"reflect"
	"testing"
)

func TestTranscriptProjection(t *testing.T) {
	label := "speaker-0"
	base := Result{Transcript: []Segment{{ID: "t", StartMS: 0, EndMS: 10, Text: "会议"}}, Speakers: []Segment{{ID: "s", StartMS: 0, EndMS: 10, Text: "会议", Speaker: &label}}}
	for _, status := range []string{"disabled", "pending", "processing", "completed", "unavailable", "failed", "legacy_unverified"} {
		t.Run(status, func(t *testing.T) {
			r := base
			r.Job.DiarizationStatus = status
			primary := r.Transcript
			if status == "completed" {
				primary = r.Speakers
			}
			if !reflect.DeepEqual(r.PrimarySegments(), primary) {
				t.Fatal("primary projection changed")
			}
			display := primary
			if status == "legacy_unverified" {
				display = r.Speakers
			}
			if !reflect.DeepEqual(r.DisplaySegments(), display) {
				t.Fatal("display projection changed")
			}
		})
	}
	for _, kind := range []string{"blank", "timing", "text", "partial"} {
		t.Run(kind, func(t *testing.T) {
			r := base
			r.Job.DiarizationStatus = "legacy_unverified"
			r.Speakers = append([]Segment{}, base.Speakers...)
			switch kind {
			case "blank":
				blank := " "
				r.Speakers[0].Speaker = &blank
			case "timing":
				r.Speakers[0].EndMS++
			case "text":
				r.Speakers[0].Text = "different"
			case "partial":
				r.Speakers = nil
			}
			if !reflect.DeepEqual(r.DisplaySegments(), r.Transcript) {
				t.Fatal("invalid legacy projection accepted")
			}
		})
	}
}

func TestASRResourceBudget(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		workers, threads, cpus uint32
		gib                    uint64
		want                   ASRResourceBudget
	}{
		{"8GiB", 2, 0, 8, 8, ASRResourceBudget{1, 8}},
		{"shared CPUs", 4, 0, 12, 32, ASRResourceBudget{4, 3}},
		{"manual ceiling", 2, 16, 8, 16, ASRResourceBudget{2, 4}},
		{"zero hardware", 0, 0, 0, 0, ASRResourceBudget{1, 1}},
		{"maximum", 100, 100, 256, 256, ASRResourceBudget{8, 32}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveASRResourceBudget(tc.workers, tc.threads, tc.cpus, tc.gib*1024*1024*1024); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
