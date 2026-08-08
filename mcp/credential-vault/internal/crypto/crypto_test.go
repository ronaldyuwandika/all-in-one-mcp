package crypto

import (
	"bytes"
	"os"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

type memoryKeyStore struct{ key []byte }

func (m *memoryKeyStore) Get() ([]byte, error) {
	if m.key == nil {
		return nil, keyring.ErrNotFound
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

type errorKeyStore struct{}

func (errorKeyStore) Get() ([]byte, error) { return nil, os.ErrPermission }
func (errorKeyStore) Set([]byte) error     { return nil }

func TestKeychainDenialDoesNotCreateKey(t *testing.T) {
	c := New(errorKeyStore{})
	if _, err := c.Encrypt([]byte("secret")); err == nil {
		t.Fatal("expected keychain denial")
	}
}

func TestConcurrentKeyInitialization(t *testing.T) {
	c := New(&memoryKeyStore{})
	const n = 32
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := c.Encrypt([]byte("secret"))
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

type coordinatedKeyStore struct {
	mu      sync.Mutex
	key     []byte
	sets    int
	barrier chan struct{}
}

func (s *coordinatedKeyStore) Get() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return nil, keyring.ErrNotFound
	}
	return bytes.Clone(s.key), nil
}

func (s *coordinatedKeyStore) Set(key []byte) error {
	s.mu.Lock()
	s.sets++
	if s.sets == 2 {
		close(s.barrier)
	}
	s.mu.Unlock()
	<-s.barrier
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		s.key = bytes.Clone(key)
	}
	return nil
}

func TestIndependentKeyInitializationUsesStoredWinner(t *testing.T) {
	store := &coordinatedKeyStore{barrier: make(chan struct{})}
	first, second := New(store), New(store)
	var wg sync.WaitGroup
	tokens := make(chan string, 2)
	for _, c := range []*Fernet{first, second} {
		wg.Add(1)
		go func(c *Fernet) {
			defer wg.Done()
			token, err := c.Encrypt([]byte("secret"))
			if err != nil {
				t.Errorf("encrypt: %v", err)
				return
			}
			tokens <- token
		}(c)
	}
	wg.Wait()
	close(tokens)
	for token := range tokens {
		if _, err := New(store).Decrypt(token); err != nil {
			t.Fatalf("stored winner cannot decrypt token: %v", err)
		}
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
