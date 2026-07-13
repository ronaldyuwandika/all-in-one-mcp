# Credential Vault — Security Model

## Overview

Credential Vault is an MCP server that protects secrets during AI agent sessions by:
1. Scanning the filesystem for credential files
2. Encrypting credential values and storing them locally
3. Redacting originals on disk (replacing values with `[REDACTED:key_name]` placeholders)
4. Providing controlled access via MCP tools with audit logging
5. Masking credential patterns from agent output
6. Restoring originals when the session ends

## Encryption

### Key Management

- **Encryption key**: Stored in macOS Keychain under service `com.credential-vault` with account `vault-key`
- **Algorithm**: Fernet (AES-128-CBC with HMAC-SHA256 authentication), via the `cryptography` library
- **Key generation**: First run generates a random Fernet key, stored in Keychain. Subsequent runs retrieve the existing key
- **Fallback**: If Keychain is unavailable (non-macOS), keys are stored in `~/.credential-vault/.vault-key` with `0600` permissions

### Data at Rest

- **Vault file**: `~/.credential-vault/vault.json` — contains encrypted credential map
- **Format**: JSON object encrypted with Fernet, written to disk with `0600` permissions
- **Contents**: `{"credentials": {name: encrypted_value}, "files": {path: original_content}, "created_at": "..."}`
- Each credential value is individually encrypted within the vault

## Scan Targets

The scanner checks these common credential locations:

| Path | Type | Sections |
|------|------|----------|
| `~/.aws/credentials` | INI | Per-profile |
| `~/.aws/config` | INI | Per-profile |
| `~/.ssh/id_*` (RSA, ed25519, ECDSA, DSA) | PEM | N/A |
| `~/.kube/config` | YAML | Flattened |
| `~/.docker/config.json` | JSON | Flattened |
| `~/.config/gcloud/application_default_credentials.json` | JSON | Flattened |
| `~/.azure/azureProfile.json` | JSON | Flattened |
| `~/.azure/credentials` | INI | No sections |
| `~/.netrc` | Netrc | N/A |
| `~/.zshrc` / `~/.bashrc` / `~/.profile` | Shell env | Per-variable |
| `.env` / `.env.local` / `.env.production` | Env | Per-variable |

### Credential Detection

Values are classified as credentials if:
- The key name matches known credential patterns (e.g., `*_TOKEN`, `*_SECRET`, `*_KEY`, `*_PASSWORD`, `DATABASE_URL`, etc.)
- The value is longer than 8 characters
- The value is not a known non-secret (e.g., `true`, `false`, `osxkeychain`, URLs without credentials)

Keys explicitly excluded: `kind`, `apiVersion`, `client_id`, `project_id`, `region`, `zone`, `endpoint`, `NODE_ENV`, `NEXT_PUBLIC_*` (non-secret prefixes).

## Disk Redaction

When `vault_scan` runs or `vault` CLI is called:

1. Original file contents are backed up to the vault
2. Each credential value found in the file is replaced with `[REDACTED:file.name.section.key]`
3. Redacted files keep their original permissions (escalated to `0600` if needed)

### Restoration

`vault_restore` (MCP tool) or `vault restore` (CLI) restores all backed-up files to their original content. Always call this at the end of an AI session to recover working configuration.

## Chat-Scoped Credentials

Credentials received through chat (via `vault_set` MCP tool) are:
1. Prefixed with `chat.` in the vault
2. Encrypted with the same Fernet key
3. Cleared by `vault_chat_clear` at session end

## Access Control

Every `vault_get` call requires a `purpose` string, which is logged in the audit trail:

```json
{
  "timestamp": "2026-07-13T12:00:00+00:00",
  "action": "get",
  "credential": "~/.aws/credentials.default.aws_access_key_id",
  "purpose": "Running terraform apply to update infrastructure"
}
```

The audit log is stored at `~/.credential-vault/audit.jsonl` with `0600` permissions.

## Threat Model

| Threat | Mitigation |
|--------|-----------|
| Agent reads credential files | Files are redacted to `[REDACTED:...]` placeholders on disk after scan |
| Agent leaks credentials in output | `vault_mask` redacts 20+ credential patterns; `run_safe` auto-masks shell output |
| Unauthorized vault access | Vault file encrypted with Fernet + Keychain-stored key; `0600` permissions |
| Keychain compromise | macOS Keychain requires user authentication; access logged by OS |
| Credential left in chat context | Chat-scoped credentials cleared by `vault_chat_clear` at session end |
| Restore failure | Original files backed up to vault before any redaction; files can be manually recovered from vault backup |
| Binary/file corruption | `_is_binary()` check skips binary files; `utf-8` with `errors="replace"` handles encoding issues |

## CLI Reference

```bash
vault status              # List stored credentials (names only)
vault get <name>          # Retrieve a credential value
vault set <name> <value>  # Store a credential from stdin/argument
vault scan                # Scan system for credentials, encrypt, redact
vault restore             # Restore original files from vault backup
vault audit               # View access audit log
vault export <file>       # Export encrypted vault to file
vault import <file>       # Import from exported vault file
vault watch               # TUI monitor for credential access
```
