#!/usr/bin/env python3
"""Credential Vault MCP Server

Protects credentials from AI agents by:
  1. Scanning for credentials across the system
  2. Encrypting and storing them in a local vault
  3. Redacting originals on disk (replacing values with [REDACTED])
  4. Providing controlled access via MCP tools with audit logging
  5. Masking credential patterns from any output
  6. Restoring originals when the session ends

Uses macOS Keychain for the vault encryption key.
"""

import json
import re
import subprocess  # nosec B404 # intentional: macOS keychain access
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

from cryptography.fernet import Fernet
from mcp.server import Server
from mcp.server.models import InitializationOptions
from mcp.types import ServerCapabilities, ToolsCapability, Tool as MCPTool
import mcp.server.stdio

VAULT_DIR = Path.home() / ".credential-vault"
VAULT_FILE = VAULT_DIR / "vault.json"
REDACT_LOG = VAULT_DIR / "audit.jsonl"
SCANNED_FLAG = VAULT_DIR / ".scanned"
KEYCHAIN_SERVICE = "com.credential-vault"
KEYCHAIN_ACCOUNT = "vault-key"

SCAN_TARGETS = [
    {"path": "~/.aws/credentials", "type": "ini", "sections": True},
    {"path": "~/.aws/config", "type": "ini", "sections": True},
    {"path": "~/.ssh/id_rsa", "type": "pem"},
    {"path": "~/.ssh/id_ed25519", "type": "pem"},
    {"path": "~/.ssh/id_ecdsa", "type": "pem"},
    {"path": "~/.ssh/id_dsa", "type": "pem"},
    {"path": "~/.kube/config", "type": "yaml"},
    {"path": "~/.docker/config.json", "type": "json"},
    {"path": "~/.config/gcloud/application_default_credentials.json", "type": "json"},
    {"path": "~/.config/gcloud/access_tokens.db", "type": "skip"},
    {"path": "~/.azure/azureProfile.json", "type": "json"},
    {"path": "~/.azure/credentials", "type": "ini", "sections": False},
    {"path": ".env", "type": "env"},
    {"path": ".env.local", "type": "env"},
    {"path": ".env.production", "type": "env"},
    {"path": "~/.netrc", "type": "netrc"},
    {"path": "~/.zshrc", "type": "shellenv"},
    {"path": "~/.zprofile", "type": "shellenv"},
    {"path": "~/.zshenv", "type": "shellenv"},
    {"path": "~/.bashrc", "type": "shellenv"},
    {"path": "~/.bash_profile", "type": "shellenv"},
    {"path": "~/.profile", "type": "shellenv"},
]

CRED_PATTERNS = [
    (
        re.compile(r"(AKIA|ASIA|AROA|AIDA|AIPA|ANPA|ANVA|APKA)[0-9A-Z]{16}"),
        "AWS_ACCESS_KEY_ID",
    ),
    (
        re.compile(r'(?i)(?:aws[_-])?secret[_-]access[_-]key[= ]+["\']?([^"\' \n]{8})([^"\' \n]+)'),
        lambda m: f"secret_access_key={m.group(1)}{'*' * min(len(m.group(2)), 32)}",
    ),
    (
        re.compile(r'(?i)(?:aws[_-])?session[_-]token[= ]+["\']?[^"\' \n]{10}([^"\' \n]+)'),
        "AWS_SESSION_TOKEN",
    ),
    (
        re.compile(
            r"-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----.*?-----END .*?PRIVATE KEY-----",
            re.DOTALL,
        ),
        "SSH_PRIVATE_KEY",
    ),
    (re.compile(r"gh[pousr]_[A-Za-z0-9_]{10,}"), "GITHUB_TOKEN"),
    (re.compile(r"github_pat_[A-Za-z0-9_]{10,}"), "GITHUB_PAT"),
    (re.compile(r"glpat-[A-Za-z0-9_\-]{10,}"), "GITLAB_TOKEN"),
    (re.compile(r"glsa_[A-Za-z0-9_]{10,}(?:_[A-Fa-f0-9]{8,})?"), "GRAFANA_TOKEN"),
    (re.compile(r"https://[a-f0-9]{8,40}@[^/]+/\d+"), "SENTRY_DSN"),
    (re.compile(r"sntrys_[A-Za-z0-9_]{10,}"), "SENTRY_TOKEN"),
    (re.compile(r'datadog_api_key[= ]+["\']?[A-Za-z0-9]{20,}'), "DATADOG_API_KEY"),
    (
        re.compile(r'(?i)new_relic_license_key[= ]+["\']?[A-Za-z0-9]{20,}'),
        "NEW_RELIC_LICENSE_KEY",
    ),
    (re.compile(r"(?i)sk_live_[A-Za-z0-9]{10,}"), "STRIPE_SECRET_KEY"),
    (re.compile(r"(?i)rk_live_[A-Za-z0-9]{10,}"), "STRIPE_SECRET_KEY"),
    (re.compile(r"(?i)whsec_[A-Za-z0-9]{10,}"), "STRIPE_WEBHOOK_SECRET"),
    (re.compile(r"cfat_[A-Za-z0-9_]{10,}"), "CLOUDFLARE_TOKEN"),
    (
        re.compile(r'(?i)(api[_-]?key|apikey|api_secret|secret_key)[=: ]+["\']?([^"\' \n]{8})([^"\' \n]+)'),
        lambda m: f"{m.group(1)}={m.group(2)}{'*' * min(len(m.group(3)), 32)}",
    ),
    (
        re.compile(r'(?i)(bearer|jwt)[=: ]+["\']?[^"\' \n]{10}([^"\' \n]{10,})'),
        lambda m: f"{m.group(1)}={'*' * min(len(m.group(0).split('=', 1)[-1].strip('"\'')), 40)}",
    ),
    (
        re.compile(r'(?i)(token|secret)[=: ]+["\']?([^"\' \n]{10})([^"\' \n]+)'),
        lambda m: f"{m.group(1)}={m.group(2)}{'*' * min(len(m.group(3)), 32)}",
    ),
    (
        re.compile(r'(?i)(password|passwd|pass|pwd)[=: ]+["\']?([^"\' \n]{3})([^"\' \n]+)'),
        lambda m: f"{m.group(1)}={m.group(2)}{'*' * min(len(m.group(3)), 32)}",
    ),
    (
        re.compile(r"(postgresql|mysql|redis|mongodb|rediss|amqp|rabbitmq)://[^@]+@"),
        lambda m: m.group(1) + "://__REDACTED__@",
    ),
    (
        re.compile(r'(DATABASE_URL|REDIS_URL|MONGO_URI|MONGODB_URI)[=: ]+["\']?[^"\' \n]{8}([^"\' \n]+)'),
        lambda m: f"{m.group(1)}={'*' * min(len(m.group(2)), 40)}",
    ),
]


def keychain_get() -> Optional[str]:
    try:
        r = subprocess.run(  # nosec B603,B607
            [
                "security",
                "find-generic-password",
                "-s",
                KEYCHAIN_SERVICE,
                "-a",
                KEYCHAIN_ACCOUNT,
                "-w",
            ],
            capture_output=True,
            text=True,
            timeout=10,
        )
        return r.stdout.strip() if r.returncode == 0 else None
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return None


def keychain_set(value: str):
    subprocess.run(  # nosec B603,B607
        [
            "security",
            "delete-generic-password",
            "-s",
            KEYCHAIN_SERVICE,
            "-a",
            KEYCHAIN_ACCOUNT,
        ],
        capture_output=True,
        timeout=10,
    )
    subprocess.run(  # nosec B603,B607
        [
            "security",
            "add-generic-password",
            "-s",
            KEYCHAIN_SERVICE,
            "-a",
            KEYCHAIN_ACCOUNT,
            "-w",
            value,
            "-U",
        ],
        capture_output=True,
        timeout=10,
    )


def _get_fernet() -> Fernet:
    key = keychain_get()
    if not key:
        key = Fernet.generate_key().decode()
        keychain_set(key)
    return Fernet(key.encode() if isinstance(key, str) else key)


def vault_encrypt(data: dict) -> str:
    f = _get_fernet()
    return f.encrypt(json.dumps(data, default=str).encode()).decode()


def vault_decrypt(token: str) -> dict:
    f = _get_fernet()
    return json.loads(f.decrypt(token.encode()))


def load_vault() -> dict:
    if not VAULT_FILE.exists():
        return {"credentials": {}, "files": {}, "created_at": None}
    try:
        return vault_decrypt(VAULT_FILE.read_text())
    except Exception:
        return {"credentials": {}, "files": {}, "created_at": None}


def save_vault(data: dict):
    VAULT_DIR.mkdir(parents=True, exist_ok=True)
    VAULT_FILE.write_text(vault_encrypt(data))
    VAULT_FILE.chmod(0o600)


def export_vault(filepath: str, plain: bool = False) -> int:
    vault = load_vault()
    creds = vault.get("credentials", {})
    export = {
        "format": "credential-vault-export",
        "version": 1,
        "created_at": datetime.now(timezone.utc).isoformat(),
        "credential_count": len(creds),
        "has_file_backups": bool(vault.get("files")),
    }
    if plain:
        for k, v in creds.items():
            try:
                export.setdefault("credentials", {})[k] = vault_decrypt(v)
            except Exception:
                export.setdefault("credentials", {})[k] = {"error": "decrypt_failed"}
    else:
        export["encrypted"] = vault_encrypt(creds)
    Path(filepath).write_text(json.dumps(export, indent=2, default=str))
    Path(filepath).chmod(0o600)
    return len(creds)


def import_vault(filepath: str, merge: bool = True) -> int:
    raw = json.loads(Path(filepath).read_text())
    if raw.get("format") != "credential-vault-export":
        raise ValueError("Not a valid credential-vault export file")

    vault = load_vault()
    imported = 0

    if raw.get("encrypted"):
        decrypted = vault_decrypt(raw["encrypted"])
        for k, v in decrypted.items():
            vault.setdefault("credentials", {})[k] = vault_encrypt(v)
            imported += 1
    elif "credentials" in raw:
        for k, v in raw["credentials"].items():
            try:
                if isinstance(v, dict) and "error" not in v:
                    vault.setdefault("credentials", {})[k] = vault_encrypt(v)
                    imported += 1
            except Exception:  # nosec B110
                pass

    save_vault(vault)
    append_audit({"action": "import", "count": imported, "source": filepath})
    return imported


def load_audit() -> list:
    if not REDACT_LOG.exists():
        return []
    return [json.loads(line) for line in REDACT_LOG.read_text().strip().split("\n") if line]


def append_audit(entry: dict):
    VAULT_DIR.mkdir(parents=True, exist_ok=True)
    entry["timestamp"] = datetime.now(timezone.utc).isoformat()
    with open(REDACT_LOG, "a") as f:
        f.write(json.dumps(entry, default=str) + "\n")
    REDACT_LOG.chmod(0o600)


def _expand_path(p: str) -> Path:
    return Path(p).expanduser().resolve()


def _is_binary(path: Path) -> bool:
    try:
        with open(path, "rb") as f:
            return b"\x00" in f.read(1024)
    except Exception:
        return False


def _parse_ini(text: str) -> dict:
    result = {}
    current_section = "default"
    for line in text.split("\n"):
        line = line.strip()
        if not line or line.startswith("#") or line.startswith(";"):
            continue
        if line.startswith("[") and line.endswith("]"):
            current_section = line[1:-1].strip()
            continue
        if "=" in line:
            k, v = line.split("=", 1)
            result[f"{current_section}.{k.strip()}"] = v.strip().strip("\"'")
    return result


def _parse_env(text: str) -> dict:
    result = {}
    for line in text.split("\n"):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" in line:
            k, v = line.split("=", 1)
            result[k.strip()] = v.strip().strip("\"'")
    return result


def _parse_shell_env(text: str) -> dict:
    result = {}
    for line in text.split("\n"):
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if stripped.startswith("export "):
            stripped = stripped[7:].strip()
        if "=" in stripped:
            k, v = stripped.split("=", 1)
            v = v.strip().strip("\"'")
            if v.startswith("$(") or v.startswith('"$('):
                continue
            result[k.strip()] = v
    return result


def _parse_yaml_text(text: str) -> dict:
    import yaml

    try:
        return yaml.safe_load(text) or {}
    except Exception:
        return {}


def _parse_json_text(text: str) -> dict:
    try:
        return json.loads(text)
    except Exception:
        return {}


def _flatten_yaml(obj: dict, prefix: str = "") -> dict:
    result = {}
    for k, v in obj.items():
        key = f"{prefix}.{k}" if prefix else k
        if isinstance(v, dict):
            result.update(_flatten_yaml(v, key))
        elif isinstance(v, str) and len(v) > 4 and not v.startswith("{{") and not v.startswith("$"):
            result[key] = v
    return result


_SKIP_KEYS = {
    "kind",
    "apiVersion",
    "current-context",
    "preferences",
    "credsStore",
    "credHelpers",
    "auths",
    "NODE_ENV",
    "NEXT_PUBLIC_APP_URL",
    "NEXT_PUBLIC_GA_MEASUREMENT_ID",
    "NEXT_PUBLIC_GTM_ID",
    "NEXT_PUBLIC_SENTRY_DSN",
    "client_id",
    "client_email",
    "private_key_id",
    "project_id",
    "type",
    "account",
    "tenant_id",
    "subscription_id",
    "region",
    "zone",
    "cluster_name",
    "endpoint",
}

_CRED_ENV_KEY_RE = re.compile(
    r"(?:^|_)("
    r"TOKEN|PASSWORD|PASS(?:WD|WORD|PHRASE)?|SECRET|KEY|PWD|DSN|URI|PAT|"
    r"CREDENTIALS|PRIVATE[_-]KEY|LICENSE[_-]KEY|"
    r"CLAIM|"
    r"INSTRUMENTATIONKEY|"
    r"CONNECTION[_-]STRING|"
    r"PGPASSWORD|PGPWD|"
    r"ACCESS[_-](?:KEY|SECRET|TOKEN|ID)|"
    r"API[_-](?:KEY|SECRET|TOKEN|CREDENTIAL)|"
    r"AUTH[_-](?:TOKEN|KEY|SECRET)|"
    r"BEARER[_-]TOKEN|SESSION[_-](?:TOKEN|SECRET|KEY)|"
    r"REFRESH[_-]TOKEN|WEBHOOK[_-]SECRET|SIGNING[_-]SECRET|"
    r"CLIENT[_-](?:SECRET|KEY|ID)|"
    r"SECRET[_-](?:KEY|ACCESS)|"
    r"CONSUMER[_-](?:KEY|SECRET)|"
    r"COOKIE[_-]SECRET|"
    r"ROOT[_-]PASSWORD|ADMIN[_-](?:PASSWORD|TOKEN)|"
    r"BOT[_-]TOKEN|"
    r"JWT[_-]SECRET|JWT[_-]TOKEN|JWT[_-]KEY|"
    r"SSL[_-]KEY|TLS[_-]KEY|TLS[_-]CERT|SSL[_-]CERT|"
    r"SSH[_-]PRIVATE[_-]KEY|SSH[_-]KEY|"
    r"HMAC[_-]KEY|MAC[_-]KEY|"
    r"SERVICE[_-]ACCOUNT|"
    r"GPG[_-]PASSPHRASE|GPG[_-]KEY|"
    r"COMPOSER[_-]AUTH|"
    r"DATABASE[_-]URL|REDIS[_-]URL|MONGO[_-]URL|MONGODB[_-]URL|"
    r"REDISCLOUD[_-]URL|CLOUDAMQP[_-]URL|CLOUDINARY[_-]URL|"
    r"CELERY[_-]BROKER[_-]URL|SIDEKIQ[_-]URL|BULL[_-]REDIS[_-]URL|"
    r"MONGO[_-]URI|MONGODB[_-]URI|REDIS[_-]URI|"
    r"CLICKHOUSE[_-]URL|NEON[_-]DATABASE[_-]URL|"
    r"[_-](?:MAC|HMAC)[_-]KEY|"
    r"[_-]ACCESS[_-]KEY[_-]ID|"
    r"[_-]SECRET[_-]ACCESS[_-]KEY|"
    r"[_-]SERVICE[_-]ACCOUNT(?:[_-]KEY)?|"
    r"[_-]APPLICATION[_-]CREDENTIALS"
    r")(?:_|$)",
)


def _is_credential_like(value: str, flattened_key: str) -> bool:
    parts = flattened_key.rsplit(".", 1)
    leaf_key = parts[-1] if len(parts) > 1 else flattened_key
    if leaf_key in _SKIP_KEYS:
        return False
    if value in ("Config", "gcloud", "osxkeychain", "orbstack"):
        return False
    if value.startswith("https://") or value.startswith("http://"):
        return False
    if value in ("true", "false", "none", "null"):
        return False
    if len(value) < 8:
        return False
    if "@" in value and "://" not in value and not re.search(r":[^@]+@", value):
        return False
    if re.fullmatch(r"[\d\-]+", value):
        return False
    return True


def _find_env_files() -> list[Path]:
    found = []
    home = Path.home()
    for p in home.iterdir():
        if p.name in (".env", ".env.local") and p.is_file():
            found.append(p)
    for d in home.iterdir():
        if d.is_dir():
            for env_name in (".env", ".env.local", ".env.production"):
                ep = d / env_name
                if ep.exists() and ep.is_file():
                    found.append(ep)
    return found


def scan_credentials() -> dict:
    vault = load_vault()
    discovered = {}
    file_backups = vault.get("files", {})

    for target in SCAN_TARGETS:
        path = _expand_path(target["path"])
        if not path.exists() or _is_binary(path):
            continue

        try:
            text = path.read_text(encoding="utf-8", errors="replace")
        except Exception:  # nosec B112
            continue

        entries = {}
        if target["type"] == "ini":
            entries = _parse_ini(text)
        elif target["type"] == "env":
            entries = _parse_env(text)
        elif target["type"] == "yaml":
            parsed = _parse_yaml_text(text)
            if isinstance(parsed, dict):
                entries = _flatten_yaml(parsed)
        elif target["type"] == "json":
            parsed = _parse_json_text(text)
            if isinstance(parsed, dict):
                entries = _flatten_yaml(parsed)
        elif target["type"] == "pem":
            if "PRIVATE KEY" in text:
                entries = {f"ssh.{path.stem}": text.strip()}
        elif target["type"] == "netrc":
            for line in text.split("\n"):
                parts = line.strip().split()
                if len(parts) >= 2 and parts[0] == "password":
                    entries["netrc.password"] = parts[1]
        elif target["type"] == "shellenv":
            entries = _parse_shell_env(text)

        for key, value in entries.items():
            if not value or len(value) < 4:
                continue
            vault_key = f"{path.name}.{key}"
            if key.startswith("NEXT_PUBLIC_"):
                continue
            if target["type"] in ("shellenv", "env"):
                if not _CRED_ENV_KEY_RE.search(key):
                    continue
            elif not _is_credential_like(value, key):
                continue
            discovered[vault_key] = value
            if vault_key in vault.get("credentials", {}):
                continue
            vault.setdefault("credentials", {})[vault_key] = vault_encrypt({vault_key: value})

    for env_file in _find_env_files():
        try:
            text = env_file.read_text(encoding="utf-8")
            entries = _parse_env(text)
        except Exception:  # nosec B112
            continue
        for key, value in entries.items():
            if not value or len(value) < 4:
                continue
            vault_key = f"{env_file.name}.{key}"
            if key.startswith("NEXT_PUBLIC_"):
                continue
            if not _CRED_ENV_KEY_RE.search(key):
                continue
            if vault_key in vault.get("credentials", {}):
                continue
            discovered[vault_key] = value
            vault.setdefault("credentials", {})[vault_key] = vault_encrypt({vault_key: value})

    new_files = {}
    for target in SCAN_TARGETS:
        path = _expand_path(target["path"])
        if path.exists():
            try:
                content = path.read_text(encoding="utf-8", errors="replace")
                if "[REDACTED:" in content:
                    continue
                new_files[str(path)] = content
            except Exception:  # nosec B110
                pass

    for env_file in _find_env_files():
        try:
            new_files[str(env_file)] = env_file.read_text(encoding="utf-8")
        except Exception:  # nosec B110
            pass

    vault["files"] = {**new_files, **file_backups}

    valid_keys = set(discovered.keys())
    stale = [vk for vk in vault.get("credentials", {}) if vk not in valid_keys]
    for vk in stale:
        del vault["credentials"][vk]
    if stale:
        vault["stale_pruned"] = len(stale)

    if "created_at" not in vault or not vault["created_at"]:
        vault["created_at"] = datetime.now(timezone.utc).isoformat()

    vault["last_scanned"] = datetime.now(timezone.utc).isoformat()
    save_vault(vault)
    return discovered


def redact_credential_files():
    vault = load_vault()
    creds = vault.get("credentials", {})

    file_keys = {}
    for vk in creds:
        if vk.startswith("."):
            second_dot = vk.find(".", 1)
            if second_dot == -1:
                continue
            fname = vk[:second_dot]
            rest = vk[second_dot + 1 :]
        else:
            parts = vk.split(".", 1)
            if len(parts) != 2:
                continue
            fname, rest = parts
        file_keys.setdefault(fname, []).append(vk)

    for fname, keys in file_keys.items():
        orig = None
        for fp_str, content in vault.get("files", {}).items():
            if Path(fp_str).name == fname or fp_str.endswith(fname):
                orig = fp_str
                break
        if not orig:
            continue

        orig_path = Path(orig)
        try:
            text = orig_path.read_text(encoding="utf-8")
        except Exception:  # nosec B112
            continue

        redacted = text
        for vk in keys:
            encrypted = creds[vk]
            try:
                decrypted_entry = vault_decrypt(encrypted)
            except Exception:  # nosec B112
                continue
            val = list(decrypted_entry.values())[0] if isinstance(decrypted_entry, dict) else str(decrypted_entry)
            if val and len(val) > 4:
                redacted = redacted.replace(val, f"[REDACTED:{vk}]")

        if redacted != text:
            try:
                orig_path.write_text(redacted)
            except PermissionError:
                orig_path.chmod(0o600)
                orig_path.write_text(redacted)

    SCANNED_FLAG.touch()


def restore_original_files():
    vault = load_vault()
    files = vault.get("files", {})
    for fp_str, content in files.items():
        fp = Path(fp_str)
        try:
            fp.write_text(content)
            fp.chmod(0o600)
        except Exception:  # nosec B110
            pass
    if SCANNED_FLAG.exists():
        SCANNED_FLAG.unlink()


def mask_text(text: str) -> str:
    result = text
    for pattern, label in CRED_PATTERNS:
        if callable(label):
            result = pattern.sub(label, result)
        else:
            result = pattern.sub(f"[REDACTED:{label}]", result)
    result = re.sub(r"\[REDACTED:[^\]]+\]", lambda m: m.group(0), result)
    result = re.sub(r"(?<!\[REDACTED:)[A-Za-z0-9+/=]{40,}(?!\])", "[REDACTED:LONG_KEY]", result)
    return result


app = Server("credential-vault")


@app.list_tools()
async def list_tools():
    return [
        MCPTool(
            name="vault_status",
            description="List all stored credentials (names only, no values)",
            inputSchema={"type": "object", "properties": {}},
        ),
        MCPTool(
            name="vault_get",
            description="Get a credential value. Requires a purpose string for audit trail.",
            inputSchema={
                "type": "object",
                "properties": {
                    "name": {
                        "type": "string",
                        "description": "Credential name from vault_status",
                    },
                    "purpose": {
                        "type": "string",
                        "description": "Why you need this credential (audit logged)",
                    },
                },
                "required": ["name", "purpose"],
            },
        ),
        MCPTool(
            name="vault_mask",
            description="Redact credential patterns from text (use before returning output to user)",
            inputSchema={
                "type": "object",
                "properties": {"text": {"type": "string", "description": "Text to redact"}},
                "required": ["text"],
            },
        ),
        MCPTool(
            name="vault_scan",
            description="Scan system for credentials, encrypt them, and redact originals on disk",
            inputSchema={"type": "object", "properties": {}},
        ),
        MCPTool(
            name="vault_restore",
            description="Restore original credential files from vault backup (use when done with AI session)",
            inputSchema={"type": "object", "properties": {}},
        ),
        MCPTool(
            name="vault_audit",
            description="Show audit log of all credential access requests",
            inputSchema={"type": "object", "properties": {}},
        ),
        MCPTool(
            name="run_safe",
            description="Run a shell command and redact any credential patterns from its output",
            inputSchema={
                "type": "object",
                "properties": {"command": {"type": "string", "description": "Shell command to run"}},
                "required": ["command"],
            },
        ),
        MCPTool(
            name="vault_set",
            description="Store a credential received through chat or other non-file source in the vault",
            inputSchema={
                "type": "object",
                "properties": {
                    "name": {
                        "type": "string",
                        "description": "Credential name (e.g. 'openai_api_key')",
                    },
                    "value": {
                        "type": "string",
                        "description": "The credential value to encrypt and store",
                    },
                },
                "required": ["name", "value"],
            },
        ),
        MCPTool(
            name="vault_chat_clear",
            description="Remove all chat-stored credentials from the vault (use at end of session)",
            inputSchema={"type": "object", "properties": {}},
        ),
    ]


@app.call_tool()
async def call_tool(name: str, arguments: dict):
    if name == "vault_status":
        vault = load_vault()
        creds = vault.get("credentials", {})
        scanned = SCANNED_FLAG.exists()
        names = sorted(creds.keys())
        file_creds = [n for n in names if not n.startswith("chat.")]
        chat_creds = [n for n in names if n.startswith("chat.")]
        return [
            {
                "type": "text",
                "text": json.dumps(
                    {
                        "scanned": scanned,
                        "count": len(names),
                        "credentials": names,
                        "file_credentials": file_creds,
                        "chat_credentials": chat_creds,
                        "last_scanned": vault.get("last_scanned", "never"),
                        "created_at": vault.get("created_at", "never"),
                    },
                    indent=2,
                ),
            }
        ]

    if name == "vault_get":
        cred_name = arguments.get("name", "")
        purpose = arguments.get("purpose", "")

        if not cred_name or not purpose:
            return [{"type": "text", "text": '{"error": "name and purpose are required"}'}]

        vault = load_vault()
        encrypted = vault.get("credentials", {}).get(cred_name)
        if not encrypted:
            return [
                {
                    "type": "text",
                    "text": f'{{"error": "credential {cred_name} not found"}}',
                }
            ]

        try:
            entry = vault_decrypt(encrypted)
            value = list(entry.values())[0] if isinstance(entry, dict) else str(entry)
        except Exception as e:
            return [{"type": "text", "text": f'{{"error": "decryption failed: {e}"}}'}]

        append_audit({"action": "get", "credential": cred_name, "purpose": purpose})
        return [{"type": "text", "text": value}]

    if name == "vault_mask":
        text = arguments.get("text", "")
        return [{"type": "text", "text": mask_text(text)}]

    if name == "vault_scan":
        discovered = scan_credentials()
        redact_credential_files()
        return [
            {
                "type": "text",
                "text": json.dumps(
                    {
                        "discovered": len(discovered),
                        "keys": sorted(discovered.keys()),
                    },
                    indent=2,
                ),
            }
        ]

    if name == "vault_restore":
        restore_original_files()
        return [{"type": "text", "text": json.dumps({"restored": True})}]

    if name == "vault_audit":
        audit = load_audit()
        return [{"type": "text", "text": json.dumps(audit, indent=2, default=str)}]

    if name == "run_safe":
        cmd = arguments.get("command", "")
        if not cmd:
            return [{"type": "text", "text": '{"error": "command is required"}'}]
        try:
            r = subprocess.run(["bash", "-c", cmd], capture_output=True, text=True, timeout=120)  # nosec B603,B607
            output = r.stdout or r.stderr
            masked = mask_text(output)
            return [{"type": "text", "text": masked}]
        except subprocess.TimeoutExpired:
            return [{"type": "text", "text": '{"error": "command timed out"}'}]
        except Exception as e:
            return [{"type": "text", "text": f'{{"error": "{e}"}}'}]

    if name == "vault_set":
        cred_name = arguments.get("name", "")
        cred_value = arguments.get("value", "")
        if not cred_name or not cred_value:
            return [{"type": "text", "text": '{"error": "name and value are required"}'}]
        vault = load_vault()
        vault_key = f"chat.{cred_name}"
        encrypted = vault_encrypt({vault_key: cred_value})
        vault.setdefault("credentials", {})[vault_key] = encrypted
        save_vault(vault)
        append_audit({"action": "set", "credential": vault_key, "purpose": "chat-origin"})
        return [
            {
                "type": "text",
                "text": json.dumps({"stored": vault_key, "length": len(cred_value)}),
            }
        ]

    if name == "vault_chat_clear":
        vault = load_vault()
        chat_keys = [k for k in vault.get("credentials", {}) if k.startswith("chat.")]
        for k in chat_keys:
            del vault["credentials"][k]
        save_vault(vault)
        return [{"type": "text", "text": json.dumps({"cleared": len(chat_keys)})}]

    return [{"type": "text", "text": f'{{"error": "unknown tool: {name}"}}'}]


async def main():
    async with mcp.server.stdio.stdio_server() as (read_stream, write_stream):
        await app.run(
            read_stream,
            write_stream,
            InitializationOptions(
                server_name="credential-vault",
                server_version="0.1.0",
                capabilities=ServerCapabilities(
                    tools=ToolsCapability(listChanged=False),
                ),
                instructions=(
                    "Credential Vault protects credentials during AI agent sessions.\n"
                    "Use vault_status to list available credentials, vault_get to retrieve one, "
                    "vault_mask to redact credential patterns from text, run_safe to execute "
                    "commands with masked output, vault_scan to scan/encrypt/redact, "
                    "vault_audit to view access logs, and vault_restore to put files back.\n"
                    "CLI also available: vault status|get|scan|restore|set|audit|export|import|watch"
                ),
            ),
        )


if __name__ == "__main__":
    import asyncio

    asyncio.run(main())
