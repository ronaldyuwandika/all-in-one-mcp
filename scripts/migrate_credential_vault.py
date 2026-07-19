#!/usr/bin/env python3
"""Stream a legacy Python credential-vault into vaultctl migrate-stdin.

The JSON stream contains decrypted values only in the pipe; never redirect it
to a file or terminal. The Go receiver immediately encrypts the records.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--legacy-dir", type=Path, default=Path.home() / ".credential-vault")
    args = parser.parse_args()
    legacy_dir = args.legacy_dir.expanduser().resolve()
    sys.path.insert(0, str(legacy_dir))
    import server as legacy  # type: ignore[import-not-found]

    source = legacy.load_vault()
    credentials: dict[str, str] = {}
    for name, token in source.get("credentials", {}).items():
        try:
            entry = legacy.vault_decrypt(token)
        except Exception:
            continue
        credentials[name] = next(iter(entry.values())) if isinstance(entry, dict) else str(entry)

    files: dict[str, dict[str, object]] = {}
    for path, content in source.get("files", {}).items():
        try:
            mode = os.stat(path).st_mode & 0o777
        except OSError:
            mode = 0o600
        files[path] = {"content": content, "mode": mode}

    json.dump(
        {"credentials": credentials, "files": files, "audit": legacy.load_audit()},
        sys.stdout,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
