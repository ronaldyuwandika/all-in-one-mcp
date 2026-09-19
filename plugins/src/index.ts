import type { Hooks, Plugin, PluginOptions } from "@opencode-ai/plugin";

export * from "./file-guard.js";
export * from "./bridge/subprocess.js";
export * from "./clients/reasoning-client.js";
export * from "./clients/vault-client.js";
export * from "./clients/reviewer-client.js";
export * from "./hooks/lifecycle.js";

import {
  PluginLifecycleCoordinator,
  type TaskCompleteData,
} from "./hooks/lifecycle.js";

interface SessionState {
  problem: string;
  modelId?: string;
  startedAt: number;
  toolCalls: NonNullable<TaskCompleteData["toolCalls"]>;
}

function optionEnabled(options: PluginOptions, key: string): boolean {
  return typeof options[key] === "boolean" ? options[key] : true;
}

export function createOpenCodeHooks(
  coordinator: PluginLifecycleCoordinator,
  directory: string,
): Hooks {
  const sessions = new Map<string, SessionState>();

  return {
    "chat.message": async (input, output) => {
      const textParts = output.parts.filter(
        (part): part is Extract<(typeof output.parts)[number], { type: "text" }> =>
          part.type === "text" && !part.synthetic,
      );
      const problem = textParts.map((part) => part.text).join("\n").trim();
      if (!problem) {
        return;
      }

      sessions.set(input.sessionID, {
        problem,
        modelId: input.model?.modelID ?? output.message.model.modelID,
        startedAt: Date.now(),
        toolCalls: [],
      });

      const result = await coordinator.onPromptSubmit(problem, {
        sessionId: input.sessionID,
        repo: directory,
      });
      if (result.injectedMemory && textParts[0]) {
        textParts[0].text = `${result.injectedMemory}\n\n${textParts[0].text}`;
      }
    },

    "tool.execute.before": async (input, output) => {
      const result = coordinator.onBeforeToolCall(input.tool, output.args, {
        sessionId: input.sessionID,
        repo: directory,
      });
      if (!result.allowed) {
        throw new Error(result.reason ?? `Tool '${input.tool}' was blocked`);
      }
    },

    "tool.execute.after": async (input, output) => {
      const result = await coordinator.onAfterToolCall(
        input.tool,
        output.output,
        { sessionId: input.sessionID, repo: directory },
      );
      output.output = result.maskedOutput;

      const state = sessions.get(input.sessionID);
      if (state) {
        state.toolCalls.push({
          tool: input.tool,
          args: input.args,
          result_excerpt: result.maskedOutput.slice(0, 1_000),
          outcome: "success",
        });
      }
    },

    event: async ({ event }) => {
      if (event.type !== "session.idle") {
        return;
      }

      const state = sessions.get(event.properties.sessionID);
      if (!state) {
        return;
      }
      sessions.delete(event.properties.sessionID);

      await coordinator.onTaskComplete(
        {
          problem: state.problem,
          outcome: "success",
          toolCalls: state.toolCalls,
          repo: directory,
          modelId: state.modelId,
          durationSeconds: Math.max(
            0,
            Math.round((Date.now() - state.startedAt) / 1_000),
          ),
        },
        {
          sessionId: event.properties.sessionID,
          repo: directory,
          modelId: state.modelId,
        },
      );
    },
  };
}

export const allInOneMCPPlugin = (async ({ directory }, options = {}) => {
  const coordinator = new PluginLifecycleCoordinator({
    enableMemoryInjection: optionEnabled(options, "enableMemoryInjection"),
    enableEpisodeCapture: optionEnabled(options, "enableEpisodeCapture"),
    enableOutputMasking: optionEnabled(options, "enableOutputMasking"),
    enableFileGuard: optionEnabled(options, "enableFileGuard"),
  });

  return createOpenCodeHooks(coordinator, directory);
}) satisfies Plugin;

export default allInOneMCPPlugin;
