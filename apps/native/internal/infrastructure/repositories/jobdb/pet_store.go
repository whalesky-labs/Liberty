package jobdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"liberty.local/liberty/internal/domain"
	"math"
	"strings"
	"time"
)

func (s *Store) PetStore(ctx context.Context, now string) (domain.PetStoreState, error) {
	var out domain.PetStoreState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := ensurePet(ctx, tx, now); err != nil {
			return err
		}
		if err := ensurePetStore(ctx, tx, now); err != nil {
			return err
		}
		p, err := loadPetProfile(ctx, tx)
		if err != nil {
			return err
		}
		out, err = petStoreState(ctx, tx, p, now)
		return err
	})
	return out, err
}
func petStoreState(ctx context.Context, tx *sql.Tx, p domain.PetProfile, now string) (domain.PetStoreState, error) {
	w, err := petWallet(ctx, tx)
	if err != nil {
		return domain.PetStoreState{}, err
	}
	inv, err := petInventory(ctx, tx)
	if err != nil {
		return domain.PetStoreState{}, err
	}
	counters, err := petCounters(ctx, tx)
	if err != nil {
		return domain.PetStoreState{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,pet_id,entry_type,currency_key,amount,balance_after,source_type,source_key,metadata,created_at FROM pet_economy_ledger WHERE pet_id='default-pet' AND currency_key='lp' ORDER BY datetime(created_at) DESC,created_at DESC,rowid DESC LIMIT 20`)
	if err != nil {
		return domain.PetStoreState{}, ErrDatabase
	}
	economy := []domain.PetEconomyEntry{}
	for rows.Next() {
		var e domain.PetEconomyEntry
		if rows.Scan(&e.ID, &e.PetID, &e.EntryType, &e.CurrencyKey, &e.Amount, &e.BalanceAfter, &e.SourceType, &e.SourceKey, &e.Metadata, &e.CreatedAt) != nil {
			rows.Close()
			return domain.PetStoreState{}, ErrDatabase
		}
		economy = append(economy, e)
	}
	if rows.Err() != nil {
		rows.Close()
		return domain.PetStoreState{}, ErrDatabase
	}
	rows.Close()
	limits := int64(0)
	date := now
	if t, e := time.Parse(time.RFC3339Nano, now); e == nil {
		date = t.Local().Format("2006-01-02")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(free_claimed,0) FROM pet_store_daily_limits WHERE pet_id='default-pet' AND item_key=? AND limit_date=?`, domain.PetGiftBox, date).Scan(&limits); err != nil && err != sql.ErrNoRows {
		return domain.PetStoreState{}, ErrDatabase
	}
	states := make([]domain.PetStoreCatalogItemState, 0, 77)
	for _, item := range domain.PetCatalog() {
		var owned, equipped bool
		var qty int64
		for _, i := range inv {
			if i.ItemKey == item.ItemKey {
				owned, equipped, qty = true, i.Equipped, i.Quantity
				break
			}
		}
		daily, claimed := int64(0), int64(0)
		if item.ItemKey == domain.PetGiftBox {
			daily, claimed = 3, limits
		}
		remain := max(daily-claimed, 0)
		zh, en := domain.PetLockReason(item, p, counters)
		locked := zh != ""
		canRepeat := owned && item.Slot == "consumable"
		purchasable := item.Enabled && (!owned || canRepeat) && !locked && item.ItemType != "badge"
		if item.ItemKey == domain.PetGiftBox {
			purchasable = purchasable && remain > 0
		} else {
			purchasable = purchasable && w.Balance >= item.PriceLP
		}
		status := "available"
		switch {
		case !item.Enabled:
			status = "coming_soon"
		case equipped:
			status = "equipped"
		case item.ItemKey == domain.PetGiftBox && remain <= 0:
			status = "daily_limit"
		case owned:
			status = "owned"
		case item.ItemType == "badge":
			status = "achievement"
		case locked:
			status = "locked"
		case item.ItemKey != domain.PetGiftBox && w.Balance < item.PriceLP:
			status = "insufficient"
		}
		growth := int64(0)
		if item.ItemType == "food" {
			growth = max(item.GrowthValue, 0)
		}
		states = append(states, domain.PetStoreCatalogItemState{Item: item, Owned: owned, Equipped: equipped, Quantity: qty, GrowthValue: growth, DailyFreeLimit: daily, DailyFreeClaimed: claimed, DailyFreeRemaining: remain, Purchasable: purchasable, LockedReasonZh: zh, LockedReasonEn: en, Status: status})
	}
	var equip domain.PetEquipmentState
	for i := range inv {
		if !inv[i].Equipped {
			continue
		}
		copy := inv[i]
		switch inv[i].Slot {
		case "pet":
			if equip.CurrentPet == nil {
				equip.CurrentPet = &copy
			}
		case "accessory":
			if equip.Accessory == nil {
				equip.Accessory = &copy
			}
		case "scene":
			if equip.Scene == nil {
				equip.Scene = &copy
			}
		case "badge":
			if equip.Badge == nil {
				equip.Badge = &copy
			}
		}
	}
	return domain.PetStoreState{Profile: p, Wallet: w, Catalog: states, Inventory: inv, Equipment: equip, Counters: counters, Economy: economy}, nil
}

type petNameValidationError struct{}

func (petNameValidationError) Error() string { return "宠物名字不能为空。" }
func (petNameValidationError) Unwrap() error { return domain.ErrPet }

func (s *Store) SavePetName(ctx context.Context, name, now string) (domain.PetProfile, error) {
	var out domain.PetProfile
	name = strings.TrimSpace(name)
	if name == "" {
		return out, petNameValidationError{}
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := ensurePet(ctx, tx, now); err != nil {
			return err
		}
		p, e := loadPetProfile(ctx, tx)
		if e != nil {
			return e
		}
		p.Name, p.UpdatedAt = name, now
		if e = savePetProfile(ctx, tx, p); e != nil {
			return e
		}
		out = p
		return nil
	})
	return out, err
}
func (s *Store) PurchasePet(ctx context.Context, key string, quantity int64, now string) (domain.PetStoreState, error) {
	var out domain.PetStoreState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := ensurePet(ctx, tx, now); e != nil {
			return e
		}
		if e := ensurePetStore(ctx, tx, now); e != nil {
			return e
		}
		p, e := loadPetProfile(ctx, tx)
		if e != nil {
			return e
		}
		item, ok := domain.FindPetItem(key)
		if !ok {
			return errors.New("商品不存在。")
		}
		if !item.Enabled {
			return errors.New("该商品暂未开放。")
		}
		if item.ItemType == "badge" {
			return errors.New("成就徽章通过达成条件自动获得，不能购买。")
		}
		quantity = min(max(quantity, 1), 99)
		exists, e := petOwned(ctx, tx, key)
		if e != nil {
			return e
		}
		if exists && item.Slot != "consumable" {
			return errors.New("该商品已在个人仓库中。")
		}
		if item.Slot != "consumable" && quantity > 1 {
			return errors.New("该商品只能购买 1 件。")
		}
		counters, e := petCounters(ctx, tx)
		if e != nil {
			return e
		}
		if zh, _ := domain.PetLockReason(item, p, counters); zh != "" {
			return errors.New(zh)
		}
		if key == domain.PetGiftBox {
			quantity = min(quantity, 3)
			var claimed int64
			date := petDate(now)
			if e = tx.QueryRowContext(ctx, `SELECT free_claimed FROM pet_store_daily_limits WHERE pet_id='default-pet' AND item_key=? AND limit_date=?`, key, date).Scan(&claimed); e != nil && !errors.Is(e, sql.ErrNoRows) {
				return ErrDatabase
			}
			remaining := max(3-claimed, 0)
			if remaining <= 0 {
				return errors.New("今日免费惊喜礼盒已领取完。")
			}
			if quantity > remaining {
				return fmt.Errorf("今日免费惊喜礼盒仅剩 %d 个。", remaining)
			}
			if e = execute(ctx, tx, `INSERT INTO pet_store_daily_limits(pet_id,item_key,limit_date,free_claimed,updated_at) VALUES('default-pet',?,?,?,?) ON CONFLICT(pet_id,item_key,limit_date) DO UPDATE SET free_claimed=free_claimed+excluded.free_claimed,updated_at=excluded.updated_at`, key, date, quantity, now); e != nil {
				return e
			}
			if e = grantPetItem(ctx, tx, item, quantity, "daily_free_store", now); e != nil {
				return e
			}
			if e = petStoreSpeech(ctx, tx, "store_daily_free", item, quantity, fmt.Sprintf("今日免费惊喜礼盒 ×%d 已收好，记得打开看看。", quantity), fmt.Sprintf("Free gift box x%d claimed for today. Open it when you are ready.", quantity), now); e != nil {
				return e
			}
		} else {
			w, e := petWallet(ctx, tx)
			if e != nil {
				return e
			}
			if item.PriceLP > 0 && quantity > math.MaxInt64/item.PriceLP {
				return domain.ErrPet
			}
			total := item.PriceLP * quantity
			if w.Balance < total {
				return errors.New("LP 余额不足。")
			}
			if e = execute(ctx, tx, `UPDATE pet_wallets SET balance=balance-?,lifetime_spent=lifetime_spent+?,updated_at=? WHERE pet_id='default-pet' AND currency_key='lp'`, total, total, now); e != nil {
				return e
			}
			meta := key
			if e = petEconomy(ctx, tx, "spend", "lp", "purchase", fmt.Sprintf("purchase:%s:%d:%s", key, quantity, now), -total, w.Balance-total, &meta, now); e != nil {
				return e
			}
			if e = grantPetItem(ctx, tx, item, quantity, "purchase", now); e != nil {
				return e
			}
		}
		out, e = petStoreState(ctx, tx, p, now)
		return e
	})
	return out, err
}
func (s *Store) EquipPetItem(ctx context.Context, key, now string) (domain.PetStoreState, error) {
	var out domain.PetStoreState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := ensurePet(ctx, tx, now); e != nil {
			return e
		}
		if e := ensurePetStore(ctx, tx, now); e != nil {
			return e
		}
		p, e := loadPetProfile(ctx, tx)
		if e != nil {
			return e
		}
		var slot string
		if e = tx.QueryRowContext(ctx, `SELECT slot FROM pet_inventory WHERE pet_id='default-pet' AND item_key=?`, key).Scan(&slot); errors.Is(e, sql.ErrNoRows) {
			return errors.New("该商品还不在个人仓库中。")
		} else if e != nil {
			return ErrDatabase
		}
		if slot == "consumable" {
			return errors.New("消耗品不能装备。")
		}
		if e = execute(ctx, tx, `UPDATE pet_inventory SET equipped=0,updated_at=? WHERE pet_id='default-pet' AND slot=?`, now, slot); e != nil {
			return e
		}
		if e = execute(ctx, tx, `UPDATE pet_inventory SET equipped=1,updated_at=? WHERE pet_id='default-pet' AND item_key=?`, now, key); e != nil {
			return e
		}
		if item, ok := domain.FindPetItem(key); ok {
			if e = petStoreSpeech(ctx, tx, "store_equip", item, 1, petEquipDialogue(item, false), petEquipDialogue(item, true), now); e != nil {
				return e
			}
		}
		out, e = petStoreState(ctx, tx, p, now)
		return e
	})
	return out, err
}
func (s *Store) UnequipPetSlot(ctx context.Context, slot, now string) (domain.PetStoreState, error) {
	var out domain.PetStoreState
	if slot == "pet" {
		return out, errors.New("当前宠物不能取消装备，只能替换。")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := ensurePet(ctx, tx, now); e != nil {
			return e
		}
		if e := ensurePetStore(ctx, tx, now); e != nil {
			return e
		}
		p, e := loadPetProfile(ctx, tx)
		if e != nil {
			return e
		}
		if e = execute(ctx, tx, `UPDATE pet_inventory SET equipped=0,updated_at=? WHERE pet_id='default-pet' AND slot=?`, now, slot); e != nil {
			return e
		}
		out, e = petStoreState(ctx, tx, p, now)
		return e
	})
	return out, err
}
func (s *Store) UsePetItem(ctx context.Context, key string, quantity int64, now string) (domain.PetStoreState, error) {
	var out domain.PetStoreState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := ensurePet(ctx, tx, now); e != nil {
			return e
		}
		if e := ensurePetStore(ctx, tx, now); e != nil {
			return e
		}
		p, e := loadPetProfile(ctx, tx)
		if e != nil {
			return e
		}
		quantity = min(max(quantity, 1), 99)
		var available int64
		var slot, itemType string
		if e = tx.QueryRowContext(ctx, `SELECT quantity,slot,item_type FROM pet_inventory WHERE pet_id='default-pet' AND item_key=?`, key).Scan(&available, &slot, &itemType); errors.Is(e, sql.ErrNoRows) {
			return errors.New("该道具还不在个人仓库中。")
		} else if e != nil {
			return ErrDatabase
		}
		if slot != "consumable" {
			return errors.New("只有互动道具可以使用。")
		}
		if available < quantity {
			return errors.New("该道具数量不足。")
		}
		if key == domain.PetGiftBox {
			_, _, _, e := openGiftBox(ctx, tx, now)
			if e != nil {
				return e
			}
		} else {
			if _, e = consumePetItem(ctx, tx, key, quantity, now); e != nil {
				return e
			}
			if item, ok := domain.FindPetItem(key); ok && itemType == "food" {
				growth := max(item.GrowthValue, 0) * quantity
				p, e = petAddGrowth(ctx, tx, p, growth, "proud", now)
				if e != nil {
					return e
				}
				if e = petStoreSpeech(ctx, tx, "store_food", item, growth, petFoodDialogue(item, growth, quantity, now, false), petFoodDialogue(item, growth, quantity, now, true), now); e != nil {
					return e
				}
			}
		}
		out, e = petStoreState(ctx, tx, p, now)
		return e
	})
	return out, err
}

func petStoreSpeech(ctx context.Context, tx *sql.Tx, kind string, item domain.PetStoreCatalogItem, value int64, zh, en, now string) error {
	return petEvent(ctx, tx, kind, item.ItemKey, value, petJSON(map[string]any{"itemKey": item.ItemKey, "itemType": item.ItemType, "nameZh": item.NameZh, "nameEn": item.NameEn, "zh": zh, "en": en}), now)
}

func petDialogueIndex(key string, salt int64, length int) int {
	sum := uint(max(salt, 0))
	for index := 0; index < len(key); index++ {
		sum += uint(key[index])
	}
	return int(sum % uint(length))
}

func petEquipDialogue(item domain.PetStoreCatalogItem, english bool) string {
	var lines []string
	if english {
		switch item.Slot {
		case "pet":
			lines = []string{"My new companion look is ready. I will stay with you like this.", "The new companion state is ready. I am standing by.", "You picked me, so I will take good care of this desktop today."}
		case "accessory":
			lines = []string{item.NameEn + " is equipped. I feel more like your companion now.", "This outfit fits well. I will wear it while keeping you company.", "I received this little gift. I look more energetic today."}
		case "scene":
			lines = []string{"The new scene is ready. This feels more like our little corner.", item.NameEn + " is ready. I will quietly stay here with you.", "The space feels more comfortable now. Keep working, I am right beside you."}
		case "badge":
			lines = []string{"The badge is equipped. It proves what we have built together.", item.NameEn + " is precious. I will wear it carefully.", "I received this badge, and I will keep helping you finish more work."}
		default:
			lines = []string{item.NameEn + " is ready. I will make good use of it."}
		}
	} else {
		switch item.Slot {
		case "pet":
			lines = []string{"我换好样子啦，接下来就这样陪着你。", "新的伙伴状态准备好了，我会认真待命。", "你选了我，我会把今天的桌面守好。"}
		case "accessory":
			lines = []string{"「" + item.NameZh + "」戴好啦，好像更像你的伙伴了。", "这个装扮很合适，我会带着它陪你工作。", "我收到这份小心意了，今天看起来更精神。"}
		case "scene":
			lines = []string{"新场景布置好了，这里现在更像我们的角落。", "「" + item.NameZh + "」准备好了，我会在这里安静陪你。", "环境变舒服了，你继续忙，我在旁边守着。"}
		case "badge":
			lines = []string{"勋章戴上啦，这是我们一起攒下来的证明。", "「" + item.NameZh + "」很珍贵，我会好好带着。", "这枚勋章我收下了，以后也继续陪你完成更多事。"}
		default:
			lines = []string{"「" + item.NameZh + "」已经准备好了，我会好好用它。"}
		}
	}
	return lines[petDialogueIndex(item.ItemKey, item.SortOrder, len(lines))]
}

func petFoodDialogue(item domain.PetStoreCatalogItem, growth, quantity int64, now string, english bool) string {
	salt := growth + quantity
	if instant, err := time.Parse(time.RFC3339Nano, now); err == nil {
		salt += max(instant.UnixMilli(), 0) / 1000
	}
	var lines []string
	if english {
		label := "this " + item.NameEn
		if quantity > 1 {
			label = "these " + item.NameEn
		}
		lines = []string{
			fmt.Sprintf("%s made me feel cared for. Growth +%d.", label, growth),
			fmt.Sprintf("Thank you for remembering me. I saved this care carefully. Growth +%d.", growth),
			label + " feels like a small rest reminder. I will stay here with you.",
			"I feel warmer after this little treat. I can keep you company more steadily now.",
			fmt.Sprintf("I received your care. Growth +%d, and I feel a little closer to you.", growth),
		}
	} else {
		label := "这份「" + item.NameZh + "」"
		if quantity > 1 {
			label = "这些「" + item.NameZh + "」"
		}
		lines = []string{
			label + "让我感觉被好好照顾了，我会带着这份暖意继续陪你。",
			fmt.Sprintf("谢谢你记得喂我，我有把这份心意认真收好，成长值也悄悄 +%d。", growth),
			label + "像一个小小的休息提醒：你也要记得照顾自己，我会在这里陪你。",
			"吃饱以后心里软软的，今天我会更安静、更认真地守在你旁边。",
			fmt.Sprintf("我收到你的照顾了，成长值 +%d，但更重要的是我感觉离你更近了一点。", growth),
			"刚刚那一口很安心，像被轻轻拍了拍肩膀。我会把这份力量慢慢攒起来。",
			fmt.Sprintf("你把好吃的分给我时，我会觉得今天没有那么孤单。成长值 +%d，心也暖了一点。", growth),
			label + "不只是食物，更像一句“我有在看着你”。我会记住的。",
			"我吃饱啦。你继续忙也没关系，我会在桌面上安静陪你把这一段走完。",
			"被照顾的感觉真好，我现在有一点点想靠近你，也有一点点更勇敢了。",
			"这份能量我会好好用，不乱跑，不打扰，只在你需要时认真回应。",
			"谢谢你给我补充能量。等你累的时候，也让我提醒你停下来喘口气。",
			"我把这份暖意收进小口袋了，之后会慢慢变成陪你的力气。",
			fmt.Sprintf("成长值 +%d。不过比数字更开心的是，你刚刚没有忘记我。", growth),
			label + "让我恢复精神了，今天我会把陪伴这件事做得更稳一点。",
		}
		for _, part := range []string{"cake", "cookie", "cupcake", "donut", "macaron", "candy", "pudding", "tart", "ice-cream"} {
			if strings.Contains(item.ItemKey, part) {
				lines = append(lines, "甜甜的味道让心情也软下来了。我会把这点甜，留给接下来陪你的时间。", label+"像一小块奖励，提醒我：努力陪伴也可以是温柔的。", "甜味刚刚好，像今天的小亮点。我会带着它，陪你继续往前一点。", "吃到甜的以后，我好像更会撒娇了。不过我会乖乖待在这里陪你。")
				break
			}
		}
		if strings.Contains(item.ItemKey, "tea") || strings.Contains(item.ItemKey, "drink") {
			lines = append(lines, "喝下去以后，像给心情开了一扇小窗。我会清醒一点陪你。", label+"让节奏慢慢回来了。你也别太急，我们一点点做完。", "这一口很轻松，像短暂休息了一下。我会陪你把注意力找回来。")
		}
		if strings.Contains(item.ItemKey, "bento") || strings.Contains(item.ItemKey, "sandwich") {
			lines = append(lines, "这一餐很踏实，像有人认真说：先吃饱，再继续。我会更稳地陪你。", label+"让我有了长一点的力气，接下来我可以安静陪你久一点。", "吃饱以后，心里也安定了。你忙你的，我会守好这片小桌面。")
		}
		if quantity > 1 {
			lines = append(lines, "你一次给了我好多份，我会慢慢收好，不浪费这份照顾。", fmt.Sprintf("收到 ×%d 份心意啦。成长值 +%d，陪伴的电量也补满了一点。", quantity, growth), "这么认真地喂我，我会有点不好意思，但真的很开心。")
		}
		if growth >= 12 {
			lines = append(lines, fmt.Sprintf("这次能量很足，我感觉自己被好好托住了。成长值 +%d，我会更可靠一点。", growth), "好像一下子恢复了很多精神。你给我的这份照顾，我会变成陪伴还给你。", "这一份真的很有力量，我会把它变成更长久、更安静的陪伴。")
		}
	}
	return lines[petDialogueIndex(item.ItemKey, salt, len(lines))]
}

func openGiftBox(ctx context.Context, tx *sql.Tx, now string) (domain.PetStoreCatalogItem, bool, int64, error) {
	var box int64
	if err := tx.QueryRowContext(ctx, `SELECT quantity FROM pet_inventory WHERE pet_id='default-pet' AND item_key=?`, domain.PetGiftBox).Scan(&box); errors.Is(err, sql.ErrNoRows) {
		return domain.PetStoreCatalogItem{}, false, 0, errors.New("惊喜礼盒还不在个人仓库中。")
	} else if err != nil {
		return domain.PetStoreCatalogItem{}, false, 0, ErrDatabase
	}
	if box < 1 {
		return domain.PetStoreCatalogItem{}, false, 0, errors.New("惊喜礼盒数量不足。")
	}
	inv, err := petInventory(ctx, tx)
	if err != nil {
		return domain.PetStoreCatalogItem{}, false, 0, err
	}
	profile, err := loadPetProfile(ctx, tx)
	if err != nil {
		return domain.PetStoreCatalogItem{}, false, 0, err
	}
	counters, err := petCounters(ctx, tx)
	if err != nil {
		return domain.PetStoreCatalogItem{}, false, 0, err
	}
	pool := []domain.PetStoreCatalogItem{}
	total := int64(0)
	for _, item := range domain.PetCatalog() {
		if item.Enabled && item.ItemType != "pet" && item.ItemType != "badge" && item.ItemType != "seed" && item.ItemKey != domain.PetGiftBox {
			if reason, _ := domain.PetLockReason(item, profile, counters); reason == "" {
				pool = append(pool, item)
				total += petGiftBoxWeight(item)
			}
		}
	}
	if len(pool) == 0 {
		return domain.PetStoreCatalogItem{}, false, 0, errors.New("惊喜礼盒奖池暂时为空。")
	}
	var seed int64
	if instant, err := time.Parse(time.RFC3339Nano, now); err == nil {
		seed = max(instant.UnixMilli(), 0)
	}
	cursor := (seed + int64(len(pool))*137) % total
	if cursor < 0 {
		cursor += total
	}
	item := pool[0]
	for _, candidate := range pool {
		weight := petGiftBoxWeight(candidate)
		if cursor < weight {
			item = candidate
			break
		}
		cursor -= weight
	}
	owned := false
	for _, i := range inv {
		if i.ItemKey == item.ItemKey {
			owned = true
			break
		}
	}
	duplicate := owned && item.Slot != "consumable"
	comp := int64(0)
	if duplicate {
		comp = domain.PetDuplicateCompensation(item)
		if comp > 0 {
			if _, err := grantPetReward(ctx, tx, "gift_box_duplicate", "gift-box:"+item.ItemKey+":"+petID("gift-box"), comp, &item.ItemKey, now); err != nil {
				return item, false, 0, err
			}
		}
	} else if err := grantPetItem(ctx, tx, item, 1, "gift_box_reward", now); err != nil {
		return item, false, 0, err
	}
	if _, err := consumePetItem(ctx, tx, domain.PetGiftBox, 1, now); err != nil {
		return item, false, 0, err
	}
	kind := "gift_box_reward"
	zh := fmt.Sprintf("惊喜礼盒开出了「%s」，我已经帮你收进仓库。", item.NameZh)
	en := fmt.Sprintf("The gift box opened %s. It is now in your inventory.", item.NameEn)
	if duplicate {
		kind = "gift_box_duplicate"
		zh = fmt.Sprintf("惊喜礼盒开出了熟悉的「%s」，已转为 %d LP 补偿。", item.NameZh, comp)
		en = fmt.Sprintf("The gift box opened another %s, converted into %d LP.", item.NameEn, comp)
	}
	if err := petEvent(ctx, tx, kind, domain.PetGiftBox, max(comp, 1), petJSON(map[string]any{"itemKey": item.ItemKey, "itemType": item.ItemType, "nameZh": item.NameZh, "nameEn": item.NameEn, "zh": zh, "en": en}), now); err != nil {
		return item, false, 0, err
	}
	return item, duplicate, comp, nil
}

func petGiftBoxWeight(item domain.PetStoreCatalogItem) int64 {
	typeWeight := map[string]int64{"food": 42, "tool": 34, "cosmetic": 16, "theme": 10}[item.ItemType]
	if typeWeight == 0 {
		typeWeight = 1
	}
	rarityWeight := map[string]int64{"first_meet": 18, "familiar": 14, "grow_together": 10, "deep_bond": 6, "bond_forever": 1}[item.Rarity]
	if rarityWeight == 0 {
		rarityWeight = 8
	}
	return typeWeight * rarityWeight
}
func (s *Store) OpenPetGiftBox(ctx context.Context, now string) (domain.PetGiftBoxOpenResult, error) {
	var out domain.PetGiftBoxOpenResult
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := ensurePet(ctx, tx, now); e != nil {
			return e
		}
		if e := ensurePetStore(ctx, tx, now); e != nil {
			return e
		}
		p, e := loadPetProfile(ctx, tx)
		if e != nil {
			return e
		}
		item, dup, comp, e := openGiftBox(ctx, tx, now)
		if e != nil {
			return e
		}
		out.Prize, out.Duplicate, out.DuplicateCompensationLP = item, dup, comp
		out.State, e = petStoreState(ctx, tx, p, now)
		return e
	})
	return out, err
}
