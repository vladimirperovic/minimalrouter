package firmware

import (
	"crypto/ed25519"
	"encoding/hex"
)

// SignManifest signs the canonical manifest payload after callers set release
// metadata. The manifest-provided public key remains informational only.
func SignManifest(manifest *FirmwareManifest, privateKey ed25519.PrivateKey) error {
	payload, err := signedPayload(manifest)
	if err != nil {
		return err
	}
	manifest.Signature = hex.EncodeToString(ed25519.Sign(privateKey, payload))
	manifest.PublicKey = hex.EncodeToString(privateKey.Public().(ed25519.PublicKey))
	return nil
}
