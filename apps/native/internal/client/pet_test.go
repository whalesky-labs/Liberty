package client

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type snapshotPet struct {
	PetService
	store       domain.PetStoreState
	readError   error
	actionError error
	redeems     int
	claims      int
}

func (service *snapshotPet) Store(context.Context) (domain.PetStoreState, error) {
	return service.store, service.readError
}
func (*snapshotPet) Settings(context.Context) (domain.PetSettings, error) {
	return domain.PetSettings{}, nil
}
func (*snapshotPet) Events(context.Context, int) ([]domain.PetEventLedgerEntry, error) {
	return nil, nil
}
func (*snapshotPet) Cosmetics(context.Context) ([]domain.PetCosmeticUnlock, error) {
	return nil, nil
}
func (*snapshotPet) CheckIn(context.Context) (domain.PetDailyCheckInState, error) {
	return domain.PetDailyCheckInState{}, nil
}
func (*snapshotPet) BlindBox(context.Context) (domain.PetBlindBoxState, error) {
	return domain.PetBlindBoxState{}, nil
}
func (*snapshotPet) Redemptions(context.Context, int) ([]domain.PetRedeemKeyRedemption, error) {
	return nil, nil
}
func (service *snapshotPet) Purchase(context.Context, string, int64) (domain.PetStoreState, error) {
	return service.store, service.actionError
}
func (service *snapshotPet) Redeem(context.Context, string) (domain.PetRedeemKeyResult, error) {
	service.redeems++
	return domain.PetRedeemKeyResult{State: service.store}, service.actionError
}
func (service *snapshotPet) ClaimCheckIn(context.Context) (domain.PetDailyCheckInClaimResult, error) {
	service.claims++
	return domain.PetDailyCheckInClaimResult{State: domain.PetDailyCheckInState{CheckedInToday: true, StoreState: service.store}}, service.actionError
}

func TestPetRefreshRecoversFromInitialAndSubsequentReadFailures(t *testing.T) {
	service := &snapshotPet{readError: errors.New("synthetic read failure")}
	callbacks := make(chan func(), 4)
	controller := NewPet(service, func(callback func()) { callbacks <- callback }, func() {})
	defer controller.Close()
	controller.Refresh()
	if !controller.Loading || !controller.Busy {
		t.Fatal("initial read has no loading state")
	}
	receive(t, callbacks)()
	if controller.Error == nil || controller.Loading || controller.Busy || controller.Store != nil {
		t.Fatal("failed read was represented as loading or empty data")
	}
	service.readError = nil
	controller.Refresh()
	receive(t, callbacks)()
	if controller.Error != nil || controller.Store == nil {
		t.Fatal("successful retry retained the old error")
	}
	service.readError = errors.New("synthetic refresh failure")
	controller.Refresh()
	receive(t, callbacks)()
	if controller.Error == nil || controller.Store == nil {
		t.Fatal("refresh failure lost the previous snapshot")
	}
	service.readError = nil
	controller.Refresh()
	receive(t, callbacks)()
	if controller.Error != nil {
		t.Fatal("successful background refresh retained the old error")
	}
}

func TestPetActionsRetainCommittedStateAndClearPreviousRedemptionResult(t *testing.T) {
	service := &snapshotPet{store: domain.PetStoreState{Wallet: domain.PetWallet{Balance: 72}}, readError: errors.New("synthetic follow-up failure")}
	callbacks := make(chan func(), 4)
	controller := NewPet(service, func(callback func()) { callbacks <- callback }, func() {})
	defer controller.Close()
	controller.Purchase("synthetic-food", 3)
	if controller.PendingAction != "purchase" || !controller.Busy {
		t.Fatal("purchase has no specific pending state")
	}
	receive(t, callbacks)()
	if controller.ActionRevision != 1 || controller.Store == nil || controller.Store.Wallet.Balance != 72 || controller.Error == nil || !controller.ErrorIsRefresh || controller.PendingAction != "" {
		t.Fatal("committed purchase was lost on a failed follow-up read")
	}
	controller.LastRedeem = &domain.PetRedeemKeyResult{}
	service.actionError = domain.ErrPetRedemption
	controller.Redeem("synthetic-invalid-key")
	if controller.LastRedeem != nil || controller.PendingAction != "redeem" {
		t.Fatal("previous redemption result remained visible during another attempt")
	}
	receive(t, callbacks)()
	if !errors.Is(controller.Error, domain.ErrPetRedemption) || controller.ErrorIsRefresh || controller.LastRedeem != nil || controller.ActionRevision != 1 || controller.PendingAction != "" {
		t.Fatal("failed redemption published success or lost its error")
	}
}

func TestPetRefreshPreservesActionFailureUntilAnotherExplicitAction(t *testing.T) {
	service := &snapshotPet{actionError: domain.ErrPetRedemption}
	callbacks := make(chan func(), 4)
	controller := NewPet(service, func(callback func()) { callbacks <- callback }, func() {})
	defer controller.Close()
	controller.Redeem("synthetic-invalid-key")
	receive(t, callbacks)()
	for _, readErr := range []error{nil, errors.New("synthetic refresh failure"), nil} {
		service.readError = readErr
		controller.Refresh()
		receive(t, callbacks)()
		if !errors.Is(controller.Error, domain.ErrPetRedemption) || controller.ErrorIsRefresh || controller.ActionRevision != 0 || service.redeems != 1 {
			t.Fatal("refresh lost or reclassified an operation error, or repeated its write")
		}
	}
	service.actionError = nil
	controller.Redeem("synthetic-new-key")
	if controller.Error != nil || !controller.Busy {
		t.Fatal("new explicit attempt did not clear the previous failure")
	}
	receive(t, callbacks)()
	if controller.Error != nil || controller.LastRedeem == nil || controller.ActionRevision != 1 || service.redeems != 2 {
		t.Fatal("new successful attempt did not replace the failed action")
	}
}

func TestPetAcceptedWritesWithReadFailureOnlyRefresh(t *testing.T) {
	for _, action := range []string{"redeem", "check-in"} {
		t.Run(action, func(t *testing.T) {
			service := &snapshotPet{readError: errors.New("synthetic follow-up failure")}
			callbacks := make(chan func(), 4)
			controller := NewPet(service, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			if action == "redeem" {
				controller.Redeem("synthetic-key")
			} else {
				controller.CheckInNow()
			}
			receive(t, callbacks)()
			if controller.ActionRevision != 1 || controller.Error == nil || !controller.ErrorIsRefresh || controller.LastAction != action || controller.Store == nil {
				t.Fatal("committed operation was confused with a failed write")
			}
			service.readError = nil
			controller.Refresh()
			receive(t, callbacks)()
			if controller.Error != nil || controller.ErrorIsRefresh || controller.ActionRevision != 1 || service.redeems+service.claims != 1 {
				t.Fatal("read recovery repeated the accepted write or retained its read error")
			}
		})
	}
}

type appearancePet struct {
	PetService
	records []string
	fail    bool
}

func TestCloseDuringBlindBoxAnimationCancelsDraw(t *testing.T) {
	dispatched := make(chan func(), 1)
	controller := NewPet(&appearancePet{}, func(callback func()) { dispatched <- callback }, func() {})
	controller.LastDraw = &domain.PetBlindBoxDrawResult{Prize: domain.PetStoreCatalogItem{ItemKey: "previous-prize"}}
	controller.Draw()
	if !controller.Busy || controller.LastDraw != nil {
		t.Fatal("new draw did not reserve the action and clear the previous result")
	}
	controller.Close()
	select {
	case callback := <-dispatched:
		callback()
	case <-time.After(2 * time.Second):
		t.Fatal("closing did not interrupt the draw timer")
	}
	if controller.LastDraw != nil || controller.ActionRevision != 0 {
		t.Fatal("cancelled draw published a result")
	}
}

func TestPetAcceptedActionClearsPriorRewardFeedbackButBusyInputDoesNot(t *testing.T) {
	dispatched := make(chan func(), 2)
	controller := NewPet(&appearancePet{}, func(callback func()) { dispatched <- callback }, func() {})
	defer controller.Close()
	controller.LastRedeem = &domain.PetRedeemKeyResult{}
	controller.LastDraw = &domain.PetBlindBoxDrawResult{}
	controller.LastGift = &domain.PetGiftBoxOpenResult{}
	controller.LastClaim = &domain.PetDailyCheckInClaimResult{}
	controller.LastMakeup = &domain.PetDailyCheckInMakeupResult{}

	controller.Busy = true
	controller.query("ignored", func(context.Context) (func(), error) { return nil, nil })
	if controller.LastRedeem == nil || controller.LastDraw == nil || controller.LastGift == nil || controller.LastClaim == nil || controller.LastMakeup == nil {
		t.Fatal("ignored busy input cleared previously published action results")
	}

	controller.Busy = false
	controller.query("accepted", func(context.Context) (func(), error) { return nil, nil })
	if controller.LastRedeem != nil || controller.LastDraw != nil || controller.LastGift != nil || controller.LastClaim != nil || controller.LastMakeup != nil {
		t.Fatal("accepted action retained an unrelated previous reward result")
	}
	controller.Close()
	receive(t, dispatched)()
}

func (service *appearancePet) Workflow(_ context.Context, event, date string) (domain.PetProfile, error) {
	service.records = append(service.records, event+":"+date)
	if service.fail {
		return domain.PetProfile{}, domain.ErrPet
	}
	return domain.PetProfile{}, nil
}

func (*appearancePet) Store(context.Context) (domain.PetStoreState, error) {
	return domain.PetStoreState{}, domain.ErrPet
}

func TestEffectiveDarkThemeRetriesWithoutDuplicatingSameUTCDate(t *testing.T) {
	service := &appearancePet{fail: true}
	dispatched := make(chan func(), 1)
	controller := NewPet(service, func(callback func()) { dispatched <- callback }, func() {})
	defer controller.Close()
	finish := func() {
		t.Helper()
		select {
		case callback := <-dispatched:
			callback()
		case <-time.After(2 * time.Second):
			t.Fatal("missing refresh")
		}
	}
	now := time.Date(2026, 11, 19, 7, 59, 59, 0, time.FixedZone("UTC+8", 8*3600))
	controller.ObserveAppearance(false, now)
	controller.Busy = true
	controller.ObserveAppearance(true, now)
	controller.Busy = false
	controller.Refresh()
	finish()
	service.fail = false
	controller.Refresh()
	finish()
	controller.ObserveAppearance(true, now)
	controller.Refresh()
	finish()
	controller.ObserveAppearance(true, now.Add(time.Second))
	finish()
	if !slices.Equal(service.records, []string{"dark_theme_used:2026-11-18", "dark_theme_used:2026-11-18", "dark_theme_used:2026-11-19"}) {
		t.Fatal(service.records)
	}
}
