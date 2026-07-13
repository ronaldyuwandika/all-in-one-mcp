#!/usr/bin/env python3
"""LLM-as-judge eval: compares code quality with vs without reasoning-memory + skill.

Multi-provider, multi-run, objective validation, two-pass judging.

Usage:
  python3 eval.py --model deepseek-v4-pro
  python3 eval.py --provider deepseek --model deepseek-v4-pro --runs 3
  DEEPSEEK_API_KEY=sk-xxx python3 eval.py
"""

import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from store import EpisodeStore

from importlib import util as _util

HAS_REQUESTS = _util.find_spec("requests") is not None

TASKS = [
    {
        "name": "go-http-handler",
        "language": "go",
        "skill": "golang-service",
        "type": "generation",
        "prompt": "Write a Go HTTP handler that reads a JSON body with a 'name' field, calls an external greeting service at http://greeting-service:8080/greet via HTTP POST, and returns the response as JSON. Include request validation, context timeout of 5s, and structured error responses.",
    },
    {
        "name": "py-fastapi-endpoint",
        "language": "python",
        "skill": "python-scriptmaster",
        "type": "generation",
        "prompt": "Write a FastAPI endpoint POST /orders that accepts order data (customer_id, items list, shipping_address), validates input with Pydantic, persists to PostgreSQL via SQLAlchemy async, and returns the created order with a 201 status.",
    },
    {
        "name": "tf-s3-module",
        "language": "terraform",
        "skill": "terraform-iac",
        "type": "generation",
        "prompt": "Write a single self-contained Terraform configuration file (main.tf) that defines an S3 bucket with AES256 encryption, versioning enabled, block public access, and a bucket policy enforcing TLS 1.2+. Define variables for bucket_name (string), environment (string), and tags (map(string)). Include a terraform block with required_providers for AWS. Do NOT use module blocks.",
    },
    {
        "name": "dockerfile-review",
        "language": "dockerfile",
        "skill": "docker-expert",
        "type": "analysis",
        "prompt": 'Review this Dockerfile for security, best practices, and efficiency issues:\n\n```dockerfile\nFROM ubuntu:latest\nRUN apt-get update && apt-get install -y python3 python3-pip curl vim netcat-openbsd\nCOPY . /app\nWORKDIR /app\nRUN pip install -r requirements.txt\nEXPOSE 8000\nCMD ["python3", "app.py"]\n```',
    },
]


def read_opencode_auth() -> dict:
    auth_file = Path.home() / ".local" / "share" / "opencode" / "auth.json"
    if auth_file.exists():
        try:
            return json.loads(auth_file.read_text())
        except (json.JSONDecodeError, OSError):
            pass
    return {}


def resolve_config(args) -> dict:
    auth = read_opencode_auth()
    deepseek_auth = auth.get("deepseek", {})
    default_key = deepseek_auth.get("key") or deepseek_auth.get("apiKey") or ""
    env_key = os.environ.get("DEEPSEEK_API_KEY") or os.environ.get("OPENAI_API_KEY") or ""

    cfg = {
        "provider": args.provider or "deepseek",
        "model": args.model or "deepseek-v4-pro",
        "api_key": args.api_key or env_key or default_key,
        "base_url": args.base_url or "https://api.deepseek.com/v1",
    }
    return cfg


_THINK_PATTERNS = [
    re.compile(r"<think>.*?</think>", re.DOTALL),
    re.compile(r"<reasoning>.*?</reasoning>", re.DOTALL),
    re.compile(r"^.*? response\s*\n", re.MULTILINE | re.DOTALL),
]


def strip_thinking(text: str) -> str:
    for pat in _THINK_PATTERNS:
        text = pat.sub("", text).strip()
    return text


def extract_code_blocks(text: str, language: str) -> list[str]:
    lang_variants = {
        "go": ["go", "golang"],
        "python": ["python", "py"],
        "terraform": ["terraform", "hcl", "tf"],
        "dockerfile": ["dockerfile", "Dockerfile"],
    }
    aliases = lang_variants.get(language, [language])
    alias_pattern = "|".join(aliases)
    blocks = re.findall(
        rf"```(?:{alias_pattern})\s*\n(.*?)```",
        text,
        re.DOTALL,
    )
    if not blocks:
        blocks = re.findall(r"```(?:\w*)\s*\n(.*?)```", text, re.DOTALL)
    cleaned = [b.strip() for b in blocks if b.strip()]
    if not cleaned:
        cleaned = [text.strip()]
    return cleaned


def validate_go(code: str) -> dict:
    errors = []
    if not code:
        return {"valid": False, "errors": ["empty code"]}
    with tempfile.NamedTemporaryFile(mode="w", suffix=".go", delete=False) as f:
        f.write(code)
        tmp = f.name
    try:
        r = subprocess.run(["go", "vet", tmp], capture_output=True, text=True, timeout=30)
        if r.returncode != 0:
            errors.append(r.stderr.strip() or r.stdout.strip())
    except FileNotFoundError:
        return {"valid": None, "errors": ["go not found"]}
    except subprocess.TimeoutExpired:
        return {"valid": None, "errors": ["go vet timed out"]}
    finally:
        Path(tmp).unlink(missing_ok=True)
    return {"valid": len(errors) == 0, "errors": errors[:5]}


def validate_python(code: str) -> dict:
    errors = []
    if not code:
        return {"valid": False, "errors": ["empty code"]}
    try:
        compile(code, "<eval>", "exec", flags=0)
    except SyntaxError as e:
        errors.append(f"syntax error: {e}")
    try:
        r = subprocess.run(
            ["python3", "-c", f"import ast; ast.parse({repr(code)})"],
            capture_output=True,
            text=True,
            timeout=15,
        )
        if r.returncode != 0:
            errors.append(r.stderr.strip()[:200])
    except (FileNotFoundError, subprocess.TimeoutExpired) as e:
        return {"valid": None, "errors": [f"exec error: {e}"]}
    if not errors:
        return {"valid": True, "errors": []}
    return {"valid": False, "errors": errors[:5]}


def llm_complete(prompt: str, system: str, cfg: dict) -> str:
    import urllib.request as _req
    import urllib.error as _err

    body = json.dumps(
        {
            "model": cfg.get("model", "deepseek-v4-pro"),
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": prompt},
            ],
            "temperature": 0.2,
            "max_tokens": 4096,
        }
    ).encode()

    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {cfg.get('api_key', '')}",
    }
    req = _req.Request(
        cfg.get("base_url", "https://api.deepseek.com/v1") + "/chat/completions",
        data=body,
        headers=headers,
        method="POST",
    )
    try:
        with _req.urlopen(req, timeout=120) as resp:
            data = json.loads(resp.read().decode())
        return data["choices"][0]["message"]["content"]
    except _err.HTTPError as e:
        return f'{{"error": "HTTP {e.code}: {e.read().decode()[:200]}"}}'
    except Exception as e:
        return f'{{"error": "{e}"}}'


def grade_output(
    task_name: str,
    language: str,
    prompt_a: str,
    code_a: str,
    prompt_b: str,
    code_b: str,
    validation_a: dict,
    validation_b: dict,
    cfg: dict,
) -> dict:
    val_a_str = (
        "PASS"
        if validation_a.get("valid")
        else ("FAIL" + (f": {validation_a['errors'][0]}" if validation_a.get("errors") else ""))
    )
    val_b_str = (
        "PASS"
        if validation_b.get("valid")
        else ("FAIL" + (f": {validation_b['errors'][0]}" if validation_b.get("errors") else ""))
    )

    judge_prompt = f"""You are a senior {language} engineer evaluating two code samples.

Task: {task_name}
Language: {language}

--- VERSION A (baseline prompt) ---
Generated code:
```{language}
{code_a[:3000]}
```

Objective validation (syntax/compile check): {val_a_str}

--- VERSION B (with skill + reasoning memory) ---
Generated code:
```{language}
{code_b[:3000]}
```

Objective validation (syntax/compile check): {val_b_str}

SCORING INSTRUCTIONS:
Pass 1 \u2014 Does it compile/parse? If validation says FAIL, max score is 2.
Pass 2 \u2014 Does it meet the requirements? Score requirements fulfillment.
Score each version from 1-5 where:
  1 = non-functional / empty
  2 = compiles but misses major requirements
  3 = works, meets most requirements
  4 = works well, all requirements met
  5 = production-ready, exceptional

Return ONLY valid JSON (no other text):
{{
  "correctness": {{"A": 1, "B": 5, "reason_short": "..."}},
  "completeness": {{"A": 1, "B": 5, "reason_short": "..."}},
  "idiomatic": {{"A": 1, "B": 5, "reason_short": "..."}},
  "error_handling": {{"A": 1, "B": 5, "reason_short": "..."}},
  "security": {{"A": 1, "B": 5, "reason_short": "..."}},
  "winner": "A or B or tie",
  "verdict": "Short explanation"
}}"""

    raw = llm_complete(
        judge_prompt,
        system="You are a strict code reviewer. Output ONLY valid JSON.",
        cfg=cfg,
    )
    try:
        json_match = re.search(r"\{[\s\S]*\}", raw)
        if json_match:
            return json.loads(json_match.group())
    except (json.JSONDecodeError, KeyError):
        pass
    return {"error": raw[:500], "winner": "unknown", "verdict": "parse failed"}


def build_rmn_context(raw_prompt: str, language: str) -> str:
    store = EpisodeStore(Path.home() / ".reasoning-memory")
    domain_map = {
        "go": ["coding"],
        "python": ["coding"],
        "terraform": ["coding"],
        "dockerfile": ["coding", "agentic"],
    }
    domains = domain_map.get(language, ["coding"])
    all_similar = store.search_local(query=raw_prompt, top_k=10)
    if not all_similar:
        return ""

    scored = []
    for ep in all_similar:
        domain_score = 2.0 if ep.get("domain") in domains else 0.5
        outcome_score = 1.5 if ep.get("outcome") == "success" else 0.5
        scored.append((domain_score * outcome_score, ep))
    scored.sort(key=lambda x: -x[0])

    selected = [ep for _, ep in scored[:3]]
    parts = ["<reasoning_memory>"]
    for ep in selected:
        full = store.get_episode(ep["id"])
        trace = (full.get("thinking_trace", "")[:500] if full else "") or ""
        parts.append(
            f'  <episode domain="{ep.get("domain", "unknown")}" '
            f'outcome="{ep.get("outcome", "unknown")}">\n'
            f"    <problem>{ep.get('problem', '')[:150]}</problem>\n"
            f"    <trace>{trace[:200]}</trace>\n"
            f"  </episode>"
        )
    parts.append("</reasoning_memory>")
    return "\n".join(parts)


if __name__ == "__main__":
    print("eval.py — run via: python3 eval.py --runs 3")
