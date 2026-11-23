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
	jobs, err := m.reader.ListJobs(ctx)
	if err != nil {
		return domain.JobPage{}, err
	}
	return domain.QueryJobs(jobs, query), nil
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
func (m *Meetings) ReadResult(ctx context.Context, id string) (domain.Result, error) {
	return m.Result(ctx, id)
}
