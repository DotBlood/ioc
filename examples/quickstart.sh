#!/usr/bin/env bash
# IOC v0.1 Quick Start — full roundtrip
set -euo pipefail

IOC="${IOC:-../bin/iocctl}"
DIR="${DIR:-/tmp/ioc-quickstart}"

echo "=== 1. Init ==="
rm -rf "$DIR"
$IOC init --dir "$DIR"

echo "=== 2. Create scopes ==="
WORKTREE=$($IOC scope create worktree --dir "$DIR" --json | jq -r '.scope_id')
echo "worktree: $WORKTREE"
WORKSPACE=$($IOC scope create workspace --dir "$DIR" --json | jq -r '.scope_id')
echo "workspace: $WORKSPACE"
SESSION=$($IOC scope create session --dir "$DIR" --json | jq -r '.scope_id')
echo "session: $SESSION"

echo "=== 3. Add artifacts ==="
$IOC artifact add "$SESSION" --dir "$DIR" --text "IOC is a stateful knowledge graph runtime"
$IOC artifact add "$SESSION" --dir "$DIR" --text "Supports deterministic retrieval with vector and BM25 search"
$IOC artifact add "$SESSION" --dir "$DIR" --text "Temporal versioning via structural snapshots and time-travel"

echo "=== 4. Search ==="
$IOC retrieval query "knowledge graph" --dir "$DIR" --topk 5

echo "=== 5. Archive ==="
ANCHOR=$($IOC scope archive "$SESSION" --dir "$DIR" --json | jq -r '.anchor_id')
echo "anchor: $ANCHOR"

echo "=== 6. List archives ==="
$IOC archive list --dir "$DIR"

echo "=== 7. Restore ==="
$IOC scope restore "$ANCHOR" --dir "$DIR"

echo "=== 8. Verify restored ==="
$IOC retrieval query "knowledge graph" --dir "$DIR" --topk 5

echo "=== Done ==="
