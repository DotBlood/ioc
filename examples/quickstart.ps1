# IOC v0.1 Quick Start — full roundtrip
$IOC = if ($env:IOC) { $env:IOC } else { "../bin/iocctl.exe" }
$DIR = if ($env:DIR) { $env:DIR } else { "$env:TEMP\ioc-quickstart" }

Write-Host "=== 1. Init ===" -ForegroundColor Cyan
Remove-Item -Recurse -Force $DIR -ErrorAction SilentlyContinue
& $IOC init --dir $DIR

Write-Host "=== 2. Create scopes ===" -ForegroundColor Cyan
$WORKTREE = & $IOC scope create worktree --dir $DIR --json | ConvertFrom-Json | Select-Object -ExpandProperty scope_id
Write-Host "worktree: $WORKTREE"
$WORKSPACE = & $IOC scope create workspace --dir $DIR --json | ConvertFrom-Json | Select-Object -ExpandProperty scope_id
Write-Host "workspace: $WORKSPACE"
$SESSION = & $IOC scope create session --dir $DIR --json | ConvertFrom-Json | Select-Object -ExpandProperty scope_id
Write-Host "session: $SESSION"

Write-Host "=== 3. Add artifacts ===" -ForegroundColor Cyan
& $IOC artifact add $SESSION --dir $DIR --text "IOC is a stateful knowledge graph runtime"
& $IOC artifact add $SESSION --dir $DIR --text "Supports deterministic retrieval with vector and BM25 search"
& $IOC artifact add $SESSION --dir $DIR --text "Temporal versioning via structural snapshots and time-travel"

Write-Host "=== 4. Search ===" -ForegroundColor Cyan
& $IOC retrieval query "knowledge graph" --dir $DIR --topk 5

Write-Host "=== 5. Archive ===" -ForegroundColor Cyan
$ANCHOR = & $IOC scope archive $SESSION --dir $DIR --json | ConvertFrom-Json | Select-Object -ExpandProperty anchor_id
Write-Host "anchor: $ANCHOR"

Write-Host "=== 6. List archives ===" -ForegroundColor Cyan
& $IOC archive list --dir $DIR

Write-Host "=== 7. Restore ===" -ForegroundColor Cyan
& $IOC scope restore $ANCHOR --dir $DIR

Write-Host "=== 8. Verify restored ===" -ForegroundColor Cyan
& $IOC retrieval query "knowledge graph" --dir $DIR --topk 5

Write-Host "=== Done ===" -ForegroundColor Green
