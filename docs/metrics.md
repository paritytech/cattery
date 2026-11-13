# Metrics

Cattery exposes Prometheus metrics on `/metrics`, alongside `/status`. By
default both are served on the agent port; set `server.statusListenAddress` to
move them to a separate port (see [configuration.md](configuration.md)). The
Helm chart ships a ServiceMonitor that scrapes this endpoint.

The standard Go process and runtime collectors are exposed too. Everything
below is Cattery's own.

## Labels

| Label | Meaning |
|-------|---------|
| `org` | GitHub organisation the tray type belongs to |
| `tray_type` | Tray type name from the config |
| `repository` | Bare repository name, without the owner — `org` already carries that |
| `provider` | Provider name (`google`, `docker`, `nomad`, …) |

A tray that has not been assigned a job yet has no repository, and appears
under an empty `repository` label rather than being dropped. It still occupies
a runner, so it still counts.

## Counters

| Metric | Labels | Meaning |
|--------|--------|---------|
| `cattery_job_seconds_total` | `org`, `tray_type`, `repository` | Runner seconds consumed by finished jobs |
| `cattery_jobs_total` | `org`, `tray_type`, `repository` | Number of finished jobs |
| `cattery_stale_trays_count` | `org`, `tray_type` | Trays cleaned up by the stale handler |
| `cattery_preempted_trays_count` | `org`, `tray_type` | Trays lost to preemption |
| `cattery_tray_provider_errors` | `org`, `provider`, `tray_type`, `operation_type` | Provider errors during tray `create` / `delete` |
| `cattery_scaleset_poll_errors` | `org`, `tray_type` | Errors handling scale set messages |

`cattery_job_seconds_total` and `cattery_jobs_total` are recorded in
`TrayManager.DeleteTray`, which every teardown path funnels through — job
completed, agent unregister on preemption or SigTerm, the stale reaper, and
creation-failure cleanup. Preempted and lost jobs are therefore counted, not
just cleanly finished ones: they consumed runner time regardless.

Each job is counted exactly once. The tray's `jobStartedAt` is read and cleared
atomically, so a tray whose provider cleanup failed and is later retried by the
stale handler is not billed twice. Trays that never ran a job have no start
time and are not counted at all.

## Gauges

| Metric | Labels | Meaning |
|--------|--------|---------|
| `cattery_registered_trays` | `org`, `tray_type`, `repository` | Trays currently registered (any status but `deleting`) |
| `cattery_scaleset_pending_jobs` | `org`, `tray_type` | Available (queued) jobs |
| `cattery_scaleset_assigned_jobs` | `org`, `tray_type` | Jobs assigned to runners |
| `cattery_scaleset_running_jobs` | `org`, `tray_type` | Currently running jobs |
| `cattery_scaleset_busy_runners` | `org`, `tray_type` | Busy runners |
| `cattery_scaleset_idle_runners` | `org`, `tray_type` | Idle runners |
| `cattery_scaleset_registered_runners` | `org`, `tray_type` | Registered runners |

`cattery_registered_trays` is computed from the database on each scrape. If the
query fails the collector logs and emits nothing for that scrape, so the series
goes stale rather than reporting a false zero.

The `cattery_scaleset_*` gauges come from GitHub's scale set statistics, which
are reported per scale set. GitHub does not break them down by repository, so
these cannot carry a `repository` label.

## Per-repository usage

```promql
# Runner hours per repository over the last 7 days.
sum by (repository) (increase(cattery_job_seconds_total[7d])) / 3600

# Biggest movers: this week's consumption over last week's.
sum by (repository) (increase(cattery_job_seconds_total[7d]))
  / sum by (repository) (increase(cattery_job_seconds_total[7d] offset 7d))

# Is a repository running more jobs, or slower ones?
sum by (repository) (increase(cattery_job_seconds_total[7d]))
  / sum by (repository) (increase(cattery_jobs_total[7d]))

# Runners held right now.
sum by (repository) (cattery_registered_trays)
```

### Cardinality

The two per-repository counters keep a series for every
`org` × `tray_type` × `repository` combination ever seen, including repositories
that have since gone quiet. The gauge only has series for live trays. If this
becomes a problem, drop or bucket repositories with `metric_relabel_configs` in
the ServiceMonitor — no Cattery change or redeploy needed.
