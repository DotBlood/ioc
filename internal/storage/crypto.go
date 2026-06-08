package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// At-rest encryption (opt-in, AES-256-GCM, stdlib only). A Box wraps an AEAD; a
// nil *Box means encryption is OFF and every code path is byte-identical to an
// unencrypted store. The key is a raw 32 bytes resolved by LoadKey from the
// environment. LOSING THE KEY MEANS LOSING THE DATA — there is no recovery and no
// in-place migration. See docs/encryption.md.
const (
	// EncryptionKeyEnv holds the key as hex (64 chars), base64, or raw 32 bytes.
	EncryptionKeyEnv = "IOC_ENCRYPTION_KEY"
	// EncryptionKeyfileEnv points to a file whose contents decode to the 32-byte key.
	EncryptionKeyfileEnv = "IOC_ENCRYPTION_KEYFILE"

	keyLen      = 32 // AES-256
	gcmNonceLen = 12
)

// Box is an AES-256-GCM sealing box. The zero value is unusable; use NewBox. A nil
// *Box is valid and reports Enabled()==false (encryption off).
type Box struct {
	aead cipher.AEAD
}

// NewBox builds a Box from a raw 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("storage: encryption key must be %d bytes, got %d", keyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("storage: aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("storage: gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Enabled reports whether b encrypts (b != nil).
func (b *Box) Enabled() bool { return b != nil }

// Overhead is the number of bytes Seal adds (nonce + GCM tag). 0 when off.
func (b *Box) Overhead() int {
	if b == nil {
		return 0
	}
	return gcmNonceLen + b.aead.Overhead()
}

// Seal returns nonce || ciphertext || tag. aad is authenticated but not encrypted
// (use it to bind the ciphertext to its address, e.g. a content hash or record id).
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, gcmNonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("storage: nonce: %w", err)
	}
	// Seal appends the ciphertext+tag to dst (the nonce), giving nonce||ct||tag.
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Open reverses Seal. A wrong key, tampered blob, or wrong aad fails authentication.
func (b *Box) Open(blob, aad []byte) ([]byte, error) {
	if len(blob) < gcmNonceLen {
		return nil, fmt.Errorf("storage: ciphertext too short")
	}
	nonce, ct := blob[:gcmNonceLen], blob[gcmNonceLen:]
	pt, err := b.aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, fmt.Errorf("storage: decrypt: %w (wrong key or corrupted data)", err)
	}
	return pt, nil
}

// LoadKey resolves the at-rest key from the environment, returning (nil, nil) when
// neither variable is set (encryption OFF). A set-but-malformed key is an error.
func LoadKey() ([]byte, error) {
	if v := strings.TrimSpace(os.Getenv(EncryptionKeyEnv)); v != "" {
		return decodeKey(v, EncryptionKeyEnv)
	}
	if path := strings.TrimSpace(os.Getenv(EncryptionKeyfileEnv)); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("storage: read %s: %w", EncryptionKeyfileEnv, err)
		}
		return decodeKey(strings.TrimSpace(string(data)), EncryptionKeyfileEnv)
	}
	return nil, nil
}

// decodeKey accepts hex (64 chars), base64 (std or raw-url), or raw 32 bytes.
func decodeKey(s, src string) ([]byte, error) {
	if b, err := hex.DecodeString(s); err == nil && len(b) == keyLen {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == keyLen {
			return b, nil
		}
	}
	if len(s) == keyLen {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("storage: %s must decode to %d bytes (hex, base64, or raw)", src, keyLen)
}
