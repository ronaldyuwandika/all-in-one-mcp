"""Abstract LLM client interface."""

from abc import ABC, abstractmethod


class BaseLLM(ABC):
    @abstractmethod
    def analyze(self, prompt: str) -> str:
        """Send a prompt to the LLM and return the response text."""
        ...

    @abstractmethod
    def provider_name(self) -> str:
        """Return the provider name for result attribution."""
        ...
