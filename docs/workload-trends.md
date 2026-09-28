# Workload trends

Open **Workload trends** using the line-chart icon in the sidebar, or visit
`/drone/monitor` when Drone is hosted below `/drone` (`/monitor` without a base path).
No new environment variables are required. Restart the server using the existing
tcsh environment:

```tcsh
go run ./cmd/drone-server
```

Startup migrations add the trend sample and LSF tracking tables. The dashboard
starts accumulating history after this version is started; it cannot reconstruct
past scheduler states. Jobs submitted before tracking was enabled are not included.

## What is counted

- **Builds processing:** default `running`; Include queued adds `pending`.
  Blocked/approval-waiting, completed and canceled builds are excluded.
- **LSF jobs processing:** default `RUN`; Include queued adds `PEND`.
  Each bsub submission counts once, regardless of CPU slots or child processes.
  Other known scheduler states (WAIT, PROV and suspended states) are not RUN/PEND
  and do not contribute. Unknown states produce a data gap for the affected repo.
- Only jobs submitted by the built-in local LSF engine are registered. Job ID,
  generated job name and repository ID are persisted. Name validation prevents
  counting an unrelated job if LSF recycles a previously tracked ID.
- The existing LSF job-information log toggle does not affect monitoring.

The built-in collector samples once every 30 seconds, including when nobody has
the dashboard open. It recovers registered jobs after server restarts. It queries
only those job IDs, in batches of 100 and with up to four concurrent commands:

```text
bjobs -a -noheader -o 'jobid stat job_name:250' <tracked job IDs...>
```

The configured DRONE_LSF_BJOBS path is used, so sourcing the mock LSF environment
uses mock jobs; production uses the configured real LSF client. The mock supports
the same output fields. No global scan of another user's jobs is performed.

A single Drone server with its local LSF engine is supported. External runner
processes do not report submissions to this collector. When the local LSF engine
is not enabled, the LSF chart is unavailable rather than showing zero.

## History and chart behavior

Raw 30-second samples are retained for seven days, independent of build-record
cleanup. Successful sampling also removes expired data. Missing frames are gaps,
while a valid sample with no active work is zero. LSF query failures affect only
the relevant repositories' LSF values; build sampling can continue. Tracking-write
failures are retried and suppress LSF samples until the tracking queue is saved.
If the process exits before a failed tracking write can be retried, that unsaved
submission cannot be recovered from the registry.

The UI refreshes every 30 seconds and offers 1h, 6h, 24h, 3d and 7d ranges. Both
charts share time and repository colors; each has its own Include queued switch.
The sidebar allows searching and selecting up to 12 repos at once (six initially).
Mouse hover or arrow keys on a focused chart inspect values in both charts.
Dates/times use the browser's local timezone.

Longer ranges use the maximum sampled value per bucket, not a sum over time:

| Range | Chart interval |
| --- | --- |
| 1h / 6h | 30 seconds |
| 24h | 2 minutes |
| 3d | 10 minutes |
| 7d | 15 minutes |

RUN and RUN+PEND peaks are calculated separately. A bucket containing missing
samples is a gap, avoiding a misleading continuous line over an outage. New
installations may need a complete bucket before long-range views show points.
Short jobs between samples may not be observed; this is trend monitoring, not
an accounting or audit log. There is no interpolation across missing samples.

## Visibility

`GET /api/monitor/trends?hours=6&repos=1,2` requires an authenticated active user.
Omitting `repos` selects the first six visible repositories; an empty `repos=`
selects none. The API accepts at most 12 selected IDs and 1–168 hours.

The server applies Drone visibility rules: system admins can see all current
repos; authenticated users can see public/internal repos; private repos require
read access from the SCM. Private permissions are rechecked on every request,
with failed checks excluded. Current repository visibility also applies to old
samples. Hidden repositories' names, IDs, counts and series are never returned.
Responses are not cacheable. Deleted repositories are not exposed in the list.

## Verification

SQLite integration tests cover 7-day retention, peak aggregation, missing samples,
build-record deletion independence, tracking persistence across service restart,
tracking-write retries and reused Job IDs. API tests cover permission revocation,
SCM errors, direct-ID requests and input limits. UI tests cover independent queue
switches, time ranges, selection and clearing stale data after API errors. The
mock-backed runner integration test exercises registration, completion and the
bjobs format. MySQL/PostgreSQL migrations are supplied, but their database servers
were not available for integration testing in this workspace.
