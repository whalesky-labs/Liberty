package domain

import (
	"errors"
	"fmt"
	"strings"
)

var ErrExportKind = errors.New("export_kind_invalid")

type TextExportKind string

const (
	ExportTranscript TextExportKind = "transcript"
	ExportNotes      TextExportKind = "notes"
	ExportBundle     TextExportKind = "bundle"
	ExportWord       TextExportKind = "word"
)

type TextExportLabels struct {
	UnknownSpeaker     string `json:"unknownSpeaker"`
	TranscriptHeading  string `json:"transcriptHeading"`
	SummaryHeading     string `json:"summaryHeading"`
	TopicsHeading      string `json:"topicsHeading"`
	DecisionsHeading   string `json:"decisionsHeading"`
	ActionItemsHeading string `json:"actionItemsHeading"`
	RisksHeading       string `json:"risksHeading"`
	FollowUpsHeading   string `json:"followUpsHeading"`
	EmptySummary       string `json:"emptySummary"`
}

func (k TextExportKind) Extension() (string, error) {
	switch k {
	case ExportTranscript:
		return "txt", nil
	case ExportNotes, ExportBundle:
		return "md", nil
	case ExportWord:
		return "docx", nil
	default:
		return "", ErrExportKind
	}
}
func RenderTextExport(r Result, kind TextExportKind, labels TextExportLabels) (string, error) {
	switch kind {
	case ExportTranscript:
		return renderTranscript(r, labels), nil
	case ExportNotes:
		return renderNotes(r, labels), nil
	case ExportBundle:
		return renderNotes(r, labels) + "\n\n" + labels.TranscriptHeading + "\n" + renderTranscript(r, labels), nil
	default:
		return "", ErrExportKind
	}
}
func renderTranscript(r Result, labels TextExportLabels) string {
	lines := []string{}
	for _, s := range r.PrimarySegments() {
		speaker := labels.UnknownSpeaker
		if s.Speaker != nil && strings.TrimSpace(*s.Speaker) != "" {
			speaker = *s.Speaker
		}
		lines = append(lines, fmt.Sprintf("[%s - %s] %s: %s", exportTimestamp(s.StartMS), exportTimestamp(s.EndMS), speaker, s.Text))
	}
	return strings.Join(lines, "\n")
}
func exportTimestamp(ms uint64) string {
	s := (ms / 1000) % (24 * 60 * 60)
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s%3600/60, s%60)
}
func renderNotes(r Result, l TextExportLabels) string {
	s := r.Summary
	overview := s.Overview
	if overview == "" {
		overview = l.EmptySummary
	}
	lines := []string{"# " + r.Job.Title, "", l.SummaryHeading, overview, "", l.TopicsHeading}
	for _, item := range s.Topics {
		lines = append(lines, "- "+item)
	}
	for _, section := range []struct {
		heading  string
		items    []string
		optional bool
	}{
		{l.DecisionsHeading, s.Decisions, false}, {l.ActionItemsHeading, s.ActionItems, false}, {l.RisksHeading, s.Risks, true}, {l.FollowUpsHeading, s.FollowUps, true},
	} {
		if section.optional && len(section.items) == 0 {
			continue
		}
		lines = append(lines, "", section.heading)
		for _, item := range section.items {
			lines = append(lines, "- "+item)
		}
	}
	return strings.Join(lines, "\n")
}

func (r Result) CanRenameSpeaker() bool {
	if len(r.Speakers) == 0 {
		return false
	}
	if r.Job.DiarizationStatus == "completed" {
		return true
	}
	if r.Job.DiarizationStatus != "legacy_unverified" || len(r.Transcript) != len(r.Speakers) {
		return false
	}
	for i, s := range r.Speakers {
		t := r.Transcript[i]
		if s.Speaker == nil || strings.TrimSpace(*s.Speaker) == "" || s.StartMS != t.StartMS || s.EndMS != t.EndMS || s.Text != t.Text {
			return false
		}
	}
	return true
}
