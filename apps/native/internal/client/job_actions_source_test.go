package client

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type sourceActionFixture struct {
	actionFixture
	scoped chan string
	finish chan struct{}
}

func (fixture sourceActionFixture) RetryForSource(ctx context.Context, source, id string) error {
	return fixture.scopedAction(ctx, source+":retry:"+id)
}
func (fixture sourceActionFixture) DeleteForSource(ctx context.Context, source, id string) error {
	return fixture.scopedAction(ctx, source+":delete:"+id)
}
func (fixture sourceActionFixture) scopedAction(ctx context.Context, operation string) error {
	fixture.scoped <- operation
	select {
	case <-fixture.finish:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestJobActionsRetainSourceUntilCompletion(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		name := "retry"
		if deleting {
			name = "delete"
		}
		t.Run(name, func(t *testing.T) {
			fixture := sourceActionFixture{actionFixture: actionFixture{calls: make(chan string, 1)}, scoped: make(chan string, 1), finish: make(chan struct{})}
			callbacks := make(chan func(), 1)
			completedSource, completedID, completedDelete := "", "", false
			controller := NewJobActions(fixture, fixture, func(context.Context, string) (bool, error) { return true, nil }, func(callback func()) { callbacks <- callback }, func() {}, func(source, id string, deleted bool) {
				completedSource, completedID, completedDelete = source, id, deleted
			})
			defer controller.Close()
			controller.Source = "remote"
			if deleting {
				controller.Delete("same-id", "Synthetic")
			} else {
				controller.Retry("same-id")
			}
			controller.Source = "local"
			if operation := receive(t, fixture.scoped); operation != "remote:"+name+":same-id" {
				t.Fatal("operation changed source while pending", operation)
			}
			close(fixture.finish)
			receive(t, callbacks)()
			if completedSource != "remote" || completedID != "same-id" || completedDelete != deleting || controller.Error != nil {
				t.Fatal("completion lost its captured source", completedSource, completedID, completedDelete, controller.Error)
			}
			select {
			case <-fixture.calls:
				t.Fatal("source-aware operation fell back to local")
			default:
			}
		})
	}
}

func TestRemoteJobActionsNeverFallBackToLocalPorts(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		fixture := actionFixture{calls: make(chan string, 1)}
		callbacks := make(chan func(), 1)
		completed := false
		controller := NewJobActions(fixture, fixture, func(context.Context, string) (bool, error) { return true, nil }, func(callback func()) { callbacks <- callback }, func() {}, func(string, string, bool) { completed = true })
		controller.Source = "remote"
		if deleting {
			controller.Delete("same-id", "Synthetic")
		} else {
			controller.Retry("same-id")
		}
		receive(t, callbacks)()
		controller.Close()
		if completed || !errors.Is(controller.Error, domain.ErrRemoteUnsupported) {
			t.Fatal("unsupported remote operation was accepted", deleting, controller.Error)
		}
		select {
		case <-fixture.calls:
			t.Fatal("remote operation reached a same-ID local job")
		default:
		}
	}
}
