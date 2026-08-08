package mcp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/zalando/go-keyring"

	vaultcrypto "github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault/internal/crypto"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault/internal/vault"
)

type testKeys struct {
	key      []byte
	getCalls int
	setCalls int
}

func (k *testKeys) Get() ([]byte, error) {
	k.getCalls++
	if k.key == nil {
		return nil, keyring.ErrNotFound
	}
	return k.key, nil
}

func (k *testKeys) Set(value []byte) error {
	k.setCalls++
	k.key = bytes.Clone(value)
	return nil
}

func TestScanReturnsMetadataWithoutCredentialValues(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	secret := "ghp_" + strings.Repeat("x", 24)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("GITHUB_TOKEN=[REDACTED], 0o600); err != nil {
		t.Fatal(err)
	}
	keys := &testKeys{}
	v := vault.New(t.TempDir(), vaultcrypto.New(keys))
	result, err := scan(v, root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Count == 0 || result.Count != len(result.Credentials) {
		t.Fatalf("result=%+v", result)
	}
	if keys.getCalls != 0 || keys.setCalls != 0 {
		t.Fatalf("scan used keychain: gets=%d sets=%d", keys.getCalls, keys.setCalls)
	}
	for _, name := range result.Credentials {
		if strings.Contains(name, secret) {
			t.Fatal("scan response contains credential value")
		}
	}
}

func TestMCPToolListCompatibility(t *testing.T) {
	t.Parallel()
	tools := newServer(vault.New(t.TempDir(), vaultcrypto.New(&testKeys{}))).ListTools()
	want := []string{"vault_audit", "vault_chat_clear", "vault_get", "vault_mask", "vault_redact", "vault_restore", "vault_scan", "vault_set", "vault_status"}
	if len(tools) != len(want) {
		t.Fatalf("tool count=%d, want %d", len(tools), len(want))
	}
	for _, name := range want {
		if _, ok := tools[name]; !ok {
			t.Fatalf("missing tool %q", name)
		}
	}
	if _, ok := tools["run_safe"]; ok {
		t.Fatal("unsupported run_safe tool is registered")
	}
}

func TestMCPScanHandlerDoesNotTouchKeychain(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=[REDACTED]+strings.Repeat("x", 24)), 0o600); err != nil {
		t.Fatal(err)
	}
	keys := &testKeys{}
	tool := newServer(vault.New(t.TempDir(), vaultcrypto.New(keys))).GetTool("vault_scan")
	result, err := tool.Handler(context.Background(), sdk.CallToolRequest{Params: sdk.CallToolParams{Name: "vault_scan", Arguments: map[string]any{"path": root}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("scan failed: %+v", result)
	}
	if keys.getCalls != 0 || keys.setCalls != 0 {
		t.Fatalf("MCP scan used keychain: gets=%d sets=%d", keys.getCalls, keys.setCalls)
	}
}
