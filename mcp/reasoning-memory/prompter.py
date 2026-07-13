import re
from pathlib import Path

SKILL_DIRS = [
    Path.home() / ".claude" / "skills",
    Path.home() / ".agents" / "skills",
    Path.home() / ".config" / "opencode" / "skill",
]

SKILL_CACHE: dict[str, dict] = {}


def load_skill(skill_name: str) -> dict | None:
    """Find a skill by name across all skill directories and return its content.

    Returns dict with keys: name, description, sections (list of heading+body pairs),
    full_text. Returns None if not found.
    """
    if skill_name in SKILL_CACHE:
        return SKILL_CACHE[skill_name]

    for base in SKILL_DIRS:
        skill_dir = base / skill_name
        skill_file = skill_dir / "SKILL.md"
        if skill_file.exists():
            text = skill_file.read_text(encoding="utf-8")
            result = _parse_skill(skill_name, text)
            SKILL_CACHE[skill_name] = result
            return result

    return None


def _parse_skill(name: str, text: str) -> dict:
    """Parse a SKILL.md into structured sections."""
    lines = text.split("\n")

    description = ""
    in_frontmatter = text.startswith("---")
    if in_frontmatter:
        parts = text.split("---", 2)
        if len(parts) >= 3:
            import yaml

            try:
                fm = yaml.safe_load(parts[1])
                if isinstance(fm, dict):
                    description = fm.get("description", "")
            except Exception:
                pass
            body = parts[2]
            lines = body.split("\n")

    sections = []
    current_heading = ""
    current_body = []
    for line in lines:
        if line.startswith("## "):
            if current_heading:
                sections.append(
                    {
                        "heading": current_heading,
                        "body": "\n".join(current_body).strip(),
                    }
                )
            current_heading = line[3:].strip()
            current_body = []
        else:
            current_body.append(line)

    if current_heading:
        sections.append(
            {
                "heading": current_heading,
                "body": "\n".join(current_body).strip(),
            }
        )

    key_sections = {}
    for s in sections:
        h = s["heading"].lower()
        if "intent" in h or "purpose" in h:
            key_sections["intent"] = s["body"]
        elif "core principle" in h or "principle" in h:
            key_sections["principles"] = s["body"]
        elif "validation" in h or "checklist" in h:
            key_sections["validation"] = s["body"]
        elif "workflow" in h or "process" in h:
            key_sections["workflow"] = s["body"]
        elif "constraint" in h or "limit" in h or "rule" in h:
            key_sections["constraints"] = s["body"]
        elif "use when" in h:
            key_sections["use_when"] = s["body"]
        elif "do not use" in h:
            key_sections["do_not_use"] = s["body"]
        elif "template" in h or "example" in h:
            key_sections["examples"] = s["body"]

    result = {
        "name": name,
        "description": description,
        "sections": sections,
        "key_sections": key_sections,
        "full_text": text,
    }
    return result


def build_skill_context(skill_data: dict) -> str:
    """Format loaded skill data into a context block for injection."""
    lines = [f"## Loaded Skill: {skill_data['name']}", ""]

    if skill_data["description"]:
        lines.append(skill_data["description"])
        lines.append("")

    ks = skill_data["key_sections"]

    if ks.get("intent"):
        lines.append("### Intent")
        lines.append("")
        lines.append(ks["intent"][:600])
        lines.append("")

    if ks.get("principles"):
        lines.append("### Core Principles")
        lines.append("")
        for line in ks["principles"].split("\n")[:8]:
            lines.append(line)
        lines.append("")

    if ks.get("validation"):
        lines.append("### Validation Checklist")
        lines.append("")
        for line in ks["validation"].split("\n")[:8]:
            lines.append(line)
        lines.append("")

    if ks.get("workflow"):
        lines.append("### Workflow")
        lines.append("")
        for line in ks["workflow"].split("\n")[:10]:
            lines.append(line)
        lines.append("")

    if ks.get("constraints"):
        lines.append("### Constraints")
        lines.append("")
        for line in ks["constraints"].split("\n")[:6]:
            lines.append(line)
        lines.append("")

    return "\n".join(lines)


def build_compact_skill_context(skill_data: dict) -> str:
    """Compact skill injection: 1-line intent + 4 constraints + format enforcement.

    For generation tasks where full skill context wastes tokens.
    """
    ks = skill_data["key_sections"]
    lines = [f"## Loaded Skill: {skill_data['name']} (compact)", ""]

    intent = ks.get("intent", skill_data.get("description", ""))
    first_meaningful = ""
    for sep in (". ", "\n\n", "Act as"):
        idx = intent.find(sep)
        if idx > 20:
            first_meaningful = intent[: idx + len(sep.split(" ")[0])]
            break
    if not first_meaningful:
        first_meaningful = intent[:120]
    if first_meaningful:
        lines.append(f"Context: {first_meaningful.strip()}")
        lines.append("")

    constraints = ks.get("constraints", "")
    c_lines = [line.strip() for line in constraints.split("\n") if line.strip().startswith("-")]
    for c in c_lines[:4]:
        short = c.replace("  ", " ").rstrip()
        lines.append(short)

    lines.append("")
    lines.append("**Output rule**: Produce ONLY a single code block with the implementation.")
    lines.append("No explanatory text, no README, no project structure files.")

    return "\n".join(lines)


TASK_PATTERNS = {
    "coding": [
        r"\b(implement|code|write|program|build|develop|refactor|debug|fix|test)\b",
        r"\b(api|function|class|method|module|library|cli|server|endpoint)\b",
        r"\b(go|python|rust|typescript|javascript|bash|java|ruby|c\+\+|\.py|\.go|\.ts)\b",
    ],
    "agentic": [
        r"\b(plan|orchestrate|coordinate|pipeline|workflow|agent|automate|deploy)\b",
        r"\b(tool|action|execute|run|schedule|monitor|manage)\b",
        r"\b(multi.?step|sequence|chain|parallel|distributed)\b",
    ],
    "analysis": [
        r"\b(analyze|evaluate|assess|review|audit|investigate|diagnose)\b",
        r"\b(why|what caused|root cause|compare|contrast|trade.?off)\b",
        r"\b(metrics|performance|security|vulnerability|risk|impact)\b",
    ],
}

DOMAIN_RULES = {
    "coding": {
        "go": "- Go: explicit error propagation (`if err != nil`), goroutine lifecycle, channel state safety",
        "python": "- Python: lazy iteration (generators), GIL-aware parallelism, clean structural readability",
        "bash": "- Bash: `set -euo pipefail`, double-quote all expansions, clean exit code handling",
    },
    "agentic": {
        "plan": "- Plan: decompose into sequential steps with clear success criteria per step",
        "tool": "- Tools: verify each tool call succeeded before proceeding; handle failures gracefully",
        "state": "- State: track intermediate results; never assume prior steps completed",
    },
    "analysis": {
        "evidence": "- Evidence: base conclusions on data, not assumptions",
        "structure": "- Structure: isolate variables, test each hypothesis independently",
        "report": "- Report: clear verdict with supporting evidence and confidence level",
    },
}


def detect_task_type(raw_prompt: str) -> str:
    """Detect the dominant task type: coding, agentic, analysis, or general."""
    lower = raw_prompt.lower()
    scores = {}
    for task_type, patterns in TASK_PATTERNS.items():
        score = 0
        for pat in patterns:
            matches = re.findall(pat, lower)
            score += len(matches)
        scores[task_type] = score

    best = max(scores, key=scores.get)
    if scores[best] == 0:
        return "general"
    return best


def detect_language(raw_prompt: str) -> str | None:
    """Detect programming language from the raw prompt."""
    lang_patterns = {
        "go": [r"\bgo\b", r"\.go\b", r"golang", r"\bgoroutine\b"],
        "python": [r"\bpython\b", r"\.py\b", r"\bpip\b", r"\bvenv\b"],
        "bash": [r"\bbash\b", r"\bsh\b", r"\bshell\b", r"\bzsh\b"],
        "typescript": [r"\btypescript\b", r"\.ts\b", r"\btsx\b"],
        "javascript": [
            r"\bjavascript\b",
            r"\.js\b",
            r"\bnode\b",
            r"\bnpm\b",
            r"\byarn\b",
        ],
        "rust": [r"\brust\b", r"\.rs\b", r"\bcargo\b"],
        "java": [r"\bjava\b", r"\.java\b", r"\bmaven\b", r"\bgradle\b"],
    }
    lower = raw_prompt.lower()
    for lang, patterns in lang_patterns.items():
        for pat in patterns:
            if re.search(pat, lower):
                return lang
    return None


def _inject_skill(lines: list, skill_context: str):
    """Insert skill context after the task header."""
    if not skill_context:
        return
    idx = 2
    while idx < len(lines) and lines[idx].startswith(("## ", "")):
        idx += 1
    while idx < len(lines) and lines[idx].strip() == "":
        idx += 1
    if idx < len(lines):
        idx += 1
    lines.insert(idx, "")
    for i, line in enumerate(skill_context.split("\n")):
        lines.insert(idx + 1 + i, line)
    lines.insert(idx + 1 + len(skill_context.split("\n")) + 1, "")


def polish_coding_prompt(raw: str, language: str | None, context: str, skill_context: str = "") -> str:
    rules = DOMAIN_RULES["coding"]

    lang_rules = []
    if language == "go":
        lang_rules = [rules["go"]]
    elif language == "python":
        lang_rules = [rules["go"], rules["python"]]
    elif language == "bash":
        lang_rules = [rules["bash"]]
    else:
        lang_rules = list(rules.values())

    lines = [
        "## Task",
        "",
        raw.strip(),
        "",
        "## Architectural Rules",
        "",
    ]
    lines.extend(lang_rules)
    lines.append("")

    _inject_skill(lines, skill_context)

    if context:
        lines.append("## Relevant Past Reasoning")
        lines.append("")
        lines.append(context)
        lines.append("")
    lines.extend(
        [
            "## Execution Protocol",
            "",
            "1. Analyze the task \u2014 decompose into distinct phases",
            "2. For each phase, evaluate approaches and trade-offs",
            "3. Implement the solution with production-grade error handling",
            "4. Verify edge cases and failure modes",
            "",
            "## Output Format",
            "",
            "- Document step-by-step reasoning inside `<think>...</think>` tags",
            "- Return final code in language-appropriate format",
            "- Include error handling for all failure modes",
        ]
    )
    return "\n".join(lines)


def polish_agentic_prompt(raw: str, context: str, skill_context: str = "") -> str:
    rules = DOMAIN_RULES["agentic"]
    lines = [
        "## Objective",
        "",
        raw.strip(),
        "",
        "## Agent Protocol",
        "",
        rules["plan"],
        rules["tool"],
        rules["state"],
        "",
    ]
    _inject_skill(lines, skill_context)
    if context:
        lines.append("## Relevant Past Reasoning")
        lines.append("")
        lines.append(context)
        lines.append("")
    lines.extend(
        [
            "## Execution Protocol",
            "",
            "1. Decompose objective into sequential steps",
            "2. Execute each step, verifying success before proceeding",
            "3. Log decisions, tool results, and state changes",
            "4. On failure: retry, escalate, or adapt the plan",
            "",
            "## Output Format",
            "",
            "- Document plan and reasoning inside `<think>...</think>` tags",
            "- Execute tool calls with verified outcomes",
            "- Final report: what was done, what was learned, next steps",
        ]
    )
    return "\n".join(lines)


def polish_analysis_prompt(raw: str, context: str, skill_context: str = "") -> str:
    rules = DOMAIN_RULES["analysis"]
    lines = [
        "## Analysis Request",
        "",
        raw.strip(),
        "",
        "## Analytical Framework",
        "",
        rules["evidence"],
        rules["structure"],
        rules["report"],
        "",
    ]
    _inject_skill(lines, skill_context)
    if context:
        lines.append("## Relevant Context")
        lines.append("")
        lines.append(context)
        lines.append("")
    lines.extend(
        [
            "## Execution Protocol",
            "",
            "1. Understand the question and scope",
            "2. Gather evidence from available data",
            "3. Evaluate hypotheses systematically",
            "4. Form a clear conclusion with confidence level",
            "",
            "## Output Format",
            "",
            "- Document analytical reasoning inside `<think>...</think>` tags",
            "- Structured analysis with evidence for each finding",
            "- Clear conclusion: what is known, unknown, and uncertain",
        ]
    )
    return "\n".join(lines)


def polish_general_prompt(raw: str, context: str, skill_context: str = "") -> str:
    lines = [
        "## Request",
        "",
        raw.strip(),
        "",
    ]
    _inject_skill(lines, skill_context)
    if context:
        lines.append("## Relevant Context")
        lines.append("")
        lines.append(context)
        lines.append("")
    lines.extend(
        [
            "## Execution Protocol",
            "",
            "1. Understand the request",
            "2. Plan the approach",
            "3. Execute with thoroughness",
            "4. Verify the result",
            "",
            "## Output Format",
            "",
            "- Document reasoning inside `<think>...</think>` tags",
            "- Provide complete, correct response",
        ]
    )
    return "\n".join(lines)


POLISHERS = {
    "coding": polish_coding_prompt,
    "agentic": polish_agentic_prompt,
    "analysis": polish_analysis_prompt,
    "general": polish_general_prompt,
}


def polish_prompt(
    raw_prompt: str,
    domain: str | None = None,
    context: str = "",
    skill_name: str = "",
    compact: bool = False,
) -> dict:
    """Take a raw prompt and return a structured, polished version.

    Args:
        raw_prompt: The user's raw/unstructured input.
        domain: Optional override ("coding", "agentic", "analysis", "general").
                Auto-detected if omitted.
        context: Optional RMN context string (<reasoning_memory> block).
        skill_name: Optional skill name to load and inject.
        compact: If True, inject only critical rules + format enforcement
                 (saves 66-82% tokens vs full skill context).

    Returns:
        dict with keys: task_type, language, polished, raw_length, polished_length,
                        skill_loaded, compact
    """
    task_type = domain or detect_task_type(raw_prompt)
    language = detect_language(raw_prompt) if task_type == "coding" else None

    skill_context = ""
    skill_loaded = False
    if skill_name:
        skill_data = load_skill(skill_name)
        if skill_data:
            skill_context = build_compact_skill_context(skill_data) if compact else build_skill_context(skill_data)
            skill_loaded = True

    polisher = POLISHERS.get(task_type, polish_general_prompt)

    kwargs = {"raw": raw_prompt, "context": context, "skill_context": skill_context}
    if task_type == "coding":
        kwargs["language"] = language

    polished = polisher(**kwargs)

    return {
        "task_type": task_type,
        "language": language,
        "polished": polished,
        "raw_length": len(raw_prompt),
        "polished_length": len(polished),
        "skill_loaded": skill_loaded,
        "skill_name": skill_name if skill_loaded else None,
        "compact": compact,
    }
