"""Pytest fixtures for the embed server. The heavy model/reranker are stubbed so
tests run fast and offline (no sentence-transformers/torch needed)."""

import numpy as np
import pytest
from fastapi.testclient import TestClient

import embed_server

DIM = 8


class _FakeModel:
    def get_sentence_embedding_dimension(self):
        return DIM

    def encode(self, texts, normalize_embeddings=True):
        # Deterministic unit-ish vectors, shape (len(texts), DIM), float32.
        return np.ones((len(texts), DIM), dtype=np.float32)


class _FakeReranker:
    def predict(self, pairs, activation_fct=None):
        # Raw logit 0.0 (the server now requests identity activation, not sigmoid).
        return [0.0 for _ in pairs]


@pytest.fixture
def client(monkeypatch):
    monkeypatch.setattr(embed_server, "get_model", lambda: _FakeModel())
    monkeypatch.setattr(embed_server, "get_reranker", lambda: _FakeReranker())
    with TestClient(embed_server.app) as c:
        yield c
