package domain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrPetRedemption = errors.New("pet_redemption_invalid")
var ErrPetRedemptionExpired = errors.New("pet_redemption_expired")
var ErrPetRedemptionUnsupported = errors.New("pet_redemption_format_unsupported")

// PetRedemptionPayload contains verified rewards. Never persist the input code.
type PetRedemptionPayload struct {
	Version    uint32              `json:"version"`
	CampaignID string              `json:"campaignId"`
	Nonce      string              `json:"nonce"`
	IssuedAt   *string             `json:"issuedAt"`
	ExpiresAt  *string             `json:"expiresAt"`
	Rewards    PetRedeemKeyRewards `json:"rewards"`
}

func NormalizePetRedemption(raw string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, raw)
}
func PetRedemptionHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
func PetRedemptionPrefix(normalized string) string {
	prefix, _, _ := strings.Cut(normalized, ".")
	prefix, _, _ = strings.Cut(prefix, "-")
	return prefix + "-" + PetRedemptionHash(normalized)[:8]
}
func VerifyPetRedemption(code string, now time.Time) (PetRedemptionPayload, error) {
	return verifyPetRedemption(code, now, ed25519.PublicKey(petRedemptionPublicKey[:]))
}

type petRedemptionError struct {
	message string
	cause   error
}

func (err petRedemptionError) Error() string { return err.message }
func (err petRedemptionError) Unwrap() error { return err.cause }

func invalidPetRedemption(message string) error {
	return petRedemptionError{message: message, cause: ErrPetRedemption}
}

func VerifyPetRedemptionContent(code string) (PetRedemptionPayload, error) {
	return verifyPetRedemptionContent(code, ed25519.PublicKey(petRedemptionPublicKey[:]))
}

func verifyPetRedemption(code string, now time.Time, public ed25519.PublicKey) (PetRedemptionPayload, error) {
	payload, err := verifyPetRedemptionContent(code, public)
	if err != nil {
		return payload, err
	}
	return payload, ValidatePetRedemptionExpiration(payload, now)
}

func verifyPetRedemptionContent(code string, public ed25519.PublicKey) (PetRedemptionPayload, error) {
	var p PetRedemptionPayload
	if len(code) == 0 {
		return p, invalidPetRedemption("请输入兑换 Key。")
	}
	if len(code) > 32*1024 || len(public) != ed25519.PublicKeySize {
		return p, ErrPetRedemption
	}
	parts := strings.Split(code, ".")
	switch {
	case len(parts) == 3 && parts[0] == "LIB1":
		sig, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
		if err != nil {
			return p, invalidPetRedemption("兑换 Key 签名格式不正确。")
		}
		if !ed25519.Verify(public, []byte(parts[1]), sig) {
			return p, invalidPetRedemption("兑换 Key 签名校验失败。")
		}
		data, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
		if err != nil {
			return p, invalidPetRedemption("兑换 Key 内容格式不正确。")
		}
		p, err = decodePetLegacyRedemption(data)
		if err != nil {
			return p, invalidPetRedemption("兑换 Key 内容无法解析。")
		}
	case len(parts) == 2 && parts[0] == "LIB2":
		token, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
		if err != nil {
			return p, invalidPetRedemption("兑换 Key 内容格式不正确。")
		}
		if len(token) != 28+ed25519.SignatureSize {
			return p, invalidPetRedemption("兑换 Key 长度不正确。")
		}
		if !ed25519.Verify(public, token[:28], token[28:]) {
			return p, invalidPetRedemption("兑换 Key 签名校验失败。")
		}
		p, err = decodePetCompactRedemption(token[:28])
		if err != nil {
			return p, err
		}
	default:
		// Short codes require the legacy symmetric signing secret. Never embed it.
		return p, ErrPetRedemptionUnsupported
	}
	if p.Version < 1 || p.Version > 3 {
		return p, invalidPetRedemption("当前版本暂不支持该兑换 Key。")
	}
	if strings.TrimSpace(p.CampaignID) == "" {
		return p, invalidPetRedemption("兑换 Key 缺少活动标识。")
	}
	if strings.TrimSpace(p.Nonce) == "" {
		return p, invalidPetRedemption("兑换 Key 缺少唯一标识。")
	}
	if p.Rewards.LP <= 0 && p.Rewards.GrowthValue <= 0 && len(p.Rewards.Items) == 0 {
		return p, invalidPetRedemption("兑换 Key 没有可发放的奖励。")
	}
	for _, i := range p.Rewards.Items {
		itemKey := strings.TrimSpace(i.ItemKey)
		if itemKey == "" {
			return p, invalidPetRedemption("兑换奖励中存在空道具。")
		}
		if i.Quantity <= 0 {
			return p, invalidPetRedemption(fmt.Sprintf("兑换奖励「%s」数量必须大于 0。", itemKey))
		}
		if _, ok := FindPetItem(itemKey); !ok {
			return p, invalidPetRedemption(fmt.Sprintf("兑换奖励中包含未知道具：%s。", itemKey))
		}
	}
	return p, nil
}

func ValidatePetRedemptionExpiration(payload PetRedemptionPayload, now time.Time) error {
	if payload.ExpiresAt == nil {
		return nil
	}
	expires, err := time.Parse("2006-01-02", *payload.ExpiresAt)
	if err != nil {
		return invalidPetRedemption("兑换 Key 过期时间格式不正确。")
	}
	if expires.Format("2006-01-02") < now.UTC().Format("2006-01-02") {
		return petRedemptionError{message: "兑换 Key 已过期。", cause: ErrPetRedemptionExpired}
	}
	return nil
}

func redemptionStringEscapes(data []byte) bool {
	data = bytes.TrimSpace(data)
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return false
	}
	for index := 1; index < len(data)-1; index++ {
		if data[index] != '\\' {
			continue
		}
		index++
		if index >= len(data)-1 {
			return false
		}
		if data[index] != 'u' {
			continue
		}
		if index+4 >= len(data)-1 {
			return false
		}
		unit, err := strconv.ParseUint(string(data[index+1:index+5]), 16, 16)
		if err != nil {
			return false
		}
		index += 4
		if unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
		if unit >= 0xd800 && unit <= 0xdbff {
			if index+6 >= len(data)-1 || data[index+1] != '\\' || data[index+2] != 'u' {
				return false
			}
			trailing, err := strconv.ParseUint(string(data[index+3:index+7]), 16, 16)
			if err != nil || trailing < 0xdc00 || trailing > 0xdfff {
				return false
			}
			index += 6
		}
	}
	return true
}

func redemptionObject(data []byte, known, required []string) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) {
		return nil, ErrPetRedemption
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, ErrPetRedemption
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		start := decoder.InputOffset()
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok || !redemptionStringEscapes(bytes.TrimLeft(data[start:decoder.InputOffset()], ", \t\r\n")) {
			return nil, ErrPetRedemption
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		for _, field := range known {
			if name == field {
				if _, exists := fields[name]; exists {
					return nil, ErrPetRedemption
				}
				fields[name] = value
				break
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, ErrPetRedemption
	}
	for _, name := range required {
		if _, exists := fields[name]; !exists {
			return nil, ErrPetRedemption
		}
	}
	return fields, nil
}

func redemptionField(fields map[string]json.RawMessage, name string, target any) error {
	value, exists := fields[name]
	if !exists {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return ErrPetRedemption
	}
	if _, isString := target.(*string); isString && !redemptionStringEscapes(value) {
		return ErrPetRedemption
	}
	return json.Unmarshal(value, target)
}

func decodePetLegacyRedemption(data []byte) (PetRedemptionPayload, error) {
	var payload PetRedemptionPayload
	fields, err := redemptionObject(data, []string{"version", "campaignId", "nonce", "issuedAt", "expiresAt", "rewards"}, []string{"version", "campaignId", "rewards"})
	if err != nil {
		return payload, err
	}
	for _, field := range []struct {
		name   string
		target any
	}{{"version", &payload.Version}, {"campaignId", &payload.CampaignID}, {"nonce", &payload.Nonce}} {
		if err := redemptionField(fields, field.name, field.target); err != nil {
			return payload, err
		}
	}
	for _, field := range []struct {
		name   string
		target any
	}{{"issuedAt", &payload.IssuedAt}, {"expiresAt", &payload.ExpiresAt}} {
		if value, exists := fields[field.name]; exists {
			if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) && !redemptionStringEscapes(value) {
				return payload, ErrPetRedemption
			}
			if err := json.Unmarshal(value, field.target); err != nil {
				return payload, err
			}
		}
	}
	rewards, err := redemptionObject(fields["rewards"], []string{"lp", "growthValue", "items"}, nil)
	if err != nil {
		return payload, err
	}
	if err := redemptionField(rewards, "lp", &payload.Rewards.LP); err != nil {
		return payload, err
	}
	if err := redemptionField(rewards, "growthValue", &payload.Rewards.GrowthValue); err != nil {
		return payload, err
	}
	var items []json.RawMessage
	if err := redemptionField(rewards, "items", &items); err != nil {
		return payload, err
	}
	payload.Rewards.Items = []PetRedeemKeyRewardItem{}
	for _, item := range items {
		fields, err := redemptionObject(item, []string{"itemKey", "quantity"}, []string{"itemKey"})
		if err != nil {
			return payload, err
		}
		value := PetRedeemKeyRewardItem{Quantity: 1}
		if err := redemptionField(fields, "itemKey", &value.ItemKey); err != nil {
			return payload, err
		}
		if err := redemptionField(fields, "quantity", &value.Quantity); err != nil {
			return payload, err
		}
		payload.Rewards.Items = append(payload.Rewards.Items, value)
	}
	return payload, nil
}
func decodePetCompactRedemption(data []byte) (PetRedemptionPayload, error) {
	var p PetRedemptionPayload
	if len(data) != 28 || data[0] != 2 {
		return p, invalidPetRedemption("当前版本暂不支持该兑换 Key。")
	}
	if data[1] > 2 {
		return p, invalidPetRedemption("兑换 Key 道具数量不正确。")
	}
	day := func(offset int) string {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(binary.BigEndian.Uint16(data[offset:offset+2]))).Format("2006-01-02")
	}
	issued := day(14)
	p = PetRedemptionPayload{Version: 2, CampaignID: fmt.Sprintf("campaign-%08x", binary.BigEndian.Uint32(data[2:6])), Nonce: hex.EncodeToString(data[6:14]), IssuedAt: &issued, Rewards: PetRedeemKeyRewards{LP: int64(binary.BigEndian.Uint16(data[18:20])), GrowthValue: int64(binary.BigEndian.Uint16(data[20:22])), Items: []PetRedeemKeyRewardItem{}}}
	if binary.BigEndian.Uint16(data[16:18]) != 0xffff {
		expires := day(16)
		p.ExpiresAt = &expires
	}
	for n := 0; n < int(data[1]); n++ {
		offset := 22 + n*3
		code := binary.BigEndian.Uint16(data[offset : offset+2])
		quantity := int64(data[offset+2])
		if code == 0 || quantity <= 0 {
			return p, invalidPetRedemption("兑换 Key 道具内容不正确。")
		}
		found := false
		for _, item := range PetCatalog() {
			sum := sha256.Sum256([]byte(item.ItemKey))
			if binary.BigEndian.Uint16(sum[:2]) == code {
				p.Rewards.Items = append(p.Rewards.Items, PetRedeemKeyRewardItem{ItemKey: item.ItemKey, Quantity: quantity})
				found = true
				break
			}
		}
		if !found {
			return p, invalidPetRedemption(fmt.Sprintf("兑换奖励中包含未知道具编码：%d。", code))
		}
	}
	return p, nil
}
