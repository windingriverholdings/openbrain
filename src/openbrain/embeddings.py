"""Local embedding via fastembed — CPU-optimised ONNX inference.

Model: BAAI/bge-small-en-v1.5
- 384 dimensions
- ~130MB on disk
- Top-tier MTEB score for its size class
- No cloud dependency, runs fully in-process
"""

from __future__ import annotations

import structlog
from fastembed import TextEmbedding

from .config import get_config

logger = structlog.get_logger(__name__)

_model: TextEmbedding | None = None


def get_model() -> TextEmbedding:
    """Lazy-load the embedding model (downloaded on first use, cached locally)."""
    global _model
    if _model is None:
        config = get_config()
        logger.info("loading_embedding_model", model=config.embedding_model)
        _model = TextEmbedding(model_name=config.embedding_model)
        logger.info("embedding_model_ready", model=config.embedding_model)
    return _model


def embed(text: str) -> list[float]:
    """Embed a single text string. Returns a list of floats (384 dims)."""
    model = get_model()
    results = list(model.embed([text]))
    return results[0].tolist()


def embed_batch(texts: list[str]) -> list[list[float]]:
    """Embed multiple texts efficiently in a single batch."""
    if not texts:
        return []
    model = get_model()
    return [vec.tolist() for vec in model.embed(texts)]
