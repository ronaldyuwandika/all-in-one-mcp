# Credential Vault

Encrypted credential vault for AI agent sessions. Protects credentials by scanning, encrypting, redacting originals, and providing controlled access via MCP tools with audit logging.

## Interfaces

| Interface | Command | Description |
|---|---|---|
| MCP Server | `python server.py` | Stdio MCP transport |
| CLI | `python cli.py <cmd>` | Terminal commands |
| TUI | `python tui.py` | Textual-based monitoring UI |

## CLI Usage

```bash
python cli.py status              # List credentials
python cli.py get <name>          # Get credential value
python cli.py scan                # Scan files, encrypt, redact
python cli.py restore             # Restore originals from backup
python cli.py audit               # Show access audit log
python cli.py export <file>       # Export vault
python cli.py import <file>       # Import vault
python cli.py watch               # Launch TUI monitor
```

## MCP Tools

| Tool | Description |
|---|---|
| `vault_status` | List all credential names |
| `vault_get` | Get credential value (audit-logged) |
| `vault_set` | Store a chat-provided credential |
| `vault_scan` | Scan + encrypt + redact files |
| `vault_restore` | Restore original files |
| `vault_mask` | Redact credential patterns from text |
| `vault_audit` | View access audit log |
| `run_safe` | Run command with masked output |
| `vault_chat_clear` | Clear chat-only credentials |

## Security

- **Encryption**: AES-256 via Fernet (cryptography library)
- **Key storage**: macOS Keychain (`com.credential-vault`)
- **Audit**: Every `get` and `set` operation is logged to `audit.jsonl`
- **Redaction**: Originals on disk are replaced with `[REDACTED:key_name]` markers
- **Restore**: `vault_restore()` reverts all redacted files from encrypted backup
