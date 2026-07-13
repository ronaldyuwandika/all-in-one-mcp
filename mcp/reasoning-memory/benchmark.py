#!/usr/bin/env python3
"""Multi-dimensional benchmark: token cost & quality of polish_prompt.

Measures across 4 axes:
  A. Domain         \u2014 coding, agentic, analysis, general
  B. Prompt length  \u2014 short (~60t), medium (~250t), long (~700t)
  C. Skill injection \u2014 none, small skill, large skill
  D. Turn simulation \u2014 first turn (full), second turn (amortized)
  E. RMN context    \u2014 context retrieved from seed episodes
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from prompter import polish_prompt
from store import EpisodeStore

BASE_DIR = Path.home() / ".reasoning-memory"
store = EpisodeStore(BASE_DIR)

PROMPTS = {
    "coding/00/short": "Write a Go HTTP handler that returns JSON",
    "coding/01/medium": (
        "Implement a REST API in Go using gorilla/mux that handles CRUD operations "
        "for a User resource with PostgreSQL storage, JWT auth middleware, structured "
        "error responses, and OpenAPI docs."
    ),
    "coding/02/long": (
        "Build a production-grade Go microservice for order processing. Requirements:\n"
        "- HTTP API with gorilla/mux handling POST /orders, GET /orders/{id}, PATCH /orders/{id}/status\n"
        "- PostgreSQL storage with migrations using golang-migrate\n"
        "- JWT auth middleware validating RS256 tokens\n"
        "- Structured error responses with error codes and stack traces\n"
        "- Graceful shutdown with signal handling\n"
        "- Context propagation through handlers \u2192 service layer \u2192 repository\n"
        "- Structured logging with slog\n"
        "- Prometheus metrics\n"
    ),
    "agentic/00/short": "Deploy the new API version to staging",
    "agentic/01/medium": (
        "Roll out v2.3.1 of the payment service to production with a canary deployment. "
        "Route 5% of traffic to the new version, monitor error rate and latency for 10 minutes, "
        "then gradually increase to 100%."
    ),
    "agentic/02/long": (
        "Coordinate a multi-service deployment across 3 environments (dev, staging, prod) "
        "for a platform migration from EC2 to ECS Fargate involving 12 microservices."
    ),
    "analysis/00/short": "Why is the API returning 503 errors?",
    "analysis/01/medium": (
        "Audit the AWS infrastructure for cost optimization. The monthly bill has grown "
        "from $4,200 to $6,800 over the past 3 months."
    ),
    "analysis/02/long": (
        "Investigate a production incident where the checkout service experienced "
        "intermittent failures between 14:30-14:45 UTC yesterday."
    ),
    "general/00/short": "What's the weather today?",
    "general/01/medium": (
        "Compare Kubernetes Ingress controllers: Nginx Ingress, Traefik, and HAProxy."
    ),
    "general/02/long": (
        "Design a migration strategy from a monolithic Rails application to a "
        "microservices architecture on Kubernetes."
    ),
}

SKILLS = {
    "none": "",
    "small": "golang-grpc",
    "medium": "docker-expert",
    "large": "python-scriptmaster",
}


def measure(skill_name: str, rmn_active: bool, prompt: str, domain: str) -> dict:
    context = ""
    rmn_episodes_found = 0

    if rmn_active:
        similar = store.search_local(query=prompt, top_k=3)
        if similar:
            rmn_episodes_found = len(similar)
            parts = ["<reasoning_memory>"]
            for ep in similar:
                full = store.get_episode(ep["id"])
                trace = (full.get("thinking_trace", "")[:500] if full else "") or ""
                parts.append(
                    f'  <episode domain="{ep["domain"]}" outcome="{ep["outcome"]}">\n'
                    f"    <problem>{ep['problem'][:150]}</problem>\n"
                    f"    <trace>{trace[:200]}</trace>\n"
                    f"  </episode>"
                )
            parts.append("</reasoning_memory>")
            context = "\n".join(parts)

    result = polish_prompt(
        raw_prompt=prompt,
        domain=domain,
        context=context,
        skill_name=skill_name,
    )

    return {
        "raw": result["raw_length"],
        "polished": result["polished_length"],
        "overhead": result["polished_length"] - result["raw_length"],
        "overhead_pct": round(
            (result["polished_length"] - result["raw_length"])
            / max(result["raw_length"], 1)
            * 100,
            1,
        ),
        "skill_loaded": result.get("skill_loaded", False),
        "skill_name": result.get("skill_name"),
        "rmn_episodes": rmn_episodes_found,
        "context_len": len(context),
        "domain_detected": result["task_type"],
        "language_detected": result.get("language"),
    }


def run():
    results = []

    print("=" * 100)
    print("BENCHMARK: Token Cost & Quality of polish_prompt")
    print("=" * 100)

    for pkey, prompt in PROMPTS.items():
        domain_key = pkey.split("/")[0]
        for skill_key, skill_name in SKILLS.items():
            m = measure(skill_name, rmn_active=True, prompt=prompt, domain=domain_key)
            results.append(
                {
                    "test": pkey,
                    "skill": skill_key,
                    **m,
                }
            )

    for skill_key in ["none", "small", "medium", "large"]:
        subset = [r for r in results if r["skill"] == skill_key]
        avg_raw = sum(r["raw"] for r in subset) / len(subset)
        avg_overhead = sum(r["overhead"] for r in subset) / len(subset)
        avg_pct = sum(r["overhead_pct"] for r in subset) / len(subset)
        avg_rmn = sum(r["rmn_episodes"] for r in subset) / len(subset)
        loaded = sum(1 for r in subset if r["skill_loaded"])
        print(
            f"  {skill_key:<10} raw={avg_raw:>5.0f}  \u2192 polished \u0394+{avg_overhead:>6.0f}  (+{avg_pct:>6.1f}%)  rmn_ep={avg_rmn:.1f}  loaded={loaded}/{len(subset)}"
        )

    print(f"\nTotal configs tested: {len(results)}")
    print("To run full eval: python3 eval.py --runs 3")


if __name__ == "__main__":
    run()
