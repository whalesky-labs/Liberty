package domain

type FarmCropConfig struct {
	CropKey               string  `json:"cropKey"`
	SeedItemKey           string  `json:"seedItemKey"`
	NameZh                string  `json:"nameZh"`
	NameEn                string  `json:"nameEn"`
	DescriptionZh         string  `json:"descriptionZh"`
	DescriptionEn         string  `json:"descriptionEn"`
	DurationSeconds       int64   `json:"durationSeconds"`
	WaterRequired         int64   `json:"waterRequired"`
	PrimaryRewardItemKey  string  `json:"primaryRewardItemKey"`
	PrimaryRewardQuantity int64   `json:"primaryRewardQuantity"`
	BonusRewardItemKey    *string `json:"bonusRewardItemKey"`
	BonusChancePercent    int64   `json:"bonusChancePercent"`
	LPMin                 int64   `json:"lpMin"`
	LPMax                 int64   `json:"lpMax"`
}

type FarmPlot struct {
	ID               string          `json:"id"`
	PlotIndex        int64           `json:"plotIndex"`
	CropKey          string          `json:"cropKey"`
	Status           string          `json:"status"`
	StageIndex       int64           `json:"stageIndex"`
	PlantedAt        *string         `json:"plantedAt"`
	LastWateredAt    *string         `json:"lastWateredAt"`
	NextCareAt       *string         `json:"nextCareAt"`
	MatureAt         *string         `json:"matureAt"`
	UpdatedAt        string          `json:"updatedAt"`
	Crop             *FarmCropConfig `json:"crop"`
	ProgressRatio    float64         `json:"progressRatio"`
	RemainingSeconds int64           `json:"remainingSeconds"`
}

type FarmRewardItem struct {
	ItemKey    string `json:"itemKey"`
	Quantity   int64  `json:"quantity"`
	RewardType string `json:"rewardType"`
}

type FarmHarvestLedgerEntry struct {
	ID        string           `json:"id"`
	PlotID    string           `json:"plotId"`
	CropKey   string           `json:"cropKey"`
	Rewards   []FarmRewardItem `json:"rewards"`
	LPReward  int64            `json:"lpReward"`
	CreatedAt string           `json:"createdAt"`
}

type FarmHarvestResult struct {
	State   FarmState              `json:"state"`
	Harvest FarmHarvestLedgerEntry `json:"harvest"`
}

type FarmState struct {
	Plots     []FarmPlot               `json:"plots"`
	Crops     []FarmCropConfig         `json:"crops"`
	Harvests  []FarmHarvestLedgerEntry `json:"harvests"`
	MapStatus string                   `json:"mapStatus"`
	UpdatedAt string                   `json:"updatedAt"`
}

type WorkMapSummary struct {
	Status         string `json:"status"`
	ActivePlots    int64  `json:"activePlots"`
	NeedsCarePlots int64  `json:"needsCarePlots"`
	MaturePlots    int64  `json:"maturePlots"`
}

type WorkMap struct {
	ID            string         `json:"id"`
	NameZh        string         `json:"nameZh"`
	NameEn        string         `json:"nameEn"`
	DescriptionZh string         `json:"descriptionZh"`
	DescriptionEn string         `json:"descriptionEn"`
	Category      string         `json:"category"`
	Status        string         `json:"status"`
	Route         string         `json:"route"`
	Outputs       []string       `json:"outputs"`
	Enabled       bool           `json:"enabled"`
	Summary       WorkMapSummary `json:"summary"`
}

type WorkMarketState struct {
	Maps      []WorkMap `json:"maps"`
	UpdatedAt string    `json:"updatedAt"`
}

type WorkGameJobConfig struct {
	GameKey               string   `json:"gameKey"`
	JobKey                string   `json:"jobKey"`
	SlotIndex             int64    `json:"slotIndex"`
	NameZh                string   `json:"nameZh"`
	NameEn                string   `json:"nameEn"`
	DescriptionZh         string   `json:"descriptionZh"`
	DescriptionEn         string   `json:"descriptionEn"`
	DurationSeconds       int64    `json:"durationSeconds"`
	CareRequired          int64    `json:"careRequired"`
	PrimaryRewardItemKey  string   `json:"primaryRewardItemKey"`
	PrimaryRewardQuantity int64    `json:"primaryRewardQuantity"`
	BonusRewardItemKey    *string  `json:"bonusRewardItemKey"`
	BonusChancePercent    int64    `json:"bonusChancePercent"`
	LPMin                 int64    `json:"lpMin"`
	LPMax                 int64    `json:"lpMax"`
	CareActionsZh         []string `json:"careActionsZh"`
	CareActionsEn         []string `json:"careActionsEn"`
}

type WorkGameTask struct {
	ID               string             `json:"id"`
	GameKey          string             `json:"gameKey"`
	SlotIndex        int64              `json:"slotIndex"`
	JobKey           string             `json:"jobKey"`
	Status           string             `json:"status"`
	StageIndex       int64              `json:"stageIndex"`
	StartedAt        *string            `json:"startedAt"`
	LastCaredAt      *string            `json:"lastCaredAt"`
	NextCareAt       *string            `json:"nextCareAt"`
	ClaimableAt      *string            `json:"claimableAt"`
	UpdatedAt        string             `json:"updatedAt"`
	Job              *WorkGameJobConfig `json:"job"`
	ProgressRatio    float64            `json:"progressRatio"`
	RemainingSeconds int64              `json:"remainingSeconds"`
}

type WorkGameRewardItem = FarmRewardItem

type WorkGameRewardLedgerEntry struct {
	ID        string               `json:"id"`
	GameKey   string               `json:"gameKey"`
	TaskID    string               `json:"taskId"`
	JobKey    string               `json:"jobKey"`
	Rewards   []WorkGameRewardItem `json:"rewards"`
	LPReward  int64                `json:"lpReward"`
	CreatedAt string               `json:"createdAt"`
}

type WorkGameState struct {
	GameKey       string                      `json:"gameKey"`
	NameZh        string                      `json:"nameZh"`
	NameEn        string                      `json:"nameEn"`
	DescriptionZh string                      `json:"descriptionZh"`
	DescriptionEn string                      `json:"descriptionEn"`
	MapStatus     string                      `json:"mapStatus"`
	Tasks         []WorkGameTask              `json:"tasks"`
	Jobs          []WorkGameJobConfig         `json:"jobs"`
	Rewards       []WorkGameRewardLedgerEntry `json:"rewards"`
	UpdatedAt     string                      `json:"updatedAt"`
}

type WorkGameClaimResult struct {
	State  WorkGameState             `json:"state"`
	Reward WorkGameRewardLedgerEntry `json:"reward"`
}
