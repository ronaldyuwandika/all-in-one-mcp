import { describe, it, expect } from "vitest";
import {
  ReasoningMemoryClient,
  resolveReasoningBinary,
} from "../src/clients/reasoning-client.js";
import {
  VaultClient,
  resolveVaultBinary,
} from "../src/clients/vault-client.js";
import {
  ReviewerClient,
  resolveReviewerCommand,
} from "../src/clients/reviewer-client.js";

describe("Binary & Command Resolution", () => {
  it("resolves reasoning-memory binary", () => {
    const bin = resolveReasoningBinary();
    expect(bin).toBeTruthy();
  });

  it("resolves vaultctl binary", () => {
    const bin = resolveVaultBinary();
    expect(bin).toBeTruthy();
  });

  it("resolves reviewer command target", () => {
    const target = resolveReviewerCommand();
    expect(target.command).toBeTruthy();
    expect(Array.isArray(target.argsPrefix)).toBe(true);
  });
});

describe("VaultClient", () => {
  const client = new VaultClient();

  it("masks secret credentials via vaultctl mask", async () => {
    const raw = "export GITHUB_TOKEN=ghp_123456789012345678901234567890123456";
    const masked = await client.mask(raw);
    expect(masked).not.toContain("ghp_123456789012345678901234567890123456");
    expect(masked).toContain("[REDACTED");
  });

  it("returns empty string on empty input", async () => {
    const res = await client.mask("");
    expect(res).toBe("");
  });
});

describe("ReasoningMemoryClient", () => {
  const client = new ReasoningMemoryClient();

  it("injects context for a given problem", async () => {
    const context = await client.inject("Snapshot testing in Go");
    expect(context).toContain("<reasoning_memory>");
    expect(context).toContain("</reasoning_memory>");
  });

  it("retrieves matching episodes", async () => {
    const episodes = await client.retrieve("test", { topK: 2 });
    expect(Array.isArray(episodes)).toBe(true);
    if (episodes.length > 0) {
      expect(episodes[0].id).toBeTruthy();
      expect(episodes[0].problem).toBeTruthy();
    }
  });

  it("polishes a prompt with structured markdown", async () => {
    const res = await client.polish("Write unit tests for authentication handler", {
      agent: "codex",
    });
    expect(res.polished_prompt).toBeTruthy();
    expect(res.polished_prompt.length).toBeGreaterThan(20);
  });

  it("captures an episode via stdin JSON", async () => {
    const res = await client.capture({
      problem: "Automated test problem from vitest suite",
      thinking_trace: "1. test implementation step",
      outcome: "success",
      tags: ["vitest", "client-test"],
    });

    expect(res.id).toMatch(/^re-/);
    expect(res.status).toBe("captured");
  });
});

describe("ReviewerClient", () => {
  const client = new ReviewerClient();

  it("validates reviewer configuration", async () => {
    const report = await client.validate();
    expect(typeof report.valid).toBe("boolean");
    expect(Array.isArray(report.warnings)).toBe(true);
  });

  it("inspects audit log entries", async () => {
    const audit = await client.audit(5);
    expect(typeof audit.count).toBe("number");
    expect(Array.isArray(audit.entries)).toBe(true);
  });
});
