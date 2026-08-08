// Package mcp exposes credential-vault operations through local stdio MCP.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault/internal/vault"
)

func Serve(v *vault.Vault) error { return server.ServeStdio(newServer(v)) }

func newServer(v *vault.Vault) *server.MCPServer {
	s := server.NewMCPServer("credentials-vault", "1.0.0", server.WithInstructions("Local-only credential broker. Store and fetch secrets with vault_set/vault_get; use vault_mask before returning command output. Never send credential values to network tools."))
	add := func(t sdk.Tool, h server.ToolHandlerFunc) { s.AddTool(t, h) }
	add(sdk.NewTool("vault_status", sdk.WithDescription("List local credential metadata without values")), jsonHandler(func(_ map[string]any) (any, error) { return v.Stats() }))
	add(sdk.NewTool("vault_get", sdk.WithDescription("Retrieve a local credential and audit the purpose"), sdk.WithString("name", sdk.Required()), sdk.WithString("purpose", sdk.Required())), textHandler(func(a map[string]any) (string, error) { return v.Get(str(a, "name"), str(a, "purpose")) }))
	add(sdk.NewTool("vault_set", sdk.WithDescription("Store a credential locally; the value never leaves this process"), sdk.WithString("name", sdk.Required()), sdk.WithString("value", sdk.Required())), jsonHandler(func(a map[string]any) (any, error) {
		name := "chat." + str(a, "name")
		return map[string]string{"stored": name}, v.Set(name, str(a, "value"), "mcp")
	}))
	add(sdk.NewTool("vault_chat_clear", sdk.WithDescription("Remove credentials supplied through chat")), jsonHandler(func(_ map[string]any) (any, error) { n, e := v.ClearChat(); return map[string]int{"cleared": n}, e }))
	add(sdk.NewTool("vault_mask", sdk.WithDescription("Redact credential patterns from text"), sdk.WithString("text", sdk.Required())), textHandler(func(a map[string]any) (string, error) { return vault.MaskText(str(a, "text")), nil }))
	add(sdk.NewTool("vault_scan", sdk.WithDescription("Detect credential patterns in a scoped local directory without changing files or vault state; redact=true is rejected for compatibility"), sdk.WithString("path"), sdk.WithBoolean("redact")), jsonHandler(func(a map[string]any) (any, error) {
		if redact, supplied := a["redact"].(bool); supplied && redact {
			return nil, errors.New("vault_scan redact=true is no longer supported; call vault_redact with an explicit path")
		}
		p := str(a, "path")
		if p == "" {
			p = "."
		}
		return scan(v, p)
	}))
	add(sdk.NewTool("vault_redact", sdk.WithDescription("Redact detected credentials in a scoped local directory and save encrypted backups"), sdk.WithString("path", sdk.Required())), jsonHandler(func(a map[string]any) (any, error) {
		return scanAndRedact(v, str(a, "path"))
	}))
	add(sdk.NewTool("vault_restore", sdk.WithDescription("Restore files from encrypted local backups")), restoreHandler(v))
	add(sdk.NewTool("vault_audit", sdk.WithDescription("Read local credential access audit entries")), jsonHandler(func(_ map[string]any) (any, error) { return v.Audit(50) }))
	return s
}

type scanResult struct {
	Count       int      `json:"count"`
	Credentials []string `json:"credentials"`
}

func scan(v *vault.Vault, path string) (scanResult, error) {
	found, err := v.DetectDir(path)
	if err != nil {
		return scanResult{}, err
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
		delete(found, name)
	}
	sort.Strings(names)
	return scanResult{Count: len(names), Credentials: names}, nil
}

func scanAndRedact(v *vault.Vault, path string) (scanResult, error) {
	found, err := v.RedactDir(path)
	if err != nil {
		return scanResult{}, err
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return scanResult{Count: len(names), Credentials: names}, nil
}

func restoreHandler(v *vault.Vault) server.ToolHandlerFunc {
	return func(_ context.Context, r sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		n, err := v.Restore()
		result := map[string]any{"restored": n}
		if err != nil {
			result["error"] = err.Error()
		}
		raw, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return sdk.NewToolResultError(marshalErr.Error()), nil
		}
		if err != nil {
			return sdk.NewToolResultError(string(raw)), nil
		}
		return sdk.NewToolResultText(string(raw)), nil
	}
}

func str(a map[string]any, k string) string { s, _ := a[k].(string); return s }
func textHandler(fn func(map[string]any) (string, error)) server.ToolHandlerFunc {
	return func(_ context.Context, r sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		s, e := fn(arguments(r.Params.Arguments))
		if e != nil {
			return sdk.NewToolResultError(e.Error()), nil
		}
		return sdk.NewToolResultText(s), nil
	}
}
func jsonHandler(fn func(map[string]any) (any, error)) server.ToolHandlerFunc {
	return func(_ context.Context, r sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		v, e := fn(arguments(r.Params.Arguments))
		if e != nil {
			return sdk.NewToolResultError(e.Error()), nil
		}
		raw, e := json.Marshal(v)
		if e != nil {
			return sdk.NewToolResultError(e.Error()), nil
		}
		return sdk.NewToolResultText(string(raw)), nil
	}
}

func arguments(raw any) map[string]any {
	args, _ := raw.(map[string]any)
	if args == nil {
		return map[string]any{}
	}
	return args
}
