"""Tests for the embed server HTTP surface and the V10 hardening, using a stubbed
model (see conftest.py) so no real weights are loaded."""

import importlib

import pytest

import embed_server
from conftest import DIM


@pytest.mark.parametrize(
    "host,expected",
    [
        ("localhost", True),
        ("127.0.0.1", True),
        ("::1", True),
        ("127.0.0.5", True),
        ("0.0.0.0", False),
        ("8.8.8.8", False),
        ("example.com", False),
        ("not an ip", False),
    ],
)
def test_is_loopback(host, expected):
    assert embed_server._is_loopback(host) is expected


def test_module_imports_without_model():
    # Re-importing must not instantiate a model (lazy load); _model stays None
    # until first use.
    importlib.reload(embed_server)
    assert embed_server._model is None


def test_health_shape(client):
    r = client.get("/health")
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"
    assert body["dimension"] == DIM
    assert "model" in body


def test_embed_shape(client):
    r = client.post("/embed", json={"texts": ["a", "b"]})
    assert r.status_code == 200
    body = r.json()
    assert body["dimension"] == DIM
    assert len(body["vectors"]) == 2
    assert all(len(v) == DIM for v in body["vectors"])
    assert "model" in body


def test_embed_empty(client):
    r = client.post("/embed", json={"texts": []})
    assert r.status_code == 400


def test_body_limit_413(client, monkeypatch):
    monkeypatch.setattr(embed_server, "MAX_BODY", 10)
    r = client.post(
        "/embed",
        json={"texts": ["this body is larger than ten bytes"]},
        headers={"content-length": "1000"},
    )
    assert r.status_code == 413


def test_body_limit_bad_length(client):
    r = client.post("/embed", content=b"{}", headers={"content-length": "not-a-number"})
    assert r.status_code == 400


def test_rerank_shape(client):
    r = client.post("/rerank", json={"query": "q", "passages": ["x", "y"]})
    assert r.status_code == 200
    assert len(r.json()["scores"]) == 2


def test_rerank_empty(client):
    r = client.post("/rerank", json={"query": "q", "passages": []})
    assert r.status_code == 200
    assert r.json()["scores"] == []
