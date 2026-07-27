# omni_exporter

A Prometheus exporter for [Omni](https://github.com/siderolabs/omni).

It connects to the Omni API using a service account and exposes the state of the Omni instance as per-object Prometheus metrics: clusters, machines, machine sets, upgrades and etcd backups.
Think of it as kube-state-metrics for Omni.
It works against both self-hosted and SaaS Omni instances.

Omni itself also exposes Prometheus metrics (with the `omni_` prefix), but those are instance-level aggregates on an internal endpoint, e.g. the total number of machines.
This exporter complements them with per-cluster and per-machine series that can be scraped with your own observability stack, so you can alert on things like "cluster X is not ready" or "machine Y is disconnected".
All metrics of this exporter use the `omni_exporter_` prefix, so the two sets never collide.

## Quick start

Create a read-only service account on your Omni instance:

```sh
omnictl serviceaccount create --use-user-role=false --role=Reader omni-exporter
```

The `Reader` role is enough for everything the exporter reads, do not give it more.
Note that `--use-user-role` defaults to true, which would clone the role of the user running the command instead.

The command prints an `OMNI_SERVICE_ACCOUNT_KEY`.
Run the exporter with it:

```sh
docker run -d -p 10048:10048 \
  -e OMNI_ENDPOINT=https://<account>.omni.siderolabs.io \
  -e OMNI_SERVICE_ACCOUNT_KEY=<key> \
  ghcr.io/siderolabs/omni_exporter:latest
```

Metrics are served on `http://localhost:10048/metrics`.
The key can alternatively be read from a file via `--omni.service-account-key-file`, e.g. for Kubernetes secret mounts.
See `--help` for all flags, including `--web.config.file` for TLS and basic authentication on the metrics endpoint.

Example scrape configuration:

```yaml
scrape_configs:
  - job_name: omni
    static_configs:
      - targets: ["localhost:10048"]
```

## Metrics

State metrics representing an enum (phases, stages, statuses) emit one series per possible value with a 0/1 value, so alerting expressions can rely on simple `== 1` matches and series do not come and go on state transitions.

| Name (prefix `omni_exporter_` omitted) | Labels | Description |
| --- | --- | --- |
| `cluster_phase` | `cluster_id`, `phase` | Whether the cluster is in the given phase. |
| `cluster_ready` | `cluster_id` | Whether the cluster is ready. |
| `cluster_available` | `cluster_id` | Whether the cluster is available. |
| `cluster_kubernetes_api_ready` | `cluster_id` | Whether the Kubernetes API of the cluster is ready. |
| `cluster_control_plane_ready` | `cluster_id` | Whether the control plane of the cluster is ready. |
| `cluster_machines` | `cluster_id` | Number of machines in the cluster. |
| `cluster_machines_healthy` | `cluster_id` | Number of healthy machines in the cluster. |
| `cluster_machines_connected` | `cluster_id` | Number of machines in the cluster connected to Omni. |
| `cluster_machines_requested` | `cluster_id` | Number of machines requested for the cluster, which can differ from `cluster_machines` when machine classes are used. |
| `cluster_info` | `cluster_id`, `talos_version`, `kubernetes_version` | Cluster information. |
| `cluster_talos_upgrade_phase` | `cluster_id`, `phase` | Whether the Talos upgrade of the cluster is in the given phase. |
| `cluster_kubernetes_upgrade_phase` | `cluster_id`, `phase` | Whether the Kubernetes upgrade of the cluster is in the given phase. |
| `cluster_etcd_backup_enabled` | `cluster_id` | Whether etcd backups are enabled for the cluster. |
| `cluster_etcd_backup_status` | `cluster_id`, `status` | Whether the last etcd backup of the cluster is in the given status (series exist only after the first backup attempt, so enabled=1 with no status series means no backup was attempted yet). |
| `cluster_etcd_backup_last_success_timestamp_seconds` | `cluster_id` | Unix timestamp of the last successful etcd backup. |
| `cluster_etcd_backup_last_attempt_timestamp_seconds` | `cluster_id` | Unix timestamp of the last etcd backup attempt. |
| `machine_connected` | `machine_id` | Whether the machine is connected to Omni. |
| `machine_power_state` | `machine_id`, `state` | Whether the machine is in the given power state. |
| `machine_info` | `machine_id`, `cluster_id`, `role`, `hostname`, `management_address`, `talos_version`, `platform`, `arch` | Machine information to be joined with the state metrics, with an empty `cluster_id` for unallocated machines. |
| `cluster_machine_stage` | `cluster_id`, `machine_set_id`, `machine_id`, `stage` | Whether the cluster machine is in the given stage. |
| `cluster_machine_ready` | `cluster_id`, `machine_set_id`, `machine_id` | Whether the cluster machine is ready. |
| `machine_set_phase` | `cluster_id`, `machine_set_id`, `phase` | Whether the machine set is in the given phase. |
| `machine_set_ready` | `cluster_id`, `machine_set_id` | Whether the machine set is ready. |
| `machine_set_machines` | `cluster_id`, `machine_set_id` | Number of machines in the machine set. |
| `machine_set_machines_healthy` | `cluster_id`, `machine_set_id` | Number of healthy machines in the machine set. |
| `machine_set_machines_connected` | `cluster_id`, `machine_set_id` | Number of machines in the machine set connected to Omni. |
| `machine_set_machines_requested` | `cluster_id`, `machine_set_id` | Number of machines requested for the machine set. |
| `etcd_backup_store_info` | `configuration_name` | Etcd backup store information, with the configured store type, e.g. `s3` or `disabled`. |
| `etcd_backup_store_ready` | | Whether the etcd backup store configuration of the instance has no errors (it is 1 also when backups are disabled altogether, check `etcd_backup_store_info` for that). |
| `up` | | Whether Omni is reachable and every collector has completed its initial sync and renders successfully. |
| `reachable` | | Whether Omni answered the reachability probe of the current scrape. |
| `collector_success` | `collector` | Whether the object metrics of a single collector are exposed: Omni reachable, initial sync completed, rendering succeeded. |
| `collector_events_total` | `collector`, `event` | Watch events applied to the in-memory view, excluding the initial sync. |
| `collector_watch_attempts_total` | `collector` | Watch establishment attempts, counted before the attempt; each success is a full resynchronization (above one means a previous attempt had failed). |
| `collector_cached_resources` | `collector` | Number of resources in the in-memory view. |
| `collector_last_sync_timestamp_seconds` | `collector` | Unix timestamp of the last sync or applied event (absent until the first sync, and quiet resource types legitimately keep an old value). |
| `build_info` | `version`, `revision`, ... | Exporter build information. |

The `build_info` metric and the standard Go and process metrics are added by the exporter binary, everything else comes from the collector library (see below).

Aggregations are left to PromQL.
For example, the number of machines of a cluster by stage is `sum by (cluster_id, stage) (omni_exporter_cluster_machine_stage)`, and the machine count of the whole instance is `count(omni_exporter_machine_connected)`.

## How it works and failure behavior

The exporter maintains one watch per resource type, keeps an in-memory view updated by the events, and scrapes render from that view.
The cost of running it is therefore driven by the actual state churn, not by the scrape frequency: the only Omni access on the scrape path is a tiny reachability probe.
The probe is bounded by a timeout of a few seconds, so keep the Prometheus scrape timeout above that.

Object metrics are suppressed only when serving them would be an outright wrong answer, not merely an old one:

- Every scrape starts with a cheap, deadline-bounded reachability probe.
  When Omni does not answer it, the object metrics are suppressed for that scrape (Prometheus marks the series stale immediately, exactly as if the objects were read on every scrape), `omni_exporter_reachable` and `omni_exporter_up` go to 0, and only the exporter self-metrics keep being served.
  An unreachable Omni puts no bound on how far the view has drifted, which is what makes suppression the right answer there.
- Until a resource type has completed its initial sync, its object metrics are suppressed and its `omni_exporter_collector_success` is 0.
  A half-filled view would read as "these objects do not exist", so this is the one case that has to fail closed.

A watch failure after the initial sync does not suppress anything.
The client resumes the stream from its last bookmark, and a resume the backend refuses surfaces as an error which re-synchronizes that resource type from scratch, visible in `omni_exporter_collector_watch_attempts_total`.
A resume applies the missed events to the existing view, a re-synchronization swaps in a fresh one when it completes, and either way the last view keeps being served throughout, so the metrics go stale instead of disappearing and coming back.

That choice has a cost. `omni_exporter_up` covers reachability and the initial sync, but says nothing about the ongoing health of a watch.
So if Omni keeps answering the reachability probe while a watch stays broken, the exporter serves an ever older view with `up` at 1, for as long as that lasts.
Alerting on `rate(omni_exporter_collector_watch_attempts_total[10m]) > 0` catches a resource type that keeps re-synchronizing.
`omni_exporter_collector_last_sync_timestamp_seconds` cannot fill that role, because a resource type with no changes legitimately keeps an old value.

An outage of Omni therefore looks the same as it would with a read-per-scrape exporter: object metrics disappear and `omni_exporter_up` is 0, within one scrape interval.

Notes:

- Alerts on object metrics should be gated on `omni_exporter_up == 1`, and `omni_exporter_up == 0` deserves its own alert.
  In particular, absence-based alerts cannot distinguish a deleted object from an unreachable Omni without that gate.
- An expired, deleted or unauthorized service account surfaces through the reachability probe on the next scrape.
  Service account keys have a limited lifetime (one year by default), and rotating the key requires a restart.

## Caveats

- The `cluster_id`, `machine_id` and `machine_set_id` labels are deliberately not named `cluster` etc., to avoid clashing with target labels commonly attached by scrape configurations.
- Cluster and machine set IDs are user-chosen names.
  A cluster deleted and recreated under the same name continues the same series.
- Different resource types are read independently within a scrape, so they can be a moment apart from each other.
  Use alert `for` durations longer than one scrape interval instead of expecting cross-metric consistency within a single scrape.

## Using as a library

The collectors live in the importable [`pkg/collector`](pkg/collector) package: it takes a COSI state and returns a `prometheus.Collector`, and only depends on the Omni client SDK.
The watches are started via its `Run` method, which retries all upstream failures until the given context is canceled.
The standalone binary wires it to the Omni API, but it can be embedded into any process with access to an Omni state.

## License

Mozilla Public License 2.0, see [LICENSE](LICENSE).
