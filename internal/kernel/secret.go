package kernel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Sealer encrypts secrets at rest with AES-256-GCM (SDD §14.1, T-043).
type Sealer struct {
	aead cipher.AEAD
	key  []byte
}

// NewSealer returns a sealer for a 32-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("the secret key is %d bytes; it must be 32", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead, key: append([]byte{}, key...)}, nil
}

// SealerFromBase64 returns a sealer for a base64 key, as in SPECCY_MASTER_KEY.
func SealerFromBase64(s string) (*Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("the master key is not base64: %w", err)
	}
	return NewSealer(key)
}

// LocalSealer reads the key file at path, and creates it with mode 0600 on first use.
func LocalSealer(path string) (*Sealer, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		_, werr := f.WriteString(base64.StdEncoding.EncodeToString(key))
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return nil, werr
		}
		return NewSealer(key)
	}
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other users; run chmod 600 %s", path, path)
	}
	return SealerFromBase64(string(raw))
}

// Seal encrypts plain. The nonce is stored in front of the ciphertext.
func (s *Sealer) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, nil), nil
}

// ErrOtherKey is the error of Open for a secret that another key sealed: the key file was
// lost and Speccy made a new one, or SPECCY_MASTER_KEY changed. The caller says with OtherKey
// which secret it is and where a person enters it again.
var ErrOtherKey = errors.New("the secret was sealed with another key; the key file or SPECCY_MASTER_KEY changed")

// Open decrypts what Seal returned.
func (s *Sealer) Open(sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("the sealed secret is too short")
	}
	plain, err := s.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, ErrOtherKey
	}
	return plain, nil
}

// OtherKey is the error at use of a secret that another key sealed. what names the secret, such
// as "The secret of the Claude backend", and where the admin page that takes it again.
func OtherKey(what, where string) *Error {
	return Invalid("secret_other_key", "%s was sealed with another key: the key file or SPECCY_MASTER_KEY changed after it was stored. "+
		"Enter the secret again in %s.", what, where)
}

// Last4 returns the last 4 characters of a secret, which is all the API shows (SDD §14.1).
func Last4(secret string) string {
	r := []rune(secret)
	if len(r) <= 4 {
		return strings.Repeat("•", len(r))
	}
	return string(r[len(r)-4:])
}

// MAC returns an HMAC-SHA256 of data under a key derived from the sealer key for purpose, so
// a signature for one purpose never verifies for another.
func (s *Sealer) MAC(purpose string, data []byte) []byte {
	k := hmac.New(sha256.New, s.key)
	k.Write([]byte("speccy-mac:" + purpose))
	m := hmac.New(sha256.New, k.Sum(nil))
	m.Write(data)
	return m.Sum(nil)
}
