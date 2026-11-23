package desktop

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

type companionLifecycleWindow struct {
	instance      int
	settings      domain.PetSettings
	visible       bool
	alwaysOnTop   bool
	closed        func()
	inputRegion   []mygo.Rectangle
	destroys      int
	shows         int
	topChanges    int
	invalidations int
}

func (window *companionLifecycleWindow) Destroy() {
	window.destroys++
	window.visible = false
	if window.closed != nil {
		closed := window.closed
		window.closed = nil
		closed()
	}
}

func (window *companionLifecycleWindow) IsVisible() bool     { return window.visible }
func (window *companionLifecycleWindow) IsAlwaysOnTop() bool { return window.alwaysOnTop }
func (window *companionLifecycleWindow) SetAlwaysOnTop(value bool) {
	window.alwaysOnTop = value
	window.topChanges++
}
func (window *companionLifecycleWindow) Invalidate() { window.invalidations++ }
func (window *companionLifecycleWindow) OnClosed(closed func()) func() {
	window.closed = closed
	return func() { window.closed = nil }
}
func (window *companionLifecycleWindow) SetInputRegion(region []mygo.Rectangle) {
	window.inputRegion = append([]mygo.Rectangle(nil), region...)
}
func (window *companionLifecycleWindow) ShowInactive() {
	window.shows++
	window.visible = true
}

type companionLifecycleFactory struct {
	windows []*companionLifecycleWindow
}

func (factory *companionLifecycleFactory) create(instance int, settings domain.PetSettings) companionWindow {
	window := &companionLifecycleWindow{instance: instance, settings: settings, alwaysOnTop: settings.AlwaysOnTop}
	factory.windows = append(factory.windows, window)
	return window
}

type companionLifecycleService struct {
	client.PetService
	settings domain.PetSettings
	saveErr  error
	readErr  error
	requests chan domain.PetSettings
	release  <-chan struct{}
}

func (service *companionLifecycleService) SaveSettings(ctx context.Context, settings domain.PetSettings) (domain.PetSettings, error) {
	service.requests <- settings
	if service.release != nil {
		select {
		case <-service.release:
		case <-ctx.Done():
			return domain.PetSettings{}, ctx.Err()
		}
	}
	if service.saveErr != nil {
		return domain.PetSettings{}, service.saveErr
	}
	service.settings = settings
	return settings, nil
}

func (service *companionLifecycleService) Store(context.Context) (domain.PetStoreState, error) {
	return domain.PetStoreState{}, service.readErr
}

func (service *companionLifecycleService) Settings(context.Context) (domain.PetSettings, error) {
	return service.settings, nil
}

func (*companionLifecycleService) Events(context.Context, int) ([]domain.PetEventLedgerEntry, error) {
	return nil, nil
}

func (*companionLifecycleService) Cosmetics(context.Context) ([]domain.PetCosmeticUnlock, error) {
	return nil, nil
}

func (*companionLifecycleService) CheckIn(context.Context) (domain.PetDailyCheckInState, error) {
	return domain.PetDailyCheckInState{}, nil
}

func (*companionLifecycleService) BlindBox(context.Context) (domain.PetBlindBoxState, error) {
	return domain.PetBlindBoxState{}, nil
}

func (*companionLifecycleService) Redemptions(context.Context, int) ([]domain.PetRedeemKeyRedemption, error) {
	return nil, nil
}

func companionLifecycleReceive[Value any](t *testing.T, values <-chan Value) Value {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("companion lifecycle operation did not finish")
		var zero Value
		return zero
	}
}

func TestCompanionOpenExtraIgnoresUnavailableAndBusySettings(t *testing.T) {
	for _, settings := range []*domain.PetSettings{nil, {DesktopEnabled: false}, {DesktopEnabled: true}} {
		view := &PetView{Client: &client.PetController{Settings: settings, Busy: true}}
		companions := newCompanions(view, func(context.Context, int, int) error { return nil })
		factory := &companionLifecycleFactory{}
		companions.create = factory.create
		companions.openExtra()
		if companions.awaitEnable || companions.nextExtra != 0 || len(companions.windows) != 0 || len(factory.windows) != 0 {
			t.Error("unavailable or busy settings created a pending companion")
		}
		companions.close()
	}
	view := &PetView{Client: &client.PetController{}}
	companions := newCompanions(view, func(context.Context, int, int) error { return nil })
	factory := &companionLifecycleFactory{}
	companions.create = factory.create
	companions.openExtra()
	companions.sync()
	if companions.awaitEnable || companions.nextExtra != 0 || companions.visibleCount() != 0 || len(factory.windows) != 0 {
		t.Error("missing settings created a pending companion")
	}
	companions.close()
}

func TestCompanionEnableSaveFailureNeverOpensWindow(t *testing.T) {
	for _, readFailure := range []bool{false, true} {
		name := "refresh-succeeds"
		if readFailure {
			name = "refresh-fails"
		}
		t.Run(name, func(t *testing.T) {
			saveFailure := errors.New("synthetic companion enable save failure")
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			service := &companionLifecycleService{
				settings: domain.PetSettings{PetID: "default-pet", AlwaysOnTop: true, Muted: true, ProactiveLevel: 1},
				saveErr:  saveFailure, requests: make(chan domain.PetSettings, 2), release: release,
			}
			if readFailure {
				service.readErr = errors.New("synthetic companion refresh failure")
			}
			callbacks := make(chan func(), 4)
			view := &PetView{Locale: "en-US"}
			companions := newCompanions(view, func(context.Context, int, int) error { return nil })
			factory := &companionLifecycleFactory{}
			companions.create = factory.create
			controller := client.NewPet(service, func(callback func()) { callbacks <- callback }, companions.sync)
			view.Client = controller
			initial := service.settings
			controller.Settings, controller.Store = &initial, &domain.PetStoreState{}
			t.Cleanup(func() {
				unblock()
				controller.Close()
				companions.close()
			})
			companions.openExtra()
			request := companionLifecycleReceive(t, service.requests)
			expected := initial
			expected.DesktopEnabled = true
			if request != expected || !controller.Busy || !companions.awaitEnable || initial.DesktopEnabled || len(companions.windows) != 0 || len(factory.windows) != 0 {
				t.Fatal("enable did not wait for persisted settings", request, controller.Busy, companions.awaitEnable)
			}
			companions.openExtra()
			companions.sync()
			select {
			case duplicate := <-service.requests:
				t.Fatal("busy enable queued another save", duplicate)
			default:
			}
			unblock()
			companionLifecycleReceive(t, callbacks)()
			if !errors.Is(controller.Error, saveFailure) || controller.Busy || controller.Settings.DesktopEnabled || controller.ActionRevision != 0 || companions.awaitEnable || companions.nextExtra != 0 || len(companions.windows) != 0 || companions.visibleCount() != 0 {
				t.Fatal("failed enable created a window or retained a deferred extra", controller.Error, companions.awaitEnable, companions.nextExtra)
			}
			for iteration := 0; iteration < 3; iteration++ {
				companions.sync()
			}
			if companions.awaitEnable || companions.nextExtra != 0 || companions.visibleCount() != 0 || len(factory.windows) != 0 {
				t.Fatal("later sync revived the failed enable request")
			}
		})
	}
}

func TestCompanionOpenExtraCreatesExactlyOneInstancePerRequest(t *testing.T) {
	settings := domain.PetSettings{DesktopEnabled: true, AlwaysOnTop: true}
	view := &PetView{Client: &client.PetController{Settings: &settings}}
	companions := newCompanions(view, func(context.Context, int, int) error { return nil })
	factory := &companionLifecycleFactory{}
	companions.create = factory.create
	t.Cleanup(companions.close)
	for iteration := 0; iteration < 3; iteration++ {
		companions.sync()
	}
	if len(factory.windows) != 1 || len(companions.windows) != 1 || companions.visibleCount() != 1 || companions.windows[0] != factory.windows[0] {
		t.Fatal("sync did not create exactly one primary companion")
	}
	primary := companions.windows[0]
	for instance := 1; instance <= 3; instance++ {
		companions.openExtra()
		if len(factory.windows) != instance+1 || len(companions.windows) != instance+1 || companions.visibleCount() != instance+1 || companions.nextExtra != instance || companions.windows[0] != primary {
			t.Fatal("openExtra did not add exactly one extra companion", instance)
		}
		window := factory.windows[instance]
		if window.instance != instance || companions.windows[instance] != window || window.settings != settings || window.shows != 1 || window.closed == nil || !reflect.DeepEqual(window.inputRegion, []mygo.Rectangle{companionSprite}) {
			t.Fatal("extra companion was not registered, configured and shown once", instance, window)
		}
		companions.sync()
		companions.sync()
		if len(factory.windows) != instance+1 || companions.visibleCount() != instance+1 {
			t.Fatal("sync duplicated a requested extra companion", instance)
		}
	}
	settings.AlwaysOnTop = false
	companions.sync()
	for _, window := range factory.windows {
		if window.IsAlwaysOnTop() || window.topChanges != 1 || window.invalidations == 0 || window.shows != 1 {
			t.Fatal("sync did not update existing companions in place", window)
		}
	}
	factory.windows[2].Destroy()
	companions.sync()
	if companions.windows[2] != nil || companions.visibleCount() != 3 || len(factory.windows) != 4 {
		t.Fatal("an independently closed extra remained registered or was recreated")
	}
	companions.openExtra()
	if companions.nextExtra != 4 || len(factory.windows) != 5 || companions.windows[4] != factory.windows[4] || companions.visibleCount() != 4 || companions.windows[0] != primary {
		t.Fatal("the next explicit request did not create exactly one new instance")
	}
}

func TestCompanionEnableSaveSuccessOpensOneDeferredExtra(t *testing.T) {
	for _, readFailure := range []bool{false, true} {
		name := "refresh-succeeds"
		if readFailure {
			name = "refresh-fails"
		}
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			service := &companionLifecycleService{
				settings: domain.PetSettings{PetID: "default-pet", AlwaysOnTop: true, Muted: true, ProactiveLevel: 1},
				requests: make(chan domain.PetSettings, 2), release: release,
			}
			if readFailure {
				service.readErr = errors.New("synthetic companion refresh failure")
			}
			callbacks := make(chan func(), 4)
			view := &PetView{Locale: "en-US"}
			companions := newCompanions(view, func(context.Context, int, int) error { return nil })
			factory := &companionLifecycleFactory{}
			companions.create = factory.create
			controller := client.NewPet(service, func(callback func()) { callbacks <- callback }, companions.sync)
			view.Client = controller
			initial := service.settings
			controller.Settings, controller.Store = &initial, &domain.PetStoreState{}
			t.Cleanup(func() {
				unblock()
				controller.Close()
				companions.close()
			})
			companions.openExtra()
			request := companionLifecycleReceive(t, service.requests)
			expected := initial
			expected.DesktopEnabled = true
			if request != expected || !controller.Busy || !companions.awaitEnable || len(factory.windows) != 0 {
				t.Fatal("enable did not wait for persisted settings")
			}
			companions.openExtra()
			companions.sync()
			if len(factory.windows) != 0 || companions.nextExtra != 0 {
				t.Fatal("busy enable created a window before the save completed")
			}
			unblock()
			companionLifecycleReceive(t, callbacks)()
			if controller.Busy || !controller.Settings.DesktopEnabled || controller.ActionRevision != 1 || !errors.Is(controller.Error, service.readErr) || companions.awaitEnable || companions.nextExtra != 1 || len(factory.windows) != 2 || len(companions.windows) != 2 || companions.visibleCount() != 2 {
				t.Fatal("successful enable did not open a primary and one deferred extra", controller.Error, companions.nextExtra, len(factory.windows))
			}
			for instance, window := range factory.windows {
				if window.instance != instance || window.settings != expected || companions.windows[instance] != window || window.shows != 1 {
					t.Fatal("deferred companion used the wrong instance or saved settings", instance, window)
				}
			}
			for iteration := 0; iteration < 3; iteration++ {
				companions.sync()
			}
			if len(factory.windows) != 2 || companions.visibleCount() != 2 {
				t.Fatal("later sync repeated the deferred extra request")
			}
			companions.openExtra()
			if len(factory.windows) != 3 || companions.nextExtra != 2 || companions.visibleCount() != 3 {
				t.Fatal("a later explicit request did not add exactly one companion")
			}
			select {
			case duplicate := <-service.requests:
				t.Fatal("openExtra repeated the enable save", duplicate)
			default:
			}
		})
	}
}

func TestCompanionHideClearsAllWindows(t *testing.T) {
	for _, disabledSync := range []bool{false, true} {
		name := "explicit-hide"
		if disabledSync {
			name = "disabled-sync"
		}
		t.Run(name, func(t *testing.T) {
			settings := domain.PetSettings{DesktopEnabled: true}
			view := &PetView{Client: &client.PetController{Settings: &settings}}
			companions := newCompanions(view, func(context.Context, int, int) error { return nil })
			factory := &companionLifecycleFactory{}
			companions.create = factory.create
			var closeOnce sync.Once
			closeCompanions := func() { closeOnce.Do(companions.close) }
			t.Cleanup(closeCompanions)
			companions.sync()
			companions.openExtra()
			companions.openExtra()
			if companions.visibleCount() != 3 || len(factory.windows) != 3 {
				t.Fatal("initial primary and extra companions were not visible")
			}
			factory.windows[1].visible = false
			if companions.visibleCount() != 2 {
				t.Fatal("visible count included a hidden window")
			}
			if disabledSync {
				settings.DesktopEnabled = false
				companions.sync()
			} else {
				companions.hide()
			}
			companions.hide()
			if companions.visibleCount() != 0 || len(companions.windows) != 0 {
				t.Fatal("hide left companions visible or registered")
			}
			for _, window := range factory.windows {
				if window.destroys != 1 || window.IsVisible() || window.closed != nil {
					t.Fatal("hide did not destroy and unregister each window once", window)
				}
			}
			settings.DesktopEnabled = false
			companions.sync()
			if len(factory.windows) != 3 || companions.visibleCount() != 0 {
				t.Fatal("disabled sync recreated hidden companions")
			}
			settings.DesktopEnabled = true
			companions.sync()
			if len(factory.windows) != 4 || companions.visibleCount() != 1 || len(companions.windows) != 1 || companions.windows[0] != factory.windows[3] || factory.windows[3].instance != 0 {
				t.Fatal("re-enabling recreated stale extra companions")
			}
			closeCompanions()
			if companions.visibleCount() != 0 || len(companions.windows) != 0 || factory.windows[3].destroys != 1 {
				t.Fatal("close did not destroy the reopened primary companion")
			}
		})
	}
}

func TestCompanionCloseDrainsLatestPositionAndWaitsForWriter(t *testing.T) {
	for _, writeFailure := range []bool{false, true} {
		name := "writes-succeed"
		if writeFailure {
			name = "first-write-fails"
		}
		t.Run(name, func(t *testing.T) {
			first := mygo.Point{X: -900, Y: 125}
			last := mygo.Point{X: -1035, Y: -20}
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			started := make(chan mygo.Point, 4)
			var points []mygo.Point
			var contexts []context.Context
			settings := domain.PetSettings{DesktopEnabled: true}
			view := &PetView{Client: &client.PetController{Settings: &settings}}
			companions := newCompanions(view, func(ctx context.Context, x, y int) error {
				point := mygo.Point{X: x, Y: y}
				points = append(points, point)
				contexts = append(contexts, ctx)
				started <- point
				if point == first {
					<-release
					if writeFailure {
						return errors.New("synthetic companion position write failure")
					}
				}
				return nil
			})
			factory := &companionLifecycleFactory{}
			companions.create = factory.create
			var closeOnce sync.Once
			closed := make(chan struct{})
			closeCompanions := func() { closeOnce.Do(func() { companions.close(); close(closed) }) }
			t.Cleanup(func() { unblock(); closeCompanions() })
			companions.sync()
			companions.openExtra()
			companions.remember(first)
			if got := companionLifecycleReceive(t, started); got != first {
				t.Fatal("unexpected first saved position", got)
			}
			companions.remember(mygo.Point{X: 100, Y: 200})
			companions.remember(mygo.Point{X: 300, Y: 400})
			companions.remember(last)
			go closeCompanions()
			select {
			case <-closed:
				t.Fatal("close returned with a position write still in progress")
			case <-time.After(25 * time.Millisecond):
			}
			unblock()
			companionLifecycleReceive(t, closed)
			if !reflect.DeepEqual(points, []mygo.Point{first, last}) {
				t.Fatal("close lost the final position or persisted superseded positions", points)
			}
			if companions.visibleCount() != 0 || len(companions.windows) != 0 || len(factory.windows) != 2 {
				t.Fatal("close left companions visible or registered")
			}
			for _, window := range factory.windows {
				if window.destroys != 1 || window.IsVisible() {
					t.Fatal("close did not destroy each companion once", window)
				}
			}
			for _, ctx := range contexts {
				if _, ok := ctx.Deadline(); !ok || !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatal("position writer lacks a deadline or retained its context after close", ctx.Err())
				}
			}
			select {
			case <-companions.done:
			default:
				t.Fatal("close returned before the writer exited")
			}
		})
	}
}
