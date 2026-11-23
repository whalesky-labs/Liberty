package client

import (
	"math"
	"regexp"
	"strings"

	"liberty.local/liberty/internal/domain"
)

type DetailProgress struct {
	source, id string
	started    uint64
	Percent    float64
}

func (p *DetailProgress) Update(job domain.JobSummary) float64 {
	stage := job.OverallStatus
	if stage == "idle" {
		stage = job.SummaryStatus
		if stage == "idle" {
			stage = job.ASRStatus
			if stage == "idle" {
				stage = job.UploadStatus
			}
		}
	}
	bands := map[string][2]float64{"idle": {0, 0}, "uploaded": {4, 8}, "queued": {8, 14}, "transcribing": {18, 72}, "speaker_processing": {72, 96}, "summarizing": {96, 99}, "completed": {100, 100}, "failed": {0, 99}}
	band := bands[stage]
	next := band[0]
	if job.ProgressPercent != nil && !math.IsNaN(*job.ProgressPercent) {
		next = max(band[0], min(band[1], math.Round(*job.ProgressPercent)))
	}
	started := uint64(0)
	if job.ProcessingStartedAtMS != nil {
		started = *job.ProcessingStartedAtMS
	}
	if p.source != job.Source || p.id != job.ID || p.started != started {
		p.Percent = next
	} else {
		p.Percent = max(p.Percent, next)
	}
	p.source, p.id, p.started = job.Source, job.ID, started
	return p.Percent
}

type LogLine struct{ Text, Tone string }

var ansiColor = regexp.MustCompile("\x1b\\[[0-9;]*m")

func DetailLogs(log *string) []LogLine {
	lines := []LogLine{}
	if log == nil {
		return lines
	}
	raw := strings.FieldsFunc(*log, func(r rune) bool { return r == '\r' || r == '\n' })
	for i := len(raw) - 1; i >= 0; i-- {
		text := strings.TrimSpace(ansiColor.ReplaceAllString(raw[i], ""))
		if text == "" {
			continue
		}
		lower := strings.ToLower(text)
		tone := "info"
		for _, group := range []struct {
			tone  string
			words []string
		}{{"error", []string{"traceback", "permissionerror", "runtimeerror", "error", "failed", "失败"}}, {"warning", []string{"warning", "warn"}}, {"success", []string{"completed", "success", "已完成", "完成"}}} {
			matched := false
			for _, word := range group.words {
				matched = matched || strings.Contains(lower, word)
			}
			if matched {
				tone = group.tone
				break
			}
		}
		lines = append(lines, LogLine{Text: text, Tone: tone})
	}
	return lines
}
