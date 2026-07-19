package bench

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vaultcrypto "github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/crypto"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/vault"
)

type keys struct{ key []byte }

func (k *keys) Get() ([]byte, error) {
	if k.key == nil {
		return nil, errors.New("missing")
	}
	return k.key, nil
}
func (k *keys) Set(v []byte) error { k.key = bytes.Clone(v); return nil }
func BenchmarkEncrypt(b *testing.B) {
	c := vaultcrypto.New(&keys{})
	payload := bytes.Repeat([]byte("x"), 1024)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.Encrypt(payload); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkDecrypt(b *testing.B) {
	c := vaultcrypto.New(&keys{})
	token, err := c.Encrypt(bytes.Repeat([]byte("x"), 1024))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err = c.Decrypt(token); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkMaskText(b *testing.B) {
	text := strings.Repeat("ordinary application log line without credentials\n", 210) + "GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz\n"
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		_ = vault.MaskText(text)
	}
}
func BenchmarkScanFiles(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 100; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%03d.env", i)), []byte(fmt.Sprintf("API_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz%d\n", i)), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		v := vault.New(b.TempDir(), vaultcrypto.New(&keys{}))
		if _, err := v.ScanDir(root, false); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkRedactFile1MB(b *testing.B) {
	content := bytes.Repeat([]byte("API_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz\n"), 25000)
	b.SetBytes(int64(len(content)))
	for b.Loop() {
		root := b.TempDir()
		path := filepath.Join(root, ".env")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			b.Fatal(err)
		}
		v := vault.New(b.TempDir(), vaultcrypto.New(&keys{}))
		if err := v.RedactFile(path); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkAuditAppend(b *testing.B) {
	v := vault.New(b.TempDir(), vaultcrypto.New(&keys{}))
	b.ResetTimer()
	for b.Loop() {
		if err := v.AppendAudit(vault.AuditEntry{Action: "benchmark"}); err != nil {
			b.Fatal(err)
		}
	}
}
