# @all-in-one-mcp/plugins

TypeScript plugin workspace and OpenCode integration for the `all-in-one-mcp` hybrid architecture.

This workspace provides an `@opencode-ai/plugin`-compatible plugin suite that hooks directly into the OpenCode runtime. It augments the development workflow with fast in-process file guards, automatic memory injection, transparent secret masking, and typed access to automated review—bridging editor events to native Go and Python headless CLI utilities.

## Architecture

The plugin package acts as a fast, resilient bridge between editor lifecycle hooks and native components:

```
OpenCode Runtime
  │
  ├─► chat.message         ──► ReasoningMemoryClient ──► reasoning-memory inject
  ├─► tool.execute.before  ──► FileGuard (sub-0.1ms)   ──► [BLOCKED if sensitive]
  ├─► tool.execute.after   ──► VaultClient            ──► vaultctl mask
  └─► event (session.idle) ──► ReasoningMemoryClient ──► reasoning-memory capture
```

### Components

- **`src/file-guard.ts` (`FileGuard`)**: Synchronous, zero-dependency in-process guard with observed sub-0.1ms average execution latency on modern hardware. Evaluates file and directory paths against compiled sensitive patterns to block unauthorized access to credentials, configuration, and private keys.
- **`src/bridge/subprocess.ts` (`spawnSubprocess`)**: Resilient process execution engine featuring stdin streaming, strict timeout enforcement, maximum output byte limits (default 10 MB), automatic error token scrubbing (`maskErrorText`), and a 3-state `CircuitBreaker` (`CLOSED`, `OPEN`, `HALF_OPEN`) to prevent cascading failures.
- **`src/clients/`**: Typed clients communicating with headless native binaries:
  - `ReasoningMemoryClient`: Interfaces with `reasoning-memory` subcommands (`inject`, `retrieve`, `capture`, `polish`).
  - `VaultClient`: Interfaces with `vaultctl` subcommands (`mask`, `get`, `set`, `status`).
  - `ReviewerClient`: Interfaces with `pr-reviewer` CLI (`review --stdin`, `review --url`, `audit`, `validate`).
- **`src/hooks/lifecycle.ts` (`PluginLifecycleCoordinator`)**: Coordinates lifecycle workflows, wiring together memory injection (`onPromptSubmit`), security guarding (`onBeforeToolCall`), secret masking (`onAfterToolCall`), and episode capture (`onTaskComplete`).
- **`src/index.ts` (`allInOneMCPPlugin`)**: Default plugin export implementing the `@opencode-ai/plugin` `Plugin` interface. Provides `createOpenCodeHooks` to wire OpenCode runtime hooks (`chat.message`, `tool.execute.before`, `tool.execute.after`, `event` for `session.idle`) to the `PluginLifecycleCoordinator`.

## Lifecycle Hooks

The plugin default export (`allInOneMCPPlugin`) provides the standard `@opencode-ai/plugin` hooks via `createOpenCodeHooks`, backed by the `PluginLifecycleCoordinator`:

| Hook | OpenCode Event / Trigger | Execution | Purpose |
|------|--------------------------|-----------|---------|
| `chat.message` | User prompt submission | Async | Intercepts user prompt before model inference, calls `reasoning-memory inject --json` via `onPromptSubmit`, and prepends relevant `<reasoning_memory>` XML context to message text parts. Fails open (non-blocking) if memory is unreachable. |
| `tool.execute.before` | Pre-tool execution | Synchronous | Intercepts tool calls (`read`, `write`, `read_batch`, etc.). Inspects path arguments with `FileGuard` (observed sub-0.1ms average latency) via `onBeforeToolCall` and throws an error if a sensitive path is accessed, blocking tool execution. |
| `tool.execute.after` | Post-tool execution | Async | Intercepts tool stdout/stderr output and streams it through `vaultctl mask` (with fallback to in-process `maskErrorText`) via `onAfterToolCall` to redact leaked secrets before returning text to the agent. Records tool execution history for episode capture. |
| `event` | `session.idle` event | Async | Triggered when the OpenCode session becomes idle. Calls `onTaskComplete` with the task problem, duration, tool executions, and outcome to persist the trace via `reasoning-memory capture --json`. |

## Security File Guard

The `FileGuard` intercepts sensitive paths before tool execution occurs. It checks both raw arguments and normalized paths (expanding tildes and resolving relative segments).

### Blocked Path Patterns

- **Environment files**: `.env`, `.env.*` (e.g. `.env.local`, `.env.production`), `credentials.env`
- **Cloud credentials & configurations**: `~/.aws/credentials`, `~/.aws/config`, `~/.config/gcloud/**`, `~/.azure/**`
- **SSH keys, config & known hosts**: `~/.ssh/id_*`, `~/.ssh/config`, `~/.ssh/known_hosts`, `~/.ssh/authorized_keys`, `id_rsa`, `id_ed25519`, `id_ecdsa`, `id_dsa` (including `.pub`)
- **Kubernetes configs**: `~/.kube/config`
- **Shell profiles**: `~/.zshrc`, `~/.bashrc`, `~/.bash_profile`, `~/.profile`, `~/.bash_login`, `~/.zprofile`
- **GitHub CLI credentials**: `~/.config/gh/hosts.yml`, `~/.config/gh/hosts.yaml` (`hosts.yml` / `hosts.yaml`)
- **Service accounts & tokens**: `credentials.json`, `service-account*.json`, `~/.netrc`
- **Certificates & private keys**: `*.pem`, `*.key`, `*.pfx`, `*.p12`, `*.pkcs12`, `*.keystore`
- **Generic secret files**: `secret.yaml`, `secrets.json`, `passwords.txt`, `credentials.env` (matching `(?:secret|secrets|passwords?|creds?|credentials)\.(?:ya?ml|json|toml|ini|txt|env)`)

## Subprocess Bridge & Resilience

All child process invocations (`reasoning-memory`, `vaultctl`, `python -m core.cli`) run through `spawnSubprocess`:

- **Stdin Streaming**: Payloads are streamed directly into child process `stdin` without writing temporary files to disk.
- **Circuit Breaker**: Monitored per binary (`reasoning-memory`, `vaultctl`, `pr-reviewer`). Trips to `OPEN` after reaching the failure threshold (default 3–5 failures), fast-failing subsequent calls for 30 seconds before testing recovery in `HALF_OPEN`.
- **Token Masking**: The `maskErrorText` scrubber filters known token formats (GitHub PATs, GitLab tokens, AWS keys, Bearer tokens, OpenAI/DeepSeek keys) from `stderr` and exception messages.
- **Output Bounds**: Caps process output to `maxOutputBytes` (default 10 MB) and enforces execution timeouts (`timeoutMs`).

## OpenCode Configuration

The plugin integrates with OpenCode via `.opencode/opencode.json` as the supported local plugin configuration:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "plugin": [
    [
      "../plugins/dist/index.js",
      {
        "enableMemoryInjection": true,
        "enableEpisodeCapture": true,
        "enableOutputMasking": true,
        "enableFileGuard": true
      }
    ]
  ]
}
```

Options configured in the plugin tuple object:
- `enableMemoryInjection` (boolean, default: `true`): Inject relevant reasoning memory context on `chat.message`.
- `enableFileGuard` (boolean, default: `true`): Block sensitive file access synchronously on `tool.execute.before`.
- `enableOutputMasking` (boolean, default: `true`): Mask credentials in tool outputs on `tool.execute.after`.
- `enableEpisodeCapture` (boolean, default: `true`): Capture task execution episodes on `event` (`session.idle`).

## Setup & Testing

### Prerequisites

- Node.js 20.19.0+ or 22.12.0+
- npm 10+

### Makefile Targets

From the repository root:

```bash
make setup-plugins   # Install npm dependencies
make build-plugins   # Compile TypeScript to dist/ (tsc)
make lint-plugins    # Type-check test suite without emitting
make test-plugins    # Run Vitest unit tests
```

### Direct npm Scripts

From the `plugins/` directory:

```bash
npm install          # Install dependencies
npm run build        # Build TypeScript to dist/
npm run lint         # Type-check with tsconfig.test.json
npm test             # Run Vitest test suite
npm run test:watch   # Vitest in watch mode
npm run test:node    # Native Node test runner with strip-types
```

### Test Suite Structure

- `tests/file-guard.test.ts`: Validates sensitive path matching, path normalization, safe path allowance, and benchmarked sub-0.1ms average execution latency.
- `tests/subprocess.test.ts`: Validates stdin streaming, process timeouts, byte caps, error masking, and circuit breaker state transitions.
- `tests/clients.test.ts`: Validates binary resolution and typed client methods against native CLI components.
- `tests/lifecycle.test.ts`: Validates end-to-end hook coordinator workflows for `onPromptSubmit`, `onBeforeToolCall`, `onAfterToolCall`, and `onTaskComplete`.
