import { existsSync } from "node:fs";
import { resolve, join } from "node:path";
import { spawnSubprocess, CircuitBreaker } from "../bridge/subprocess.js";

export interface VaultClientOptions {
  binaryPath?: string;
  circuitBreaker?: CircuitBreaker;
  timeoutMs?: number;
}

/**
 * Locate the vaultctl binary.
 */
export function resolveVaultBinary(customPath?: string): string {
  if (customPath && existsSync(customPath)) {
    return customPath;
  }
  if (process.env.VAULTCTL_BIN && existsSync(process.env.VAULTCTL_BIN)) {
    return process.env.VAULTCTL_BIN;
  }

  const cwd = process.cwd();
  const candidates = [
    resolve(cwd, "bin", "vaultctl"),
    resolve(cwd, "..", "bin", "vaultctl"),
    resolve(cwd, "mcp", "credential-vault-go", "vaultctl"),
    resolve(cwd, "..", "mcp", "credential-vault-go", "vaultctl"),
    join(process.env.HOME || "", "mcp", "bin", "vaultctl"),
  ];

  for (const candidate of candidates) {
    if (existsSync(candidate)) {
      return candidate;
    }
  }

  return "vaultctl";
}

/**
 * Typed client for credential-vault-go CLI (vaultctl).
 */
export class VaultClient {
  private readonly binaryPath: string;
  private readonly circuitBreaker: CircuitBreaker;
  private readonly defaultTimeoutMs: number;

  constructor(options: VaultClientOptions = {}) {
    this.binaryPath = resolveVaultBinary(options.binaryPath);
    this.circuitBreaker = options.circuitBreaker ?? new CircuitBreaker("vaultctl", { failureThreshold: 5 });
    this.defaultTimeoutMs = options.timeoutMs ?? 5_000;
  }

  /**
   * Mask sensitive credentials from text using vaultctl mask with stdin streaming.
   */
  public async mask(text: string): Promise<string> {
    if (!text) return "";

    const res = await spawnSubprocess({
      command: this.binaryPath,
      args: ["mask"],
      stdin: text,
      timeoutMs: this.defaultTimeoutMs,
      circuitBreaker: this.circuitBreaker,
    });

    return res.stdout;
  }

  /**
   * Retrieve a decrypted credential with audit purpose.
   */
  public async get(name: string, purpose: string): Promise<string> {
    const res = await spawnSubprocess({
      command: this.binaryPath,
      args: ["get", name, "-p", purpose, "-q"],
      timeoutMs: this.defaultTimeoutMs,
      circuitBreaker: this.circuitBreaker,
    });

    return res.stdout.trim();
  }

  /**
   * Encrypt and store a secret value in the vault.
   */
  public async set(name: string, value: string): Promise<void> {
    await spawnSubprocess({
      command: this.binaryPath,
      args: ["set", name],
      stdin: value,
      timeoutMs: this.defaultTimeoutMs,
      circuitBreaker: this.circuitBreaker,
    });
  }

  /**
   * List all stored credential names in the vault.
   */
  public async status(): Promise<string[]> {
    const res = await spawnSubprocess({
      command: this.binaryPath,
      args: ["status"],
      timeoutMs: this.defaultTimeoutMs,
      circuitBreaker: this.circuitBreaker,
    });

    return res.stdout
      .split("\n")
      .map((s) => s.trim())
      .filter((s) => s.length > 0);
  }
}
