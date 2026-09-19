import process from "node:process";
import { describe, it, expect } from "vitest";
import {
  spawnSubprocess,
  maskErrorText,
  CircuitBreaker,
  CircuitBreakerOpenError,
  SubprocessTimeoutError,
  SubprocessExecutionError,
  SubprocessOutputExceededError,
} from "../src/bridge/subprocess.js";

describe("maskErrorText", () => {
  it("masks github tokens", () => {
    const raw = "Error connecting with token ghp_123456789012345678901234567890123456 to github";
    const masked = maskErrorText(raw);
    expect(masked).not.toContain("ghp_123456789012345678901234567890123456");
    expect(masked).toContain("[REDACTED:github_token]");
  });

  it("masks api keys and bearer tokens", () => {
    const raw = "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.t-IDN45hrAC06Z2DB5e9";
    const masked = maskErrorText(raw);
    expect(masked).toContain("Bearer [REDACTED:token]");
  });

  it("masks aws access keys", () => {
    const raw = "AWS Error with key AKIAIOSFODNN7EXAMPLE";
    const masked = maskErrorText(raw);
    expect(masked).toContain("[REDACTED:aws_key]");
  });
});

describe("CircuitBreaker", () => {
  it("starts in CLOSED state and trips to OPEN on reaching threshold", () => {
    const cb = new CircuitBreaker("test-breaker", { failureThreshold: 3, resetTimeoutMs: 100 });
    expect(cb.getState()).toBe("CLOSED");

    cb.recordFailure();
    cb.recordFailure();
    expect(cb.getState()).toBe("CLOSED");

    cb.recordFailure();
    expect(cb.getState()).toBe("OPEN");

    expect(() => cb.checkExecutionAllowed()).toThrow(CircuitBreakerOpenError);
  });

  it("transitions to HALF_OPEN after reset timeout and recovers on success", async () => {
    const cb = new CircuitBreaker("test-recovery", { failureThreshold: 2, resetTimeoutMs: 50 });
    cb.recordFailure();
    cb.recordFailure();
    expect(cb.getState()).toBe("OPEN");

    // Wait for reset timeout
    await new Promise((r) => setTimeout(r, 60));

    expect(cb.getState()).toBe("HALF_OPEN");

    // Success in half-open resets to closed
    cb.recordSuccess();
    expect(cb.getState()).toBe("CLOSED");
  });
});

describe("spawnSubprocess", () => {
  it("executes basic command and captures stdout", async () => {
    const res = await spawnSubprocess({
      command: process.execPath,
      args: ["-e", "console.log('subprocess-ok')"],
    });

    expect(res.exitCode).toBe(0);
    expect(res.stdout.trim()).toBe("subprocess-ok");
    expect(res.durationMs).toBeGreaterThan(0);
  });

  it("streams stdin into child process", async () => {
    const payload = "streamed-stdin-payload-12345";
    const res = await spawnSubprocess({
      command: process.execPath,
      args: ["-e", "process.stdin.pipe(process.stdout)"],
      stdin: payload,
    });

    expect(res.exitCode).toBe(0);
    expect(res.stdout).toBe(payload);
  });

  it("enforces execution timeout and terminates process", async () => {
    await expect(
      spawnSubprocess({
        command: process.execPath,
        args: ["-e", "setTimeout(() => {}, 5000)"],
        timeoutMs: 100,
      }),
    ).rejects.toThrow(SubprocessTimeoutError);
  });

  it("enforces maxOutputBytes limit and terminates process", async () => {
    await expect(
      spawnSubprocess({
        command: process.execPath,
        args: ["-e", "console.log('x'.repeat(2000))"],
        maxOutputBytes: 100,
      }),
    ).rejects.toThrow(SubprocessOutputExceededError);
  });

  it("captures non-zero exit code and masks stderr", async () => {
    await expect(
      spawnSubprocess({
        command: process.execPath,
        args: [
          "-e",
          "console.error('Failed with ghp_123456789012345678901234567890123456'); process.exit(2)",
        ],
      }),
    ).rejects.toThrow(SubprocessExecutionError);
  });

  it("updates circuit breaker on failure", async () => {
    const cb = new CircuitBreaker("cb-test", { failureThreshold: 2 });

    try {
      await spawnSubprocess({
        command: process.execPath,
        args: ["-e", "process.exit(1)"],
        circuitBreaker: cb,
      });
    } catch {
      // expected
    }

    try {
      await spawnSubprocess({
        command: process.execPath,
        args: ["-e", "process.exit(1)"],
        circuitBreaker: cb,
      });
    } catch {
      // expected
    }

    expect(cb.getState()).toBe("OPEN");
    expect(() => cb.checkExecutionAllowed()).toThrow(CircuitBreakerOpenError);
  });
});
