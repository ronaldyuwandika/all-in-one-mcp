# Credential handling policy

- Use the local `credentials_vault` MCP as the default interface for storing or retrieving credentials.
- Call `vault_get` with an explicit purpose and `vault_set` for credentials supplied during a task.
- Never place credential values in web searches, URLs, issue bodies, pull requests, logs, or other internet-facing tools.
- Mask command output with `vault_mask` or execute trusted local commands with `run_safe` before sharing output.
- Clear chat-origin credentials with `vault_chat_clear` when they are no longer needed.
