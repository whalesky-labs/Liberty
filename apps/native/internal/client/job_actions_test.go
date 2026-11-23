package client

import (
	"context"
	"testing"
)

type actionFixture struct{ calls chan string }

func (f actionFixture) Retry(_ context.Context, id string) error {
	f.calls <- "retry:" + id
	return nil
}
func (f actionFixture) Delete(_ context.Context, id string) error {
	f.calls <- "delete:" + id
	return nil
}
func TestDeleteConfirmationCancellationAndClosedView(t *testing.T) {
	fixture := actionFixture{calls: make(chan string, 2)}
	callbacks := make(chan func(), 2)
	confirmed := make(chan bool, 1)
	completed := 0
	c := NewJobActions(fixture, fixture, func(context.Context, string) (bool, error) { return <-confirmed, nil }, func(f func()) { callbacks <- f }, func() {}, func(string, string, bool) { completed++ })
	c.Delete("one", "Synthetic")
	confirmed <- false
	receive(t, callbacks)()
	if completed != 0 || c.Busy {
		t.Fatal("cancelled deletion reported completion")
	}
	select {
	case <-fixture.calls:
		t.Fatal("cancelled deletion reached repository")
	default:
	}
	c.Delete("two", "Synthetic")
	c.Retry("two")
	confirmed <- true
	if receive(t, fixture.calls) != "delete:two" {
		t.Fatal("concurrent action accepted")
	}
	c.Close()
	receive(t, callbacks)()
	if completed != 0 {
		t.Fatal("closed view received completion")
	}
}
