package jobdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"liberty.local/liberty/internal/domain"
	"math"
	"strings"
	"time"
)

func (s *Store) PetCheckIn(ctx context.Context, now string) (domain.PetDailyCheckInState, error) {
	var out domain.PetDailyCheckInState
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
		out, e = petCheckInState(ctx, tx, p, now)
		return e
	})
	return out, err
}
func petHistory(ctx context.Context, tx *sql.Tx, limit int) ([]domain.PetDailyCheckInEntry, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,pet_id,check_in_date,streak_count,cycle_day,reward_lp,growth_value,reward_items_json,created_at FROM pet_daily_check_ins WHERE pet_id='default-pet' ORDER BY check_in_date DESC,datetime(created_at) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	out := []domain.PetDailyCheckInEntry{}
	for rows.Next() {
		var e domain.PetDailyCheckInEntry
		var items string
		if rows.Scan(&e.ID, &e.PetID, &e.CheckInDate, &e.StreakCount, &e.CycleDay, &e.RewardLP, &e.GrowthValue, &items, &e.CreatedAt) != nil {
			return nil, ErrDatabase
		}
		_ = json.Unmarshal([]byte(items), &e.RewardItems)
		if e.RewardItems == nil {
			e.RewardItems = []domain.PetRewardItem{}
		}
		out = append(out, e)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return out, nil
}
func checkInRule(day int64) (int64, int64, []domain.PetRewardItem) {
	lp := int64(20)
	items := []domain.PetRewardItem{}
	switch day {
	case 3:
		lp = 30
		items = append(items, petRewardItem("gift-box-tool", 1))
	case 5:
		lp = 30
		items = append(items, petRewardItem("cupcake-food", 1))
	case 7:
		lp = 40
		items = append(items, petRewardItem("clover-badge", 1))
	case 10:
		lp = 40
		items = append(items, petRewardItem("gift-box-tool", 1))
	case 12:
		lp = 30
		items = append(items, petRewardItem("ice-cream-cone-food", 1))
	case 14:
		lp = 60
		items = append(items, petRewardItem("sprout-badge", 1))
	}
	return lp, 5, items
}
func petRewardItem(key string, qty int64) domain.PetRewardItem {
	i, _ := domain.FindPetItem(key)
	return domain.PetRewardItem{ItemKey: key, ItemType: i.ItemType, Quantity: qty}
}
func (s *Store) PetClaimCheckIn(ctx context.Context, now string) (domain.PetDailyCheckInClaimResult, error) {
	var result domain.PetDailyCheckInClaimResult
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
		date := petDate(now)
		existing, e := petCheckInForDate(ctx, tx, date)
		if e != nil {
			return e
		}
		if existing != nil {
			result.Entry, result.Duplicate = *existing, true
			result.State, e = petCheckInState(ctx, tx, p, now)
			return e
		}
		history, e := petHistory(ctx, tx, 30)
		if e != nil {
			return e
		}
		streak := max(nextCheckInStreak(history, date), 1)
		entry, e := createPetCheckIn(ctx, tx, p, date, streak, "daily_check_in", now)
		if e != nil {
			return e
		}
		result.Entry = entry
		p, e = loadPetProfile(ctx, tx)
		if e != nil {
			return e
		}
		result.State, e = petCheckInState(ctx, tx, p, now)
		return e
	})
	return result, err
}

func petCheckInForDate(ctx context.Context, tx *sql.Tx, date string) (*domain.PetDailyCheckInEntry, error) {
	var entry domain.PetDailyCheckInEntry
	var items string
	err := tx.QueryRowContext(ctx, `SELECT id,pet_id,check_in_date,streak_count,cycle_day,reward_lp,growth_value,reward_items_json,created_at FROM pet_daily_check_ins WHERE pet_id='default-pet' AND check_in_date=?`, date).Scan(&entry.ID, &entry.PetID, &entry.CheckInDate, &entry.StreakCount, &entry.CycleDay, &entry.RewardLP, &entry.GrowthValue, &items, &entry.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrDatabase
	}
	_ = json.Unmarshal([]byte(items), &entry.RewardItems)
	if entry.RewardItems == nil {
		entry.RewardItems = []domain.PetRewardItem{}
	}
	return &entry, nil
}

func nextCheckInStreak(history []domain.PetDailyCheckInEntry, date string) int64 {
	if len(history) == 0 {
		return 1
	}
	if history[0].CheckInDate == date {
		return history[0].StreakCount
	}
	if previousDate(history[0].CheckInDate, date) {
		return incrementCheckInStreak(history[0].StreakCount)
	}
	return 1
}

func incrementCheckInStreak(streak int64) int64 {
	if streak == math.MaxInt64 {
		return streak
	}
	return streak + 1
}

func previousDate(a, b string) bool {
	pa, e := time.Parse("2006-01-02", a)
	if e != nil {
		return false
	}
	pb, e := time.Parse("2006-01-02", b)
	return e == nil && pa.AddDate(0, 0, 1).Equal(pb)
}
func createPetCheckIn(ctx context.Context, tx *sql.Tx, p domain.PetProfile, date string, streak int64, source, now string) (domain.PetDailyCheckInEntry, error) {
	lp, growth, items := checkInRule(streak)
	granted := []domain.PetRewardItem{}
	for _, r := range items {
		i, ok := domain.FindPetItem(r.ItemKey)
		if !ok {
			continue
		}
		owned, e := petOwned(ctx, tx, r.ItemKey)
		if e != nil {
			return domain.PetDailyCheckInEntry{}, e
		}
		if owned && i.Slot != "consumable" {
			r.DuplicateCompensationLP = domain.PetDuplicateCompensation(i)
			if r.DuplicateCompensationLP > 0 {
				key := fmt.Sprintf("daily-check-in-duplicate:%s:%s:%s", source, date, i.ItemKey)
				if _, e = grantPetReward(ctx, tx, source+"_duplicate", key, r.DuplicateCompensationLP, &r.ItemKey, now); e != nil {
					return domain.PetDailyCheckInEntry{}, e
				}
			}
		} else if e = grantPetItem(ctx, tx, i, r.Quantity, source, now); e != nil {
			return domain.PetDailyCheckInEntry{}, e
		}
		granted = append(granted, r)
	}
	key := "daily-check-in:" + date
	if source == "daily_check_in_makeup" {
		key = "daily-check-in-makeup:" + date
	}
	meta := petJSON(map[string]any{"date": date, "streakCount": streak, "cycleDay": streak, "rewardLp": lp, "growthValue": growth, "items": granted, "source": source})
	if _, e := grantPetReward(ctx, tx, source, key, lp, meta, now); e != nil {
		return domain.PetDailyCheckInEntry{}, e
	}
	_, e := petAddGrowth(ctx, tx, p, growth, "cheerful", now)
	if e != nil {
		return domain.PetDailyCheckInEntry{}, e
	}
	data, _ := json.Marshal(granted)
	entry := domain.PetDailyCheckInEntry{ID: petID("pet-check-in"), PetID: domain.PetID, CheckInDate: date, StreakCount: streak, CycleDay: max(streak, 1), RewardLP: lp, GrowthValue: growth, RewardItems: granted, CreatedAt: now}
	if e = execute(ctx, tx, `INSERT INTO pet_daily_check_ins(id,pet_id,check_in_date,streak_count,cycle_day,reward_lp,growth_value,reward_items_json,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, entry.ID, entry.PetID, date, streak, entry.CycleDay, lp, growth, string(data), now); e != nil {
		return entry, e
	}
	makeup := source == "daily_check_in_makeup"
	actionZh, actionEn := "今日签到完成", "Daily check-in complete"
	if makeup {
		actionZh, actionEn = "补签完成", "Make-up check-in complete"
	}
	lineZh := fmt.Sprintf("%s，连续 %d 天。奖励和商店物品都已收好。", actionZh, streak)
	lineEn := fmt.Sprintf("%s. Streak %d days. Rewards and store items are saved.", actionEn, streak)
	if len(granted) == 0 {
		lineZh = fmt.Sprintf("%s，连续 %d 天。LP +%d，成长值 +%d。", actionZh, streak, lp, growth)
		lineEn = fmt.Sprintf("%s. Streak %d days. LP +%d, growth +%d.", actionEn, streak, lp, growth)
	}
	eventMetadata := petJSON(map[string]any{"source": source, "makeup": makeup, "date": date, "streakCount": streak, "cycleDay": entry.CycleDay, "finalLp": lp, "growthValue": growth, "items": granted, "zh": lineZh, "en": lineEn})
	if e = petEvent(ctx, tx, "daily_check_in", source, growth, eventMetadata, now); e != nil {
		return entry, e
	}
	return entry, nil
}
func petCheckInState(ctx context.Context, tx *sql.Tx, p domain.PetProfile, now string) (domain.PetDailyCheckInState, error) {
	date := petDate(now)
	history, e := petHistory(ctx, tx, 20)
	if e != nil {
		return domain.PetDailyCheckInState{}, e
	}
	checked := false
	streak := nextCheckInStreak(history, date)
	for _, entry := range history {
		if entry.CheckInDate == date {
			checked, streak = true, entry.StreakCount
			break
		}
	}
	missed := int64(0)
	available := false
	var makeupDate *string
	reason := "暂无可补签的历史记录。"
	if checked {
		reason = "今日已经签到过了。"
	}
	if len(history) > 0 && !checked {
		prev, err := time.Parse("2006-01-02", history[0].CheckInDate)
		cur, err2 := time.Parse("2006-01-02", date)
		switch {
		case err != nil:
			reason = "最近签到日期异常，暂时不能补签。"
		case err2 != nil:
			reason = "当前签到日期异常，暂时不能补签。"
		case !cur.After(prev):
			reason = "当前没有可补签的断签记录。"
		default:
			reason = "当前没有可补签的断签记录。"
			missed = max((cur.Unix()-prev.Unix())/(24*60*60)-1, 0)
			if missed > 0 && missed < 7 {
				available = true
				d := prev.AddDate(0, 0, 1).Format("2006-01-02")
				makeupDate = &d
				reason = ""
			} else if missed >= 7 {
				reason = "连续断签已达到 7 天，不能补签。"
			}
		}
	}
	next := max(streak, 1)
	if available {
		streak = history[0].StreakCount
		next = max(incrementCheckInStreak(streak), 1)
	}
	lp, growth, items := checkInRule(next)
	preview := domain.PetDailyCheckInRewardPreview{CycleDay: next, RewardLP: lp, GrowthValue: growth, Items: items}
	rewards := make([]domain.PetDailyCheckInRewardPreview, 14)
	for i := range rewards {
		a, b, c := checkInRule(int64(i + 1))
		rewards[i] = domain.PetDailyCheckInRewardPreview{CycleDay: int64(i + 1), RewardLP: a, GrowthValue: b, Items: c}
	}
	store, e := petStoreState(ctx, tx, p, now)
	if e != nil {
		return domain.PetDailyCheckInState{}, e
	}
	quantity := int64(0)
	for _, item := range store.Inventory {
		if item.ItemKey == domain.PetMakeupTicket {
			quantity = item.Quantity
		}
	}
	return domain.PetDailyCheckInState{CheckInDate: date, CheckedInToday: checked, CurrentStreak: streak, NextCycleDay: next, CycleLength: 14, MissedDays: missed, MakeupAvailable: available, MakeupDate: makeupDate, MakeupTicketItemKey: domain.PetMakeupTicket, MakeupTicketQuantity: quantity, MakeupBlockedReason: nullable(reason), TodayReward: preview, Rewards: rewards, History: history, StoreState: store}, nil
}
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func (s *Store) PetMakeupCheckIn(ctx context.Context, now string) (domain.PetDailyCheckInMakeupResult, error) {
	var out domain.PetDailyCheckInMakeupResult
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
		state, e := petCheckInState(ctx, tx, p, now)
		if e != nil {
			return e
		}
		if !state.MakeupAvailable || state.MakeupDate == nil {
			if state.MakeupBlockedReason != nil {
				return errors.New(*state.MakeupBlockedReason)
			}
			return errors.New("当前没有可补签的断签日期。")
		}
		if _, e = consumePetItem(ctx, tx, domain.PetMakeupTicket, 1, now); e != nil {
			if errors.Is(e, domain.ErrPet) {
				return errors.New("补签票券数量不足。")
			}
			return e
		}
		history, e := petHistory(ctx, tx, 30)
		if e != nil {
			return e
		}
		streak := int64(1)
		if len(history) > 0 {
			streak = max(incrementCheckInStreak(history[0].StreakCount), 1)
		}
		entry, e := createPetCheckIn(ctx, tx, p, *state.MakeupDate, streak, "daily_check_in_makeup", now)
		if e != nil {
			return e
		}
		out.Entry, out.TicketItemKey = entry, domain.PetMakeupTicket
		p, e = loadPetProfile(ctx, tx)
		if e != nil {
			return e
		}
		out.State, e = petCheckInState(ctx, tx, p, now)
		for _, i := range out.State.StoreState.Inventory {
			if i.ItemKey == domain.PetMakeupTicket {
				out.TicketQuantityAfter = i.Quantity
			}
		}
		return e
	})
	return out, err
}
func (s *Store) PetBlindBox(ctx context.Context, now string) (domain.PetBlindBoxState, error) {
	var out domain.PetBlindBoxState
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
		out, e = petBlindBoxState(ctx, tx, p, now)
		return e
	})
	return out, err
}
func petBlindBoxState(ctx context.Context, tx *sql.Tx, p domain.PetProfile, now string) (domain.PetBlindBoxState, error) {
	date := petDate(now)
	var used int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM pet_blind_box_draws WHERE pet_id='default-pet' AND draw_date=?`, date).Scan(&used); err != nil {
		return domain.PetBlindBoxState{}, ErrDatabase
	}
	inventory, e := petInventory(ctx, tx)
	if e != nil {
		return domain.PetBlindBoxState{}, e
	}
	pool := []domain.PetBlindBoxPoolItem{}
	for _, item := range domain.PetCatalog() {
		if !item.Enabled || item.ItemType == "pet" {
			continue
		}
		owned := false
		for _, i := range inventory {
			if i.ItemKey == item.ItemKey {
				owned = true
			}
		}
		weight := petItemWeight(item)
		pool = append(pool, domain.PetBlindBoxPoolItem{Item: item, Owned: owned, Weight: weight, DuplicateCompensationLP: domain.PetDuplicateCompensation(item)})
	}
	empty := domain.PetStoreCatalogItem{ItemKey: "nothing", ItemType: "none", Slot: "none", NameZh: "什么都没抽中", NameEn: "Nothing This Time", DescriptionZh: "这次盲盒没有获得物品，今日次数仍会正常消耗。", DescriptionEn: "No item was gained from this opening. The daily attempt is still consumed.", Rarity: "first_meet", LevelGate: 1, AssetKey: "gift_box", Enabled: true, SortOrder: 9999}
	pool = append(pool, domain.PetBlindBoxPoolItem{Item: empty, Weight: 18 * 18})
	rows, e := tx.QueryContext(ctx, `SELECT id,pet_id,draw_date,item_key,item_type,quantity,duplicate_compensation_lp,created_at FROM pet_blind_box_draws WHERE pet_id='default-pet' ORDER BY datetime(created_at) DESC,created_at DESC,rowid DESC LIMIT 20`)
	if e != nil {
		return domain.PetBlindBoxState{}, ErrDatabase
	}
	defer rows.Close()
	hist := []domain.PetBlindBoxDrawEntry{}
	for rows.Next() {
		var d domain.PetBlindBoxDrawEntry
		if rows.Scan(&d.ID, &d.PetID, &d.DrawDate, &d.ItemKey, &d.ItemType, &d.Quantity, &d.DuplicateCompensationLP, &d.CreatedAt) != nil {
			return domain.PetBlindBoxState{}, ErrDatabase
		}
		hist = append(hist, d)
	}
	if err := rows.Err(); err != nil {
		return domain.PetBlindBoxState{}, ErrDatabase
	}
	store, e := petStoreState(ctx, tx, p, now)
	if e != nil {
		return domain.PetBlindBoxState{}, e
	}
	return domain.PetBlindBoxState{DrawDate: date, DailyLimit: 10, UsedToday: used, RemainingToday: max(10-used, 0), Pool: pool, History: hist, StoreState: store}, nil
}
func petItemWeight(item domain.PetStoreCatalogItem) int64 {
	a := map[string]int64{"none": 18, "food": 48, "tool": 30, "cosmetic": 12, "theme": 8, "badge": 4}[item.ItemType]
	b := map[string]int64{"first_meet": 18, "familiar": 14, "grow_together": 10, "deep_bond": 6, "bond_forever": 1}[item.Rarity]
	if a == 0 {
		a = 1
	}
	if b == 0 {
		b = 8
	}
	return a * b
}
func (s *Store) PetDrawBlindBox(ctx context.Context, now string) (domain.PetBlindBoxDrawResult, error) {
	var out domain.PetBlindBoxDrawResult
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
		state, e := petBlindBoxState(ctx, tx, p, now)
		if e != nil {
			return e
		}
		if state.UsedToday >= 10 {
			return errors.New("今日每日盲盒次数已用完。")
		}
		if len(state.Pool) == 0 {
			return errors.New("每日盲盒奖池暂时为空。")
		}
		total := int64(0)
		for _, v := range state.Pool {
			total += max(v.Weight, 1)
		}
		seed := int64(0)
		if t, err := time.Parse(time.RFC3339Nano, now); err == nil {
			seed = max(t.UnixMilli(), 0)
		}
		cursor := (seed + petBlindBoxSalt(p.Experience, 31) + petBlindBoxSalt(state.UsedToday, 97)) % total
		if cursor < 0 {
			cursor += total
		}
		var prize domain.PetBlindBoxPoolItem
		for _, v := range state.Pool {
			w := max(v.Weight, 1)
			if cursor < w {
				prize = v
				break
			}
			cursor -= w
		}
		empty := prize.Item.ItemKey == "nothing"
		dup := !empty && prize.Owned && prize.Item.Slot != "consumable"
		comp := int64(0)
		if dup {
			comp = domain.PetDuplicateCompensation(prize.Item)
			if comp > 0 {
				if _, e = grantPetReward(ctx, tx, "daily_blind_box", fmt.Sprintf("daily-blind-box:%s:%d", state.DrawDate, state.UsedToday+1), comp, &prize.Item.ItemKey, now); e != nil {
					return e
				}
			}
		} else if !empty {
			if e = grantPetItem(ctx, tx, prize.Item, 1, "daily_blind_box", now); e != nil {
				return e
			}
		}
		kind := "blind_box_reward"
		mood := "excited"
		if empty {
			kind, mood = "blind_box_empty", "needy"
		} else if dup {
			kind, mood = "blind_box_duplicate", "cheerful"
		}
		meta := petJSON(map[string]any{"itemKey": prize.Item.ItemKey, "itemType": prize.Item.ItemType, "nameZh": prize.Item.NameZh, "nameEn": prize.Item.NameEn, "drawNumber": state.UsedToday + 1, "duplicate": dup, "compensationLp": comp, "mood": mood, "zh": petBlindBoxDialogue(prize.Item, empty, dup, comp, state.UsedToday+1, now, false), "en": petBlindBoxDialogue(prize.Item, empty, dup, comp, state.UsedToday+1, now, true)})
		if e = petEvent(ctx, tx, kind, "daily_blind_box", max(comp, 1), meta, now); e != nil {
			return e
		}
		p.CurrentMood, p.UpdatedAt = mood, now
		if e = savePetProfile(ctx, tx, p); e != nil {
			return e
		}
		entry := domain.PetBlindBoxDrawEntry{ID: petID("pet-blind-box"), PetID: domain.PetID, DrawDate: state.DrawDate, ItemKey: prize.Item.ItemKey, ItemType: prize.Item.ItemType, Quantity: 1, DuplicateCompensationLP: comp, CreatedAt: now}
		if e = execute(ctx, tx, `INSERT INTO pet_blind_box_draws(id,pet_id,draw_date,item_key,item_type,quantity,duplicate_compensation_lp,created_at) VALUES(?,?,?,?,?,?,?,?)`, entry.ID, entry.PetID, entry.DrawDate, entry.ItemKey, entry.ItemType, entry.Quantity, comp, now); e != nil {
			return e
		}
		out.Draw, out.Prize, out.Duplicate = entry, prize.Item, dup
		out.State, e = petBlindBoxState(ctx, tx, p, now)
		return e
	})
	return out, err
}

func petBlindBoxSalt(value, factor int64) int64 {
	if value > math.MaxInt64/factor {
		return math.MaxInt64
	}
	if value < math.MinInt64/factor {
		return math.MinInt64
	}
	return value * factor
}

func petBlindBoxDialogue(item domain.PetStoreCatalogItem, empty, duplicate bool, compensation, number int64, now string, english bool) string {
	var lines []string
	salt := number
	if duplicate {
		salt = compensation
	}
	if instant, err := time.Parse(time.RFC3339Nano, now); err == nil {
		salt += max(instant.UnixMilli(), 0) / 1000
	}
	if english {
		switch {
		case empty:
			lines = []string{"Nothing came out this time... I am a little disappointed, but I still want to try again with you.", "The box was empty. I will stay beside you and save the next bit of luck.", "No reward this time. I am pouting a little, but I have not given up.", "It missed this time. Let me hold the luck for the next box."}
		case duplicate:
			lines = []string{fmt.Sprintf("We already have %s, so I tucked away %d LP for us instead.", item.NameEn, compensation), fmt.Sprintf("%s came back again. I am still happy; it turned into %d LP.", item.NameEn, compensation), fmt.Sprintf("A familiar gift appeared. I saved the extra care as %d LP.", compensation)}
		default:
			lines = []string{fmt.Sprintf("I opened %s! My heart jumped a little. This feels like luck arriving.", item.NameEn), fmt.Sprintf("Look, %s came out! I want to show it to you right away.", item.NameEn), fmt.Sprintf("This box hid %s inside. I feel bright and excited now.", item.NameEn), fmt.Sprintf("We got %s. I will keep this surprise carefully with you.", item.NameEn)}
		}
	} else {
		switch {
		case empty:
			lines = []string{"这次盒子空空的……我有一点点失落，但还是想和你再试一次。", "什么都没开出来。我先把下一次的好运抱紧一点，别让它跑掉。", "这次没有奖励，我有点想鼓鼓脸，不过我还没有放弃。", "它这次和我们擦肩而过了。下一次，让我来帮你守住好运。", "空盒子也算一次回应吧……只是它回答得有点小声，我会继续陪你。"}
		case duplicate:
			lines = []string{fmt.Sprintf("「%s」又回来啦，虽然已经拥有了，但我把它认真换成了 %d LP。", item.NameZh, compensation), fmt.Sprintf("是熟悉的「%s」。我没有难过哦，它变成 %d LP 留给我们了。", item.NameZh, compensation), fmt.Sprintf("重复奖励也不是白来一趟，我把多出来的心意收成了 %d LP。", compensation), fmt.Sprintf("这个我们已经有啦。不过能再次遇见它，我还是有点开心，补偿 %d LP 已收好。", compensation)}
		default:
			lines = []string{fmt.Sprintf("开到「%s」啦！我刚刚心里亮了一下，像好运真的落到我们手里了。", item.NameZh), fmt.Sprintf("你看你看，是「%s」！我有点激动，想马上把它举给你看。", item.NameZh), fmt.Sprintf("这只盲盒里藏着「%s」。我现在开心得有点坐不住了。", item.NameZh), fmt.Sprintf("我们抽到「%s」啦。我会把这份惊喜认真收好，陪你一起用。", item.NameZh), fmt.Sprintf("「%s」出现的那一刻，我感觉今天的运气被点亮了一点。", item.NameZh), fmt.Sprintf("这份奖励我很喜欢，因为它是我们一起开出来的「%s」。", item.NameZh)}
		}
	}
	return lines[petDialogueIndex(item.ItemKey, salt, len(lines))]
}

var _ = strings.TrimSpace
