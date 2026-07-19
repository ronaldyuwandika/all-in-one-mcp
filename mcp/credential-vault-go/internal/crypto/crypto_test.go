package crypto

import (
	"bytes"
	"errors"
	"testing"
)

type memoryKeyStore struct{ key []byte }

func (m *memoryKeyStore) Get() ([]byte, error) {
	if m.key == nil {
		return nil, errors.New("missing")
	}
	return m.key, nil
}
func (m *memoryKeyStore) Set(k []byte) error { m.key = bytes.Clone(k); return nil }
func TestEncryptRoundTrip(t *testing.T) {
	t.Parallel()
	c := New(&memoryKeyStore{})
	token, err := c.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Decrypt(token)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "secret" {
		t.Fatalf("got %q", plain)
	}
	if token == "secret" {
		t.Fatal("ciphertext exposed plaintext")
	}
}
func TestDecryptRejectsTampering(t *testing.T) {
	t.Parallel()
	c := New(&memoryKeyStore{})
	token, err := c.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(token)
	raw[len(raw)-1] ^= 1
	if _, err = c.Decrypt(string(raw)); err == nil {
		t.Fatal("expected tamper error")
	}
}
