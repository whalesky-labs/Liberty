package domain

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

func testRedemptionKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, private
}
func signedPetJSON(private ed25519.PrivateKey, data string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(data))
	sig := ed25519.Sign(private, []byte(payload))
	return "LIB1." + payload + "." + base64.RawURLEncoding.EncodeToString(sig)
}
func TestPetSignedRedemptionValidatesRewardsAndUTCExpiration(t *testing.T) {
	pub, private := testRedemptionKey(t)
	now := time.Date(2026, 11, 18, 23, 50, 0, 0, time.FixedZone("test", -8*3600))
	valid := `{"version":1,"campaignId":"synthetic","nonce":"test-1","expiresAt":"2026-11-19","rewards":{"lp":75,"growthValue":23,"items":[{"itemKey":"wheat-seed"}]}}`
	code := signedPetJSON(private, valid)
	got, err := verifyPetRedemption(code, now, pub)
	if err != nil || got.Rewards.LP != 75 || got.Rewards.GrowthValue != 23 || len(got.Rewards.Items) != 1 || got.Rewards.Items[0].Quantity != 1 {
		t.Fatalf("verified payload mismatch: %v", err)
	}
	tests := []struct {
		name, data string
		want       error
	}{
		{"expired", strings.ReplaceAll(valid, "2026-11-19", "2026-11-18"), ErrPetRedemptionExpired},
		{"invalid date", strings.ReplaceAll(valid, "2026-11-19", "2026-02-30"), ErrPetRedemption},
		{"missing campaign", strings.ReplaceAll(valid, `"campaignId":"synthetic"`, `"campaignId":" "`), ErrPetRedemption},
		{"missing nonce", strings.ReplaceAll(valid, `"nonce":"test-1"`, `"nonce":" "`), ErrPetRedemption},
		{"unsupported version", strings.ReplaceAll(valid, `"version":1`, `"version":4`), ErrPetRedemption},
		{"zero quantity", strings.ReplaceAll(valid, `"wheat-seed"}`, `"wheat-seed","quantity":0}`), ErrPetRedemption},
		{"unknown item", strings.ReplaceAll(valid, "wheat-seed", "does-not-exist"), ErrPetRedemption},
		{"no reward", `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{}}`, ErrPetRedemption},
		{"missing rewards", `{"version":1,"campaignId":"synthetic","nonce":"a"}`, ErrPetRedemption},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := verifyPetRedemption(signedPetJSON(private, tc.data), now, pub); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	parts := strings.Split(code, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(strings.ReplaceAll(valid, "75", "750")))
	if _, err := verifyPetRedemption(strings.Join(parts, "."), now, pub); !errors.Is(err, ErrPetRedemption) {
		t.Fatal("tampered signed payload accepted")
	}
	other, _ := testRedemptionKey(t)
	if _, err := verifyPetRedemption(code, now, other); !errors.Is(err, ErrPetRedemption) {
		t.Fatal("wrong signer accepted")
	}
	if _, err := VerifyPetRedemption(code, now); !errors.Is(err, ErrPetRedemption) {
		t.Fatal("test signer accepted by production verifier")
	}
}
func TestPetCompactRedemptionMatchesBinaryProtocol(t *testing.T) {
	pub, private := testRedemptionKey(t)
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	data := make([]byte, 28)
	data[0], data[1] = 2, 2
	binary.BigEndian.PutUint32(data[2:6], 0x10203040)
	copy(data[6:14], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	binary.BigEndian.PutUint16(data[14:16], 321)
	binary.BigEndian.PutUint16(data[16:18], 0xffff)
	binary.BigEndian.PutUint16(data[18:20], 65535)
	binary.BigEndian.PutUint16(data[20:22], 123)
	for n, key := range []string{"wheat-seed", "sandwich-food"} {
		sum := sha256.Sum256([]byte(key))
		copy(data[22+n*3:24+n*3], sum[:2])
		data[24+n*3] = byte(n + 1)
	}
	sign := func(b []byte) string {
		token := append(append([]byte(nil), b...), ed25519.Sign(private, b)...)
		return "LIB2." + base64.RawURLEncoding.EncodeToString(token)
	}
	got, err := verifyPetRedemption(sign(data), now, pub)
	if err != nil || got.CampaignID != "campaign-10203040" || got.Nonce != "0102030405060708" || got.IssuedAt == nil || *got.IssuedAt != "2026-11-18" || got.ExpiresAt != nil || got.Rewards.LP != 65535 || len(got.Rewards.Items) != 2 || got.Rewards.Items[1].ItemKey != "sandwich-food" {
		t.Fatalf("compact fields mismatch: %v", err)
	}
	for _, mutate := range []func([]byte){func(b []byte) { b[0] = 3 }, func(b []byte) { b[1] = 3 }, func(b []byte) { b[24] = 0 }, func(b []byte) { b[22], b[23] = 0, 0 }} {
		bad := append([]byte(nil), data...)
		mutate(bad)
		if _, err := verifyPetRedemption(sign(bad), now, pub); !errors.Is(err, ErrPetRedemption) {
			t.Fatal("invalid compact payload accepted")
		}
	}
	for n := 0; n < 92; n++ {
		if _, err := verifyPetRedemption("LIB2."+base64.RawURLEncoding.EncodeToString(make([]byte, n)), now, pub); err == nil {
			t.Fatal("invalid compact length accepted")
		}
	}
}
func TestPetRedemptionNormalizationAndUnsupportedShortCodes(t *testing.T) {
	if NormalizePetRedemption(" \tLIB1.a\n.b\u3000") != "LIB1.a.b" {
		t.Fatal("normalization changed")
	}
	if len(PetRedemptionHash("LIB1.test.signature")) != 64 || !strings.HasPrefix(PetRedemptionPrefix("LIB1.test.signature"), "LIB1-") {
		t.Fatal("redacted identity changed")
	}
	if _, err := VerifyPetRedemption("TEST-000000000000", time.Now()); !errors.Is(err, ErrPetRedemptionUnsupported) {
		t.Fatal("short code must fail closed")
	}
	for _, code := range []string{"", strings.Repeat("a", 32769), "LIB1.a.a", "LIB2.a"} {
		if _, err := VerifyPetRedemption(code, time.Now()); err == nil {
			t.Fatal("invalid code accepted")
		}
	}
}

func TestPetSignedRedemptionRejectsKnownDuplicateNullAndCaseVariants(t *testing.T) {
	public, private := testRedemptionKey(t)
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for name, payload := range map[string]string{
		"duplicate identity": `{"version":1,"campaignId":"synthetic","campaignId":"synthetic","nonce":"a","rewards":{"lp":1}}`,
		"duplicate reward":   `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1,"lp":1}}`,
		"duplicate quantity": `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"items":[{"itemKey":"wheat-seed","quantity":1,"quantity":1}]}}`,
		"wrong field case":   `{"Version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1}}`,
		"null quantity":      `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"items":[{"itemKey":"wheat-seed","quantity":null}]}}`,
		"null LP":            `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":null,"growthValue":1}}`,
		"null growth":        `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1,"growthValue":null}}`,
		"null items":         `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1,"items":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifyPetRedemption(signedPetJSON(private, payload), now, public); !errors.Is(err, ErrPetRedemption) {
				t.Fatal("signed payload rejected by Serde was accepted", err)
			}
		})
	}
	unknown := `{"version":1,"campaignId":"synthetic","nonce":"a","unknown":{"version":null},"unknown":2,"rewards":{"lp":1}}`
	if _, err := verifyPetRedemption(signedPetJSON(private, unknown), now, public); err != nil {
		t.Fatal("unknown fields changed the existing Serde policy", err)
	}
}

func redemptionUnicodePayload(field, raw string) string {
	campaign, nonce, extra := `"synthetic"`, `"unicode"`, ""
	rewards := `{"lp":1}`
	switch field {
	case "campaignId":
		campaign = raw
	case "nonce":
		nonce = raw
	case "issuedAt", "expiresAt":
		extra = `,"` + field + `":` + raw
	case "itemKey":
		rewards = `{"lp":1,"items":[{"itemKey":` + raw + `}]}`
	case "root":
		extra = "," + raw
	case "rewards":
		rewards = `{"lp":1,` + raw + `}`
	case "item":
		rewards = `{"lp":1,"items":[{"itemKey":"wheat-seed",` + raw + `}]}`
	default:
		panic("unknown synthetic unicode target")
	}
	return `{"version":1,"campaignId":` + campaign + `,"nonce":` + nonce + `,"rewards":` + rewards + extra + `}`
}

func TestPetSignedRedemptionUnicodeStringPolicy(t *testing.T) {
	public, private := testRedemptionKey(t)
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	verify := func(t *testing.T, field, raw string, rejected bool) {
		t.Helper()
		_, err := verifyPetRedemption(signedPetJSON(private, redemptionUnicodePayload(field, raw)), now, public)
		if rejected {
			if !errors.Is(err, ErrPetRedemption) || err.Error() != "兑换 Key 内容无法解析。" {
				t.Fatal("malformed UTF-16 escape did not fail during signed content parsing", err)
			}
		} else if err != nil {
			t.Fatal("valid string or ignored unknown value was rejected", err)
		}
	}
	for _, field := range []string{"campaignId", "nonce", "issuedAt", "expiresAt", "itemKey"} {
		for _, raw := range []string{`"\ud800"`, `"\udc00"`} {
			t.Run(field+"/"+raw, func(t *testing.T) { verify(t, field, raw, true) })
		}
	}
	for _, raw := range []string{`"\ud800\u0041"`, `"\ud800\ud800"`, `"\ud800x"`, `"\udc00\ud800"`, `"\ud800\\udc00"`, `"\\ud800\udc00"`, `"\uD800"`} {
		t.Run("broken-pair/"+raw, func(t *testing.T) { verify(t, "campaignId", raw, true) })
	}
	for _, field := range []string{"root", "rewards", "item"} {
		for _, raw := range []string{`"\ud800":1`, `"\udc00":1`} {
			t.Run("field-name/"+field+"/"+raw, func(t *testing.T) { verify(t, field, raw, true) })
		}
		for _, raw := range []string{`"unknown":"\ud800"`, `"unknown":"\udc00"`, `"unknown":{"\ud800":"\udc00"}`, `"unknown":[{"\udc00":"\ud800"},"\udc00"]`} {
			t.Run("ignored-value/"+field+"/"+raw, func(t *testing.T) { verify(t, field, raw, false) })
		}
	}
	for _, field := range []string{"campaignId", "nonce", "issuedAt"} {
		for _, raw := range []string{`"\ud83d\ude00"`, `"�"`, `"\ufffd"`, `"\\ud800"`} {
			t.Run("valid-string/"+field+"/"+raw, func(t *testing.T) { verify(t, field, raw, false) })
		}
	}
	for _, raw := range []string{`"\ud83d\ude00":1`, `"�":1`, `"\\ud800":1`, `"unknown":"\ud800","unknown":"\udc00"`} {
		t.Run("valid-unknown-name/"+raw, func(t *testing.T) { verify(t, "root", raw, false) })
	}
	for _, field := range []string{"issuedAt", "expiresAt"} {
		t.Run("nullable/"+field, func(t *testing.T) { verify(t, field, "null", false) })
	}
	for _, scenario := range []struct{ raw, expected string }{
		{`"\ud83d\ude00"`, "😀"},
		{`"�"`, "�"},
		{`"\ufffd"`, "�"},
		{`"\\ud800"`, `\ud800`},
	} {
		payload, err := verifyPetRedemption(signedPetJSON(private, redemptionUnicodePayload("nonce", scenario.raw)), now, public)
		if err != nil || payload.Nonce != scenario.expected {
			t.Fatal("valid Unicode identity was replaced or normalized", err)
		}
	}
}
