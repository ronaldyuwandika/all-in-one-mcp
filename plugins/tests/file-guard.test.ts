import { describe, it, expect } from "vitest";
import { FileGuard, defaultFileGuard } from "../src/file-guard.js";

describe("FileGuard", () => {
  const guard = new FileGuard();

  it("blocks known sensitive files", () => {
    const sensitive = [
      ".env",
      ".env.local",
      ".env.production",
      "/path/to/.env",
      "~/.aws/credentials",
      "~/.aws/config",
      "~/.ssh/id_rsa",
      "~/.ssh/id_ed25519",
      "~/.ssh/known_hosts",
      "id_rsa",
      "id_ed25519.pub",
      "~/.kube/config",
      "~/.zshrc",
      "~/.bashrc",
      "~/.netrc",
      "service-account.json",
      "credentials.json",
      "server.key",
      "cert.pem",
      "secret.yaml",
      "secrets.json",
      "password.txt",
      "credentials.env",
      "~/.config/gh/hosts.yml",
      "~/.config/gh/hosts.yaml",
      "~/.config/gcloud/configurations/config_default",
      "~/.config/gcloud/application_default_credentials.json",
      "~/.azure/azureProfile.json",
      "~/.azure/msal_token_cache.json",
    ];

    for (const file of sensitive) {
      const res = guard.validatePath(file);
      expect(res.allowed, `Expected ${file} to be blocked`).toBe(false);
      expect(guard.isSensitivePath(file)).toBe(true);
    }
  });

  it("allows normal project files", () => {
    const safe = [
      "src/index.ts",
      "package.json",
      "README.md",
      "cmd/vault/main.go",
      "internal/cli/root.go",
      "/Users/dev/project/Makefile",
      "tsconfig.json",
      "tests/file-guard.test.ts",
    ];

    for (const file of safe) {
      const res = guard.validatePath(file);
      expect(res.allowed, `Expected ${file} to be allowed`).toBe(true);
      expect(guard.isSensitivePath(file)).toBe(false);
    }
  });

  it("guards tool calls with sensitive arguments", () => {
    const readBlock = guard.guardToolCall("read", { filePath: "/app/.env" });
    expect(readBlock.blocked).toBe(true);
    expect(readBlock.path).toBe("/app/.env");

    const writeBlock = guard.guardToolCall("write", { path: "~/.ssh/id_rsa" });
    expect(writeBlock.blocked).toBe(true);

    const batchBlock = guard.guardToolCall("read_batch", {
      paths: ["src/index.ts", "/etc/secret.yaml"],
    });
    expect(batchBlock.blocked).toBe(true);
    expect(batchBlock.path).toBe("/etc/secret.yaml");

    const safeCall = guard.guardToolCall("read", { filePath: "src/file-guard.ts" });
    expect(safeCall.blocked).toBe(false);
  });

  it("executes path validation synchronously with <1ms overhead", () => {
    const iterations = 1000;
    const testPaths = [
      "src/file-guard.ts",
      ".env",
      "~/.aws/credentials",
      "package.json",
      "~/.ssh/id_rsa",
      "node_modules/vitest/index.js",
    ];

    const start = performance.now();
    for (let i = 0; i < iterations; i++) {
      const p = testPaths[i % testPaths.length];
      guard.validatePath(p);
    }
    const elapsed = performance.now() - start;
    const avgPerCallMs = elapsed / iterations;

    // Must be well below 1 millisecond
    expect(avgPerCallMs).toBeLessThan(0.1);
  });

  it("defaultFileGuard singleton is initialized", () => {
    expect(defaultFileGuard).toBeInstanceOf(FileGuard);
    expect(defaultFileGuard.isSensitivePath(".env")).toBe(true);
  });
});
