package client

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type resourceRecoveryRead struct {
	kind  application.ResourceKind
	reply chan error
}

type resourceRecoveryDelete struct {
	kind  application.ResourceKind
	id    string
	reply chan error
}

type resourceRecoveryService struct {
	reads   chan resourceRecoveryRead
	deletes chan resourceRecoveryDelete
}

func (s *resourceRecoveryService) read(ctx context.Context, kind application.ResourceKind) error {
	call := resourceRecoveryRead{kind: kind, reply: make(chan error, 1)}
	select {
	case s.reads <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-call.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *resourceRecoveryService) Models(ctx context.Context) ([]domain.AIModel, error) {
	return []domain.AIModel{{ID: "synthetic-model", Name: "Synthetic model"}}, s.read(ctx, application.Models)
}
func (s *resourceRecoveryService) Templates(ctx context.Context) ([]domain.SummaryTemplate, error) {
	return []domain.SummaryTemplate{{ID: "synthetic-template", Name: "Synthetic template"}}, s.read(ctx, application.Templates)
}
func (s *resourceRecoveryService) Members(ctx context.Context) ([]domain.MeetingMember, error) {
	return []domain.MeetingMember{{ID: "synthetic-member", Name: "Synthetic member"}}, s.read(ctx, application.Members)
}
func (s *resourceRecoveryService) remove(ctx context.Context, kind application.ResourceKind, id string) error {
	call := resourceRecoveryDelete{kind: kind, id: id, reply: make(chan error, 1)}
	select {
	case s.deletes <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-call.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *resourceRecoveryService) DeleteModel(ctx context.Context, id string) (application.MutationOutcome, error) {
	return application.MutationOutcome{}, s.remove(ctx, application.Models, id)
}
func (s *resourceRecoveryService) DeleteTemplate(ctx context.Context, id string) error {
	return s.remove(ctx, application.Templates, id)
}
func (s *resourceRecoveryService) DeleteMember(ctx context.Context, id string) error {
	return s.remove(ctx, application.Members, id)
}

func TestResourceDeleteFailureSurvivesQueuedRefreshUntilExplicitRetry(t *testing.T) {
	for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
		t.Run(string(kind), func(t *testing.T) {
			callbacks := make(chan func(), 8)
			service := &resourceRecoveryService{reads: make(chan resourceRecoveryRead, 4), deletes: make(chan resourceRecoveryDelete, 2)}
			controller := NewResources(service, func(callback func()) { callbacks <- callback }, func() {})
			t.Cleanup(controller.Close)
			controller.Delete(kind, "synthetic-resource", func(context.Context) (bool, error) { return true, nil })
			deletion := receive(t, service.deletes)
			controller.Refresh(kind) // Focus can return while the confirmation/action is still pending.
			failure := errors.New("synthetic deletion failure")
			deletion.reply <- failure
			firstRead := receive(t, service.reads)
			firstRead.reply <- nil
			receive(t, callbacks)()
			if !errors.Is(controller.Error, failure) || !controller.Busy {
				t.Fatal("queued refresh hid the failed deletion before its snapshot returned")
			}
			queuedRead := receive(t, service.reads)
			queuedRead.reply <- nil
			receive(t, callbacks)()
			if !errors.Is(controller.Error, failure) || controller.Busy || len(service.deletes) != 0 {
				t.Fatal("successful queued read cleared or replayed the failed deletion")
			}
			controller.Refresh(kind)
			retryRead := receive(t, service.reads)
			retryRead.reply <- nil
			receive(t, callbacks)()
			if controller.Error != nil || controller.Busy || len(service.deletes) != 0 {
				t.Fatal("manual refresh did not acknowledge the failure without repeating deletion")
			}
		})
	}
}

func TestMemberFileFailureSurvivesQueuedRefresh(t *testing.T) {
	for _, operation := range []string{"import", "export"} {
		t.Run(operation, func(t *testing.T) {
			callbacks := make(chan func(), 4)
			files := &controlledMemberFiles{imports: make(chan context.Context, 1), exports: make(chan context.Context, 1), replies: make(chan memberFileReply, 1)}
			service := &resourceRecoveryService{reads: make(chan resourceRecoveryRead, 2)}
			controller := NewResources(service, func(callback func()) { callbacks <- callback }, func() {})
			t.Cleanup(controller.Close)
			if operation == "import" {
				controller.Import(files)
				receive(t, files.imports)
			} else {
				controller.Export(files)
				receive(t, files.exports)
			}
			controller.Refresh(application.Members)
			failure := errors.New("synthetic file operation failure")
			files.replies <- memberFileReply{selected: true, err: failure}
			receive(t, callbacks)()
			if !errors.Is(controller.Error, failure) || !controller.Busy {
				t.Fatal("queued refresh hid the failed file operation")
			}
			read := receive(t, service.reads)
			read.reply <- nil
			receive(t, callbacks)()
			if !errors.Is(controller.Error, failure) || controller.Busy || controller.Exported || controller.ImportResult != nil || len(files.imports) != 0 || len(files.exports) != 0 {
				t.Fatal("follow-up read cleared the error or replayed a failed file dialog")
			}
		})
	}
}
