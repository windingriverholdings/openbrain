"""LLM provider abstraction — supports Ollama (local) and Claude API.

Configured via OPENBRAIN_EXTRACT_PROVIDER and OPENBRAIN_EXTRACT_MODEL.
Set provider to "none" to disable LLM features entirely.

Smart routing: if EXTRACT_MODEL_FAST is set, short/simple text uses the fast
model while long or correction-heavy text uses the primary model.
"""

from __future__ import annotations

import re
from typing import Protocol

import structlog

logger = structlog.get_logger(__name__)

# Patterns that indicate supersede/correction — these need the accurate model
_CORRECTION_PATTERNS = re.compile(
    r"(actually[,:\s]|correction[,:\s]|update[,:\s]|no longer[,:\s]"
    r"|now instead[,:\s]|changed?[,:\s]|not \d+%.*but \d+%"
    r"|was wrong|I was mistaken|turns out)",
    re.IGNORECASE,
)


class LLMProvider(Protocol):
    """Minimal interface for text generation."""

    async def generate(self, prompt: str, system: str = "") -> str: ...


class OllamaProvider:
    """Local Ollama HTTP API provider."""

    def __init__(self, base_url: str, model: str) -> None:
        self._base_url = base_url.rstrip("/")
        self._model = model

    async def generate(self, prompt: str, system: str = "") -> str:
        import httpx

        messages = []
        if system:
            messages.append({"role": "system", "content": system})
        messages.append({"role": "user", "content": prompt})

        async with httpx.AsyncClient(timeout=120.0) as client:
            resp = await client.post(
                f"{self._base_url}/api/chat",
                json={
                    "model": self._model,
                    "messages": messages,
                    "stream": False,
                },
            )
            resp.raise_for_status()
            data = resp.json()

        content = data.get("message", {}).get("content", "")
        logger.info(
            "ollama_response",
            model=self._model,
            prompt_len=len(prompt),
            response_len=len(content),
        )
        return content


class ClaudeProvider:
    """Anthropic Claude API provider."""

    def __init__(self, api_key: str, model: str) -> None:
        self._api_key = api_key
        self._model = model

    async def generate(self, prompt: str, system: str = "") -> str:
        import httpx

        headers = {
            "x-api-key": self._api_key,
            "anthropic-version": "2023-06-01",
            "content-type": "application/json",
        }
        body: dict = {
            "model": self._model,
            "max_tokens": 4096,
            "messages": [{"role": "user", "content": prompt}],
        }
        if system:
            body["system"] = system

        async with httpx.AsyncClient(timeout=60.0) as client:
            resp = await client.post(
                "https://api.anthropic.com/v1/messages",
                headers=headers,
                json=body,
            )
            resp.raise_for_status()
            data = resp.json()

        content = data.get("content", [{}])[0].get("text", "")
        logger.info(
            "claude_response",
            model=self._model,
            prompt_len=len(prompt),
            response_len=len(content),
        )
        return content


def _needs_primary_model(text: str, threshold: int) -> bool:
    """Return True if text should use the primary (accurate) model."""
    if len(text) > threshold:
        return True
    if _CORRECTION_PATTERNS.search(text):
        return True
    return False


def _build_provider(provider_type: str, model: str, config) -> LLMProvider:
    """Build a provider instance for the given model."""
    if provider_type == "ollama":
        return OllamaProvider(base_url=config.ollama_base_url, model=model)
    if provider_type == "claude":
        if not config.anthropic_api_key:
            raise RuntimeError("OPENBRAIN_ANTHROPIC_API_KEY required for claude provider")
        return ClaudeProvider(api_key=config.anthropic_api_key, model=model)
    raise ValueError(f"Unknown extract_provider: {provider_type}")


# Cached providers
_primary: LLMProvider | None = None
_fast: LLMProvider | None = None
_has_fast: bool = False


def get_provider(text: str = "") -> LLMProvider | None:
    """Return the appropriate LLM provider for the given text.

    If a fast model is configured, short simple text gets the fast provider.
    Long or correction-heavy text always gets the primary provider.
    If no text is provided, returns the primary provider.
    """
    global _primary, _fast, _has_fast

    from .config import get_config
    config = get_config()

    if config.extract_provider == "none":
        logger.info("llm_provider_disabled")
        return None

    # Build primary on first call
    if _primary is None:
        _primary = _build_provider(config.extract_provider, config.extract_model, config)
        _has_fast = bool(config.extract_model_fast and config.extract_model_fast != config.extract_model)
        if _has_fast:
            _fast = _build_provider(config.extract_provider, config.extract_model_fast, config)
            logger.info(
                "llm_providers_ready",
                provider=config.extract_provider,
                primary=config.extract_model,
                fast=config.extract_model_fast,
                threshold=config.extract_fast_threshold,
            )
        else:
            logger.info("llm_provider_ready", provider=config.extract_provider, model=config.extract_model)

    # Route based on text
    if _has_fast and text and not _needs_primary_model(text, config.extract_fast_threshold):
        logger.info("llm_routed_fast", model=config.extract_model_fast, text_len=len(text))
        return _fast

    if _has_fast and text:
        reason = "length" if len(text) > config.extract_fast_threshold else "correction_pattern"
        logger.info("llm_routed_primary", model=config.extract_model, text_len=len(text), reason=reason)

    return _primary
