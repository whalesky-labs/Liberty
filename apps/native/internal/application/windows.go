package application

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"

	"liberty.local/liberty/internal/domain"
)

var ErrWindowScope = errors.New("window_scope_invalid")

type WindowRole string

const (
	Workbench    WindowRole = "job-workbench"
	AISummary    WindowRole = "ai-summary"
	MeetingNotes WindowRole = "meeting-notes"
)

var localJobID = regexp.MustCompile(`^job-[0-9]{13}-[0-9]{1,20}$`)

type resultReader interface {
	Result(context.Context, string) (domain.Result, error)
}

// A mode router must read a window's original source even after the main
// window changes processing mode. IDs alone do not identify a remote job.
type sourceResultReader interface {
	ResultForSource(context.Context, string, string) (domain.Result, error)
}

type remoteCapabilityReader interface {
	RemoteCapabilities(context.Context) (domain.RemoteCapabilities, error)
}

type remoteWindowBinder interface {
	bindRemoteWindow(context.Context) (*remoteWindowBinding, error)
}

type boundRemoteSpeakerWriter interface {
	renameSpeakerBound(context.Context, *remoteWindowBinding, string, string, string) error
}

// WindowScopes is retained by the trusted application shell. It grants narrow,
// process-local ports instead of accepting caller-supplied window labels/tokens.
// A grant's pointer identity is its unforgeable capability; it is never serialized.
type WindowScopes struct {
	mu      sync.Mutex
	reader  resultReader
	current map[WindowRole]*JobWindow
	writer  SpeakerWriter
	word    *WordExport
	summary *SummaryService
	pet     PetWorkflow
}

func (s *WindowScopes) EnableExportRewards(pet PetWorkflow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pet = pet
}

type SpeakerWriter interface {
	RenameSpeaker(context.Context, string, string, string) error
}

type sourceSpeakerWriter interface {
	RenameSpeakerForSource(context.Context, string, string, string, string) error
}

func (s *WindowScopes) readResult(ctx context.Context, source, id string) (domain.Result, error) {
	if reader, ok := s.reader.(sourceResultReader); ok {
		return reader.ResultForSource(ctx, source, id)
	}
	return s.reader.Result(ctx, id)
}

// EnableEditing is only called by the trusted composition root. Grants never
// receive the repository, a caller-supplied job ID, or a global writer port.
func (s *WindowScopes) EnableEditing(writer SpeakerWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writer = writer
}

func (g *JobWindow) RenameSpeaker(ctx context.Context, from, to string) error {
	if g == nil || g.owner == nil {
		return ErrWindowScope
	}
	g.owner.mu.Lock()
	allowed := g.authorized() && g.role == Workbench && g.owner.writer != nil
	g.owner.mu.Unlock()
	if !allowed {
		return ErrWindowScope
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r, err := g.readResult(ctx)
	if err != nil {
		return err
	}
	if r.Job.ID != g.id || r.Job.Source != g.source || !r.CanRenameSpeaker() {
		return ErrWindowScope
	}
	release, err := g.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	g.owner.mu.Lock()
	allowed = g.authorized()
	writer := g.owner.writer
	g.owner.mu.Unlock()
	if !allowed || writer == nil {
		return ErrWindowScope
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Revocation and replacement wait until this accepted mutation finishes.
	if g.remote != nil {
		bound, ok := writer.(boundRemoteSpeakerWriter)
		if !ok {
			return ErrWindowScope
		}
		return bound.renameSpeakerBound(ctx, g.remote, g.id, from, to)
	}
	if writer, ok := writer.(sourceSpeakerWriter); ok {
		return writer.RenameSpeakerForSource(ctx, g.source, g.id, from, to)
	}
	if g.source != "local" {
		return ErrWindowScope
	}
	return writer.RenameSpeaker(ctx, g.id, from, to)
}

// ExportText publishes a fresh bound snapshot. The desktop adapter owns the
// selected destination; an old/replaced grant cannot invoke its publisher.
func (g *JobWindow) ExportText(ctx context.Context, kind domain.TextExportKind, labels domain.TextExportLabels, publish func([]byte) error) error {
	if g == nil || g.owner == nil {
		return ErrWindowScope
	}
	g.owner.mu.Lock()
	allowed := g.authorized() && g.role == Workbench && publish != nil
	g.owner.mu.Unlock()
	if !allowed {
		return ErrWindowScope
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r, err := g.readResult(ctx)
	if err != nil {
		return err
	}
	if r.Job.ID != g.id || r.Job.Source != g.source {
		return ErrWindowScope
	}
	content, err := domain.RenderTextExport(r, kind, labels)
	if err != nil {
		return err
	}
	release, err := g.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	if g.remote != nil {
		if err := g.remote.validate(ctx); err != nil {
			return err
		}
	}
	g.owner.mu.Lock()
	allowed = g.authorized()
	pet := g.owner.pet
	g.owner.mu.Unlock()
	if !allowed {
		return ErrWindowScope
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	err = publish([]byte(content))
	if err == nil {
		recordPetWorkflow(ctx, pet, "export_completed", g.id)
	}
	return err
}

func (g *JobWindow) finishExport(ctx context.Context, err error) error {
	if err == nil {
		recordPetWorkflow(ctx, g.owner.pet, "export_completed", g.id)
	}
	return err
}

func NewWindowScopes(reader resultReader) *WindowScopes {
	return &WindowScopes{reader: reader, current: make(map[WindowRole]*JobWindow)}
}

type JobWindow struct {
	owner    *WindowScopes
	role     WindowRole
	source   string
	id       string
	remote   *remoteWindowBinding
	mutation chan struct{}
}

func (g *JobWindow) lockMutation(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case g.mutation <- struct{}{}:
		return func() { <-g.mutation }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *JobWindow) readResult(ctx context.Context) (domain.Result, error) {
	if g.remote != nil {
		return g.remote.readResult(ctx, g.id)
	}
	return g.owner.readResult(ctx, g.source, g.id)
}

func validWindow(role WindowRole, source, id string) bool {
	if role != Workbench && role != AISummary && role != MeetingNotes {
		return false
	}
	if source == "local" {
		return localJobID.MatchString(id)
	}
	return source == "remote" && role == Workbench && id != "" && len(id) <= 256
}

// OpenFromMain checks the actual repository source before issuing a grant.
func (s *WindowScopes) OpenFromMain(ctx context.Context, role WindowRole, source, id string) (*JobWindow, error) {
	source, id = strings.TrimSpace(source), strings.TrimSpace(id)
	if !validWindow(role, source, id) {
		return nil, ErrWindowScope
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var binding *remoteWindowBinding
	var err error
	if binder, ok := s.reader.(remoteWindowBinder); ok && source == "remote" {
		binding, err = binder.bindRemoteWindow(ctx)
		if err != nil {
			return nil, err
		}
	}
	var job domain.Result
	if binding != nil {
		job, err = binding.readResult(ctx, id)
	} else {
		job, err = s.readResult(ctx, source, id)
	}
	if err != nil {
		return nil, err
	}
	if job.Job.ID != id || job.Job.Source != source {
		return nil, ErrWindowScope
	}
	return s.replace(ctx, role, source, id, binding, nil)
}

func (s *WindowScopes) replace(ctx context.Context, role WindowRole, source, id string, binding *remoteWindowBinding, parent *JobWindow) (*JobWindow, error) {
	grant := &JobWindow{owner: s, role: role, source: source, id: id, remote: binding, mutation: make(chan struct{}, 1)}
	for {
		s.mu.Lock()
		previous := s.current[role]
		s.mu.Unlock()
		release := func() {}
		if previous != nil {
			var err error
			release, err = previous.lockMutation(ctx)
			if err != nil {
				return nil, err
			}
		}
		if binding != nil {
			if err := binding.validate(ctx); err != nil {
				release()
				return nil, err
			}
		}
		s.mu.Lock()
		if s.current[role] != previous {
			s.mu.Unlock()
			release()
			continue
		}
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			release()
			return nil, err
		}
		if parent != nil && !parent.authorized() {
			s.mu.Unlock()
			release()
			return nil, ErrWindowScope
		}
		s.current[role] = grant
		s.mu.Unlock()
		release()
		return grant, nil
	}
}

func (g *JobWindow) authorized() bool {
	return g != nil && g.owner != nil && g.owner.current[g.role] == g
}

func (g *JobWindow) OpenChild(role WindowRole) (*JobWindow, error) {
	if g == nil || g.owner == nil {
		return nil, ErrWindowScope
	}
	g.owner.mu.Lock()
	allowed := g.authorized() && g.role == Workbench && g.source == "local" && (role == AISummary || role == MeetingNotes)
	g.owner.mu.Unlock()
	if !allowed {
		return nil, ErrWindowScope
	}
	return g.owner.replace(context.Background(), role, g.source, g.id, nil, g)
}

func (g *JobWindow) Result(ctx context.Context, id string) (domain.Result, error) {
	if g == nil || g.owner == nil {
		return domain.Result{}, ErrWindowScope
	}
	g.owner.mu.Lock()
	allowed := g.authorized() && id == g.id
	g.owner.mu.Unlock()
	if !allowed {
		return domain.Result{}, ErrWindowScope
	}
	if err := ctx.Err(); err != nil {
		return domain.Result{}, err
	}
	result, err := g.readResult(ctx)
	if err != nil {
		return domain.Result{}, err
	}
	// Revocation may race with I/O. Never deliver a result through an old grant.
	g.owner.mu.Lock()
	defer g.owner.mu.Unlock()
	if !g.authorized() || result.Job.Source != g.source || result.Job.ID != g.id {
		return domain.Result{}, ErrWindowScope
	}
	if err := ctx.Err(); err != nil {
		return domain.Result{}, err
	}
	return result, nil
}

// RemoteCapabilities exposes only the service capability snapshot, through the
// same source-bound grant as the result. It never exposes settings or secrets.
// Switching the main window to local mode does not redirect this request.
func (g *JobWindow) RemoteCapabilities(ctx context.Context) (domain.RemoteCapabilities, error) {
	if g == nil || g.owner == nil {
		return domain.RemoteCapabilities{}, ErrWindowScope
	}
	g.owner.mu.Lock()
	allowed := g.authorized() && g.role == Workbench && g.source == "remote"
	reader, supported := g.owner.reader.(remoteCapabilityReader)
	g.owner.mu.Unlock()
	if !allowed || !supported && g.remote == nil {
		return domain.RemoteCapabilities{}, ErrWindowScope
	}
	if err := ctx.Err(); err != nil {
		return domain.RemoteCapabilities{}, err
	}
	var capabilities domain.RemoteCapabilities
	var err error
	if g.remote != nil {
		capabilities, err = g.remote.capabilities(ctx)
	} else {
		capabilities, err = reader.RemoteCapabilities(ctx)
	}
	if err != nil {
		return domain.RemoteCapabilities{}, err
	}
	g.owner.mu.Lock()
	defer g.owner.mu.Unlock()
	if !g.authorized() {
		return domain.RemoteCapabilities{}, ErrWindowScope
	}
	if err := ctx.Err(); err != nil {
		return domain.RemoteCapabilities{}, err
	}
	return capabilities, nil
}

func (g *JobWindow) Close() {
	if g == nil || g.owner == nil {
		return
	}
	g.owner.mu.Lock()
	allowed := g.authorized()
	g.owner.mu.Unlock()
	if !allowed {
		return
	}
	release, _ := g.lockMutation(context.Background())
	defer release()
	g.owner.mu.Lock()
	defer g.owner.mu.Unlock()
	if g.authorized() {
		delete(g.owner.current, g.role)
	}
}
