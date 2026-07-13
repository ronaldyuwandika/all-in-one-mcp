#!/usr/bin/env python3
"""Seed reasoning memory with episodes generated from installed skills.

Usage:
  python3 seed.py --skills docker-expert,terraform-iac     # specific skills
  python3 seed.py --all                                     # all installed skills
  python3 seed.py --skills go,kubernetes --dry-run          # preview only

Idempotent: episodes tagged with skill + "seed" are skipped.
"""

import argparse
import sys
from pathlib import Path
from store import EpisodeStore
from prompter import detect_task_type, detect_language

BASE_DIR = Path.home() / ".reasoning-memory"
store = EpisodeStore(BASE_DIR)

SKILL_DIRS = {
    "claude": Path.home() / ".claude" / "skills",
    "agents": Path.home() / ".agents" / "skills",
    "opencode": Path.home() / ".config" / "opencode" / "skill",
}

SKILL_CACHE: dict[str, dict] = {}


def read_skill(skill_name: str) -> dict | None:
    if skill_name in SKILL_CACHE:
        return SKILL_CACHE[skill_name]
    for source, path in SKILL_DIRS.items():
        skill_file = path / skill_name / "SKILL.md"
        if skill_file.exists():
            text = skill_file.read_text(encoding="utf-8")
            desc = ""
            in_frontmatter = text.startswith("---")
            if in_frontmatter:
                parts = text.split("---", 2)
                if len(parts) >= 3:
                    try:
                        import yaml

                        fm = yaml.safe_load(parts[1])
                        if isinstance(fm, dict):
                            desc = fm.get("description", "")
                    except Exception:
                        pass
            result = {
                "name": skill_name,
                "description": desc,
                "source": source,
                "full_text": text[:600],
            }
            SKILL_CACHE[skill_name] = result
            return result
    return None


def guess_domain(skill: dict) -> str:
    name = skill["name"].lower()
    desc = skill["description"].lower()
    text = desc + " " + name

    if any(
        w in text
        for w in [
            "analyze",
            "audit",
            "review",
            "diagnose",
            "research",
            "investigate",
            "cost",
            "security",
            "assessment",
            "finops",
            "troubleshooting",
        ]
    ):
        return "analysis"
    if any(
        w in text
        for w in [
            "deploy",
            "orchestrate",
            "pipeline",
            "workflow",
            "automate",
            "plan",
            "coordinate",
            "monitor",
            "manage",
            "ci-cd",
            "incident",
        ]
    ):
        return "agentic"

    task_type = detect_task_type(skill["description"][:500])
    return task_type if task_type != "general" else "coding"


def guess_language(skill: dict) -> str | None:
    lang = detect_language(skill["description"][:500])
    return lang


def generate_problem(skill: dict) -> str:
    name = skill["name"]
    _ = skill["description"][:120] if skill["description"] else name

    templates = {
        "coding": [
            f"Implement a production-grade {name} configuration for a cloud-native service",
            f"Write a {name} module following best practices and security guidelines",
            f"Create a {name} setup with proper error handling, logging, and validation",
            f"Refactor the existing {name} to meet production standards",
            f"Build a reusable {name} component with tests and documentation",
            f"Write a {name} script that is robust, idempotent, and well-documented",
            f"Set up {name} with monitoring, alerting, and disaster recovery",
        ],
        "agentic": [
            f"Deploy and configure {name} across staging and production environments",
            f"Set up a CI/CD pipeline for {name} with automated testing and rollback",
            f"Migrate the existing {name} to a new infrastructure with zero downtime",
            f"Automate {name} provisioning across multiple environments",
            f"Orchestrate a multi-step {name} rollout with canary deployments",
        ],
        "analysis": [
            f"Audit the current {name} setup for security vulnerabilities and cost waste",
            f"Analyze the {name} performance and recommend optimization strategies",
            f"Investigate {name} failure patterns and propose remediation",
            f"Review {name} configuration against industry best practices",
        ],
    }

    import random

    domain = guess_domain(skill)
    pool = templates.get(domain, templates["coding"])
    problem = random.choice(pool)
    return problem


def generate_thinking_trace(skill: dict) -> str:
    name = skill["name"]
    desc = skill["description"][:200] if skill["description"] else name
    domain = guess_domain(skill)
    lang = guess_language(skill)

    generic_reasoning = [
        f"1. Analysis: {desc}. Scope includes correctness, security, performance, and maintainability.",
        "2. Option generation: Evaluate multiple approaches based on trade-offs: complexity, reliability, operational cost.",
        "3. Decision: Choose the approach that maximizes safety and readability while meeting requirements.",
        "4. Implementation: Write code following the skill's Core Principles \u2014 minimal, well-documented, production-grade.",
        "5. Verification: Validate with tests, dry-run, and edge-case analysis.",
    ]

    if domain == "agentic":
        generic_reasoning = [
            f"1. Decompose: Break down the {name} task into sequential steps with clear success criteria.",
            "2. Step 1: Verify prerequisites (credentials, tools, environment state).",
            f"3. Step 2: Execute {name} provisioning with idempotency checks.",
            "4. Step 3: Validate the deployment \u2014 health checks, integration tests, and rollback readiness.",
            "5. Step 4: Log outcomes, tag resources, update runbooks.",
        ]
    elif domain == "analysis":
        generic_reasoning = [
            f"1. Scoping: Define the boundaries of the {name} analysis. Gather baseline metrics.",
            f"2. Evidence collection: Review {name} configuration, logs, and performance data.",
            "3. Hypothesis testing: Isolate variables and test each potential cause independently.",
            "4. Findings: Document root causes with supporting evidence and confidence levels.",
            "5. Recommendations: Prioritize remediation by impact-to-effort ratio.",
        ]

    if lang == "go":
        generic_reasoning.append("")
        generic_reasoning.append("Go-specific: explicit error propagation, goroutine lifecycle, channel safety.")

    return "\n".join(generic_reasoning)


def generate_tool_calls(skill: dict) -> list[dict]:
    name = skill["name"]
    domain = guess_domain(skill)

    calls = [
        {
            "tool": "read",
            "args": f"Read existing {name} config files",
            "outcome": "success",
        },
        {"tool": "edit", "args": f"Write {name} implementation", "outcome": "success"},
        {"tool": "shell", "args": f"Validate {name} setup", "outcome": "success"},
    ]

    if domain == "agentic":
        calls = [
            {
                "tool": "shell",
                "args": f"Check {name} prerequisites",
                "outcome": "success",
            },
            {
                "tool": "shell",
                "args": f"Deploy {name} to environment",
                "outcome": "success",
            },
            {
                "tool": "shell",
                "args": f"Verify {name} health and connectivity",
                "outcome": "success",
            },
        ]
    elif domain == "analysis":
        calls = [
            {
                "tool": "read",
                "args": f"Gather {name} configuration and logs",
                "outcome": "success",
            },
            {"tool": "shell", "args": f"Run {name} diagnostics", "outcome": "success"},
            {"tool": "edit", "args": f"Document {name} findings", "outcome": "success"},
        ]

    return calls


def list_all_skills() -> list[dict]:
    seen = set()
    skills = []
    for source, path in SKILL_DIRS.items():
        if path.exists():
            for d in sorted(path.iterdir()):
                if d.is_dir() and (d / "SKILL.md").exists():
                    if d.name not in seen:
                        seen.add(d.name)
                        skill = read_skill(d.name)
                        if skill:
                            skills.append(skill)
    return skills


def main():
    parser = argparse.ArgumentParser(description="Seed reasoning memory from installed skills")
    parser.add_argument(
        "--skills",
        help="Comma-separated skill names (e.g. docker-expert,terraform-iac)",
    )
    parser.add_argument("--all", action="store_true", help="Seed from ALL installed skills")
    parser.add_argument("--dry-run", action="store_true", help="Preview only, don't write")
    parser.add_argument("--tag", default="skill-seed", help="Tag for idempotency (default: skill-seed)")
    args = parser.parse_args()

    if not args.skills and not args.all:
        parser.print_help()
        print("\nProvide --skills <names> or --all to seed.")
        sys.exit(1)

    if args.all:
        targets = list_all_skills()
    else:
        names = [s.strip() for s in args.skills.split(",")]
        targets = []
        for name in names:
            skill = read_skill(name)
            if skill:
                targets.append(skill)
            else:
                print(f"  [SKIP] Skill not found: {name}")
                print(f"         Scanned: {', '.join(str(p) for p in SKILL_DIRS.values())}")

    if not targets:
        print("No skills found. Aborting.")
        sys.exit(1)

    existing = [s["id"] for s in store.list_episodes(limit=1000) if args.tag in s.get("tags", [])]
    existing_skills = set()
    for eid in existing:
        ep = store.get_episode(eid)
        if ep:
            tags = ep.get("tags", [])
            for sk in targets:
                if sk["name"] in tags:
                    existing_skills.add(sk["name"])

    if existing_skills:
        print(f"Already seeded ({len(existing_skills)} skills found with tag '{args.tag}'):")
        for s in sorted(existing_skills):
            print(f"  - {s}")
        print("\nUse --tag <other> to seed fresh, or delete existing seed episodes.")
        if set(s["name"] for s in targets).issubset(existing_skills):
            print("All requested skills already seeded. Nothing to do.")
            return

    to_seed = [s for s in targets if s["name"] not in existing_skills]

    if args.dry_run:
        print(f"[DRY RUN] Would seed {len(to_seed)} episodes:\n")
        for skill in to_seed:
            domain = guess_domain(skill)
            problem = generate_problem(skill)
            print(f"  [{domain:>8}] {skill['name']:40s} {problem}")
        return

    created = []

    for skill in to_seed:
        domain = guess_domain(skill)
        _ = guess_language(skill)
        episode = {
            "problem": generate_problem(skill),
            "thinking_trace": generate_thinking_trace(skill),
            "tool_calls": generate_tool_calls(skill),
            "outcome": "success",
            "tags": [args.tag, skill["name"], skill["source"], domain],
            "domain": domain,
            "duration_seconds": 300,
            "model_id": "seed-script",
        }
        eid = store.create_episode(**episode)
        created.append(f"  [{eid}] [{domain:>8}] {skill['name']}")

    created.sort()
    print(f"Seeded {len(created)} episodes (tag: '{args.tag}'):\n")
    for line in created:
        print(line)

    print(f"\nEpisodes: {store.episodes_dir}")
    print("\nNow run: reasoning-memory.consolidate_reasoning(strategy='auto')")


if __name__ == "__main__":
    main()
