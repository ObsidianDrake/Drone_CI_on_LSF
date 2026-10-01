# Pinned pipeline runtime

This package is copied from `github.com/drone/drone-runtime/runtime` at
`v1.1.1-0.20200623162453-61e33e2cab5d` (commit `61e33e2cab5d`).
The upstream Apache 2.0 LICENSE, NOTICE, source headers and tests are preserved.

Local change: synchronize every read/write of the pipeline error, including
snapshots and run-policy decisions. Parallel submission failures exposed an
upstream race while testing LSF detached services. Engine types and clients
continue to come from the existing pinned upstream module; no new module
version or company proxy download is required for this patch.

The LSF lifecycle adapter separately joins detached log uploads and records
cleanup outcomes. Scheduling and exit-code policies here remain unchanged.

Run `go test -race ./internal/pipelineruntime ./operator/runner ./operator/runner/lsf`.
