import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it, vi } from "vitest";
import type { PluginInput } from "@opencode-ai/plugin";
import allInOneMCPPlugin, { createOpenCodeHooks } from "../src/index.js";
import { PluginLifecycleCoordinator } from "../src/hooks/lifecycle.js";
import type { ReasoningMemoryClient } from "../src/clients/reasoning-client.js";
import type { VaultClient } from "../src/clients/vault-client.js";

function fixture() {
  const captured: unknown[] = [];
  const reasoningClient = {
    inject: vi.fn(async () => "<reasoning_memory>prior context</reasoning_memory>"),
    capture: vi.fn(async (episode: unknown) => {
      captured.push(episode);
      return { id: "re-adapter", status: "captured" };
    }),
  } as unknown as ReasoningMemoryClient;
  const vaultClient = {
    mask: vi.fn(async (text: string) => text.replace("secret-value", "[REDACTED]")),
  } as unknown as VaultClient;
  const coordinator = new PluginLifecycleCoordinator({
    reasoningClient,
    vaultClient,
  });

  return {
    captured,
    hooks: createOpenCodeHooks(coordinator, "/workspace/project"),
  };
}

function chatOutput(text: string) {
  return {
    message: {
      id: "message-1",
      sessionID: "session-1",
      role: "user" as const,
      time: { created: Date.now() },
      agent: "build",
      model: { providerID: "provider", modelID: "model" },
    },
    parts: [
      {
        id: "part-1",
        sessionID: "session-1",
        messageID: "message-1",
        type: "text" as const,
        text,
      },
    ],
  };
}

describe("OpenCode hook adapter", () => {
  it("exports a loadable official plugin factory", async () => {
    expect(typeof allInOneMCPPlugin).toBe("function");

    const hooks = await allInOneMCPPlugin({
      directory: "/workspace/project",
    } as PluginInput);

    expect(hooks["chat.message"]).toBeTypeOf("function");
    expect(hooks["tool.execute.before"]).toBeTypeOf("function");
    expect(hooks["tool.execute.after"]).toBeTypeOf("function");
    expect(hooks.event).toBeTypeOf("function");
  });

  it("maps chat, tool, and session hooks to coordinator behavior", async () => {
    const { captured, hooks } = fixture();
    const message = chatOutput("Fix the adapter");

    await hooks["chat.message"]?.(
      {
        sessionID: "session-1",
        model: { providerID: "provider", modelID: "model" },
      },
      message,
    );
    expect(message.parts[0].text).toContain("<reasoning_memory>");
    expect(message.parts[0].text).toContain("Fix the adapter");

    const toolOutput = {
      title: "result",
      output: "token=secret-value",
      metadata: {},
    };
    await hooks["tool.execute.after"]?.(
      {
        tool: "read",
        sessionID: "session-1",
        callID: "call-1",
        args: { filePath: "src/index.ts" },
      },
      toolOutput,
    );
    expect(toolOutput.output).toBe("token=[REDACTED]");

    await hooks.event?.({
      event: {
        type: "session.idle",
        properties: { sessionID: "session-1" },
      },
    });
    expect(captured).toHaveLength(1);
    expect(captured[0]).toMatchObject({
      problem: "Fix the adapter",
      repo: "/workspace/project",
      model_id: "model",
      tool_calls: [
        {
          tool: "read",
          result_excerpt: "token=[REDACTED]",
          outcome: "success",
        },
      ],
    });
  });

  it("blocks protected paths in tool.execute.before", async () => {
    const { hooks } = fixture();

    await expect(
      hooks["tool.execute.before"]?.(
        { tool: "read", sessionID: "session-1", callID: "call-1" },
        { args: { filePath: "~/.config/gcloud/configurations/config_default" } },
      ),
    ).rejects.toThrow("blocked by security policy");
  });

  it("uses supported local plugin configuration", () => {
    const __dirname = dirname(fileURLToPath(import.meta.url));
    const configPath = resolve(__dirname, "../../.opencode/opencode.json");
    const rawConfig = readFileSync(configPath, "utf-8");
    const parsed = JSON.parse(rawConfig);

    expect(parsed.$schema).toBe("https://opencode.ai/config.json");
    expect(parsed.plugin).toEqual([
      [
        "../plugins/dist/index.js",
        {
          enableMemoryInjection: true,
          enableEpisodeCapture: true,
          enableOutputMasking: true,
          enableFileGuard: true,
        },
      ],
    ]);
  });
});
