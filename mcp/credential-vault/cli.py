#!/usr/bin/env python3
"""vault \u2014 credential vault CLI.

Usage:
  vault status          List all stored credentials
  vault get <name>      Get a credential (prompts for purpose)
  vault scan            Scan files, encrypt, redact originals
  vault restore         Restore original files from backup
  vault set <name>      Store a chat credential (reads value from stdin)
  vault audit           Show access audit log
  vault export <file>   Export vault (encrypted, use --plain for JSON)
  vault import <file>   Import from export file
  vault watch           Launch TUI monitor (requires textual)
"""

import argparse
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import server as sv


def cmd_status(args):
    vault = sv.load_vault()
    creds = vault.get("credentials", {})
    names = sorted(creds.keys())
    file_creds = [n for n in names if not n.startswith("chat.")]
    chat_creds = [n for n in names if n.startswith("chat.")]
    scanned = sv.SCANNED_FLAG.exists()

    print("Credential Vault")
    print(f"{'=' * 50}")
    print(f"  Status:      {'scanned' if scanned else 'not scanned'}")
    print(f"  Total:       {len(names)} credentials")
    print(f"  File:        {len(file_creds)} credentials from disk")
    print(f"  Chat:        {len(chat_creds)} credentials from session")
    print(f"  Created:     {vault.get('created_at', 'never')}")
    print(f"  Last scan:   {vault.get('last_scanned', 'never')}")
    if args.verbose:
        print("\n  All credentials:")
        for n in names:
            tag = " [chat]" if n.startswith("chat.") else ""
            print(f"    {n}{tag}")
    elif names:
        print(f"\n  Credentials ({len(names)}):")
        for n in names[:20]:
            tag = " [chat]" if n.startswith("chat.") else ""
            print(f"    {n}{tag}")
        if len(names) > 20:
            print(f"    ... and {len(names) - 20} more (use --verbose)")


def cmd_get(args):
    vault = sv.load_vault()
    creds = vault.get("credentials", {})
    name = args.name

    if name not in creds:
        print(f"Error: credential '{name}' not found", file=sys.stderr)
        print("Use 'vault status' to list available credentials", file=sys.stderr)
        sys.exit(1)

    purpose = args.purpose or input("Purpose for accessing this credential: ").strip()
    if not purpose:
        print("Error: purpose is required", file=sys.stderr)
        sys.exit(1)

    try:
        encrypted = creds[name]
        entry = sv.vault_decrypt(encrypted)
        value = list(entry.values())[0] if isinstance(entry, dict) else str(entry)
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)

    sv.append_audit({"action": "get", "credential": name, "purpose": purpose})

    if args.quiet:
        print(value)
    else:
        print(f"Credential: {name}")
        print(f"Purpose:    {purpose}")
        print(f"{'=' * 50}")
        print(value)


def cmd_scan(args):
    print("Scanning for credentials...")
    discovered = sv.scan_credentials()
    print(f"  Found {len(discovered)} credentials")

    if args.redact is not False:
        print("Redacting files...")
        sv.redact_credential_files()
        print("  Done")

    for k in sorted(discovered.keys()):
        print(f"  \u2713 {k}")


def cmd_restore(args):
    print("Restoring original files...")
    sv.restore_original_files()
    print("  Done \u2014 all files restored from vault backup")


def cmd_set(args):
    name = args.name
    if args.value:
        value = args.value
    else:
        value = sys.stdin.read().strip()
    if not value:
        print("Error: value is required (pipe or pass as argument)", file=sys.stderr)
        sys.exit(1)

    vault = sv.load_vault()
    vault_key = f"chat.{name}"
    encrypted = sv.vault_encrypt({vault_key: value})
    vault.setdefault("credentials", {})[vault_key] = encrypted
    sv.save_vault(vault)
    sv.append_audit({"action": "set", "credential": vault_key, "purpose": "cli-set"})
    print(f"Stored: {vault_key} ({len(value)} characters)")


def cmd_audit(args):
    audit = sv.load_audit()
    if not audit:
        print("No audit records")
        return
    print(f"Audit log ({len(audit)} entries):")
    print(f"{'=' * 60}")
    for entry in audit[-50:]:
        ts = entry.get("timestamp", "?")[:19]
        action = entry.get("action", "?")
        cred = entry.get("credential", "?")
        purpose = entry.get("purpose", "")
        print(f"  {ts}  {action:10s}  {cred:30s}  {purpose}")


def cmd_export(args):
    count = sv.export_vault(args.file, plain=args.plain)
    print(f"Exported {count} credentials to {args.file}")
    if args.plain:
        print("  WARNING: Plaintext export \u2014 do not commit or share this file")


def cmd_import(args):
    count = sv.import_vault(args.file, merge=True)
    print(f"Imported {count} credentials from {args.file}")


def cmd_watch(args):
    try:
        from tui import run_tui
    except ImportError:
        print("TUI requires 'textual'. Install with: pip install textual", file=sys.stderr)
        sys.exit(1)
    run_tui(interval=args.interval, show_audit=not args.no_audit)


def main():
    parser = argparse.ArgumentParser(
        description="Credential Vault \u2014 protect secrets from AI agent exfiltration",
    )
    sub = parser.add_subparsers(dest="cmd")

    p_status = sub.add_parser("status", help="List stored credentials")
    p_status.add_argument("-v", "--verbose", action="store_true", help="Show all credential names")

    p_get = sub.add_parser("get", help="Get a credential value")
    p_get.add_argument("name", help="Credential name (from vault status)")
    p_get.add_argument("--purpose", "-p", help="Why you need this (skips prompt)")
    p_get.add_argument("--quiet", "-q", action="store_true", help="Output only the value")

    p_scan = sub.add_parser("scan", help="Scan and redact files")
    p_scan.add_argument(
        "--no-redact",
        dest="redact",
        action="store_false",
        help="Scan only, don't redact files",
    )

    sub.add_parser("restore", help="Restore original files from backup")

    p_set = sub.add_parser("set", help="Store a chat credential")
    p_set.add_argument("name", help="Credential name")
    p_set.add_argument("value", nargs="?", help="Value (omit to pipe from stdin)")

    sub.add_parser("audit", help="Show access audit log")

    p_export = sub.add_parser("export", help="Export vault")
    p_export.add_argument("file", help="Output file path")
    p_export.add_argument("--plain", action="store_true", help="Export as readable JSON (DANGEROUS)")

    p_import = sub.add_parser("import", help="Import vault from export file")
    p_import.add_argument("file", help="Input file path")

    p_watch = sub.add_parser("watch", help="Launch TUI monitor")
    p_watch.add_argument(
        "--interval",
        type=float,
        default=2.0,
        help="Poll interval in seconds (default: 2)",
    )
    p_watch.add_argument("--no-audit", action="store_true", help="Hide audit panel")

    args = parser.parse_args()

    commands = {
        "status": cmd_status,
        "get": cmd_get,
        "scan": cmd_scan,
        "restore": cmd_restore,
        "set": cmd_set,
        "audit": cmd_audit,
        "export": cmd_export,
        "import": cmd_import,
        "watch": cmd_watch,
    }

    if args.cmd in commands:
        commands[args.cmd](args)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
