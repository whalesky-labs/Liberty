// Package application defines use cases over typed ports, independent of MyGo.
package application

import (
	"context"
	"errors"
	"strings"

	"liberty.local/liberty/internal/domain"
)

var ErrJobNotFound = errors.New("job_not_found")

type MeetingReader interface {
	ListJobs(context.Context) ([]domain.JobSummary, error)
	ReadResult(context.Context, string) (domain.Result, error)
}

type Meetings struct{ reader MeetingReader }

func NewMeetings(reader MeetingReader) *Meetings { return &Meetings{reader: reader} }

func (m *Meetings) Queue(ctx context.Context, query domain.QueueQuery) (domain.JobPage, error) {
	if reader, ok := m.reader.(interface {
		QueryJobs(context.Context, domain.QueueQuery) (domain.JobPage, error)
	}); ok {
		return reader.QueryJobs(ctx, query)
	}
	jobs, err := m.reader.ListJobs(ctx)
	if err != nil {
		return domain.JobPage{}, err
	}
	return domain.QueryJobs(jobs, query), nil
}

// A zero revision means the reader cannot cheaply detect committed changes.
// Callers must keep reading in that case (including remote services).
func (m *Meetings) ResultRevision(ctx context.Context, _ string) (uint64, error) {
	return meetingRevision(ctx, m.reader)
}

func meetingRevision(ctx context.Context, reader MeetingReader) (uint64, error) {
	if reader, ok := reader.(interface {
		ReadRevision(context.Context) (uint64, error)
	}); ok {
		return reader.ReadRevision(ctx)
	}
	return 0, nil
}

func (m *Meetings) Result(ctx context.Context, id string) (domain.Result, error) {
	if strings.TrimSpace(id) == "" {
		return domain.Result{}, ErrJobNotFound
	}
	return m.reader.ReadResult(ctx, id)
}

func (m *Meetings) ListJobs(ctx context.Context) ([]domain.JobSummary, error) {
	return m.reader.ListJobs(ctx)
}
func (m *Meetings) QueryJobs(ctx context.Context, query domain.QueueQuery) (domain.JobPage, error) {
	return m.Queue(ctx, query)
}
func (m *Meetings) ReadRevision(ctx context.Context) (uint64, error) {
	return meetingRevision(ctx, m.reader)
}
func (m *Meetings) ReadResult(ctx context.Context, id string) (domain.Result, error) {
	return m.Result(ctx, id)
}
