import { spawn } from "node:child_process";
import { Readable } from "node:stream";

/**
 * Mask common sensitive patterns from error messages and stderr.
 */
const SENSITIVE_TEXT_PATTERNS: Array<{ regex: RegExp; replacement: string }> = [
  { regex: /ghp_[a-zA-Z0-9]{36}/g, replacement: "[REDACTED:github_token]" },
  { regex: /gho_[a-zA-Z0-9]{36}/g, replacement: "[REDACTED:github_oauth]" },
  { regex: /glpat-[a-zA-Z0-9_-]{20,}/g, replacement: "[REDACTED:gitlab_token]" },
  { regex: /sk-[a-zA-Z0-9_-]{20,}/g, replacement: "[REDACTED:api_key]" },
  { regex: /AKIA[0-9A-Z]{16}/g, replacement: "[REDACTED:aws_key]" },
  { regex: /Bearer\s+[a-zA-Z0-9._~+/-]+=*/gi, replacement: "Bearer [REDACTED:token]" },
  {
    regex: /(api[-_]?key|password|passwd|secret|token)\s*[:=]\s*["']?[^\s"'&]+["']?/gi,
    replacement: "$1: [REDACTED]",
  },
];

export function maskErrorText(text: string): string {
  if (!text) return "";
  let masked = text;
  for (const { regex, replacement } of SENSITIVE_TEXT_PATTERNS) {
    masked = masked.replace(regex, replacement);
  }
  return masked;
}

export type CircuitBreakerState = "CLOSED" | "OPEN" | "HALF_OPEN";

export interface CircuitBreakerOptions {
  failureThreshold?: number;
  resetTimeoutMs?: number;
}

export class CircuitBreakerOpenError extends Error {
  constructor(public readonly name: string, public readonly resetTimeRemainingMs: number) {
    super(`Circuit breaker '${name}' is OPEN. Fast failing to prevent cascading errors. Reset in ${resetTimeRemainingMs}ms.`);
    this.name = "CircuitBreakerOpenError";
  }
}

export class SubprocessTimeoutError extends Error {
  constructor(public readonly command: string, public readonly timeoutMs: number) {
    super(`Command '${command}' timed out after ${timeoutMs}ms`);
    this.name = "SubprocessTimeoutError";
  }
}

export class SubprocessOutputExceededError extends Error {
  constructor(public readonly command: string, public readonly maxOutputBytes: number) {
    super(`Command '${command}' exceeded maximum output limit of ${maxOutputBytes} bytes`);
    this.name = "SubprocessOutputExceededError";
  }
}

export class SubprocessExecutionError extends Error {
  constructor(
    public readonly command: string,
    public readonly exitCode: number,
    public readonly stderr: string,
    public readonly stdout: string,
  ) {
    super(`Command '${command}' failed with exit code ${exitCode}: ${maskErrorText(stderr).trim() || maskErrorText(stdout).trim()}`);
    this.name = "SubprocessExecutionError";
  }
}

/**
 * Resilient circuit breaker for child process executions.
 */
export class CircuitBreaker {
  private state: CircuitBreakerState = "CLOSED";
  private failureCount = 0;
  private lastFailureTime = 0;
  private readonly failureThreshold: number;
  private readonly resetTimeoutMs: number;

  constructor(public readonly name: string, options: CircuitBreakerOptions = {}) {
    this.failureThreshold = options.failureThreshold ?? 5;
    this.resetTimeoutMs = options.resetTimeoutMs ?? 30_000;
  }

  public getState(): CircuitBreakerState {
    if (this.state === "OPEN") {
      const elapsed = Date.now() - this.lastFailureTime;
      if (elapsed >= this.resetTimeoutMs) {
        this.state = "HALF_OPEN";
      }
    }
    return this.state;
  }

  public recordSuccess(): void {
    this.failureCount = 0;
    this.state = "CLOSED";
  }

  public recordFailure(): void {
    this.failureCount++;
    this.lastFailureTime = Date.now();
    if (this.failureCount >= this.failureThreshold) {
      this.state = "OPEN";
    }
  }

  public checkExecutionAllowed(): void {
    const currentState = this.getState();
    if (currentState === "OPEN") {
      const remaining = Math.max(0, this.resetTimeoutMs - (Date.now() - this.lastFailureTime));
      throw new CircuitBreakerOpenError(this.name, remaining);
    }
  }
}

export interface SubprocessOptions {
  command: string;
  args?: string[];
  stdin?: string | Buffer | Readable;
  timeoutMs?: number;
  maxOutputBytes?: number;
  cwd?: string;
  env?: NodeJS.ProcessEnv;
  circuitBreaker?: CircuitBreaker;
}

export interface SubprocessResult {
  stdout: string;
  stderr: string;
  exitCode: number;
  durationMs: number;
}

/**
 * Spawn a subprocess with stdin streaming, strict timeout, error masking, and circuit breaker.
 */
export async function spawnSubprocess(options: SubprocessOptions): Promise<SubprocessResult> {
  const {
    command,
    args = [],
    stdin,
    timeoutMs = 10_000,
    maxOutputBytes = 10 * 1024 * 1024,
    cwd,
    env = process.env,
    circuitBreaker,
  } = options;

  if (circuitBreaker) {
    circuitBreaker.checkExecutionAllowed();
  }

  const startTime = Date.now();

  return new Promise<SubprocessResult>((resolve, reject) => {
    let child: ReturnType<typeof spawn>;
    try {
      child = spawn(command, args, {
        cwd,
        env,
        stdio: ["pipe", "pipe", "pipe"],
      });
    } catch (err: unknown) {
      circuitBreaker?.recordFailure();
      const message = err instanceof Error ? err.message : String(err);
      return reject(new Error(`Failed to spawn command '${command}': ${maskErrorText(message)}`));
    }

    let stdoutData = "";
    let stderrData = "";
    let totalBytes = 0;
    let outputExceeded = false;
    let timedOut = false;
    let finished = false;

    const timer = setTimeout(() => {
      timedOut = true;
      try {
        child.kill("SIGTERM");
      } catch {
        // ignore
      }
      setTimeout(() => {
        if (!finished) {
          try {
            child.kill("SIGKILL");
          } catch {
            // ignore
          }
        }
      }, 1000).unref();
    }, timeoutMs);

    const killOnLimit = () => {
      if (outputExceeded) return;
      outputExceeded = true;
      try {
        child.kill("SIGTERM");
      } catch {
        // ignore
      }
      setTimeout(() => {
        if (!finished) {
          try {
            child.kill("SIGKILL");
          } catch {
            // ignore
          }
        }
      }, 1000).unref();
    };

    if (child.stdout) {
      child.stdout.on("data", (chunk: Buffer | string) => {
        if (outputExceeded) return;
        const len = Buffer.isBuffer(chunk) ? chunk.length : Buffer.byteLength(chunk);
        totalBytes += len;
        if (totalBytes > maxOutputBytes) {
          killOnLimit();
          return;
        }
        stdoutData += chunk.toString("utf8");
      });
    }

    if (child.stderr) {
      child.stderr.on("data", (chunk: Buffer | string) => {
        if (outputExceeded) return;
        const len = Buffer.isBuffer(chunk) ? chunk.length : Buffer.byteLength(chunk);
        totalBytes += len;
        if (totalBytes > maxOutputBytes) {
          killOnLimit();
          return;
        }
        stderrData += chunk.toString("utf8");
      });
    }

    child.on("error", (err: Error) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      circuitBreaker?.recordFailure();
      if (outputExceeded) {
        return reject(new SubprocessOutputExceededError(command, maxOutputBytes));
      }
      reject(new Error(`Process '${command}' error: ${maskErrorText(err.message)}`));
    });

    child.on("close", (exitCode: number | null) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);

      if (outputExceeded) {
        circuitBreaker?.recordFailure();
        return reject(new SubprocessOutputExceededError(command, maxOutputBytes));
      }

      const durationMs = Date.now() - startTime;
      const code = exitCode ?? (timedOut ? 124 : 1);

      if (timedOut) {
        circuitBreaker?.recordFailure();
        return reject(new SubprocessTimeoutError(command, timeoutMs));
      }

      if (code !== 0) {
        circuitBreaker?.recordFailure();
        return reject(
          new SubprocessExecutionError(command, code, maskErrorText(stderrData), stdoutData),
        );
      }

      circuitBreaker?.recordSuccess();
      resolve({
        stdout: stdoutData,
        stderr: maskErrorText(stderrData),
        exitCode: 0,
        durationMs,
      });
    });

    // Feed stdin if provided
    if (child.stdin) {
      child.stdin.on("error", () => {
        // Ignore EPIPE or write errors if child process closed stdin early
      });

      if (stdin !== undefined && stdin !== null) {
        if (typeof stdin === "string" || Buffer.isBuffer(stdin)) {
          child.stdin.write(stdin);
          child.stdin.end();
        } else if (stdin instanceof Readable) {
          stdin.pipe(child.stdin);
        }
      } else {
        child.stdin.end();
      }
    }
  });
}
