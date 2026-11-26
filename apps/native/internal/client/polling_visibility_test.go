package client

import (
	"context"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func TestRuntimePollingUsesVisibilityAndActiveOperations(t *testing.T) {
	s := runtimeService{calls: make(chan runtimeCall, 4), actions: make(chan string, 2)}
	callbacks := make(chan func(), 4)
	changes := 0
	c := NewRuntime(s, func(f func()) { callbacks <- f }, func() { changes++ })
	defer c.Close()
	c.Refresh()
	call := receive(t, s.calls)
	call.reply <- domain.RuntimePanel{}
	receive(t, callbacks)()
	before := changes
	c.SetVisible(false)
	c.lastPoll = time.Now().Add(-10 * time.Second)
	c.pollDue()
	if c.loading {
		t.Fatal("hidden idle runtime was polled too early")
	}
	c.lastPoll = time.Now().Add(-31 * time.Second)
	c.pollDue()
	call = receive(t, s.calls)
	call.reply <- domain.RuntimePanel{}
	receive(t, callbacks)()
	if changes != before {
		t.Fatal("unchanged runtime caused a redraw")
	}
	c.SetVisible(true)
	call = receive(t, s.calls)
	call.reply <- domain.RuntimePanel{}
	receive(t, callbacks)()
	c.Panel.Overview.Python.Operation.Kind = "downloading"
	c.lastPoll = time.Now().Add(-2 * time.Second)
	c.pollDue()
	if !c.loading {
		t.Fatal("active installation was throttled")
	}
	call = receive(t, s.calls)
	call.reply <- domain.RuntimePanel{}
	receive(t, callbacks)()
}

type visibilityPet struct {
	snapshotPet
	stores, profiles int
}

func (s *visibilityPet) Store(ctx context.Context) (domain.PetStoreState, error) {
	s.stores++
	return s.snapshotPet.Store(ctx)
}
func (s *visibilityPet) Profile(context.Context) (domain.PetProfile, error) {
	s.profiles++
	return s.store.Profile, nil
}

func TestHiddenPetPollingKeepsCompanionWithoutLoadingStore(t *testing.T) {
	s := &visibilityPet{}
	callbacks := make(chan func(), 4)
	changes := 0
	c := NewPet(s, func(f func()) { callbacks <- f }, func() { changes++ })
	defer c.Close()
	c.Refresh()
	receive(t, callbacks)()
	c.SetVisible(false)
	before := changes
	for range 3 {
		c.poll()
		receive(t, callbacks)()
	}
	if s.stores != 1 || s.profiles != 3 || changes != before {
		t.Fatal("hidden companion reloaded the store or repainted unchanged state")
	}
	s.store.Profile.Name = "updated"
	c.poll()
	receive(t, callbacks)()
	if c.Profile.Name != "updated" || changes != before+1 {
		t.Fatal("companion profile stopped synchronizing")
	}
	c.SetVisible(true)
	receive(t, callbacks)()
	if s.stores != 2 {
		t.Fatal("returning to pet page did not refresh store")
	}
}
