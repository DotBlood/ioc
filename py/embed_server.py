# py/embed_server.py
#
# IOC Embedding Service — HTTP over Unix socket.
# Single-model process: one model per server instance.
#
# Usage:
#   mkdir -p /tmp/ioc
#   uvicorn embed_server:app --uds /tmp/ioc/embedder.sock
#
# Endpoints:
#   GET  /health                 → health check
#   POST /embed                  → batch embedding inference
#
# Security (V10): default bind is the Unix socket. A non-loopback TCP bind
# (IOC_EMBED_HOST) is REFUSED unless IOC_EMBED_ALLOW_REMOTE=1 (the service is
# unauthenticated). Request bodies over IOC_EMBED_MAX_BODY (default 16 MiB) → 413.

import logging
import os

import numpy as np
import uvicorn
from fastapi import FastAPI, Response
from pydantic import BaseModel

# sentence-transformers (and torch) are heavy. Import lazily so the module can be
# imported for tests without them installed — tests monkeypatch get_model/get_reranker.
try:
    from sentence_transformers import CrossEncoder, SentenceTransformer
except ImportError:  # pragma: no cover - exercised only when the dep is absent
    SentenceTransformer = CrossEncoder = None

logger = logging.getLogger("ioc-embedder")

MODEL_NAME = os.environ.get("IOC_EMBED_MODEL", "BAAI/bge-small-en-v1.5")
RERANK_MODEL = os.environ.get("IOC_RERANK_MODEL", "BAAI/bge-reranker-base")
SOCKET_PATH = os.environ.get(
    "IOC_EMBED_SOCKET",
    os.environ.get("XDG_RUNTIME_DIR", "/tmp") + "/ioc/embedder.sock",
)

# The embedding model loads lazily on first use (like the reranker) so importing
# this module is cheap (no ~400MB download at import time).
_model = None


def get_model():
    global _model
    if _model is None:
        if SentenceTransformer is None:
            raise RuntimeError("sentence-transformers is not installed")
        logger.info("loading model: %s", MODEL_NAME)
        _model = SentenceTransformer(MODEL_NAME)
        logger.info(
            "model loaded: %s, dimension=%d",
            MODEL_NAME,
            _model.get_sentence_embedding_dimension(),
        )
    return _model


app = FastAPI(title="IOC Embedder", version="0.1.0")

# Max request body in bytes (V10): reject oversized uploads with 413. Override via
# IOC_EMBED_MAX_BODY. Far above any real embedding batch.
MAX_BODY = int(os.environ.get("IOC_EMBED_MAX_BODY", str(16 * 1024 * 1024)))


@app.middleware("http")
async def limit_body(request, call_next):
    cl = request.headers.get("content-length")
    if cl is not None:
        try:
            if int(cl) > MAX_BODY:
                return Response(status_code=413, content=b"payload too large")
        except ValueError:
            return Response(status_code=400, content=b"bad content-length")
    return await call_next(request)


def _is_loopback(host: str) -> bool:
    """Literal-loopback check — the Python mirror of isLoopbackHost in
    internal/embed/endpoint.go (no DNS resolution). Keep the two in sync."""
    if host in ("localhost", "127.0.0.1", "::1"):
        return True
    try:
        import ipaddress

        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


class EmbedRequest(BaseModel):
    texts: list[str]


class EmbedResponse(BaseModel):
    dimension: int
    vectors: list[list[float]]
    model: str


@app.on_event("startup")
async def startup():
    logger.info("embedder service started on %s", SOCKET_PATH)


@app.get("/health")
async def health():
    return {
        "status": "ok",
        "model": MODEL_NAME,
        "dimension": get_model().get_sentence_embedding_dimension(),
    }


@app.post("/embed")
async def embed(req: EmbedRequest):
    if not req.texts:
        return Response(
            status_code=400,
            content='{"error": "empty texts"}',
            media_type="application/json",
        )

    embeddings = get_model().encode(req.texts, normalize_embeddings=True)
    dimension = embeddings.shape[1]

    vectors = embeddings.astype(np.float32).tolist()

    return EmbedResponse(
        dimension=dimension,
        vectors=vectors,
        model=MODEL_NAME,
    )


# Reranker (cross-encoder) is loaded lazily on first /rerank to keep startup light.
_reranker = None


def get_reranker():
    global _reranker
    if _reranker is None:
        if CrossEncoder is None:
            raise RuntimeError("sentence-transformers is not installed")
        logger.info("loading reranker: %s", RERANK_MODEL)
        _reranker = CrossEncoder(RERANK_MODEL)
    return _reranker


class RerankRequest(BaseModel):
    query: str
    passages: list[str]


class RerankResponse(BaseModel):
    scores: list[float]
    model: str


def _identity_activation():
    """Identity activation so CrossEncoder.predict returns RAW logits (no sigmoid).
    Returns None when torch is absent (test stubs ignore it)."""
    try:
        import torch

        return torch.nn.Identity()
    except ImportError:
        return None


@app.post("/rerank")
async def rerank(req: RerankRequest):
    if not req.passages:
        return RerankResponse(scores=[], model=RERANK_MODEL)
    ce = get_reranker()
    pairs = [[req.query, p] for p in req.passages]
    # Return RAW cross-encoder logits. bge-reranker's default predict() applies a sigmoid;
    # IOC applies its OWN sigmoid once (internal/engine/rerank.go). Emitting the sigmoid
    # here would double-sigmoid and compress scores into ~[0.5, 0.73], collapsing the
    # present/absent separation the rerank confidence floor relies on (R5).
    scores = ce.predict(pairs, activation_fct=_identity_activation())
    return RerankResponse(scores=[float(s) for s in scores], model=RERANK_MODEL)


if __name__ == "__main__":
    # TCP mode (works on Windows): set IOC_EMBED_HOST (and optionally IOC_EMBED_PORT).
    # Otherwise fall back to a Unix domain socket (Linux/WSL/macOS).
    host = os.environ.get("IOC_EMBED_HOST")
    if host:
        port = int(os.environ.get("IOC_EMBED_PORT", "8088"))
        # Fail-closed (V10): this service is UNAUTHENTICATED. Refuse to bind a
        # non-loopback host (e.g. 0.0.0.0) unless the operator explicitly opts in,
        # so a misconfig can't expose a public, unauthenticated compute/embedding
        # endpoint. Mirrors IOC's Go embed-endpoint policy (IOC_ALLOW_REMOTE_EMBED).
        allow_remote = os.environ.get("IOC_EMBED_ALLOW_REMOTE", "").lower() in ("1", "true", "yes")
        if not _is_loopback(host) and not allow_remote:
            logger.error(
                "refusing to bind non-loopback host %r without IOC_EMBED_ALLOW_REMOTE=1 "
                "(this embedder is unauthenticated)",
                host,
            )
            raise SystemExit(2)
        if not _is_loopback(host):
            logger.warning(
                "binding NON-LOOPBACK host %r — this embedder is UNAUTHENTICATED; "
                "ensure the network is trusted and access-controlled",
                host,
            )
        logger.info("serving on http://%s:%d", host, port)
        uvicorn.run(app, host=host, port=port, log_level="info")
    else:
        os.makedirs(os.path.dirname(SOCKET_PATH), exist_ok=True)
        uvicorn.run(app, uds=SOCKET_PATH, log_level="info")
