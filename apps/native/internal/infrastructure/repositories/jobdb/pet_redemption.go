package jobdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
)

const redemptionColumns = `id,pet_id,key_hash,code_prefix,campaign_id,reward_json,status,redeemed_at,metadata`

type redemptionScanner interface{ Scan(...any) error }

func scanPetRedemption(row redemptionScanner) (domain.PetRedeemKeyRedemption, error) {
	var v domain.PetRedeemKeyRedemption
	err := row.Scan(&v.ID, &v.PetID, &v.KeyHash, &v.CodePrefix, &v.CampaignID, &v.RewardJson, &v.Status, &v.RedeemedAt, &v.Metadata)
	return v, err
}
func (s *Store) PetRedemptions(ctx context.Context, limit int) ([]domain.PetRedeemKeyRedemption, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+redemptionColumns+` FROM pet_redeem_key_redemptions WHERE pet_id=? ORDER BY datetime(redeemed_at) DESC,redeemed_at DESC LIMIT ?`, domain.PetID, min(max(limit, 1), 100))
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	out := []domain.PetRedeemKeyRedemption{}
	for rows.Next() {
		v, err := scanPetRedemption(rows)
		if err != nil {
			return nil, ErrDatabase
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return out, nil
}
func (s *Store) RedeemKey(ctx context.Context, raw, now string) (domain.PetRedeemKeyResult, error) {
	return s.redeemKey(ctx, raw, now, func(code string, _ time.Time) (domain.PetRedemptionPayload, error) {
		return domain.VerifyPetRedemptionContent(code)
	})
}

// The verifier is fixed by the public entry point. The internal parameter lets
// transaction tests use generated test credentials, never production codes.
func (s *Store) redeemKey(ctx context.Context, raw, now string, verify func(string, time.Time) (domain.PetRedemptionPayload, error)) (domain.PetRedeemKeyResult, error) {
	var out domain.PetRedeemKeyResult
	if len(raw) > 64*1024 {
		return out, domain.ErrPetRedemption
	}
	code := domain.NormalizePetRedemption(raw)
	if code == "" {
		_, err := domain.VerifyPetRedemptionContent(code)
		return out, err
	}
	if len(code) > 32*1024 {
		return out, domain.ErrPetRedemption
	}
	instant, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return out, domain.ErrPetRedemption
	}
	hash := domain.PetRedemptionHash(code)
	err = s.transaction(ctx, func(tx *sql.Tx) error {
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
		previous, err := scanPetRedemption(tx.QueryRowContext(ctx, `SELECT `+redemptionColumns+` FROM pet_redeem_key_redemptions WHERE pet_id=? AND key_hash=?`, domain.PetID, hash))
		if err == nil {
			// Preserve original duplicate behavior even after a code expires.
			out.Redemption, out.Duplicate = previous, true
			if json.Unmarshal([]byte(previous.RewardJson), &out.Rewards) != nil {
				out.Rewards = domain.PetRedeemKeyRewards{Items: []domain.PetRedeemKeyRewardItem{}}
			}
			out.State, err = petStoreState(ctx, tx, p, now)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return ErrDatabase
		}
		payload, err := verify(code, instant)
		if err != nil {
			return err
		}
		previous, err = loadRedemptionByIdentity(ctx, tx, payload.CampaignID, payload.Nonce)
		if err != nil {
			return err
		}
		if previous.ID != "" {
			out.Redemption, out.Duplicate = previous, true
			if json.Unmarshal([]byte(previous.RewardJson), &out.Rewards) != nil {
				out.Rewards = domain.PetRedeemKeyRewards{Items: []domain.PetRedeemKeyRewardItem{}}
			}
			out.State, err = petStoreState(ctx, tx, p, now)
			return err
		}
		if err := domain.ValidatePetRedemptionExpiration(payload, instant); err != nil {
			return err
		}
		metadata := petJSON(map[string]any{"version": payload.Version, "campaignId": payload.CampaignID, "nonce": payload.Nonce, "issuedAt": payload.IssuedAt, "expiresAt": payload.ExpiresAt, "keyHash": hash})
		if _, err := grantPetReward(ctx, tx, "redeem_key", hash+":lp", max(payload.Rewards.LP, 0), metadata, now); err != nil {
			return err
		}
		for _, reward := range payload.Rewards.Items {
			item, ok := domain.FindPetItem(strings.TrimSpace(reward.ItemKey))
			if !ok || reward.Quantity <= 0 {
				return domain.ErrPetRedemption
			}
			if err := grantPetItem(ctx, tx, item, min(reward.Quantity, 99), "redeem_key", now); err != nil {
				return err
			}
		}
		growth := max(payload.Rewards.GrowthValue, 0)
		if growth > 0 {
			p, err = petAddGrowth(ctx, tx, p, growth, "proud", now)
			if err != nil {
				return err
			}
			if err = petAutoUnlock(ctx, tx, p, now); err != nil {
				return err
			}
		}
		if err := petEvent(ctx, tx, "redeem_key", "redeem_key", growth, metadata, now); err != nil {
			return err
		}
		rewards, err := json.Marshal(payload.Rewards)
		if err != nil {
			return domain.ErrPetRedemption
		}
		entry := domain.PetRedeemKeyRedemption{ID: petID("pet-redeem"), PetID: domain.PetID, KeyHash: hash, CodePrefix: domain.PetRedemptionPrefix(code), CampaignID: payload.CampaignID, RewardJson: string(rewards), Status: "redeemed", RedeemedAt: now, Metadata: metadata}
		if err := execute(ctx, tx, `INSERT INTO pet_redeem_key_redemptions (`+redemptionColumns+`) VALUES(?,?,?,?,?,?,?,?,?)`, entry.ID, entry.PetID, entry.KeyHash, entry.CodePrefix, entry.CampaignID, entry.RewardJson, entry.Status, entry.RedeemedAt, entry.Metadata); err != nil {
			return err
		}
		out.Redemption, out.Rewards = entry, payload.Rewards
		out.State, err = petStoreState(ctx, tx, p, now)
		return err
	})
	if err != nil {
		return domain.PetRedeemKeyResult{}, err
	}
	return out, nil
}

func loadRedemptionByIdentity(ctx context.Context, tx *sql.Tx, campaignID, nonce string) (domain.PetRedeemKeyRedemption, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+redemptionColumns+` FROM pet_redeem_key_redemptions WHERE pet_id=? AND campaign_id=? AND metadata LIKE ? ORDER BY datetime(redeemed_at) DESC,redeemed_at DESC`, domain.PetID, campaignID, `%"nonce":"%`)
	if err != nil {
		return domain.PetRedeemKeyRedemption{}, ErrDatabase
	}
	defer rows.Close()
	for rows.Next() {
		entry, err := scanPetRedemption(rows)
		if err != nil {
			return domain.PetRedeemKeyRedemption{}, ErrDatabase
		}
		var metadata struct {
			Nonce string `json:"nonce"`
		}
		if entry.Metadata != nil && json.Unmarshal([]byte(*entry.Metadata), &metadata) == nil && metadata.Nonce == nonce {
			return entry, nil
		}
	}
	if rows.Err() != nil {
		return domain.PetRedeemKeyRedemption{}, ErrDatabase
	}
	return domain.PetRedeemKeyRedemption{}, nil
}
