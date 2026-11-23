package domain

type PetStageThreshold struct {
	Stage   string `json:"stage"`
	LabelZh string `json:"labelZh"`
	LabelEn string `json:"labelEn"`
	Level   int64  `json:"level"`
}

type PetLevelSnapshot struct {
	Level               int64   `json:"level"`
	CurrentLevelExp     int64   `json:"currentLevelExp"`
	NextLevelRequired   int64   `json:"nextLevelRequired"`
	TotalExperience     int64   `json:"totalExperience"`
	CurrentStage        string  `json:"currentStage"`
	CurrentStageLabelZh string  `json:"currentStageLabelZh"`
	CurrentStageLabelEn string  `json:"currentStageLabelEn"`
	NextStage           *string `json:"nextStage"`
	NextStageLevel      *int64  `json:"nextStageLevel"`
	ProgressRatio       float64 `json:"progressRatio"`
	IsMaxLevel          bool    `json:"isMaxLevel"`
}

type PetProfile struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Level         int64            `json:"level"`
	Experience    int64            `json:"experience"`
	Stage         string           `json:"stage"`
	LevelSnapshot PetLevelSnapshot `json:"levelSnapshot"`
	CurrentMood   string           `json:"currentMood"`
	CreatedAt     string           `json:"createdAt"`
	UpdatedAt     string           `json:"updatedAt"`
}

type PetSettings struct {
	PetID            string   `json:"petId"`
	DesktopEnabled   bool     `json:"desktopEnabled"`
	AlwaysOnTop      bool     `json:"alwaysOnTop"`
	Muted            bool     `json:"muted"`
	FocusModeEnabled bool     `json:"focusModeEnabled"`
	ProactiveLevel   int64    `json:"proactiveLevel"`
	LastWindowX      *float64 `json:"lastWindowX"`
	LastWindowY      *float64 `json:"lastWindowY"`
	UpdatedAt        string   `json:"updatedAt"`
}

type PetCosmeticUnlock struct {
	ID           string `json:"id"`
	PetID        string `json:"petId"`
	CosmeticType string `json:"cosmeticType"`
	CosmeticKey  string `json:"cosmeticKey"`
	UnlockedAt   string `json:"unlockedAt"`
	Equipped     bool   `json:"equipped"`
}

type PetEventLedgerEntry struct {
	ID          string  `json:"id"`
	PetID       string  `json:"petId"`
	EventType   string  `json:"eventType"`
	EventSource string  `json:"eventSource"`
	EventValue  int64   `json:"eventValue"`
	EventTime   string  `json:"eventTime"`
	Metadata    *string `json:"metadata"`
}

type PetWallet struct {
	PetID          string `json:"petId"`
	CurrencyKey    string `json:"currencyKey"`
	Balance        int64  `json:"balance"`
	LifetimeEarned int64  `json:"lifetimeEarned"`
	LifetimeSpent  int64  `json:"lifetimeSpent"`
	UpdatedAt      string `json:"updatedAt"`
}

type PetInventoryItem struct {
	ID          string `json:"id"`
	PetID       string `json:"petId"`
	ItemKey     string `json:"itemKey"`
	ItemType    string `json:"itemType"`
	Slot        string `json:"slot"`
	Quantity    int64  `json:"quantity"`
	Equipped    bool   `json:"equipped"`
	Source      string `json:"source"`
	PurchasedAt string `json:"purchasedAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type PetEconomyEntry struct {
	ID           string  `json:"id"`
	PetID        string  `json:"petId"`
	EntryType    string  `json:"entryType"`
	CurrencyKey  string  `json:"currencyKey"`
	Amount       int64   `json:"amount"`
	BalanceAfter int64   `json:"balanceAfter"`
	SourceType   string  `json:"sourceType"`
	SourceKey    string  `json:"sourceKey"`
	Metadata     *string `json:"metadata"`
	CreatedAt    string  `json:"createdAt"`
}

type PetMilestoneCounter struct {
	PetID        string `json:"petId"`
	CounterKey   string `json:"counterKey"`
	CounterValue int64  `json:"counterValue"`
	LastEventKey string `json:"lastEventKey"`
	UpdatedAt    string `json:"updatedAt"`
}

type PetStoreCatalogItem struct {
	ItemKey       string `json:"itemKey"`
	ItemType      string `json:"itemType"`
	Slot          string `json:"slot"`
	NameZh        string `json:"nameZh"`
	NameEn        string `json:"nameEn"`
	DescriptionZh string `json:"descriptionZh"`
	DescriptionEn string `json:"descriptionEn"`
	Rarity        string `json:"rarity"`
	PriceLP       int64  `json:"priceLp"`
	LevelGate     int64  `json:"levelGate"`
	StageGate     string `json:"stageGate"`
	MilestoneGate string `json:"milestoneGate"`
	AssetKey      string `json:"assetKey"`
	GrowthValue   int64  `json:"growthValue"`
	Enabled       bool   `json:"enabled"`
	SortOrder     int64  `json:"sortOrder"`
}

type PetStoreCatalogItemState struct {
	Item               PetStoreCatalogItem `json:"item"`
	Owned              bool                `json:"owned"`
	Equipped           bool                `json:"equipped"`
	Quantity           int64               `json:"quantity"`
	GrowthValue        int64               `json:"growthValue"`
	DailyFreeLimit     int64               `json:"dailyFreeLimit"`
	DailyFreeClaimed   int64               `json:"dailyFreeClaimed"`
	DailyFreeRemaining int64               `json:"dailyFreeRemaining"`
	Purchasable        bool                `json:"purchasable"`
	LockedReasonZh     string              `json:"lockedReasonZh"`
	LockedReasonEn     string              `json:"lockedReasonEn"`
	Status             string              `json:"status"`
}

type PetEquipmentState struct {
	CurrentPet *PetInventoryItem `json:"currentPet"`
	Accessory  *PetInventoryItem `json:"accessory"`
	Scene      *PetInventoryItem `json:"scene"`
	Badge      *PetInventoryItem `json:"badge"`
}

type PetStoreState struct {
	Profile   PetProfile                 `json:"profile"`
	Wallet    PetWallet                  `json:"wallet"`
	Catalog   []PetStoreCatalogItemState `json:"catalog"`
	Inventory []PetInventoryItem         `json:"inventory"`
	Equipment PetEquipmentState          `json:"equipment"`
	Counters  []PetMilestoneCounter      `json:"counters"`
	Economy   []PetEconomyEntry          `json:"economy"`
}

type PetBlindBoxDrawEntry struct {
	ID                      string `json:"id"`
	PetID                   string `json:"petId"`
	DrawDate                string `json:"drawDate"`
	ItemKey                 string `json:"itemKey"`
	ItemType                string `json:"itemType"`
	Quantity                int64  `json:"quantity"`
	DuplicateCompensationLP int64  `json:"duplicateCompensationLp"`
	CreatedAt               string `json:"createdAt"`
}

type PetBlindBoxPoolItem struct {
	Item                    PetStoreCatalogItem `json:"item"`
	Owned                   bool                `json:"owned"`
	Weight                  int64               `json:"weight"`
	DuplicateCompensationLP int64               `json:"duplicateCompensationLp"`
}

type PetBlindBoxState struct {
	DrawDate       string                 `json:"drawDate"`
	DailyLimit     int64                  `json:"dailyLimit"`
	UsedToday      int64                  `json:"usedToday"`
	RemainingToday int64                  `json:"remainingToday"`
	Pool           []PetBlindBoxPoolItem  `json:"pool"`
	History        []PetBlindBoxDrawEntry `json:"history"`
	StoreState     PetStoreState          `json:"storeState"`
}

type PetBlindBoxDrawResult struct {
	State     PetBlindBoxState     `json:"state"`
	Draw      PetBlindBoxDrawEntry `json:"draw"`
	Prize     PetStoreCatalogItem  `json:"prize"`
	Duplicate bool                 `json:"duplicate"`
}

type PetRewardItem struct {
	ItemKey                 string `json:"itemKey"`
	ItemType                string `json:"itemType"`
	Quantity                int64  `json:"quantity"`
	DuplicateCompensationLP int64  `json:"duplicateCompensationLp"`
}

type PetDailyCheckInEntry struct {
	ID          string          `json:"id"`
	PetID       string          `json:"petId"`
	CheckInDate string          `json:"checkInDate"`
	StreakCount int64           `json:"streakCount"`
	CycleDay    int64           `json:"cycleDay"`
	RewardLP    int64           `json:"rewardLp"`
	GrowthValue int64           `json:"growthValue"`
	RewardItems []PetRewardItem `json:"rewardItems"`
	CreatedAt   string          `json:"createdAt"`
}

type PetDailyCheckInRewardPreview struct {
	CycleDay    int64           `json:"cycleDay"`
	RewardLP    int64           `json:"rewardLp"`
	GrowthValue int64           `json:"growthValue"`
	Items       []PetRewardItem `json:"items"`
}

type PetDailyCheckInState struct {
	CheckInDate          string                         `json:"checkInDate"`
	CheckedInToday       bool                           `json:"checkedInToday"`
	CurrentStreak        int64                          `json:"currentStreak"`
	NextCycleDay         int64                          `json:"nextCycleDay"`
	CycleLength          int64                          `json:"cycleLength"`
	MissedDays           int64                          `json:"missedDays"`
	MakeupAvailable      bool                           `json:"makeupAvailable"`
	MakeupDate           *string                        `json:"makeupDate"`
	MakeupTicketItemKey  string                         `json:"makeupTicketItemKey"`
	MakeupTicketQuantity int64                          `json:"makeupTicketQuantity"`
	MakeupBlockedReason  *string                        `json:"makeupBlockedReason"`
	TodayReward          PetDailyCheckInRewardPreview   `json:"todayReward"`
	Rewards              []PetDailyCheckInRewardPreview `json:"rewards"`
	History              []PetDailyCheckInEntry         `json:"history"`
	StoreState           PetStoreState                  `json:"storeState"`
}

type PetDailyCheckInClaimResult struct {
	State     PetDailyCheckInState `json:"state"`
	Entry     PetDailyCheckInEntry `json:"entry"`
	Duplicate bool                 `json:"duplicate"`
}

type PetDailyCheckInMakeupResult struct {
	State               PetDailyCheckInState `json:"state"`
	Entry               PetDailyCheckInEntry `json:"entry"`
	TicketItemKey       string               `json:"ticketItemKey"`
	TicketQuantityAfter int64                `json:"ticketQuantityAfter"`
}

type PetGiftBoxOpenResult struct {
	State                   PetStoreState       `json:"state"`
	Prize                   PetStoreCatalogItem `json:"prize"`
	Duplicate               bool                `json:"duplicate"`
	DuplicateCompensationLP int64               `json:"duplicateCompensationLp"`
}

type PetRedeemKeyRewardItem struct {
	ItemKey  string `json:"itemKey"`
	Quantity int64  `json:"quantity"`
}

type PetRedeemKeyRewards struct {
	LP          int64                    `json:"lp"`
	GrowthValue int64                    `json:"growthValue"`
	Items       []PetRedeemKeyRewardItem `json:"items"`
}

type PetRedeemKeyRedemption struct {
	ID         string  `json:"id"`
	PetID      string  `json:"petId"`
	KeyHash    string  `json:"keyHash"`
	CodePrefix string  `json:"codePrefix"`
	CampaignID string  `json:"campaignId"`
	RewardJson string  `json:"rewardJson"`
	Status     string  `json:"status"`
	RedeemedAt string  `json:"redeemedAt"`
	Metadata   *string `json:"metadata"`
}

type PetRedeemKeyResult struct {
	State      PetStoreState          `json:"state"`
	Redemption PetRedeemKeyRedemption `json:"redemption"`
	Rewards    PetRedeemKeyRewards    `json:"rewards"`
	Duplicate  bool                   `json:"duplicate"`
}
