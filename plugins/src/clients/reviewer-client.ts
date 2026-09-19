import { existsSync } from "node:fs";
import { resolve, join } from "node:path";
import { spawnSubprocess, CircuitBreaker } from "../bridge/subprocess.js";

export interface ReviewComment {
  file: string;
  line: number;
  rule: string;
  severity: string;
  message: string;
  suggested_fix?: string;
}

export interface ReviewResult {
  summary: string;
  verdict: "APPROVED" | "CHANGES_REQUESTED" | "NEEDS_WORK" | string;
  confidence: number;
  comments: ReviewComment[];
  review_time_s?: number;
  llm_provider?: string;
  posted?: boolean;
}

export interface AuditEntry {
  timestamp: string;
  operation: string;
  url?: string;
  verdict?: string;
  comments_count?: number;
  error?: string;
  duration_ms?: number;
}

export interface ValidationReport {
  valid: boolean;
  warning_count: number;
  warnings: string[];
  config_summary?: Record<string, unknown>;
}

export interface ReviewerClientOptions {
  binaryPath?: string;
  pythonPath?: string;
  projectDir?: string;
  circuitBreaker?: CircuitBreaker;
  timeoutMs?: number;
}

interface CommandTarget {
  command: string;
  argsPrefix: string[];
  cwd?: string;
}

export function resolveReviewerCommand(options: ReviewerClientOptions = {}): CommandTarget {
  const cwd = process.cwd();
  const possibleRoots = [
    options.projectDir,
    resolve(cwd, "mcp", "pr-reviewer"),
    resolve(cwd, "..", "mcp", "pr-reviewer"),
    cwd,
  ].filter((p): p is string => Boolean(p && existsSync(p)));

  // Check 1: Custom binary or console script
  if (options.binaryPath && existsSync(options.binaryPath)) {
    return { command: options.binaryPath, argsPrefix: [] };
  }

  // Check 2: Virtualenv binary in pr-reviewer
  for (const root of possibleRoots) {
    const venvBin = join(root, ".venv", "bin", "pr-reviewer");
    if (existsSync(venvBin)) {
      return { command: venvBin, argsPrefix: [], cwd: root };
    }
    const venvPy = join(root, ".venv", "bin", "python");
    if (existsSync(venvPy)) {
      return { command: venvPy, argsPrefix: ["-m", "core.cli"], cwd: root };
    }
  }

  // Check 3: Python interpreter
  const pythonCmd = options.pythonPath || "python3";
  for (const root of possibleRoots) {
    if (existsSync(join(root, "core", "cli.py"))) {
      return { command: pythonCmd, argsPrefix: ["-m", "core.cli"], cwd: root };
    }
  }

  return { command: "pr-reviewer", argsPrefix: [] };
}

/**
 * Typed client for PR Reviewer CLI.
 */
export class ReviewerClient {
  private readonly target: CommandTarget;
  private readonly circuitBreaker: CircuitBreaker;
  private readonly defaultTimeoutMs: number;

  constructor(options: ReviewerClientOptions = {}) {
    this.target = resolveReviewerCommand(options);
    this.circuitBreaker = options.circuitBreaker ?? new CircuitBreaker("pr-reviewer", { failureThreshold: 3 });
    this.defaultTimeoutMs = options.timeoutMs ?? 30_000;
  }

  /**
   * Review a raw unified diff or code change via stdin streaming.
   */
  public async reviewDiff(
    diff: string,
    options: { title?: string; description?: string; repoUrl?: string } = {},
  ): Promise<ReviewResult> {
    const args = [...this.target.argsPrefix, "review", "--stdin"];
    if (options.title) args.push("--title", options.title);
    if (options.description) args.push("--description", options.description);
    if (options.repoUrl) args.push("--repo-url", options.repoUrl);

    const res = await spawnSubprocess({
      command: this.target.command,
      args,
      stdin: diff,
      cwd: this.target.cwd,
      timeoutMs: this.defaultTimeoutMs,
      circuitBreaker: this.circuitBreaker,
    });

    return JSON.parse(res.stdout.trim()) as ReviewResult;
  }

  /**
   * Review a PR or MR by URL.
   */
  public async reviewUrl(url: string, post = false): Promise<ReviewResult> {
    const args = [...this.target.argsPrefix, "review", "--url", url];
    if (post) args.push("--post");

    const res = await spawnSubprocess({
      command: this.target.command,
      args,
      cwd: this.target.cwd,
      timeoutMs: this.defaultTimeoutMs,
      circuitBreaker: this.circuitBreaker,
    });

    return JSON.parse(res.stdout.trim()) as ReviewResult;
  }

  /**
   * Fetch recent audit log entries.
   */
  public async audit(limit = 50): Promise<{ count: number; entries: AuditEntry[] }> {
    const args = [...this.target.argsPrefix, "audit", "--limit", String(limit)];

    const res = await spawnSubprocess({
      command: this.target.command,
      args,
      cwd: this.target.cwd,
      timeoutMs: 5_000,
      circuitBreaker: this.circuitBreaker,
    });

    return JSON.parse(res.stdout.trim()) as { count: number; entries: AuditEntry[] };
  }

  /**
   * Validate configuration.
   */
  public async validate(): Promise<ValidationReport> {
    const args = [...this.target.argsPrefix, "validate"];

    const res = await spawnSubprocess({
      command: this.target.command,
      args,
      cwd: this.target.cwd,
      timeoutMs: 5_000,
      circuitBreaker: this.circuitBreaker,
    });

    return JSON.parse(res.stdout.trim()) as ValidationReport;
  }
}
