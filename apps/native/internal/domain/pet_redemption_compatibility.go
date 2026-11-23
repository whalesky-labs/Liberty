//go:build compatibility

package domain

import "crypto/ed25519"

func VerifyPetRedemptionContentForCompatibility(code string, public []byte) (PetRedemptionPayload, error) {
	return verifyPetRedemptionContent(code, ed25519.PublicKey(public))
}
