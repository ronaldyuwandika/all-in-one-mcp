import { existsSync } from "node:fs";
import { resolve, join } from "node:path";
import { spawnSubprocess, CircuitBreaker } from "../bridge/subprocess.js";

export interface ToolCallInput {
  tool: string;
  args?: unknown;
  result_excerpt?: string;
  outcome?: string;
}

export interface FailedApproachInput {
  approach: string;
  failure_mode: string;
  root_cause: string;
  lesson: string;
}

export interface EpisodeInput {
  problem: string;
  thinking_trace?: string;
  outcome?: "success" | "partial" | "failure" | string;
  domain?: string;
  tier?: "episodic" | "semantic";
  tags?: string[];
  repo?: string;
  project?: string;
  model_id?: string;
  duration_seconds?: number;
  tool_calls?: ToolCallInput[];
  failed_approaches?: FailedApproachInput[];
}

export interface EpisodeSummary {
  id: string;
  created_at: string;
  problem: string;
  domain: string;
  outcome: string;
  tier?: string;
  tags: string[];
  repo?: string;
  step_count?: number;
  tool_count?: number;
  _local_score?: number;
  _vector_score?: number;
}

export interface PolishResult {
  polished_prompt: string;
  task_type?: string;
  domain?: string;
  architecture_rules?: string[];
  injected_context_count?: number;
  warnings?: string[];
}

export interface InjectResult {
  context: string;
  episode_count: number;
  pattern_count: number;
}

export interface ReasoningClientOptions {
  binaryPath?: string;
  circuitBreaker?: CircuitBreaker;
  timeoutMs?: number;
}

/**
 * Locate the reasoning-memory executable across common development and installation paths.
 */
export function resolveReasoningBinary(customPath?: string): string {
  if (customPath && existsSync(customPath)) {
    return customPath;
  }
  if (process.env.REASONING_MEMORY_BIN && existsSync(process.env.REASONING_MEMORY_BIN)) {
    return process.env.REASONING_MEMORY_BIN;
  }

  const cwd = process.cwd();
  const candidates = [
    resolve(cwd, "bin", "reasoning-memory"),
    resolve(cwd, "..", "bin", "reasoning-memory"),
    resolve(cwd, "mcp", "reasoning-memory", "reasoning-memory"),
    resolve(cwd, "..", "mcp", "reasoning-memory", "reasoning-memory"),
    join(process.env.HOME || "", "mcp", "bin", "reasoning-memory"),
  ];

  for (const candidate of candidates) {
    if (existsSync(candidate)) {
      return candidate;
    }
  }

  return "reasoning-memory";
}

/**
 * Typed client for reasoning-memory CLI subcommands (inject, retrieve, capture, polish).
 */
export class ReasoningMemoryClient {
  private readonly binaryPath: string;
  private readonly circuitBreaker: CircuitBreaker;
  private readonly defaultTimeoutMs: number;

  constructor(options: ReasoningClientOptions = {}) {
    this.binaryPath = resolveReasoningBinary(options.binaryPath);
    this.circuitBreaker = options.circuitBreaker ?? new CircuitBreaker("reasoning-memory", { failureThreshold: 5 });
    this.defaultTimeoutMs = options.timeoutMs ?? 10_000;
  }

  /**
   * Inject relevant reasoning context as XML block (<reasoning_memory>...</reasoning_memory>).
   */
  public async inject(
    problem: string,
    options: { topK?: number; includeTraces?: boolean } = {},
  ): Promise<string> {
    const args = ["inject", "--json"];
    if (options.topK) {
      args.push("--top-k", String(options.topK));
    }
    if (options.includeTraces) {
      args.push("--include-traces");
    }

    const res = await spawnSubprocess({
      command: this.binaryPath,
      args,
      stdin: problem,
      timeoutMs: this.defaultTimeoutMs,
      circuitBreaker: this.circuitBreaker,
    });

    try {
      const parsed = JSON.parse(res.stdout.trim()) as InjectResult;
      return parsed.context || "";
    } catch {
      return res.stdout.trim();
    }
  }

  /**
   * Retrieve matching historical episodes from the structured index.
   */
  public async retrieve(
    problem: string,
    options: {
      domain?: string;
      outcome?: string;
      repo?: string;
      tags?: string[];
      topK?: number;
    } = {},
  ): Promise<EpisodeSummary[]> {
    const args = ["retrieve", "--json"];
    if (options.domain) args.push("--domain", options.domain);
    if (options.outcome) args.push("--outcome", options.outcome);
    if (options.repo) args.push("--repo", options.repo);
    if (options.tags && options.tags.length > 0) args.push("--tags", options.tags.join(","));
    if (options.topK) args.push("--top-k", String(options.topK));

    const res = await spawnSubprocess({
      command: this.binaryPath,
      args,
      stdin: problem,
      timeoutMs: this.defaultTimeoutMs,
      circuitBreaker: this.circuitBreaker,
    });

    return JSON.parse(res.stdout.trim()) as EpisodeSummary[];
  }

  /**
   * Capture a completed reasoning episode via JSON stdin.
   */
  public async capture(episode: EpisodeInput): Promise<{ id: string; status: string }> {
    const args = ["capture", "--json"];

    const res = await spawnSubprocess({
      command: this.binaryPath,
      args,
      stdin: JSON.stringify(episode),
      timeoutMs: this.defaultTimeoutMs,
      circuitBreaker: this.circuitBreaker,
    });

    try {
      const parsed = JSON.parse(res.stdout.trim()) as { id: string; status: string };
      return parsed;
    } catch {
      return { id: res.stdout.trim(), status: "captured" };
    }
  }

  /**
   * Polish an unstructured prompt with agent rules and memory context.
   */
  public async polish(
    rawPrompt: string,
    options: {
      agent?: string;
      domain?: string;
      repo?: string;
      skill?: string;
      format?: string;
      topK?: number;
      includeContext?: boolean;
    } = {},
  ): Promise<PolishResult> {
    const args = ["polish", "--json"];
    if (options.agent) args.push("--agent", options.agent);
    if (options.domain) args.push("--domain", options.domain);
    if (options.repo) args.push("--repo", options.repo);
    if (options.skill) args.push("--skill", options.skill);
    if (options.format) args.push("--format", options.format);
    if (options.topK) args.push("--top-k", String(options.topK));
    if (options.includeContext !== undefined) {
      args.push(`--include-context=${options.includeContext}`);
    }

    const res = await spawnSubprocess({
      command: this.binaryPath,
      args,
      stdin: rawPrompt,
      timeoutMs: this.defaultTimeoutMs,
      circuitBreaker: this.circuitBreaker,
    });

    return JSON.parse(res.stdout.trim()) as PolishResult;
  }
}
