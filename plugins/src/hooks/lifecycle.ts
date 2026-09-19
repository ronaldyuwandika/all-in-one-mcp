import { FileGuard, defaultFileGuard, ToolGuardResult } from "../file-guard.js";
import { ReasoningMemoryClient, EpisodeInput } from "../clients/reasoning-client.js";
import { VaultClient } from "../clients/vault-client.js";
import { maskErrorText } from "../bridge/subprocess.js";

export interface HookContext {
  sessionId?: string;
  repo?: string;
  modelId?: string;
  [key: string]: unknown;
}

export interface PromptSubmitResult {
  modifiedPrompt: string;
  injectedMemory?: string;
  injected: boolean;
}

export interface TaskCompleteData {
  problem: string;
  thinkingTrace?: string;
  toolCalls?: Array<{ tool: string; args?: unknown; result_excerpt?: string; outcome?: string }>;
  outcome?: "success" | "partial" | "failure" | string;
  tags?: string[];
  repo?: string;
  modelId?: string;
  durationSeconds?: number;
}

export interface TaskCompleteResult {
  episodeId?: string;
  captured: boolean;
  error?: string;
}

export interface ToolAfterResult {
  maskedOutput: string;
  tool: string;
}

export interface ToolBeforeResult {
  allowed: boolean;
  reason?: string;
  tool: string;
  path?: string;
}

export interface PluginHooksOptions {
  reasoningClient?: ReasoningMemoryClient;
  vaultClient?: VaultClient;
  fileGuard?: FileGuard;
  enableMemoryInjection?: boolean;
  enableEpisodeCapture?: boolean;
  enableOutputMasking?: boolean;
  enableFileGuard?: boolean;
}

/**
 * OpenCode lifecycle hooks coordinator for memory injection, episode capture, and secret protection.
 */
export class PluginLifecycleCoordinator {
  public readonly reasoningClient: ReasoningMemoryClient;
  public readonly vaultClient: VaultClient;
  public readonly fileGuard: FileGuard;

  public enableMemoryInjection: boolean;
  public enableEpisodeCapture: boolean;
  public enableOutputMasking: boolean;
  public enableFileGuard: boolean;

  constructor(options: PluginHooksOptions = {}) {
    this.reasoningClient = options.reasoningClient ?? new ReasoningMemoryClient();
    this.vaultClient = options.vaultClient ?? new VaultClient();
    this.fileGuard = options.fileGuard ?? defaultFileGuard;

    this.enableMemoryInjection = options.enableMemoryInjection ?? true;
    this.enableEpisodeCapture = options.enableEpisodeCapture ?? true;
    this.enableOutputMasking = options.enableOutputMasking ?? true;
    this.enableFileGuard = options.enableFileGuard ?? true;
  }

  /**
   * Hook: onPromptSubmit
   * Injects relevant past reasoning history at the START of a task.
   */
  public async onPromptSubmit(
    prompt: string,
    context?: HookContext,
  ): Promise<PromptSubmitResult> {
    if (!this.enableMemoryInjection || !prompt || prompt.trim().length === 0) {
      return { modifiedPrompt: prompt, injected: false };
    }

    try {
      const memoryBlock = await this.reasoningClient.inject(prompt, { topK: 3 });
      if (memoryBlock && memoryBlock.includes("<reasoning_memory>")) {
        const modifiedPrompt = `${memoryBlock}\n\n${prompt}`;
        return {
          modifiedPrompt,
          injectedMemory: memoryBlock,
          injected: true,
        };
      }
    } catch {
      // Graceful fallback: never block user prompts if reasoning memory is unavailable
    }

    return { modifiedPrompt: prompt, injected: false };
  }

  /**
   * Hook: onBeforeToolCall
   * Synchronously guards against reading/writing sensitive paths (<1ms latency).
   */
  public onBeforeToolCall(
    toolName: string,
    args: Record<string, unknown>,
    _context?: HookContext,
  ): ToolBeforeResult {
    if (!this.enableFileGuard) {
      return { allowed: true, tool: toolName };
    }

    const guardRes: ToolGuardResult = this.fileGuard.guardToolCall(toolName, args);
    if (guardRes.blocked) {
      return {
        allowed: false,
        reason: guardRes.reason,
        tool: toolName,
        path: guardRes.path,
      };
    }

    return { allowed: true, tool: toolName };
  }

  /**
   * Hook: onAfterToolCall
   * Masks sensitive credentials and tokens in tool output before returning to agent context.
   */
  public async onAfterToolCall(
    toolName: string,
    output: string,
    _context?: HookContext,
  ): Promise<ToolAfterResult> {
    if (!this.enableOutputMasking || !output) {
      return { maskedOutput: output, tool: toolName };
    }

    try {
      // Use vaultctl mask CLI for primary redaction
      const masked = await this.vaultClient.mask(output);
      return { maskedOutput: masked, tool: toolName };
    } catch {
      // Fall back immediately to compiled in-process regex masking
      return { maskedOutput: maskErrorText(output), tool: toolName };
    }
  }

  /**
   * Hook: onTaskComplete
   * Captures reasoning episode at the END of a task.
   */
  public async onTaskComplete(
    data: TaskCompleteData,
    context?: HookContext,
  ): Promise<TaskCompleteResult> {
    if (!this.enableEpisodeCapture || !data.problem) {
      return { captured: false };
    }

    try {
      const episode: EpisodeInput = {
        problem: data.problem,
        thinking_trace: data.thinkingTrace || "",
        outcome: data.outcome || "success",
        tool_calls: data.toolCalls || [],
        tags: data.tags ?? ["opencode-plugin"],
        repo: data.repo || context?.repo,
        model_id: data.modelId || context?.modelId,
        duration_seconds: data.durationSeconds,
      };

      const res = await this.reasoningClient.capture(episode);
      return { episodeId: res.id, captured: true };
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return { captured: false, error: msg };
    }
  }
}

export const defaultLifecycleCoordinator = new PluginLifecycleCoordinator();
