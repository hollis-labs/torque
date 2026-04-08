package plugin

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// VerifySignature checks an ed25519 signature against a file's SHA-256 hash.
func VerifySignature(filePath string, signatureHex string, publicKey ed25519.PublicKey) error {
	hash, err := fileHash(filePath)
	if err != nil {
		return fmt.Errorf("hash file: %w", err)
	}

	sig, err := hex.DecodeString(signatureHex)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	if !ed25519.Verify(publicKey, hash, sig) {
		return fmt.Errorf("signature verification failed for %s", filePath)
	}
	return nil
}

// VerifyChecksum verifies a file's SHA-256 checksum.
func VerifyChecksum(filePath, expectedHex string) error {
	hash, err := fileHash(filePath)
	if err != nil {
		return err
	}
	actual := hex.EncodeToString(hash)
	if actual != expectedHex {
		return fmt.Errorf("checksum mismatch: got %s, want %s", actual, expectedHex)
	}
	return nil
}

func fileHash(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
