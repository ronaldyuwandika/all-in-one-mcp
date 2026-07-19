// Package crypto encrypts vault payloads and manages the local master key.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zalando/go-keyring"
)

const service = "com.credential-vault.go"
const account = "vault-key"

type KeyStore interface {
	Get() ([]byte, error)
	Set([]byte) error
}

type SystemKeyStore struct{}

func (SystemKeyStore) Get() ([]byte, error) {
	if encoded := os.Getenv("CREDENTIAL_VAULT_TEST_KEY"); encoded != "" {
		return base64.RawURLEncoding.DecodeString(encoded)
	}
	encoded, err := keyring.Get(service, account)
	if err != nil {
		return nil, fmt.Errorf("read keychain: %w", err)
	}
	return base64.RawURLEncoding.DecodeString(encoded)
}

func (SystemKeyStore) Set(key []byte) error {
	if os.Getenv("CREDENTIAL_VAULT_TEST_KEY") != "" {
		return nil
	}
	if err := keyring.Set(service, account, base64.RawURLEncoding.EncodeToString(key)); err != nil {
		return fmt.Errorf("write keychain: %w", err)
	}
	return nil
}

type Fernet struct{ store KeyStore }

func New(store KeyStore) *Fernet { return &Fernet{store: store} }

// Probe verifies that a usable 256-bit key can be read or created locally.
func (f *Fernet) Probe() error {
	_, err := f.key()
	return err
}

func (f *Fernet) key() ([]byte, error) {
	key, err := f.store.Get()
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("vault key must be 32 bytes")
		}
		return key, nil
	}
	key = make([]byte, 32)
	if _, err = io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	if err = f.store.Set(key); err != nil {
		return nil, err
	}
	return key, nil
}

func (f *Fernet) Encrypt(plain []byte) (string, error) {
	key, err := f.key()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, plain, nil)), nil
}

func (f *Fernet) Decrypt(token string) ([]byte, error) {
	key, err := f.key()
	if err != nil {
		return nil, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("decode token: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("encrypted token is truncated")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}
	return plain, nil
}
