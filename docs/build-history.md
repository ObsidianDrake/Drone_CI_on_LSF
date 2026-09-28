# Build history management

Drone system administrators can delete history from the repository Builds page.
Repository administrators without the system admin flag cannot use this feature.

The list has 25 builds per page. Checkboxes apply only to the displayed page;
changing pages clears the selection. The summary describes the displayed page.
“Clean up history…” provides two repository-wide options:

- “Non-success builds”: removes completed failure, error, killed and skipped
  builds across **all pages**, preserving successful builds.
- “Older builds”: removes completed builds before #N across **all pages**,
  including successful builds, but excluding #N itself. N must be a positive integer.

Both options preview the exact eligible count before confirmation, even if the
current page contains no matching builds.

Only `success`, `failure`, `error`, `killed`, and `skipped` builds are eligible.
Non-success excludes `success`, regardless of individual step outcomes. Builds
with unfinished stages or steps are also preserved until teardown finishes.

The selection bar shows the number selected and displays “Delete selected” only
after selecting builds. A trash icon on each completed build starts single-build
deletion. Cleanup options have scope badges and descriptions; the older-build
input explains which build numbers will be kept.

Every deletion first previews eligible build numbers, then opens a confirmation dialog
showing repository, scope and count. The server checks the snapshot again inside
the database transaction. A changed selection returns HTTP 409 and requires a new
preview/confirmation. Database deletions are atomic, including stages, steps,
logs, cards and latest indexes. Branch/PR indexes fall back to remaining history.
Repository build counters are never decremented and numbers are not reused.

The new API is `POST /api/repos/{owner}/{repo}/builds/history/delete`:

- Preview selected builds: `{"numbers":[12,15]}`.
- Preview non-success across all pages: `{"non_success":true}`.
- An explicit non-success selection is also supported: `{"numbers":[12,15],"non_success":true}`.
- Preview all completed builds before a number: `{"before":20}`.
- Commit: send the same filter plus `"commit":true` and the returned `"token"`.

Explicit selections are limited to 100 unique positive numbers. Do not send
`numbers` and `before` together. Both preview and commit require a system admin.
The old unconfirmed `DELETE /api/repos/{owner}/{repo}/builds?before=N` route is no
longer registered. `DELETE /builds/{number}` remains the existing cancel action.

With S3/Azure log storage, object cleanup runs after the SQL transaction. External
storage cannot participate in that transaction. If it fails, the UI reports a
warning and server logs record the affected step IDs for manual cleanup; the build
records have already been deleted. Normal SQL-backed logs are removed atomically.
Deleting history does not modify Gitea commits or remote commit check statuses.

Restart the server to load the changes and the updated embedded UI. No environment
changes or database migrations are required:

```tcsh
go run ./cmd/drone-server
```

The UI bundle URL is content-versioned, including when served below `/drone`.
