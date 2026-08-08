// Package crypto encrypts vault payloads and manages the local master key.
package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

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

type Fernet struct {
	store     KeyStore
	mu        sync.Mutex
	cachedKey []byte
}

func New(store KeyStore) *Fernet { return &Fernet{store: store} }

// Probe verifies that a usable 256-bit key can be read or created locally.
func (f *Fernet) Probe() error {
	_, err := f.key()
	return err
}

func (f *Fernet) key() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cachedKey != nil {
		return bytes.Clone(f.cachedKey), nil
	}
	key, err := f.store.Get()
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("vault key must be 32 bytes")
		}
		f.cachedKey = bytes.Clone(key)
		return bytes.Clone(f.cachedKey), nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return nil, fmt.Errorf("read keychain: %w", err)
	}
	key = make([]byte, 32)
	if _, err = io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	if err = f.store.Set(key); err != nil {
		return nil, fmt.Errorf("create keychain key: %w", err)
	}
	key, err = f.store.Get()
	if err != nil {
		return nil, fmt.Errorf("verify keychain key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("vault key must be 32 bytes")
	}
	f.cachedKey = bytes.Clone(key)
	return bytes.Clone(f.cachedKey), nil
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
	return f.DecryptReader(strings.NewReader(token))
}

func (f *Fernet) DecryptReader(r io.Reader) ([]byte, error) {
	reader, err := f.NewDecryptReader(r)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}

func (f *Fernet) NewDecryptReader(r io.Reader) (io.Reader, error) {
	key, err := f.key()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	decoded := base64.NewDecoder(base64.RawURLEncoding, r)
	nonce := make([]byte, 12)
	if _, err = io.ReadFull(decoded, nonce); err != nil {
		return nil, fmt.Errorf("read token nonce: %w", err)
	}
	j0 := make([]byte, 16)
	copy(j0, nonce)
	binary.BigEndian.PutUint32(j0[12:], 1)
	counter := bytes.Clone(j0)
	binary.BigEndian.PutUint32(counter[12:], 2)
	return &gcmReader{
		decoded:   decoded,
		stream:    cipher.NewCTR(block, counter),
		block:     block,
		j0:        j0,
		ghash:     newGHASH(block),
		pending:   make([]byte, 0, 16),
		readBuf:   make([]byte, 32*1024),
		keystream: make([]byte, 16),
	}, nil
}

type gcmReader struct {
	decoded       io.Reader
	stream        cipher.Stream
	block         cipher.Block
	j0            []byte
	ghash         *ghash
	pending       []byte
	plain         []byte
	readBuf       []byte
	keystream     []byte
	ciphertextLen uint64
	sourceEOF     bool
	verified      bool
	err           error
}

func (r *gcmReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.plain) == 0 && !r.verified && r.err == nil {
		if r.sourceEOF {
			r.verify()
			break
		}
		n, err := r.decoded.Read(r.readBuf)
		if n > 0 {
			data := make([]byte, len(r.pending)+n)
			copy(data, r.pending)
			copy(data[len(r.pending):], r.readBuf[:n])
			if len(data) <= 16 {
				r.pending = append(r.pending[:0], data...)
			} else {
				cut := len(data) - 16
				ciphertext := data[:cut]
				r.pending = append(r.pending[:0], data[cut:]...)
				r.ghash.Write(ciphertext)
				r.ciphertextLen += uint64(len(ciphertext))
				plain := make([]byte, len(ciphertext))
				r.stream.XORKeyStream(plain, ciphertext)
				r.plain = append(r.plain, plain...)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				r.sourceEOF = true
			} else {
				r.err = fmt.Errorf("read token stream: %w", err)
			}
		}
	}
	if len(r.plain) > 0 {
		n := copy(p, r.plain)
		r.plain = r.plain[n:]
		return n, nil
	}
	if r.err != nil {
		return 0, r.err
	}
	if r.verified {
		return 0, io.EOF
	}
	return 0, nil
}

func (r *gcmReader) verify() {
	if r.verified || r.err != nil {
		return
	}
	if len(r.pending) != 16 {
		r.err = errors.New("encrypted token is truncated")
		return
	}
	tag := r.ghash.Sum(r.ciphertextLen)
	mask := make([]byte, 16)
	r.block.Encrypt(mask, r.j0)
	for i := range tag {
		tag[i] ^= mask[i]
	}
	if subtle.ConstantTimeCompare(tag, r.pending) != 1 {
		r.err = errors.New("decrypt token: authentication failed")
		return
	}
	r.verified = true
}

type ghash struct {
	h   [16]byte
	x   [16]byte
	buf []byte
}

func newGHASH(block cipher.Block) *ghash {
	g := &ghash{}
	block.Encrypt(g.h[:], make([]byte, 16))
	return g
}

func (g *ghash) Write(data []byte) {
	g.buf = append(g.buf, data...)
	for len(g.buf) >= 16 {
		g.mix(g.buf[:16])
		g.buf = g.buf[16:]
	}
}

func (g *ghash) Sum(length uint64) []byte {
	if len(g.buf) > 0 {
		var block [16]byte
		copy(block[:], g.buf)
		g.mix(block[:])
	}
	var lengths [16]byte
	binary.BigEndian.PutUint64(lengths[8:], length*8)
	g.mix(lengths[:])
	return bytes.Clone(g.x[:])
}

func (g *ghash) mix(block []byte) {
	for i := range g.x {
		g.x[i] ^= block[i]
	}
	var z [16]byte
	v := g.h
	for i := 0; i < 128; i++ {
		if g.x[i/8]&(byte(1)<<uint(7-i%8)) != 0 {
			for j := range z {
				z[j] ^= v[j]
			}
		}
		lsb := v[15] & 1
		for j := len(v) - 1; j > 0; j-- {
			v[j] = v[j]>>1 | v[j-1]<<7
		}
		v[0] >>= 1
		if lsb != 0 {
			v[0] ^= 0xe1
		}
	}
	g.x = z
}
