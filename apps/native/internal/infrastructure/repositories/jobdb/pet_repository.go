package jobdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"liberty.local/liberty/internal/domain"
	"math"
	"strings"
	"time"
)

func petID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("system entropy unavailable")
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}
func petDate(now string) string {
	t, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return ""
	}
	return t.Local().Format("2006-01-02")
}
func ensurePet(ctx context.Context, tx *sql.Tx, now string) error {
	if err := execute(ctx, tx, `INSERT OR IGNORE INTO pet_profile(id,name,level,experience,stage,current_mood,created_at,updated_at) VALUES('default-pet','Libby',1,0,'first_meet','idle',?,?)`, now, now); err != nil {
		return err
	}
	if err := execute(ctx, tx, `INSERT OR IGNORE INTO pet_settings(pet_id,desktop_enabled,always_on_top,muted,focus_mode_enabled,proactive_level,last_window_x,last_window_y,updated_at) VALUES('default-pet',1,1,0,0,2,NULL,NULL,?)`, now); err != nil {
		return err
	}
	p, err := loadPetProfile(ctx, tx)
	if err != nil {
		return err
	}
	normalized := domain.PetNormalizeProfile(p, true)
	if p.Level != normalized.Level || p.Experience != normalized.Experience || p.Stage != normalized.Stage {
		normalized.UpdatedAt = now
		return savePetProfile(ctx, tx, normalized)
	}
	return nil
}
func loadPetProfile(ctx context.Context, tx *sql.Tx) (domain.PetProfile, error) {
	var p domain.PetProfile
	err := tx.QueryRowContext(ctx, `SELECT id,name,level,experience,stage,current_mood,created_at,updated_at FROM pet_profile WHERE id='default-pet'`).Scan(&p.ID, &p.Name, &p.Level, &p.Experience, &p.Stage, &p.CurrentMood, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, ErrDatabase
	}
	p.LevelSnapshot = domain.PetLevel(p.Experience)
	return p, nil
}
func savePetProfile(ctx context.Context, tx *sql.Tx, p domain.PetProfile) error {
	return execute(ctx, tx, `UPDATE pet_profile SET name=?,level=?,experience=?,stage=?,current_mood=?,updated_at=? WHERE id='default-pet'`, p.Name, p.Level, p.Experience, p.Stage, p.CurrentMood, p.UpdatedAt)
}
func petStageUnlock(ctx context.Context, tx *sql.Tx, p domain.PetProfile, previous, now string) error {
	if previous == p.Stage {
		return nil
	}
	key := map[string]string{"familiar": "clover-bow", "grow_together": "strawberry-candy", "deep_bond": "bell-accessory"}[p.Stage]
	if key == "" {
		return nil
	}
	return execute(ctx, tx, `INSERT OR IGNORE INTO pet_cosmetic_unlocks(id,pet_id,cosmetic_type,cosmetic_key,unlocked_at,equipped) VALUES(?,'default-pet','accessory',?,?,1)`, "pet-cosmetic-default-pet-"+key, key, now)
}
func ensurePetStore(ctx context.Context, tx *sql.Tx, now string) error {
	if err := execute(ctx, tx, `INSERT OR IGNORE INTO pet_wallets(pet_id,currency_key,balance,lifetime_earned,lifetime_spent,updated_at) VALUES('default-pet','lp',0,0,0,?)`, now); err != nil {
		return err
	}
	item, _ := domain.FindPetItem("libby-default")
	if err := upsertPetInventory(ctx, tx, item, 1, true, "default", now); err != nil {
		return err
	}
	// Existing inventory owns equipment choices; old unlocks only supply an initial value.
	rows, err := tx.QueryContext(ctx, `SELECT u.cosmetic_key,u.unlocked_at,COALESCE(i.equipped,u.equipped)
 FROM pet_cosmetic_unlocks u LEFT JOIN pet_inventory i ON i.pet_id=u.pet_id AND i.item_key=u.cosmetic_key
 WHERE u.pet_id='default-pet'`)
	if err != nil {
		return ErrDatabase
	}
	type unlock struct {
		key, at  string
		equipped bool
	}
	var unlocks []unlock
	for rows.Next() {
		var u unlock
		if rows.Scan(&u.key, &u.at, &u.equipped) != nil {
			rows.Close()
			return ErrDatabase
		}
		unlocks = append(unlocks, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrDatabase
	}
	for _, u := range unlocks {
		if item, ok := domain.FindPetItem(u.key); ok {
			if err := upsertPetInventory(ctx, tx, item, 1, u.equipped, "growth", u.at); err != nil {
				return err
			}
		}
	}
	return nil
}
func upsertPetInventory(ctx context.Context, tx *sql.Tx, item domain.PetStoreCatalogItem, qty int64, equipped bool, source, now string) error {
	return execute(ctx, tx, `INSERT INTO pet_inventory(id,pet_id,item_key,item_type,slot,quantity,equipped,source,purchased_at,updated_at) VALUES(?,'default-pet',?,?,?,?,?,?,?,?) ON CONFLICT(pet_id,item_key) DO UPDATE SET quantity=MAX(pet_inventory.quantity,excluded.quantity),equipped=CASE WHEN pet_inventory.equipped=1 THEN 1 ELSE excluded.equipped END,updated_at=excluded.updated_at`, "pet-inventory-default-pet-"+item.ItemKey, item.ItemKey, item.ItemType, item.Slot, qty, equipped, source, now, now)
}
func petInventory(ctx context.Context, tx *sql.Tx) ([]domain.PetInventoryItem, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,pet_id,item_key,item_type,slot,quantity,equipped,source,purchased_at,updated_at FROM pet_inventory WHERE pet_id='default-pet' AND quantity>0 ORDER BY datetime(updated_at) DESC,updated_at DESC`)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	items := []domain.PetInventoryItem{}
	for rows.Next() {
		var i domain.PetInventoryItem
		if rows.Scan(&i.ID, &i.PetID, &i.ItemKey, &i.ItemType, &i.Slot, &i.Quantity, &i.Equipped, &i.Source, &i.PurchasedAt, &i.UpdatedAt) != nil {
			return nil, ErrDatabase
		}
		items = append(items, i)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return items, nil
}
func petOwned(ctx context.Context, tx *sql.Tx, key string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM pet_inventory WHERE pet_id='default-pet' AND item_key=?`, key).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrDatabase
	}
	return true, nil
}
func grantPetItem(ctx context.Context, tx *sql.Tx, item domain.PetStoreCatalogItem, qty int64, source, now string) error {
	qty = min(max(qty, 1), 99)
	exists, err := petOwned(ctx, tx, item.ItemKey)
	if err != nil {
		return err
	}
	if exists && item.Slot == "consumable" {
		res, err := tx.ExecContext(ctx, `UPDATE pet_inventory SET quantity=quantity+?,source=?,updated_at=? WHERE pet_id='default-pet' AND item_key=? AND quantity<=?`, qty, source, now, item.ItemKey, math.MaxInt64-qty)
		if err != nil {
			return ErrDatabase
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			return domain.ErrPet
		}
		return nil
	}
	return upsertPetInventory(ctx, tx, item, qty, false, source, now)
}
func consumePetItem(ctx context.Context, tx *sql.Tx, key string, qty int64, now string) (int64, error) {
	qty = min(max(qty, 1), 99)
	var amount int64
	var slot string
	if tx.QueryRowContext(ctx, `SELECT quantity,slot FROM pet_inventory WHERE pet_id='default-pet' AND item_key=?`, key).Scan(&amount, &slot) != nil {
		return 0, domain.ErrPet
	}
	if slot != "consumable" || amount < qty {
		return 0, domain.ErrPet
	}
	if err := execute(ctx, tx, `UPDATE pet_inventory SET quantity=quantity-?,updated_at=? WHERE pet_id='default-pet' AND item_key=?`, qty, now, key); err != nil {
		return 0, err
	}
	if err := execute(ctx, tx, `DELETE FROM pet_inventory WHERE pet_id='default-pet' AND item_key=? AND quantity<=0`, key); err != nil {
		return 0, err
	}
	return amount - qty, nil
}
func petWallet(ctx context.Context, tx *sql.Tx) (domain.PetWallet, error) {
	var w domain.PetWallet
	err := tx.QueryRowContext(ctx, `SELECT pet_id,currency_key,balance,lifetime_earned,lifetime_spent,updated_at FROM pet_wallets WHERE pet_id='default-pet' AND currency_key='lp'`).Scan(&w.PetID, &w.CurrencyKey, &w.Balance, &w.LifetimeEarned, &w.LifetimeSpent, &w.UpdatedAt)
	if err != nil {
		return w, ErrDatabase
	}
	return w, nil
}
func petRewardExists(ctx context.Context, tx *sql.Tx, source, key string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM pet_economy_ledger WHERE pet_id='default-pet' AND source_type=? AND source_key=? AND currency_key='lp' LIMIT 1`, source, key).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrDatabase
	}
	return true, nil
}
func grantPetReward(ctx context.Context, tx *sql.Tx, source, key string, amount int64, metadata *string, now string) (bool, error) {
	if amount <= 0 {
		return false, nil
	}
	exists, err := petRewardExists(ctx, tx, source, key)
	if err != nil || exists {
		return false, err
	}
	if err := ensurePetWallet(ctx, tx, now); err != nil {
		return false, err
	}
	w, err := petWallet(ctx, tx)
	if err != nil {
		return false, err
	}
	if w.Balance > math.MaxInt64-amount || w.LifetimeEarned > math.MaxInt64-amount {
		return false, domain.ErrPet
	}
	if err := execute(ctx, tx, `UPDATE pet_wallets SET balance=balance+?,lifetime_earned=lifetime_earned+?,updated_at=? WHERE pet_id='default-pet' AND currency_key='lp'`, amount, amount, now); err != nil {
		return false, err
	}
	return true, petEconomy(ctx, tx, "earn", "lp", source, key, amount, w.Balance+amount, metadata, now)
}
func ensurePetWallet(ctx context.Context, tx *sql.Tx, now string) error {
	return execute(ctx, tx, `INSERT OR IGNORE INTO pet_wallets(pet_id,currency_key,balance,lifetime_earned,lifetime_spent,updated_at) VALUES('default-pet','lp',0,0,0,?)`, now)
}
func petEconomy(ctx context.Context, tx *sql.Tx, kind, currency, source, key string, amount, balance int64, metadata *string, now string) error {
	return execute(ctx, tx, `INSERT INTO pet_economy_ledger(id,pet_id,entry_type,currency_key,amount,balance_after,source_type,source_key,metadata,created_at) VALUES(?,'default-pet',?,?,?,?,?,?,?,?)`, petID("pet-economy"), kind, currency, amount, balance, source, key, metadata, now)
}
func claimPetEvent(ctx context.Context, tx *sql.Tx, source, key string, metadata *string, now string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM pet_economy_ledger WHERE pet_id='default-pet' AND source_type=? AND source_key=? AND currency_key IN('lp','event_key') LIMIT 1`, source, key).Scan(&n)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, ErrDatabase
	}
	return true, petEconomy(ctx, tx, "event", "event_key", source, key, 0, 0, metadata, now)
}
func petCounter(ctx context.Context, tx *sql.Tx, key, event, now string) error {
	claimed, err := claimPetEvent(ctx, tx, "milestone_counter:"+key, event, nil, now)
	if err != nil || !claimed {
		return err
	}
	return execute(ctx, tx, `INSERT INTO pet_milestone_counters(pet_id,counter_key,counter_value,last_event_key,updated_at) VALUES('default-pet',?,1,?,?) ON CONFLICT(pet_id,counter_key) DO UPDATE SET counter_value=counter_value+1,last_event_key=excluded.last_event_key,updated_at=excluded.updated_at`, key, event, now)
}
func petCounters(ctx context.Context, tx *sql.Tx) ([]domain.PetMilestoneCounter, error) {
	rows, err := tx.QueryContext(ctx, `SELECT pet_id,counter_key,counter_value,last_event_key,updated_at FROM pet_milestone_counters WHERE pet_id='default-pet' ORDER BY counter_key`)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	items := []domain.PetMilestoneCounter{}
	for rows.Next() {
		var c domain.PetMilestoneCounter
		if rows.Scan(&c.PetID, &c.CounterKey, &c.CounterValue, &c.LastEventKey, &c.UpdatedAt) != nil {
			return nil, ErrDatabase
		}
		items = append(items, c)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return items, nil
}
func petAutoUnlock(ctx context.Context, tx *sql.Tx, p domain.PetProfile, now string) error {
	counters, err := petCounters(ctx, tx)
	if err != nil {
		return err
	}
	for _, i := range domain.PetCatalog() {
		if i.ItemType != "badge" || !i.Enabled {
			continue
		}
		owned, err := petOwned(ctx, tx, i.ItemKey)
		if err != nil {
			return err
		}
		zh, _ := domain.PetLockReason(i, p, counters)
		if !owned && zh == "" {
			if err := grantPetItem(ctx, tx, i, 1, "achievement", now); err != nil {
				return err
			}
		}
	}
	return nil
}
func petEvent(ctx context.Context, tx *sql.Tx, kind, source string, value int64, metadata *string, now string) error {
	return execute(ctx, tx, `INSERT INTO pet_event_ledger(id,pet_id,event_type,event_source,event_value,event_time,metadata) VALUES(?,'default-pet',?,?,?,?,?)`, petID("pet-event"), kind, source, value, now, metadata)
}
func petJSON(value any) *string {
	b, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}
func petAddGrowth(ctx context.Context, tx *sql.Tx, p domain.PetProfile, amount int64, mood, now string) (domain.PetProfile, error) {
	previous := p.Stage
	if amount > 0 && p.Experience > math.MaxInt64-amount {
		return p, domain.ErrPet
	}
	p.Experience = max(p.Experience+amount, 0)
	p = domain.PetNormalizeProfile(p, false)
	p.CurrentMood, p.UpdatedAt = mood, now
	if err := savePetProfile(ctx, tx, p); err != nil {
		return p, err
	}
	return p, petStageUnlock(ctx, tx, p, previous, now)
}
func (s *Store) PetGrowth(ctx context.Context, eventType, source string, metadata *string, now string) (domain.PetProfile, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	if metadata != nil {
		normalized := strings.TrimSpace(*metadata)
		metadata = nil
		if normalized != "" {
			metadata = &normalized
		}
	}
	if eventType == "interaction" {
		metadata = nil
	}
	var out domain.PetProfile
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
		p = domain.PetNormalizeProfile(p, false)
		rule := domain.PetWorkflow(source)
		if eventType == "interaction" {
			var count int
			if tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM pet_event_ledger WHERE event_type='interaction' AND event_source=? AND date(event_time,'localtime')=?`, source, petDate(now)).Scan(&count) != nil {
				return ErrDatabase
			}
			if count >= 20 {
				label := map[string]string{"tap": "点击", "pet": "抚摸", "feed": "投喂", "encourage": "鼓励"}[source]
				if label == "" {
					label = "互动"
				}
				return fmt.Errorf("今天的%s互动已经达到上限（20次）。", label)
			}
			rule = domain.PetWorkflowRule{Growth: 1, LP: 1, Mood: "cheerful", Counter: rule.Counter}
			if source == "feed" {
				rule.Mood = "proud"
			}
		}
		key := eventType + ":" + source + ":global"
		if metadata != nil && strings.TrimSpace(*metadata) != "" {
			key = eventType + ":" + source + ":" + strings.TrimSpace(*metadata)
		}
		if source == "daily_open" {
			key = eventType + ":" + source + ":" + petDate(now)
		}
		if eventType == "interaction" && source != "daily_open" {
			key = eventType + ":" + source + ":" + petID("interaction")
		}
		exists, err := petRewardExists(ctx, tx, "workflow_reward", key)
		if err != nil {
			return err
		}
		claimed, err := claimPetEvent(ctx, tx, "pet_growth", key, metadata, now)
		if err != nil {
			return err
		}
		if !claimed || (exists && eventType != "interaction") {
			out = p
			return nil
		}
		gm, lm := domain.PetGrowthMultiplier(p.Level), domain.PetLPMultiplier(p.Level)
		if eventType == "interaction" {
			gm, lm = 1, 1
		}
		growth, lp := domain.PetScale(rule.Growth, gm), domain.PetScale(rule.LP, lm)
		p, err = petAddGrowth(ctx, tx, p, growth, rule.Mood, now)
		if err != nil {
			return err
		}
		if rule.Counter != "" {
			if err := petCounter(ctx, tx, rule.Counter, key, now); err != nil {
				return err
			}
		}
		if _, err := grantPetReward(ctx, tx, "workflow_reward", key, lp, metadata, now); err != nil {
			return err
		}
		if err := petAutoUnlock(ctx, tx, p, now); err != nil {
			return err
		}
		original := ""
		if metadata != nil {
			original = *metadata
		}
		if err := petEvent(ctx, tx, eventType, source, growth, petJSON(map[string]any{"source": original, "baseGrowth": rule.Growth, "growthMultiplier": gm, "finalGrowth": growth, "baseLp": rule.LP, "lpMultiplier": lm, "finalLp": lp}), now); err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}
