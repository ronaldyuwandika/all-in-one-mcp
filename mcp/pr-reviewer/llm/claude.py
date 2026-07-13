"""Anthropic Claude LLM adapter."""

import json
import logging
import os

from llm.base import BaseLLM

logger = logging.getLogger("pr-reviewer.llm.claude")

HAS_ANTHROPIC = False
try:
    import anthropic

    HAS_ANTHROPIC = True
except ImportError:
    pass


class ClaudeLLM(BaseLLM):
    def __init__(self, config: dict):
        self.model_name = config.get("model", "claude-sonnet-4-20260514")
        api_key = os.environ.get(config.get("api_key_env", "ANTHROPIC_API_KEY"), "")
        self.api_key = api_key

        if HAS_ANTHROPIC and api_key:
            self._client = anthropic.Anthropic(api_key=api_key)
        else:
            self._client = None

    def analyze(self, prompt: str) -> str:
        if self._client:
            return self._sdk_analyze(prompt)
        return self._http_analyze(prompt)

    def _sdk_analyze(self, prompt: str) -> str:
        try:
            message = self._client.messages.create(
                model=self.model_name,
                max_tokens=4096,
                temperature=0.3,
                messages=[{"role": "user", "content": prompt}],
            )
            return message.content[0].text if message.content else ""
        except Exception as e:
            logger.warning("Claude SDK failed: %s", e)
            return self._http_analyze(prompt)

    def _http_analyze(self, prompt: str) -> str:
        import httpx

        api_key = self.api_key or os.environ.get("ANTHROPIC_API_KEY", "")
        if not api_key:
            return json.dumps(
                {
                    "summary": "Anthropic API key not configured (set ANTHROPIC_API_KEY)",
                    "verdict": "needs_work",
                    "confidence": 0.0,
                    "comments": [],
                }
            )

        url = "https://api.anthropic.com/v1/messages"
        headers = {
            "x-api-key": api_key,
            "anthropic-version": "2023-06-01",
            "content-type": "application/json",
        }
        payload = {
            "model": self.model_name,
            "max_tokens": 4096,
            "temperature": 0.3,
            "messages": [{"role": "user", "content": prompt}],
        }

        try:
            resp = httpx.post(url, json=payload, headers=headers, timeout=120)
            resp.raise_for_status()
            data = resp.json()
            content = data.get("content", [])
            if content:
                return content[0].get("text", "")
            return json.dumps(
                {
                    "summary": "Empty response from Claude",
                    "verdict": "needs_work",
                    "confidence": 0.0,
                    "comments": [],
                }
            )
        except Exception as e:
            logger.error("Claude HTTP request failed: %s", e)
            return json.dumps(
                {
                    "summary": f"Claude API error: {e}",
                    "verdict": "needs_work",
                    "confidence": 0.0,
                    "comments": [],
                }
            )

    def provider_name(self) -> str:
        return "claude"
