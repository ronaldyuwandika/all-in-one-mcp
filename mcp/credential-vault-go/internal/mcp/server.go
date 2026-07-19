// Package mcp exposes credential-vault operations through local stdio MCP.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/vault"
)

func Serve(v *vault.Vault) error {
	s := server.NewMCPServer("credentials-vault", "1.0.0", server.WithInstructions("Local-only credential broker. Store and fetch secrets with vault_set/vault_get; use vault_mask or run_safe before returning command output. Never send credential values to network tools."))
	add := func(t sdk.Tool, h server.ToolHandlerFunc) { s.AddTool(t, h) }
	add(sdk.NewTool("vault_status", sdk.WithDescription("List local credential metadata without values")), jsonHandler(func(_ map[string]any) (any, error) { return v.Stats() }))
	add(sdk.NewTool("vault_get", sdk.WithDescription("Retrieve a local credential and audit the purpose"), sdk.WithString("name", sdk.Required()), sdk.WithString("purpose", sdk.Required())), textHandler(func(a map[string]any) (string, error) { return v.Get(str(a, "name"), str(a, "purpose")) }))
	add(sdk.NewTool("vault_set", sdk.WithDescription("Store a credential locally; the value never leaves this process"), sdk.WithString("name", sdk.Required()), sdk.WithString("value", sdk.Required())), jsonHandler(func(a map[string]any) (any, error) {
		name := "chat." + str(a, "name")
		return map[string]string{"stored": name}, v.Set(name, str(a, "value"), "mcp")
	}))
	add(sdk.NewTool("vault_chat_clear", sdk.WithDescription("Remove credentials supplied through chat")), jsonHandler(func(_ map[string]any) (any, error) { n, e := v.ClearChat(); return map[string]int{"cleared": n}, e }))
	add(sdk.NewTool("vault_mask", sdk.WithDescription("Redact credential patterns from text"), sdk.WithString("text", sdk.Required())), textHandler(func(a map[string]any) (string, error) { return vault.MaskText(str(a, "text")), nil }))
	add(sdk.NewTool("vault_scan", sdk.WithDescription("Scan a local directory and optionally redact detected credentials"), sdk.WithString("path"), sdk.WithBoolean("redact")), jsonHandler(func(a map[string]any) (any, error) {
		p := str(a, "path")
		if p == "" {
			p = "."
		}
		redact, supplied := a["redact"].(bool)
		if !supplied {
			redact = true
		}
		return v.ScanDir(p, redact)
	}))
	add(sdk.NewTool("vault_restore", sdk.WithDescription("Restore files from encrypted local backups")), jsonHandler(func(_ map[string]any) (any, error) { n, e := v.Restore(); return map[string]int{"restored": n}, e }))
	add(sdk.NewTool("vault_audit", sdk.WithDescription("Read local credential access audit entries")), jsonHandler(func(_ map[string]any) (any, error) { return v.Audit(50) }))
	add(sdk.NewTool("run_safe", sdk.WithDescription("Run a local command and mask secrets in combined output"), sdk.WithString("command", sdk.Required())), textHandler(func(a map[string]any) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		out, e := exec.CommandContext(ctx, "/bin/sh", "-c", str(a, "command")).CombinedOutput() // #nosec G204 -- run_safe intentionally executes the caller's local command and masks output.
		masked := vault.MaskText(string(out))
		if e != nil {
			return masked, fmt.Errorf("command failed: %w", e)
		}
		return masked, nil
	}))
	return server.ServeStdio(s)
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
