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

import logging
import os

import numpy as np
import uvicorn
from fastapi import FastAPI, Response
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer

logger = logging.getLogger("ioc-embedder")

MODEL_NAME = os.environ.get("IOC_EMBED_MODEL", "BAAI/bge-small-en-v1.5")
SOCKET_PATH = os.environ.get(
    "IOC_EMBED_SOCKET",
    os.environ.get("XDG_RUNTIME_DIR", "/tmp") + "/ioc/embedder.sock",
)

logger.info("loading model: %s", MODEL_NAME)
model = SentenceTransformer(MODEL_NAME)
logger.info("model loaded: %s, dimension=%d", MODEL_NAME, model.get_sentence_embedding_dimension())

app = FastAPI(title="IOC Embedder", version="0.1.0")


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
        "dimension": model.get_sentence_embedding_dimension(),
    }


@app.post("/embed")
async def embed(req: EmbedRequest):
    if not req.texts:
        return Response(
            status_code=400,
            content='{"error": "empty texts"}',
            media_type="application/json",
        )

    embeddings = model.encode(req.texts, normalize_embeddings=True)
    dimension = embeddings.shape[1]

    vectors = embeddings.astype(np.float32).tolist()

    return EmbedResponse(
        dimension=dimension,
        vectors=vectors,
        model=MODEL_NAME,
    )


if __name__ == "__main__":
    # TCP mode (works on Windows): set IOC_EMBED_HOST (and optionally IOC_EMBED_PORT).
    # Otherwise fall back to a Unix domain socket (Linux/WSL/macOS).
    host = os.environ.get("IOC_EMBED_HOST")
    if host:
        port = int(os.environ.get("IOC_EMBED_PORT", "8088"))
        logger.info("serving on http://%s:%d", host, port)
        uvicorn.run(app, host=host, port=port, log_level="info")
    else:
        os.makedirs(os.path.dirname(SOCKET_PATH), exist_ok=True)
        uvicorn.run(app, uds=SOCKET_PATH, log_level="info")
