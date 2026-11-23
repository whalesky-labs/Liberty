package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
	"sync"
)

// RemoteConnection stays between application and transport. Public window
// ports expose neither credentials and references nor an arbitrary request port.
type RemoteConnection struct {
	URL       string
	Token     domain.Secret
	reference string
}
type RemoteTransport interface {
	Capabilities(context.Context, RemoteConnection) (domain.RemoteCapabilities, error)
	List(context.Context, RemoteConnection) ([]domain.Result, error)
	Read(context.Context, RemoteConnection, string, bool) (domain.Result, error)
	Retry(context.Context, RemoteConnection, string) (domain.Result, error)
	Delete(context.Context, RemoteConnection, string) error
	Rename(context.Context, RemoteConnection, string, string, string) (domain.Result, error)
}

// ProcessingMode does not consult the OS credential store. Polling the local
// queue must not launch a credential lookup or prompt just to choose a reader.
func (s *Resources) ProcessingMode(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrWindowScope
	}
	r, err := s.repository.Settings(ctx)
	if err != nil {
		return "", err
	}
	return r.Snapshot.Normalized().ProcessingMode, nil
}

func (s *Resources) remoteConnection(ctx context.Context) (RemoteConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RemoteConnection{}, err
	}
	if s.closed {
		return RemoteConnection{}, ErrWindowScope
	}
	r, err := s.repository.Settings(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return RemoteConnection{}, ctx.Err()
		}
		return RemoteConnection{}, domain.ErrRemote
	}
	if r.LegacyPresent {
		return RemoteConnection{}, domain.ErrCredentialMigration
	}
	if r.Reference == "" {
		return RemoteConnection{}, domain.ErrRemote
	}
	token, err := s.credentials.Get(ctx, r.Reference)
	if ctx.Err() != nil {
		return RemoteConnection{}, ctx.Err()
	}
	if err != nil || token.Empty() {
		return RemoteConnection{}, domain.ErrRemote
	}
	return RemoteConnection{URL: r.Snapshot.BackendURL, Token: token, reference: r.Reference}, nil
}

type remoteWindowBinding struct {
	owner      *RemoteMeetings
	connection RemoteConnection
}

func (m *ModeMeetings) bindRemoteWindow(ctx context.Context) (*remoteWindowBinding, error) {
	ctx, done, err := m.remote.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	connection, err := m.remote.resources.remoteConnection(ctx)
	if err != nil {
		return nil, err
	}
	return &remoteWindowBinding{owner: m.remote, connection: connection}, nil
}

func (binding *remoteWindowBinding) validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	binding.owner.mu.Lock()
	closed := binding.owner.closed
	binding.owner.mu.Unlock()
	if closed {
		return ErrWindowScope
	}
	current, err := binding.owner.resources.remoteConnection(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || current != binding.connection {
		return ErrWindowScope
	}
	return nil
}

func (binding *remoteWindowBinding) begin(ctx context.Context) (context.Context, func(), error) {
	ctx, done, err := binding.owner.begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := binding.validate(ctx); err != nil {
		done()
		return nil, nil, err
	}
	return ctx, done, nil
}

func (binding *remoteWindowBinding) readResult(ctx context.Context, id string) (domain.Result, error) {
	ctx, done, err := binding.begin(ctx)
	if err != nil {
		return domain.Result{}, err
	}
	defer done()
	result, err := binding.owner.transport.Read(ctx, binding.connection, id, true)
	if err != nil {
		return domain.Result{}, err
	}
	if err := binding.validate(ctx); err != nil {
		return domain.Result{}, err
	}
	return result, nil
}

func (binding *remoteWindowBinding) capabilities(ctx context.Context) (domain.RemoteCapabilities, error) {
	ctx, done, err := binding.begin(ctx)
	if err != nil {
		return domain.RemoteCapabilities{}, err
	}
	defer done()
	capabilities, err := binding.owner.transport.Capabilities(ctx, binding.connection)
	if err != nil {
		return domain.RemoteCapabilities{}, err
	}
	if err := binding.validate(ctx); err != nil {
		return domain.RemoteCapabilities{}, err
	}
	return capabilities, nil
}

func (m *ModeMeetings) renameSpeakerBound(ctx context.Context, binding *remoteWindowBinding, id, from, to string) error {
	if binding.owner != m.remote {
		return ErrWindowScope
	}
	ctx, done, err := binding.begin(ctx)
	if err != nil {
		return err
	}
	defer done()
	_, err = binding.owner.transport.Rename(ctx, binding.connection, id, from, to)
	return err
}

type RemoteMeetings struct {
	resources *Resources
	transport RemoteTransport
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	wg        sync.WaitGroup
}

func NewRemoteMeetings(resources *Resources, transport RemoteTransport) *RemoteMeetings {
	ctx, cancel := context.WithCancel(context.Background())
	return &RemoteMeetings{resources: resources, transport: transport, ctx: ctx, cancel: cancel}
}
func (r *RemoteMeetings) begin(ctx context.Context) (context.Context, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, nil, ErrWindowScope
	}
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	r.wg.Add(1)
	return operation, func() { stop(); cancel(); r.wg.Done() }, nil
}
func (r *RemoteMeetings) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.wg.Wait()
}
func (r *RemoteMeetings) Capabilities(ctx context.Context) (domain.RemoteCapabilities, error) {
	ctx, done, err := r.begin(ctx)
	if err != nil {
		return domain.RemoteCapabilities{}, err
	}
	defer done()
	c, err := r.resources.remoteConnection(ctx)
	if err != nil {
		return domain.RemoteCapabilities{}, err
	}
	return r.transport.Capabilities(ctx, c)
}
func (r *RemoteMeetings) ListJobs(ctx context.Context) ([]domain.JobSummary, error) {
	results, err := r.ListResults(ctx)
	if err != nil {
		return nil, err
	}
	jobs := make([]domain.JobSummary, len(results))
	for i, v := range results {
		jobs[i] = v.Job
	}
	return jobs, nil
}
func (r *RemoteMeetings) ListResults(ctx context.Context) ([]domain.Result, error) {
	ctx, done, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	c, err := r.resources.remoteConnection(ctx)
	if err != nil {
		return nil, err
	}
	return r.transport.List(ctx, c)
}
func (r *RemoteMeetings) ReadResult(ctx context.Context, id string) (domain.Result, error) {
	ctx, done, err := r.begin(ctx)
	if err != nil {
		return domain.Result{}, err
	}
	defer done()
	c, err := r.resources.remoteConnection(ctx)
	if err != nil {
		return domain.Result{}, err
	}
	return r.transport.Read(ctx, c, id, true)
}
func (r *RemoteMeetings) ReadJob(ctx context.Context, id string) (domain.Result, error) {
	ctx, done, err := r.begin(ctx)
	if err != nil {
		return domain.Result{}, err
	}
	defer done()
	c, err := r.resources.remoteConnection(ctx)
	if err != nil {
		return domain.Result{}, err
	}
	return r.transport.Read(ctx, c, id, false)
}
func (r *RemoteMeetings) Retry(ctx context.Context, id string) error {
	ctx, done, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer done()
	c, err := r.resources.remoteConnection(ctx)
	if err != nil {
		return err
	}
	_, err = r.transport.Retry(ctx, c, id)
	return err
}
func (r *RemoteMeetings) Delete(ctx context.Context, id string) error {
	ctx, done, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer done()
	c, err := r.resources.remoteConnection(ctx)
	if err != nil {
		return err
	}
	return r.transport.Delete(ctx, c, id)
}
func (r *RemoteMeetings) RenameSpeaker(ctx context.Context, id, from, to string) error {
	ctx, done, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer done()
	c, err := r.resources.remoteConnection(ctx)
	if err != nil {
		return err
	}
	_, err = r.transport.Rename(ctx, c, id, from, to)
	return err
}

type ProcessingSettings interface {
	Settings(context.Context) (domain.Settings, error)
}
type MeetingRetry interface {
	Retry(context.Context, string) error
}
type MeetingDelete interface {
	Delete(context.Context, string) error
}

// ModeMeetings routes main-window queries through the persisted mode. Bound
// windows and mutations use an explicit source so switching settings cannot
// redirect an already accepted action to another job with the same ID.
type ModeMeetings struct {
	settings ProcessingSettings
	local    MeetingReader
	remote   *RemoteMeetings
	retry    MeetingRetry
	deleter  MeetingDelete
	speaker  SpeakerWriter
}

func NewModeMeetings(settings ProcessingSettings, local MeetingReader, remote *RemoteMeetings, retry MeetingRetry, deleter MeetingDelete, speaker SpeakerWriter) *ModeMeetings {
	return &ModeMeetings{settings, local, remote, retry, deleter, speaker}
}
func (m *ModeMeetings) ProcessingMode(ctx context.Context) (string, error) {
	if settings, ok := m.settings.(interface {
		ProcessingMode(context.Context) (string, error)
	}); ok {
		return settings.ProcessingMode(ctx)
	}
	s, err := m.settings.Settings(ctx)
	if err != nil {
		return "", err
	}
	return s.Normalized().ProcessingMode, nil
}
func (m *ModeMeetings) source(source string) (MeetingReader, error) {
	switch source {
	case "local":
		return m.local, nil
	case "remote":
		return m.remote, nil
	}
	return nil, ErrWindowScope
}
func (m *ModeMeetings) ListJobs(ctx context.Context) ([]domain.JobSummary, error) {
	mode, err := m.ProcessingMode(ctx)
	if err != nil {
		return nil, err
	}
	r, err := m.source(mode)
	if err != nil {
		return nil, err
	}
	return r.ListJobs(ctx)
}
func (m *ModeMeetings) Capabilities(ctx context.Context) (domain.RemoteCapabilities, error) {
	mode, err := m.ProcessingMode(ctx)
	if err != nil {
		return domain.RemoteCapabilities{}, err
	}
	if mode != "remote" {
		return domain.RemoteCapabilities{}, domain.ErrRemoteUnsupported
	}
	return m.remote.Capabilities(ctx)
}

// RemoteCapabilities serves a source-bound workbench even after the main
// window switches back to local mode; WindowScopes owns the authorization.
func (m *ModeMeetings) RemoteCapabilities(ctx context.Context) (domain.RemoteCapabilities, error) {
	return m.remote.Capabilities(ctx)
}
func (m *ModeMeetings) Queue(ctx context.Context, q domain.QueueQuery) (domain.JobPage, error) {
	mode, err := m.ProcessingMode(ctx)
	if err != nil {
		return domain.JobPage{}, err
	}
	reader, err := m.source(mode)
	if err != nil {
		return domain.JobPage{}, err
	}
	jobs, err := reader.ListJobs(ctx)
	if err != nil {
		return domain.JobPage{}, err
	}
	q.Source = mode
	return domain.QueryJobs(jobs, q), nil
}
func (m *ModeMeetings) Result(ctx context.Context, id string) (domain.Result, error) {
	mode, err := m.ProcessingMode(ctx)
	if err != nil {
		return domain.Result{}, err
	}
	return m.ResultForSource(ctx, mode, id)
}
func (m *ModeMeetings) ReadResult(ctx context.Context, id string) (domain.Result, error) {
	return m.Result(ctx, id)
}
func (m *ModeMeetings) ResultForSource(ctx context.Context, source, id string) (domain.Result, error) {
	r, err := m.source(source)
	if err != nil {
		return domain.Result{}, err
	}
	result, err := r.ReadResult(ctx, id)
	if err == nil && (result.Job.Source != source || result.Job.ID != id) {
		return domain.Result{}, ErrWindowScope
	}
	return result, err
}

// JobForSource reads the main window's detail projection. Remote details only
// require jobs.read; the standalone workbench still uses jobs.result.read.
func (m *ModeMeetings) JobForSource(ctx context.Context, source, id string) (domain.Result, error) {
	var result domain.Result
	var err error
	switch source {
	case "local":
		result, err = m.local.ReadResult(ctx, id)
	case "remote":
		result, err = m.remote.ReadJob(ctx, id)
	default:
		return domain.Result{}, ErrWindowScope
	}
	if err == nil && (result.Job.Source != source || result.Job.ID != id) {
		return domain.Result{}, ErrWindowScope
	}
	return result, err
}
func (m *ModeMeetings) Retry(ctx context.Context, id string) error {
	mode, err := m.ProcessingMode(ctx)
	if err != nil {
		return err
	}
	return m.RetryForSource(ctx, mode, id)
}
func (m *ModeMeetings) RetryForSource(ctx context.Context, source, id string) error {
	switch source {
	case "local":
		return m.retry.Retry(ctx, id)
	case "remote":
		return m.remote.Retry(ctx, id)
	}
	return ErrWindowScope
}
func (m *ModeMeetings) Delete(ctx context.Context, id string) error {
	mode, err := m.ProcessingMode(ctx)
	if err != nil {
		return err
	}
	return m.DeleteForSource(ctx, mode, id)
}
func (m *ModeMeetings) DeleteForSource(ctx context.Context, source, id string) error {
	switch source {
	case "local":
		return m.deleter.Delete(ctx, id)
	case "remote":
		return m.remote.Delete(ctx, id)
	}
	return ErrWindowScope
}
func (m *ModeMeetings) RenameSpeaker(ctx context.Context, id, from, to string) error {
	mode, err := m.ProcessingMode(ctx)
	if err != nil {
		return err
	}
	return m.RenameSpeakerForSource(ctx, mode, id, from, to)
}
func (m *ModeMeetings) RenameSpeakerForSource(ctx context.Context, source, id, from, to string) error {
	switch source {
	case "local":
		return m.speaker.RenameSpeaker(ctx, id, from, to)
	case "remote":
		return m.remote.RenameSpeaker(ctx, id, from, to)
	}
	return ErrWindowScope
}
