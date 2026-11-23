package desktop

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnosticlog"
)

func TestCompanionDiagnosticCallbacksKeepTheirProfileRecorder(t *testing.T) {
	for _, closedSource := range []bool{false, true} {
		for _, failedSave := range []bool{false, true} {
			name := "live-source"
			if closedSource {
				name = "closed-source"
			}
			if failedSave {
				name += "/failed-position"
			} else {
				name += "/saved-position"
			}
			t.Run(name, func(t *testing.T) {
				directory := t.TempDir()
				previous := diagnosticlog.Open(directory)
				defer previous.Close()
				release, started := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				settings := domain.PetSettings{DesktopEnabled: true}
				view := &PetView{Client: &client.PetController{Settings: &settings}}
				companions := newCompanions(view, func(context.Context, int, int) error {
					close(started)
					<-release
					if failedSave {
						return errors.New("synthetic private position error")
					}
					return nil
				}, previous.Record)
				var closeOnce sync.Once
				closeCompanions := func() { closeOnce.Do(companions.close) }
				t.Cleanup(func() { unblock(); closeCompanions() })
				factory := &companionLifecycleFactory{}
				companions.create = factory.create
				companions.sync()
				companions.openExtra()
				companions.remember(mygo.Point{X: -987, Y: -654})
				companionLifecycleReceive(t, started)
				current := diagnosticlog.Open(t.TempDir())
				defer current.Close()
				window := &companionTestWindow{x: -987, y: -654}
				pointer := &companionPointer{window: window, instance: diagnosticlog.Extra, record: previous.Record, tap: func() {}}
				pointer.input(ui.InputEvent{Kind: ui.InputPointerDown, X: 10, Y: 10})
				pointer.input(ui.InputEvent{Kind: ui.InputPointerMove, X: 30, Y: 30})
				pointer.input(ui.InputEvent{Kind: ui.InputPointerUp, X: 30, Y: 30})
				companions.hide()
				if closedSource {
					previous.Close()
					pointer.input(ui.InputEvent{Kind: ui.InputPointerDown, X: 10, Y: 10})
					pointer.input(ui.InputEvent{Kind: ui.InputPointerUp, X: 10, Y: 10})
				}
				unblock()
				closeCompanions()
				previous.Close()
				data, err := os.ReadFile(filepath.Join(directory, diagnosticlog.Filename))
				if err != nil {
					t.Fatal("original companion log unavailable")
				}
				for _, event := range []string{"window_shown", "window_closed", "pointer_pressed", "drag_started", "drag_finished"} {
					if !bytes.Contains(data, []byte(`"event":"`+event+`"`)) {
						t.Fatal("original recorder lost a companion callback", event)
					}
				}
				positionEvent := "position_saved"
				if failedSave {
					positionEvent = "position_save_failed"
				}
				if bytes.Contains(data, []byte(`"event":"`+positionEvent+`"`)) == closedSource || bytes.Contains(data, []byte(`"event":"tapped"`)) {
					t.Fatal("late callbacks ignored the original source lifecycle")
				}
				for _, sensitive := range []string{"-987", "-654", "synthetic private position error"} {
					if bytes.Contains(data, []byte(sensitive)) {
						t.Fatal("companion diagnostic included coordinates or a raw error")
					}
				}
				other, err := current.DiagnosticLog(context.Background())
				if err != nil || bytes.Contains(other, []byte(`"instance":"primary"`)) || bytes.Contains(other, []byte(`"instance":"extra"`)) {
					t.Fatal("old companion callback reached the next profile recorder")
				}
			})
		}
	}
}
