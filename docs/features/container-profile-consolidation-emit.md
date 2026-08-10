# Container-profile consolidation: emit slug only on change

## Summary

During time-series consolidation, `ContainerProfileProcessor.consolidateKeyTimeSeries`
(`pkg/registry/file/containerprofile_processor.go`) sends the consolidated workload
**slug** to `ConsolidatedSlugChannel` **only when the run actually produced new
aggregated data** — i.e. when the aggregated ApplicationProfile / NetworkNeighborhood
were rewritten this tick. For k8s host type the consumer (event-ingester-service's
container-profile consolidator) turns each slug into a downstream `onFinish` message
that ultimately upserts `container_statuses`.

## Behavior

- `updateProfile` and `processTimeSeriesInTransaction` return `aggregatedUpdated bool`.
  It mirrors the existing `newData` gate: aggregated AP/NN are only rewritten via
  `updateAggregatedProfiles` when new time-series data arrived this run.
- `consolidateKeyTimeSeries` emits the slug only when
  `HostType == Kubernetes && aggregatedUpdated`.
- Idle / already-consolidated workloads (a series with `has_data=false` and no new
  entries) therefore produce **no** emission, instead of re-emitting every 30s tick.

## Why

Emitting on every run for every workload floods the downstream
`synchronizer-finished-v1` topic and makes the `container_statuses` upsert path
re-process unchanged workloads. Under high concurrency those upserts deadlock
(SQLSTATE 40P01); when the consumer treated the deadlock as a hard error and
re-queued the message, the backlog wedged (observed ~1.17M backlog, 0 drain in
prod-ap-southeast-2). Gating the emission on real change removes the churn at the
source; the downstream deadlock is separately hardened with retry + deterministic
lock ordering in `postgres-connector`.

## Related code

- `pkg/registry/file/containerprofile_processor.go` — `consolidateKeyTimeSeries`,
  `processTimeSeriesInTransaction`, `updateProfile`, `sendConsolidatedSlugToChannel`.
