package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func companionFoodEvent(id string, when time.Time, amount int64) domain.PetEventLedgerEntry {
	return domain.PetEventLedgerEntry{ID: id, EventType: "store_food", EventValue: amount, EventTime: domain.PersistedUTC(when)}
}

func TestCompanionGrowthFromLedgerDoesNotReplayOrExtend(t *testing.T) {
	view := &PetView{Client: &client.PetController{}, Locale: "en-US"}
	var growth companionGrowth
	render := func(c *ui.Context) { view.companion(c, "", time.Time{}, &growth, nil) }
	first := ui.NewTester(render, companionWidth, companionHeight)
	history := companionFoodEvent("historical", first.Now().Add(-time.Minute), 90)
	view.Client.Events = []domain.PetEventLedgerEntry{history}
	first.Frame()
	if first.HasText("+90") {
		t.Fatal("initial ledger load replayed history")
	}
	first.Advance(time.Second)
	now := first.Now()
	positive := companionFoodEvent("current", now, 12)
	other := companionFoodEvent("other", now, 99)
	other.EventType = "workflow"
	view.Client.Events = []domain.PetEventLedgerEntry{other, companionFoodEvent("zero", now, 0), companionFoodEvent("negative", now, -3), positive, history}
	first.Frame()
	if !first.HasText("+12") || first.HasText("+99") || first.HasText("+0") || first.HasText("+90") {
		t.Fatal("growth must come from the new positive store_food ledger row")
	}
	first.Advance(2 * time.Second)
	view.Client.Events = []domain.PetEventLedgerEntry{positive, history}
	first.Frame()
	if !first.HasText("+12") {
		t.Fatal("growth disappeared before its deadline")
	}
	first.Advance(time.Second)
	if first.HasText("+12") {
		t.Fatal("unchanged ledger extended the three-second deadline")
	}
	view.Client.Events = nil
	first.Frame()
	view.Client.Events = []domain.PetEventLedgerEntry{history, positive}
	first.Frame()
	if first.HasText("+12") || first.HasText("+90") {
		t.Fatal("reordered or reappearing ledger rows replayed")
	}
}

func TestCompanionGrowthKeepsIndependentWindowHistory(t *testing.T) {
	baseline := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	when := baseline.Add(time.Second)
	first := companionFoodEvent("first-food", when, 12)
	second := companionFoodEvent("second-food", when, 24)
	var primary, extra companionGrowth
	primary.observe(baseline, []domain.PetEventLedgerEntry{})
	extra.observe(baseline, []domain.PetEventLedgerEntry{})
	primary.observe(when, []domain.PetEventLedgerEntry{first})
	extra.observe(when, []domain.PetEventLedgerEntry{first})
	if primary.value != 12 || extra.value != 12 {
		t.Fatal("one window consumed the other window's event")
	}
	var reopened companionGrowth
	reopened.observe(when, []domain.PetEventLedgerEntry{first})
	reopened.observe(when.Add(time.Second), []domain.PetEventLedgerEntry{first})
	if reopened.value != 0 {
		t.Fatal("a reopened window inherited active growth")
	}
	primary.observe(when.Add(time.Second), []domain.PetEventLedgerEntry{first, second})
	if primary.value != 24 || !primary.started.Equal(when.Add(time.Second)) {
		t.Fatal("a distinct committed event with the same timestamp was missed")
	}
	primary.observe(when.Add(2*time.Second), []domain.PetEventLedgerEntry{second, first})
	if primary.value != 24 || !primary.started.Equal(when.Add(time.Second)) {
		t.Fatal("reordering same-timestamp rows replayed or extended growth")
	}
}

func TestCompanionGrowthMotionAndOverlappingDrag(t *testing.T) {
	for _, mode := range []struct {
		name   string
		reduce bool
	}{{"animated", false}, {"reduced", true}} {
		t.Run(mode.name, func(t *testing.T) {
			window := &companionTestWindow{}
			var saved []mygo.Point
			taps := 0
			pointer := companionPointer{window: window, remember: func(point mygo.Point) { saved = append(saved, point) }, tap: func() { taps++ }}
			view := &PetView{Client: &client.PetController{Events: []domain.PetEventLedgerEntry{}}, Locale: "en-US"}
			var growth companionGrowth
			tester := ui.NewTesterWith(func(c *ui.Context) {
				view.companion(c, "", time.Time{}, &growth, pointer.input)
			}, companionWidth, companionHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: mode.reduce}})
			tester.Advance(time.Second)
			tester.Press(215, 110)
			view.Client.Events = []domain.PetEventLedgerEntry{companionFoodEvent("during-press", tester.Now(), 12)}
			tester.Frame()
			initial, ok := tester.Find("+12")
			if !ok {
				t.Fatal("growth label did not appear during pointer capture")
			}
			tester.Move(230, 120)
			tester.Release(215, 110)
			if len(saved) != 1 || saved[0] != (mygo.Point{X: 15, Y: 10}) || taps != 0 {
				t.Fatal("appearing growth label interrupted an active drag", saved, taps)
			}
			tester.Press(215, 110)
			tester.Move(225, 120)
			tester.Advance(2 * time.Second)
			later, ok := tester.Find("+12")
			if !ok || mode.reduce && initial.Y != later.Y || !mode.reduce && later.Y >= initial.Y {
				t.Fatal("growth position did not obey the motion preference", initial, later)
			}
			tester.Advance(time.Second)
			tester.Move(220, 115)
			tester.Release(215, 110)
			if tester.HasText("+12") || len(saved) != 2 || saved[1] != (mygo.Point{X: 30, Y: 25}) || taps != 0 {
				t.Fatal("growth hit-testing or expiry interrupted a drag started beneath the label", saved, taps)
			}
		})
	}
}
