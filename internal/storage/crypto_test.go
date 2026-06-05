package storage

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, keyLen)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

func TestBox_SealOpenRoundTrip(t *testing.T) {
	b, err := NewBox(testKey(t))
	require.NoError(t, err)
	require.True(t, b.Enabled())

	for _, pt := range [][]byte{[]byte("hello"), {}, make([]byte, 4096)} {
		blob, err := b.Seal(pt, []byte("aad"))
		require.NoError(t, err)
		require.Len(t, blob, len(pt)+b.Overhead())
		got, err := b.Open(blob, []byte("aad"))
		require.NoError(t, err)
		require.Equal(t, len(pt), len(got)) // GCM Open returns nil for empty plaintext
		if len(pt) > 0 {
			require.Equal(t, pt, got)
		}
	}
}

func TestBox_Rejections(t *testing.T) {
	require.Nil(t, (*Box)(nil))
	require.False(t, (*Box)(nil).Enabled())

	_, err := NewBox(make([]byte, 16))
	require.Error(t, err, "non-32-byte key rejected")

	b, _ := NewBox(testKey(t))
	blob, _ := b.Seal([]byte("secret"), []byte("aad"))

	// wrong AAD fails
	_, err = b.Open(blob, []byte("other"))
	require.Error(t, err)

	// tampered ciphertext fails
	bad := append([]byte(nil), blob...)
	bad[len(bad)-1] ^= 0xff
	_, err = b.Open(bad, []byte("aad"))
	require.Error(t, err)

	// wrong key fails
	b2, _ := NewBox(testKey(t))
	_, err = b2.Open(blob, []byte("aad"))
	require.Error(t, err)

	// too short
	_, err = b.Open([]byte{1, 2, 3}, nil)
	require.Error(t, err)
}

func TestLoadKey(t *testing.T) {
	raw := testKey(t)

	t.Run("unset-is-off", func(t *testing.T) {
		t.Setenv(EncryptionKeyEnv, "")
		t.Setenv(EncryptionKeyfileEnv, "")
		k, err := LoadKey()
		require.NoError(t, err)
		require.Nil(t, k)
	})
	t.Run("hex", func(t *testing.T) {
		t.Setenv(EncryptionKeyEnv, hex.EncodeToString(raw))
		k, err := LoadKey()
		require.NoError(t, err)
		require.Equal(t, raw, k)
	})
	t.Run("base64", func(t *testing.T) {
		t.Setenv(EncryptionKeyEnv, base64.StdEncoding.EncodeToString(raw))
		k, err := LoadKey()
		require.NoError(t, err)
		require.Equal(t, raw, k)
	})
	t.Run("malformed", func(t *testing.T) {
		t.Setenv(EncryptionKeyEnv, "too-short")
		_, err := LoadKey()
		require.Error(t, err)
	})
	t.Run("keyfile", func(t *testing.T) {
		t.Setenv(EncryptionKeyEnv, "")
		f := filepath.Join(t.TempDir(), "key")
		require.NoError(t, os.WriteFile(f, []byte(hex.EncodeToString(raw)+"\n"), 0o600))
		t.Setenv(EncryptionKeyfileEnv, f)
		k, err := LoadKey()
		require.NoError(t, err)
		require.Equal(t, raw, k)
	})
}
