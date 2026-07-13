"""Google Gemini LLM adapter."""

import json
import logging
import os

from llm.base import BaseLLM

logger = logging.getLogger("pr-reviewer.llm.gemini")

HAS_GENAI = False
try:
    import google.generativeai as genai

    HAS_GENAI = True
except ImportError:
    pass


class GeminiLLM(BaseLLM):
    def __init__(self, config: dict):
        self.model_name = config.get("model", "gemini-2.0-flash")
        api_key = os.environ.get(config.get("api_key_env", "GEMINI_API_KEY"), "")

        if HAS_GENAI and api_key:
            genai.configure(api_key=api_key)

        self.api_key = api_key
        self._http_fallback = not HAS_GENAI or not api_key

    def analyze(self, prompt: str) -> str:
        if self._http_fallback:
            return self._http_analyze(prompt)

        try:
            model = genai.GenerativeModel(self.model_name)
            response = model.generate_content(
                prompt,
                generation_config={"max_output_tokens": 4096, "temperature": 0.3},
            )
            return response.text
        except Exception as e:
            logger.warning("Gemini SDK failed, falling back to HTTP: %s", e)
            return self._http_analyze(prompt)

    def _http_analyze(self, prompt: str) -> str:
        import httpx

        api_key = self.api_key or os.environ.get("GEMINI_API_KEY", "")
        if not api_key:
            return json.dumps(
                {
                    "summary": "Gemini API key not configured (set GEMINI_API_KEY)",
                    "verdict": "needs_work",
                    "confidence": 0.0,
                    "comments": [],
                }
            )

        url = f"https://generativelanguage.googleapis.com/v1beta/models/{self.model_name}:generateContent?key={api_key}"
        payload = {
            "contents": [{"parts": [{"text": prompt}]}],
            "generationConfig": {"maxOutputTokens": 4096, "temperature": 0.3},
        }

        try:
            resp = httpx.post(url, json=payload, timeout=120)
            resp.raise_for_status()
            data = resp.json()
            candidates = data.get("candidates", [])
            if candidates:
                parts = candidates[0].get("content", {}).get("parts", [])
                if parts:
                    return parts[0].get("text", "")
            return json.dumps(
                {
                    "summary": "Empty response from Gemini",
                    "verdict": "needs_work",
                    "confidence": 0.0,
                    "comments": [],
                }
            )
        except Exception as e:
            logger.error("Gemini HTTP request failed: %s", e)
            return json.dumps(
                {
                    "summary": f"Gemini API error: {e}",
                    "verdict": "needs_work",
                    "confidence": 0.0,
                    "comments": [],
                }
            )

    def provider_name(self) -> str:
        return "gemini"
