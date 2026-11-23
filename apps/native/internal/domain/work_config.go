package domain

// FarmCrops preserves the crop catalog values and bilingual copy used by the
// previous desktop implementation.
var farmCrops = []FarmCropConfig{
	{CropKey: "wheat", SeedItemKey: "wheat-seed", NameZh: "小麦", NameEn: "Wheat", DescriptionZh: "新手作物，成熟很快，适合熟悉播种和收获。", DescriptionEn: "A starter crop with a short loop for learning the farm.", DurationSeconds: 300, WaterRequired: 1, PrimaryRewardItemKey: "wheat-harvest-food", PrimaryRewardQuantity: 1, LPMin: 1, LPMax: 3},
	{CropKey: "carrot", SeedItemKey: "carrot-seed", NameZh: "胡萝卜", NameEn: "Carrot", DescriptionZh: "稳定作物，主要产出日常投喂食物。", DescriptionEn: "A stable crop that mostly yields everyday feeding items.", DurationSeconds: 900, WaterRequired: 2, PrimaryRewardItemKey: "carrot-harvest-food", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("cupcake-food"), BonusChancePercent: 25, LPMin: 3, LPMax: 6},
	{CropKey: "tomato", SeedItemKey: "tomato-seed", NameZh: "番茄", NameEn: "Tomato", DescriptionZh: "中周期作物，有机会额外收获互动道具。", DescriptionEn: "A mid-cycle crop with a chance to grant an interaction tool.", DurationSeconds: 1800, WaterRequired: 2, PrimaryRewardItemKey: "tomato-harvest-food", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("energy-drink-tool"), BonusChancePercent: 20, LPMin: 6, LPMax: 10},
	{CropKey: "pumpkin", SeedItemKey: "pumpkin-seed", NameZh: "南瓜", NameEn: "Pumpkin", DescriptionZh: "高收益作物，成熟较慢，有机会产出惊喜礼盒。", DescriptionEn: "A slower high-yield crop with a chance to grant a gift box.", DurationSeconds: 3600, WaterRequired: 3, PrimaryRewardItemKey: "pumpkin-harvest-food", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("gift-box-tool"), BonusChancePercent: 15, LPMin: 10, LPMax: 18},
	{CropKey: "corn", SeedItemKey: "corn-seed", NameZh: "玉米", NameEn: "Corn", DescriptionZh: "中短周期作物，稳定产出能量补给。", DescriptionEn: "A mid-short cycle crop that yields reliable energy supplies.", DurationSeconds: 1200, WaterRequired: 2, PrimaryRewardItemKey: "corn-harvest-food", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("energy-drink-tool"), BonusChancePercent: 18, LPMin: 4, LPMax: 7},
	{CropKey: "strawberry", SeedItemKey: "strawberry-seed", NameZh: "草莓", NameEn: "Strawberry", DescriptionZh: "甜点作物，成熟后产出高级投喂食物。", DescriptionEn: "A dessert crop that yields premium feeding treats.", DurationSeconds: 1500, WaterRequired: 2, PrimaryRewardItemKey: "strawberry-harvest-food", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("pink-donut-food"), BonusChancePercent: 18, LPMin: 5, LPMax: 8},
	{CropKey: "blueberry", SeedItemKey: "blueberry-seed", NameZh: "蓝莓", NameEn: "Blueberry", DescriptionZh: "较长周期浆果作物，有更高概率额外收获甜点。", DescriptionEn: "A longer berry crop with a better chance of bonus sweets.", DurationSeconds: 2100, WaterRequired: 3, PrimaryRewardItemKey: "blueberry-harvest-food", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("rainbow-crystal-tool"), BonusChancePercent: 24, LPMin: 7, LPMax: 11},
	{CropKey: "potato", SeedItemKey: "potato-seed", NameZh: "土豆", NameEn: "Potato", DescriptionZh: "便宜稳定的基础作物，适合持续种植。", DescriptionEn: "A cheap and steady base crop for continuous planting.", DurationSeconds: 720, WaterRequired: 1, PrimaryRewardItemKey: "potato-harvest-food", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("sandwich-food"), BonusChancePercent: 12, LPMin: 2, LPMax: 4},
}

var workGames = []WorkGameDefinition{
	{Key: "mine", NameZh: "矿场挖矿", NameEn: "Mine", DescriptionZh: "选择矿脉、照看矿点，收获工具、稀有道具和 LP。", DescriptionEn: "Pick a vein, care for the mine, and earn tools, rare items, and LP.", Outputs: []string{"tool", "rare_tool", "lp", "gift_box"}, Jobs: []WorkGameJobConfig{
		{JobKey: "shallow-vein", SlotIndex: 1, NameZh: "浅层矿脉", NameEn: "Shallow Vein", DescriptionZh: "短周期矿点，适合快速收获工具和少量 LP。", DescriptionEn: "A short mining loop for tools and a little LP.", DurationSeconds: 600, CareRequired: 1, PrimaryRewardItemKey: "energy-capsule-tool", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("rune-stone-tool"), BonusChancePercent: 12, LPMin: 3, LPMax: 6, CareActionsZh: []string{"敲击矿点", "清理碎石"}, CareActionsEn: []string{"Tap ore", "Clear rocks"}},
		{JobKey: "deep-vein", SlotIndex: 2, NameZh: "深层矿脉", NameEn: "Deep Vein", DescriptionZh: "中周期矿点，需要加固支架，收益更稳定。", DescriptionEn: "A deeper mining loop with steadier rewards.", DurationSeconds: 1500, CareRequired: 2, PrimaryRewardItemKey: "rune-stone-tool", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("rainbow-crystal-tool"), BonusChancePercent: 16, LPMin: 8, LPMax: 14, CareActionsZh: []string{"清理碎石", "加固支架", "装载矿车"}, CareActionsEn: []string{"Clear rocks", "Brace beams", "Load cart"}},
		{JobKey: "glowing-vein", SlotIndex: 3, NameZh: "闪光富矿", NameEn: "Glowing Vein", DescriptionZh: "长周期富矿，低概率带出惊喜礼盒。", DescriptionEn: "A long rich-vein loop with a small gift-box chance.", DurationSeconds: 2700, CareRequired: 3, PrimaryRewardItemKey: "rainbow-crystal-tool", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("gift-box-tool"), BonusChancePercent: 14, LPMin: 15, LPMax: 24, CareActionsZh: []string{"定位富矿", "加固支架", "装载矿车"}, CareActionsEn: []string{"Mark vein", "Brace beams", "Load cart"}},
	}},
	{Key: "factory", NameZh: "工厂打螺丝", NameEn: "Factory", DescriptionZh: "接装配订单、处理生产线，稳定获得 LP 和工具。", DescriptionEn: "Run assembly orders for steady LP and tool rewards.", Outputs: []string{"lp", "tool", "energy", "badge"}, Jobs: []WorkGameJobConfig{
		{JobKey: "basic-assembly", SlotIndex: 1, NameZh: "基础装配", NameEn: "Basic Assembly", DescriptionZh: "短周期工位，稳定产出 LP 和基础工具。", DescriptionEn: "A short station with stable LP and tool rewards.", DurationSeconds: 480, CareRequired: 1, PrimaryRewardItemKey: "energy-capsule-tool", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("star-coin-tool"), BonusChancePercent: 10, LPMin: 5, LPMax: 9, CareActionsZh: []string{"按序拧螺丝", "打包出货"}, CareActionsEn: []string{"Tighten screws", "Pack order"}},
		{JobKey: "rush-order", SlotIndex: 2, NameZh: "加急订单", NameEn: "Rush Order", DescriptionZh: "中周期订单，照看传送带后获得更高 LP。", DescriptionEn: "A mid-cycle order with higher LP after line care.", DurationSeconds: 1080, CareRequired: 2, PrimaryRewardItemKey: "energy-drink-tool", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("stopwatch-tool"), BonusChancePercent: 18, LPMin: 10, LPMax: 16, CareActionsZh: []string{"清理卡料", "质检盖章", "打包出货"}, CareActionsEn: []string{"Clear jam", "Inspect stamp", "Pack order"}},
		{JobKey: "precision-check", SlotIndex: 3, NameZh: "精密质检", NameEn: "Precision Check", DescriptionZh: "长周期质检岗位，适合收获秒表和稀有工具。", DescriptionEn: "A longer inspection station for stopwatch and rare tools.", DurationSeconds: 2100, CareRequired: 3, PrimaryRewardItemKey: "stopwatch-tool", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("golden-bell-tool"), BonusChancePercent: 14, LPMin: 16, LPMax: 25, CareActionsZh: []string{"按序拧螺丝", "质检盖章", "整理工位"}, CareActionsEn: []string{"Tighten screws", "Inspect stamp", "Reset station"}},
	}},
	{Key: "convenience-store", NameZh: "便利店值班", NameEn: "Convenience Store", DescriptionZh: "完成收银、补货和清洁，获得食物、礼盒和日常 LP。", DescriptionEn: "Handle checkout, restocking, and cleaning for food, gifts, and LP.", Outputs: []string{"food", "gift_box", "lp", "tool"}, Jobs: []WorkGameJobConfig{
		{JobKey: "day-shift", SlotIndex: 1, NameZh: "白班", NameEn: "Day Shift", DescriptionZh: "短周期值班，主要获得食物和少量 LP。", DescriptionEn: "A short store shift for food and a little LP.", DurationSeconds: 720, CareRequired: 1, PrimaryRewardItemKey: "sandwich-food", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("bubble-tea-food"), BonusChancePercent: 18, LPMin: 4, LPMax: 8, CareActionsZh: []string{"收银结账", "补齐货架"}, CareActionsEn: []string{"Checkout", "Restock shelf"}},
		{JobKey: "evening-shift", SlotIndex: 2, NameZh: "晚班", NameEn: "Evening Shift", DescriptionZh: "中周期值班，兼顾补货、加热便当和顾客需求。", DescriptionEn: "A mid-cycle shift with restocking and customer care.", DurationSeconds: 1440, CareRequired: 2, PrimaryRewardItemKey: "bento-box-food", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("gift-box-tool"), BonusChancePercent: 10, LPMin: 9, LPMax: 15, CareActionsZh: []string{"加热便当", "补齐货架", "收银结账"}, CareActionsEn: []string{"Heat meal", "Restock shelf", "Checkout"}},
		{JobKey: "night-shift", SlotIndex: 3, NameZh: "夜班", NameEn: "Night Shift", DescriptionZh: "长周期夜班，低压力但有更好的礼盒和道具概率。", DescriptionEn: "A longer night shift with better gift and tool chances.", DurationSeconds: 2400, CareRequired: 3, PrimaryRewardItemKey: "fruit-tart-food", PrimaryRewardQuantity: 1, BonusRewardItemKey: strptr("magic-potion-tool"), BonusChancePercent: 16, LPMin: 14, LPMax: 22, CareActionsZh: []string{"清洁门口", "处理顾客需求", "整理货架"}, CareActionsEn: []string{"Clean entrance", "Help customer", "Tidy shelf"}},
	}},
}

type WorkGameDefinition struct {
	Key, NameZh, NameEn, DescriptionZh, DescriptionEn string
	Outputs                                           []string
	Jobs                                              []WorkGameJobConfig
}

func FarmCropCatalog() []FarmCropConfig {
	values := append([]FarmCropConfig(nil), farmCrops...)
	for i := range values {
		if values[i].BonusRewardItemKey != nil {
			value := *values[i].BonusRewardItemKey
			values[i].BonusRewardItemKey = &value
		}
	}
	return values
}

func WorkGameCatalog() []WorkGameDefinition {
	values := append([]WorkGameDefinition(nil), workGames...)
	for i := range values {
		values[i].Outputs = append([]string(nil), values[i].Outputs...)
		values[i].Jobs = append([]WorkGameJobConfig(nil), values[i].Jobs...)
		for j := range values[i].Jobs {
			values[i].Jobs[j].GameKey = values[i].Key
			values[i].Jobs[j].CareActionsZh = append([]string(nil), values[i].Jobs[j].CareActionsZh...)
			values[i].Jobs[j].CareActionsEn = append([]string(nil), values[i].Jobs[j].CareActionsEn...)
			if values[i].Jobs[j].BonusRewardItemKey != nil {
				value := *values[i].Jobs[j].BonusRewardItemKey
				values[i].Jobs[j].BonusRewardItemKey = &value
			}
		}
	}
	return values
}

func strptr(value string) *string { return &value }
