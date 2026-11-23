package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
	"time"
)

type PetRepository interface {
	EnsurePet(context.Context, string) error
	PetProfile(context.Context, string) (domain.PetProfile, error)
	PetStore(context.Context, string) (domain.PetStoreState, error)
	PetSettings(context.Context, string) (domain.PetSettings, error)
	SavePetSettings(context.Context, domain.PetSettings, string) (domain.PetSettings, error)
	SavePetPosition(context.Context, int, int, string) error
	SavePetName(context.Context, string, string) (domain.PetProfile, error)
	PetEvents(context.Context, int) ([]domain.PetEventLedgerEntry, error)
	PetCosmetics(context.Context) ([]domain.PetCosmeticUnlock, error)
	PurchasePet(context.Context, string, int64, string) (domain.PetStoreState, error)
	EquipPetItem(context.Context, string, string) (domain.PetStoreState, error)
	UnequipPetSlot(context.Context, string, string) (domain.PetStoreState, error)
	UsePetItem(context.Context, string, int64, string) (domain.PetStoreState, error)
	OpenPetGiftBox(context.Context, string) (domain.PetGiftBoxOpenResult, error)
	PetCheckIn(context.Context, string) (domain.PetDailyCheckInState, error)
	PetClaimCheckIn(context.Context, string) (domain.PetDailyCheckInClaimResult, error)
	PetMakeupCheckIn(context.Context, string) (domain.PetDailyCheckInMakeupResult, error)
	PetBlindBox(context.Context, string) (domain.PetBlindBoxState, error)
	PetDrawBlindBox(context.Context, string) (domain.PetBlindBoxDrawResult, error)
	RedeemKey(context.Context, string, string) (domain.PetRedeemKeyResult, error)
	PetRedemptions(context.Context, int) ([]domain.PetRedeemKeyRedemption, error)
	PetGrowth(context.Context, string, string, *string, string) (domain.PetProfile, error)
}

type Pet struct {
	repository PetRepository
	now        func() string
}

func NewPet(repository PetRepository) *Pet     { return &Pet{repository: repository, now: petNow} }
func petNow() string                           { return domain.PersistedUTC(time.Now()) }
func (s *Pet) Start(ctx context.Context) error { return s.repository.EnsurePet(ctx, s.now()) }
func (s *Pet) Profile(ctx context.Context) (domain.PetProfile, error) {
	return s.repository.PetProfile(ctx, s.now())
}
func (s *Pet) Store(ctx context.Context) (domain.PetStoreState, error) {
	return s.repository.PetStore(ctx, s.now())
}
func (s *Pet) Settings(ctx context.Context) (domain.PetSettings, error) {
	return s.repository.PetSettings(ctx, s.now())
}
func (s *Pet) SaveSettings(ctx context.Context, v domain.PetSettings) (domain.PetSettings, error) {
	return s.repository.SavePetSettings(ctx, v, s.now())
}
func (s *Pet) SavePosition(ctx context.Context, x, y int) error {
	return s.repository.SavePetPosition(ctx, x, y, s.now())
}
func (s *Pet) SaveName(ctx context.Context, name string) (domain.PetProfile, error) {
	return s.repository.SavePetName(ctx, name, s.now())
}
func (s *Pet) Events(ctx context.Context, limit int) ([]domain.PetEventLedgerEntry, error) {
	return s.repository.PetEvents(ctx, limit)
}
func (s *Pet) Cosmetics(ctx context.Context) ([]domain.PetCosmeticUnlock, error) {
	return s.repository.PetCosmetics(ctx)
}
func (s *Pet) Purchase(ctx context.Context, key string, qty int64) (domain.PetStoreState, error) {
	return s.repository.PurchasePet(ctx, key, qty, s.now())
}
func (s *Pet) Equip(ctx context.Context, key string) (domain.PetStoreState, error) {
	return s.repository.EquipPetItem(ctx, key, s.now())
}
func (s *Pet) Unequip(ctx context.Context, slot string) (domain.PetStoreState, error) {
	return s.repository.UnequipPetSlot(ctx, slot, s.now())
}
func (s *Pet) Use(ctx context.Context, key string, qty int64) (domain.PetStoreState, error) {
	return s.repository.UsePetItem(ctx, key, qty, s.now())
}
func (s *Pet) GiftBox(ctx context.Context) (domain.PetGiftBoxOpenResult, error) {
	return s.repository.OpenPetGiftBox(ctx, s.now())
}
func (s *Pet) CheckIn(ctx context.Context) (domain.PetDailyCheckInState, error) {
	return s.repository.PetCheckIn(ctx, s.now())
}
func (s *Pet) ClaimCheckIn(ctx context.Context) (domain.PetDailyCheckInClaimResult, error) {
	return s.repository.PetClaimCheckIn(ctx, s.now())
}
func (s *Pet) MakeupCheckIn(ctx context.Context) (domain.PetDailyCheckInMakeupResult, error) {
	return s.repository.PetMakeupCheckIn(ctx, s.now())
}
func (s *Pet) BlindBox(ctx context.Context) (domain.PetBlindBoxState, error) {
	return s.repository.PetBlindBox(ctx, s.now())
}
func (s *Pet) DrawBlindBox(ctx context.Context) (domain.PetBlindBoxDrawResult, error) {
	return s.repository.PetDrawBlindBox(ctx, s.now())
}
func (s *Pet) Redeem(ctx context.Context, key string) (domain.PetRedeemKeyResult, error) {
	return s.repository.RedeemKey(ctx, key, s.now())
}
func (s *Pet) Redemptions(ctx context.Context, limit int) ([]domain.PetRedeemKeyRedemption, error) {
	return s.repository.PetRedemptions(ctx, limit)
}
func (s *Pet) Workflow(ctx context.Context, source, key string) (domain.PetProfile, error) {
	value := key
	return s.repository.PetGrowth(ctx, "workflow", source, &value, s.now())
}
func (s *Pet) Interact(ctx context.Context, action string) (domain.PetProfile, error) {
	value := action
	return s.repository.PetGrowth(ctx, "interaction", action, &value, s.now())
}
