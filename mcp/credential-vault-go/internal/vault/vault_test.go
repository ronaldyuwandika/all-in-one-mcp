package vault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vaultcrypto "github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/crypto"
)

type testKeys struct{ key []byte }

func (k *testKeys) Get() ([]byte, error) {
	if k.key == nil {
		return nil, errors.New("missing")
	}
	return k.key, nil
}
func (k *testKeys) Set(v []byte) error { k.key = bytes.Clone(v); return nil }
func testVault(t *testing.T) *Vault {
	t.Helper()
	return New(t.TempDir(), vaultcrypto.New(&testKeys{}))
}
func TestSetGetAudit(t *testing.T) {
	t.Parallel()
	v := testVault(t)
	if err := v.Set("chat.token", "top-secret", "test"); err != nil {
		t.Fatal(err)
	}
	got, err := v.Get("chat.token", "unit test")
	if err != nil {
		t.Fatal(err)
	}
	if got != "top-secret" {
		t.Fatalf("got %q", got)
	}
	a, err := v.Audit(0)
	if err != nil || len(a) != 2 {
		t.Fatalf("audit=%v err=%v", a, err)
	}
	raw, _ := os.ReadFile(v.vaultPath())
	if strings.Contains(string(raw), "top-secret") {
		t.Fatal("vault file contains plaintext")
	}
}
func TestScanRedactRestore(t *testing.T) {
	t.Parallel()
	v := testVault(t)
	root := t.TempDir()
	p := filepath.Join(root, ".env")
	original := "GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz\nSAFE=value\n"
	if err := os.WriteFile(p, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := v.ScanDir(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("no credentials detected")
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "ghp_abcdefghijklmnopqrstuvwxyz") {
		t.Fatal("secret remains after redaction")
	}
	n, err := v.Restore()
	if err != nil || n != 1 {
		t.Fatalf("restored=%d err=%v", n, err)
	}
	raw, _ = os.ReadFile(p)
	if string(raw) != original {
		t.Fatalf("restore mismatch: %q", raw)
	}
}
func TestMaskText(t *testing.T) {
	t.Parallel()
	in := "token ghp_abcdefghijklmnopqrstuvwxyz and PASSWORD=hunter2secret"
	out := MaskText(in)
	if strings.Contains(out, "ghp_") || strings.Contains(out, "hunter2secret") {
		t.Fatalf("unmasked: %s", out)
	}
}
