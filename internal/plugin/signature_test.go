package plugin

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifySignature(t *testing.T) {
	// Generate keypair.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Create temp file.
	f, err := os.CreateTemp(t.TempDir(), "sig-test-*")
	require.NoError(t, err)
	_, err = f.WriteString("hello clockwork")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Compute hash and sign.
	hash, err := fileHash(f.Name())
	require.NoError(t, err)
	sig := ed25519.Sign(priv, hash)
	sigHex := hex.EncodeToString(sig)

	err = VerifySignature(f.Name(), sigHex, pub)
	assert.NoError(t, err)
}

func TestVerifySignature_BadSignature(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	f, err := os.CreateTemp(t.TempDir(), "sig-test-*")
	require.NoError(t, err)
	_, err = f.WriteString("hello clockwork")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	badSig := hex.EncodeToString(make([]byte, ed25519.SignatureSize))
	err = VerifySignature(f.Name(), badSig, pub)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "signature verification failed")
}

func TestVerifySignature_MissingFile(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	err = VerifySignature("/nonexistent/path/file.bin", "deadbeef", pub)
	assert.Error(t, err)
}

func TestVerifyChecksum(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "chk-test-*")
	require.NoError(t, err)
	content := []byte("checksum content")
	_, err = f.Write(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	h := sha256.Sum256(content)
	expectedHex := hex.EncodeToString(h[:])

	err = VerifyChecksum(f.Name(), expectedHex)
	assert.NoError(t, err)
}

func TestVerifyChecksum_Mismatch(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "chk-test-*")
	require.NoError(t, err)
	_, err = f.WriteString("some content")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	err = VerifyChecksum(f.Name(), "0000000000000000000000000000000000000000000000000000000000000000")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
}
