"""OpenAI-compatible LLM adapter (works with OpenAI, DeepSeek, Ollama, etc.)."""

import json
import logging
import os

import httpx

from llm.base import BaseLLM

logger = logging.getLogger("pr-reviewer.llm.openai")


class OpenAILLM(BaseLLM):
    def __init__(self, config: dict):
        self.model_name = config.get("model", "deepseek-v4-pro")
        api_key = os.environ.get(config.get("api_key_env", "OPENAI_API_KEY"), "")
        self.api_key = api_key or ""
        self.base_url = config.get("base_url", "https://api.deepseek.com/v1").rstrip("/")

    def analyze(self, prompt: str) -> str:
        if not self.api_key:
            return json.dumps(
                {
                    "summary": "OpenAI-compatible API key not configured",
                    "verdict": "needs_work",
                    "confidence": 0.0,
                    "comments": [],
                }
            )

        url = f"{self.base_url}/chat/completions"
        payload = {
            "model": self.model_name,
            "messages": [
                {"role": "user", "content": prompt},
            ],
            "max_tokens": 4096,
            "temperature": 0.3,
        }
        headers = {
            "Content-Type": "application/json",
            "Authorization": f"Bearer {self.api_key}",
        }

        try:
            resp = httpx.post(url, json=payload, headers=headers, timeout=120)
            resp.raise_for_status()
            data = resp.json()
            return data["choices"][0]["message"]["content"]
        except Exception as e:
            logger.error("OpenAI-compatible request failed: %s", e)
            return json.dumps(
                {
                    "summary": f"LLM API error: {e}",
                    "verdict": "needs_work",
                    "confidence": 0.0,
                    "comments": [],
                }
            )

    def provider_name(self) -> str:
        return f"openai/{self.model_name}"
