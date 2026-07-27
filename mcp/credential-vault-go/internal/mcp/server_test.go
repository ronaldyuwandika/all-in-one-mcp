package mcp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vaultcrypto "github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/crypto"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/vault"
)

type testKeys struct{ key []byte }

func (k *testKeys) Get() ([]byte, error) {
	if k.key == nil {
		return nil, errors.New("missing")
	}
	return k.key, nil
}

func (k *testKeys) Set(value []byte) error {
	k.key = bytes.Clone(value)
	return nil
}

func TestScanReturnsMetadataWithoutCredentialValues(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	secret := "ghp_" + strings.Repeat("x", 24)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("GITHUB_TOKEN="+secret), 0o600); err != nil {
		t.Fatal(err)
	}
	v := vault.New(t.TempDir(), vaultcrypto.New(&testKeys{}))
	result, err := scan(v, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Count == 0 || result.Count != len(result.Credentials) {
		t.Fatalf("result=%+v", result)
	}
	for _, name := range result.Credentials {
		if strings.Contains(name, secret) {
			t.Fatal("scan response contains credential value")
		}
	}
}
