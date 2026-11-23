package client

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type memberFileReply struct {
	result   domain.MemberImportResult
	selected bool
	err      error
}

type controlledMemberFiles struct {
	imports, exports chan context.Context
	replies          chan memberFileReply
}

func (f *controlledMemberFiles) Import(ctx context.Context) (domain.MemberImportResult, bool, error) {
	f.imports <- ctx
	reply := <-f.replies
	return reply.result, reply.selected, reply.err
}

func (f *controlledMemberFiles) Export(ctx context.Context) (bool, error) {
	f.exports <- ctx
	reply := <-f.replies
	return reply.selected, reply.err
}

type memberFileLists struct {
	ResourceLists
	readErr error
	reads   int
}

func (s *memberFileLists) Members(context.Context) ([]domain.MeetingMember, error) {
	s.reads++
	return []domain.MeetingMember{{ID: "synthetic-member", Name: "合成人员"}}, s.readErr
}

func TestMemberFilesClearEarlierSuccessBeforeEachDialog(t *testing.T) {
	for _, operation := range []string{"import", "export"} {
		for _, outcome := range []string{"cancel", "failure"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				callbacks := make(chan func(), 4)
				files := &controlledMemberFiles{imports: make(chan context.Context, 2), exports: make(chan context.Context, 2), replies: make(chan memberFileReply, 1)}
				service := &memberFileLists{}
				controller := NewResources(service, func(f func()) { callbacks <- f }, func() {})
				defer controller.Close()
				controller.ImportResult = &domain.MemberImportResult{Created: 3}
				controller.Exported = true
				controller.Error = errors.New("previous failure")
				calls := files.imports
				start := func() { controller.Import(files) }
				if operation == "export" {
					calls = files.exports
					start = func() { controller.Export(files) }
				}
				start()
				receive(t, calls)
				if !controller.Busy || controller.ImportResult != nil || controller.Exported || controller.Error != nil {
					t.Error("new file operation still presents an earlier result while its dialog is pending")
				}
				start()
				reply := memberFileReply{}
				if outcome == "failure" {
					reply.selected, reply.err = true, errors.New("synthetic file failure")
				}
				files.replies <- reply
				receive(t, callbacks)()
				if controller.Busy || controller.ImportResult != nil || controller.Exported || controller.Error != reply.err || service.reads != 0 || len(calls) != 0 {
					t.Error("cancelled or failed operation reported success, duplicated a dialog, or refreshed as a committed import")
				}
			})
		}
	}
}

func TestMemberImportKeepsCommittedCountsWhenListRefreshFails(t *testing.T) {
	callbacks := make(chan func(), 4)
	files := &controlledMemberFiles{imports: make(chan context.Context, 1), replies: make(chan memberFileReply, 1)}
	service := &memberFileLists{readErr: errors.New("synthetic refresh failure")}
	controller := NewResources(service, func(f func()) { callbacks <- f }, func() {})
	defer controller.Close()
	controller.Import(files)
	receive(t, files.imports)
	result := domain.MemberImportResult{Created: 2, Updated: 1}
	files.replies <- memberFileReply{result: result, selected: true}
	receive(t, callbacks)()
	receive(t, callbacks)()
	if controller.Busy || controller.ImportResult == nil || *controller.ImportResult != result || controller.Error != service.readErr {
		t.Fatal("follow-up read failure lost the committed import result")
	}
	service.readErr = nil
	controller.Refresh("members")
	receive(t, callbacks)()
	if controller.Error != nil || len(controller.Members) != 1 || controller.ImportResult == nil || *controller.ImportResult != result || service.reads != 2 || len(files.imports) != 0 {
		t.Fatal("refresh retry repeated the import or lost its successful outcome")
	}
}

func TestMemberFilesIgnoreLateCompletionAfterClose(t *testing.T) {
	for _, operation := range []string{"import", "export"} {
		t.Run(operation, func(t *testing.T) {
			callbacks := make(chan func(), 2)
			files := &controlledMemberFiles{imports: make(chan context.Context, 1), exports: make(chan context.Context, 1), replies: make(chan memberFileReply, 1)}
			service := &memberFileLists{}
			changes := 0
			controller := NewResources(service, func(f func()) { callbacks <- f }, func() { changes++ })
			var ctx context.Context
			if operation == "import" {
				controller.Import(files)
				ctx = receive(t, files.imports)
			} else {
				controller.Export(files)
				ctx = receive(t, files.exports)
			}
			controller.Close()
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("closing the controller did not cancel the file operation")
			}
			files.replies <- memberFileReply{selected: true, result: domain.MemberImportResult{Created: 1}}
			receive(t, callbacks)()
			if controller.ImportResult != nil || controller.Exported || changes != 1 || service.reads != 0 {
				t.Fatal("late completion updated the closed view or launched another read")
			}
		})
	}
}
