import { describe, it, expect } from "vitest";
import { PluginLifecycleCoordinator } from "../src/hooks/lifecycle.js";
import { ReasoningMemoryClient } from "../src/clients/reasoning-client.js";
import { VaultClient } from "../src/clients/vault-client.js";
import { FileGuard } from "../src/file-guard.js";

describe("PluginLifecycleCoordinator", () => {
  const coordinator = new PluginLifecycleCoordinator({
    reasoningClient: new ReasoningMemoryClient(),
    vaultClient: new VaultClient(),
    fileGuard: new FileGuard(),
  });

  describe("onPromptSubmit", () => {
    it("injects reasoning memory block when problem has context", async () => {
      const prompt = "How should we handle Go snapshot testing?";
      const res = await coordinator.onPromptSubmit(prompt);

      expect(res.injected).toBe(true);
      expect(res.modifiedPrompt).toContain("<reasoning_memory>");
      expect(res.modifiedPrompt).toContain("</reasoning_memory>");
      expect(res.modifiedPrompt).toContain(prompt);
    });

    it("returns unchanged prompt if empty or whitespace", async () => {
      const res = await coordinator.onPromptSubmit("   ");
      expect(res.injected).toBe(false);
      expect(res.modifiedPrompt).toBe("   ");
    });
  });

  describe("onBeforeToolCall", () => {
    it("synchronously blocks sensitive paths like .env", () => {
      // Warm up
      coordinator.onBeforeToolCall("read", { filePath: ".env" });

      const start = performance.now();
      const res = coordinator.onBeforeToolCall("read", { filePath: ".env" });
      const elapsed = performance.now() - start;

      expect(res.allowed).toBe(false);
      expect(res.reason).toContain("blocked by security policy");
      expect(elapsed).toBeLessThan(1.0); // Sub-millisecond synchronous execution
    });

    it("synchronously allows non-sensitive paths", () => {
      // Warm up
      coordinator.onBeforeToolCall("read", { filePath: "src/index.ts" });

      const start = performance.now();
      const res = coordinator.onBeforeToolCall("read", { filePath: "src/index.ts" });
      const elapsed = performance.now() - start;

      expect(res.allowed).toBe(true);
      expect(elapsed).toBeLessThan(1.0);
    });
  });

  describe("onAfterToolCall", () => {
    it("masks secrets in tool output", async () => {
      const output = "Current environment:\nAWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\nGITHUB_TOKEN=ghp_123456789012345678901234567890123456";
      const res = await coordinator.onAfterToolCall("bash", output);

      expect(res.maskedOutput).not.toContain("ghp_123456789012345678901234567890123456");
      expect(res.maskedOutput).not.toContain("AKIAIOSFODNN7EXAMPLE");
      expect(res.maskedOutput).toContain("[REDACTED");
    });

    it("passes harmless output unchanged", async () => {
      const output = "All 12 tests passed successfully in 0.4s";
      const res = await coordinator.onAfterToolCall("test", output);
      expect(res.maskedOutput).toBe(output);
    });
  });

  describe("onTaskComplete", () => {
    it("captures completed episode into reasoning memory", async () => {
      const res = await coordinator.onTaskComplete({
        problem: "Verify plugin lifecycle coordinator task completion",
        thinkingTrace: "1. verify hooks\n2. execute assertions",
        outcome: "success",
        tags: ["lifecycle", "vitest"],
        durationSeconds: 5,
      });

      expect(res.error).toBeUndefined();
      expect(res.captured).toBe(true);
      expect(res.episodeId).toMatch(/^re-/);
    });

    it("preserves empty tags array", async () => {
      let capturedTags: string[] | undefined;
      const mockReasoningClient = {
        capture: async (ep: { tags?: string[] }) => {
          capturedTags = ep.tags;
          return { id: "re-mock", status: "captured" };
        },
      } as unknown as ReasoningMemoryClient;

      const customCoordinator = new PluginLifecycleCoordinator({
        reasoningClient: mockReasoningClient,
      });

      const res = await customCoordinator.onTaskComplete({
        problem: "Verify empty tags preservation",
        tags: [],
      });

      expect(res.captured).toBe(true);
      expect(capturedTags).toEqual([]);
    });
  });
});
