import { normalize, resolve } from "node:path";
import { homedir } from "node:os";

/**
 * Compiled regular expressions for sensitive path patterns.
 * Pre-compiled at module initialization to ensure synchronous, sub-millisecond checks.
 */
const SENSITIVE_PATH_PATTERNS: readonly RegExp[] = [
  // Environment variables and secrets
  /(?:^|[\\/])\.env(?:\.[\w.-]+)?$/i,
  // AWS credentials and config
  /(?:^|[\\/])\.aws[\\/](?:credentials|config)$/i,
  // SSH keys and config
  /(?:^|[\\/])\.ssh[\\/](?:id_[a-zA-Z0-9_-]+|known_hosts|authorized_keys|config)$/i,
  /(?:^|[\\/])id_(?:rsa|ed25519|ecdsa|dsa)(?:\.pub)?$/i,
  // Kubernetes config
  /(?:^|[\\/])\.kube[\\/]config$/i,
  // Shell profiles that often contain tokens
  /(?:^|[\\/])\.(?:zshrc|bashrc|bash_profile|profile|bash_login|zprofile)$/i,
  // Netrc
  /(?:^|[\\/])\.netrc$/i,
  // Service account keys & credentials
  /(?:^|[\\/])(?:credentials|service[-_]account.*)\.json$/i,
  // GitHub CLI credentials
  /(?:^|[\\/])\.config[\\/]gh[\\/]hosts\.ya?ml$/i,
  // Private keys and certificates
  /\.(?:pem|key|pfx|p12|pkcs12|keystore)$/i,
  // Files explicitly containing secret/token/password
  /(?:^|[\\/])(?:secret|secrets|passwords?|creds?|credentials)\.(?:ya?ml|json|toml|ini|txt|env)$/i,
  // Cloud provider configuration and credential caches
  /(?:^|[\\/])\.config[\\/]gcloud(?:[\\/]|$)/i,
  /(?:^|[\\/])\.azure(?:[\\/]|$)/i,
];

export interface PathValidationResult {
  allowed: boolean;
  reason?: string;
  matchedPattern?: string;
  normalizedPath: string;
}

export interface ToolGuardResult {
  blocked: boolean;
  reason?: string;
  tool: string;
  path?: string;
}

/**
 * In-process file guard intercepting sensitive file paths synchronously.
 */
export class FileGuard {
  private readonly patterns: readonly RegExp[];
  private readonly home: string;

  constructor(customPatterns?: RegExp[]) {
    this.patterns = customPatterns ? [...customPatterns, ...SENSITIVE_PATH_PATTERNS] : SENSITIVE_PATH_PATTERNS;
    this.home = homedir();
  }

  /**
   * Normalize path expanding tilde to home directory and standardizing separators.
   */
  public normalizePath(filePath: string): string {
    if (!filePath) {
      return "";
    }
    let p = filePath.trim();
    if (p.startsWith("~")) {
      p = this.home + p.slice(1);
    }
    return normalize(resolve(p));
  }

  /**
   * Synchronously checks if a path matches any sensitive file pattern.
   * Execution time is typically <0.05ms (sub-millisecond).
   */
  public isSensitivePath(filePath: string): boolean {
    return !this.validatePath(filePath).allowed;
  }

  /**
   * Detailed synchronous path validation.
   */
  public validatePath(filePath: string): PathValidationResult {
    if (!filePath || typeof filePath !== "string") {
      return { allowed: true, normalizedPath: "" };
    }

    const normalized = this.normalizePath(filePath);

    for (const pattern of this.patterns) {
      if (pattern.test(filePath) || pattern.test(normalized)) {
        return {
          allowed: false,
          reason: `Access to sensitive path '${filePath}' is blocked by security policy`,
          matchedPattern: pattern.source,
          normalizedPath: normalized,
        };
      }
    }

    return { allowed: true, normalizedPath: normalized };
  }

  /**
   * Intercept a tool call by scanning tool arguments for sensitive file paths.
   */
  public guardToolCall(toolName: string, args: Record<string, unknown>): ToolGuardResult {
    if (!args || typeof args !== "object") {
      return { blocked: false, tool: toolName };
    }

    // Common argument names carrying file paths
    const pathKeys = [
      "filePath",
      "path",
      "file",
      "relative_path",
      "filename",
      "uri",
      "target",
      "destination",
      "src",
    ];

    for (const key of pathKeys) {
      const val = args[key];
      if (typeof val === "string" && val.length > 0) {
        const validation = this.validatePath(val);
        if (!validation.allowed) {
          return {
            blocked: true,
            reason: validation.reason,
            tool: toolName,
            path: val,
          };
        }
      }
    }

    // Also check paths array if present (e.g. batch read)
    if (Array.isArray(args.paths)) {
      for (const p of args.paths) {
        if (typeof p === "string") {
          const validation = this.validatePath(p);
          if (!validation.allowed) {
            return {
              blocked: true,
              reason: validation.reason,
              tool: toolName,
              path: p,
            };
          }
        }
      }
    }

    return { blocked: false, tool: toolName };
  }
}

export const defaultFileGuard = new FileGuard();
