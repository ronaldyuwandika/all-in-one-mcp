# AI Guide: Credential Vault

## Architecture

```
server.py   — MCP server (854 lines): encryption, scanner, redact/restore, masking, tools
cli.py      — CLI interface: status, get, scan, restore, set, audit, export, import, watch
tui.py      — Textual-based TUI: dark/light themes, search/filter, credential detail, audit panel
vault       — Shell entry point script
```

## Scan Targets

The vault scans these credential file types:
- **ini**: AWS credentials, Azure credentials
- **pem**: SSH private keys (id_rsa, id_ed25519, etc.)
- **yaml**: Kubernetes kubeconfig
- **json**: Docker config, GCP ADC, Azure profile
- **env**: .env files (home + project directories)
- **shellenv**: .zshrc, .bashrc, .profile (exported env vars)
- **netrc**: .netrc passwords

## Detection Logic

Uses two-layer credential detection:
1. **Shell/env vars**: Key-name matching via `_CRED_ENV_KEY_RE` regex — captures vars with TOKEN, PASSWORD, SECRET, KEY, DSN, URI, PAT and 80+ specific suffixes
2. **All other files**: Value-based heuristic — filters out structural keys, URLs, booleans, UUIDs, emails, short values (<8 chars)

## Output Masking

`vault_mask()` and `run_safe()` apply 25+ regex patterns to redact:
- AWS access/secret/session keys
- SSH private keys
- GitHub tokens (ghp_, gh*, github_pat_)
- GitLab, Grafana, Sentry tokens
- Stripe, Datadog, New Relic, Cloudflare keys
- Generic API keys, tokens, passwords
- Connection strings with embedded credentials

## Session Lifecycle

```
START: vault_scan()       # scan files, encrypt, redact originals
DURING: vault_get()       # controlled access with audit
        vault_set()       # store chat-provided secrets
        vault_mask()      # mask output before returning to user
        run_safe()        # commands with masked stdout/stderr
END:   vault_restore()    # restore original files from backup
       vault_chat_clear() # purge chat-origin credentials
```

## Data Storage

- **Vault file**: `~/.credential-vault/vault.json` (Fernet-encrypted, chmod 600)
- **Audit log**: `~/.credential-vault/audit.jsonl` (plain JSON lines, chmod 600)
- **Scanned flag**: `~/.credential-vault/.scanned` (marker file)
- **Keychain service**: `com.credential-vault` / account `vault-key`
