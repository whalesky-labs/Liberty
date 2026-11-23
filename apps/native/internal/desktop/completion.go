package desktop

import (
	"context"
	"time"

	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

const jobCompletionStatusDuration = 7 * time.Second

type jobCompletionObserver struct {
	statuses map[string]string
	hydrated bool
}

func (observer *jobCompletionObserver) Observe(source string, jobs []domain.JobSummary, settled bool) []domain.JobSummary {
	if !settled || source == "" {
		return nil
	}
	if observer.statuses == nil {
		observer.statuses = make(map[string]string)
	}
	var completed []domain.JobSummary
	for _, job := range jobs {
		jobSource := job.Source
		if jobSource == "" {
			jobSource = source
		}
		key := jobSource + "\x00" + job.ID
		previous, exists := observer.statuses[key]
		if observer.hydrated && exists && previous != "completed" && job.OverallStatus == "completed" {
			completed = append(completed, job)
		}
		observer.statuses[key] = job.OverallStatus
	}
	observer.hydrated = true
	return completed
}

func nativeJobNotification(title, body string) context.CancelFunc {
	if !mygo.NotificationsSupported() {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	notification := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body})
	go func() { _ = notification.ShowContext(ctx) }()
	return cancel
}

func (w *Workspace) presentJobCompletion(job domain.JobSummary) {
	if w.closed {
		return
	}
	body := presentation.Format(w.preferences.Locale, "shell.jobCompletedBody", map[string]any{"title": job.Title})
	w.completionMessage = body
	w.completionRevision++
	revision := w.completionRevision
	if w.completionTimer != nil {
		w.completionTimer.Stop()
	}
	w.completionTimer = time.AfterFunc(jobCompletionStatusDuration, func() {
		mygo.RunOnMain(func() {
			if w.closed || revision != w.completionRevision {
				return
			}
			w.completionMessage = ""
			w.completionTimer = nil
			if w.main != nil {
				w.main.Invalidate()
			}
		})
	})
	if w.main != nil {
		w.main.Invalidate()
	}
	if w.notificationCancel != nil {
		w.notificationCancel()
		w.notificationCancel = nil
	}
	if w.showJobNotification != nil {
		w.notificationCancel = w.showJobNotification(presentation.Message(w.preferences.Locale, "shell.jobCompletedTitle"), body)
	}
}

func (w *Workspace) stopJobCompletionFeedback() {
	w.completionRevision++
	if w.completionTimer != nil {
		w.completionTimer.Stop()
		w.completionTimer = nil
	}
	if w.notificationCancel != nil {
		w.notificationCancel()
		w.notificationCancel = nil
	}
}
