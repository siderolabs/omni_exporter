# omni_exporter — agent guide

This is the ever-growing knowledge base for the project.
Maintain it as you go: whenever you learn something durable about how this repo works, add it here.
It is not append-only, so fix or delete anything that becomes wrong or outdated.
Only capture timeless, project-general knowledge here, never in-flight work or an individual's local setup.
Prefer pointers over copies and invariants over inventories: metric lists, flag lists, constants and version numbers live in the code and rot here.

Markdown note: this file is linted (`markdownlint` with the `sentences-per-line` rule), so keep one sentence per line and the first line as the single top-level heading.

`CLAUDE.md` and `GEMINI.md` just import this file, so this is the one place to edit.

## What this repo is

A Prometheus exporter that exposes the state of an [Omni](https://github.com/siderolabs/omni) instance as per-object metrics: one series per cluster, machine, machine set, upgrade and etcd backup.
It is kube-state-metrics for Omni, and the analogy is worth taking seriously, because most design questions here have already been answered by that project.
It authenticates with an Omni service account that only needs the `Reader` role, and nothing in it ever writes to Omni.

The deliberate non-goals:

- No aggregation.
  Per-object series go out raw and PromQL does the summing and counting, so the exporter never has to guess which rollups someone wants.
- No shipped alerting rules or user-facing dashboards.
  The dashboard under `hack/compose` exists to develop against, not to ship.
- No duplication of the metrics Omni exposes natively under the `omni_` prefix.
  Those are instance-level aggregates on an internal endpoint, while this is the per-object complement, and the distinct `omni_exporter_` prefix keeps the two from ever colliding, including when both are scraped into one Prometheus.
- No reimplementation of anything the Omni client SDK already owns, in particular the transport, the request signing and the transparent resumption of broken watches.

## The cast

- Omni owns the resource model, the state and its own instance-level metrics.
  Resource types, their specs and their enums come from the Omni client SDK, so what this repo can express is bounded by what that SDK's generated code knows.
- The COSI protobuf state client under the Omni client SDK owns the gRPC watch transport.
  It resumes a broken watch stream transparently from a bookmark it tracks internally, which this repo relies on rather than reimplementing, and it hands over an ordinary error whenever it cannot.
  Its retry budget runs from the moment the stream is established, not from the moment it breaks, so a long-lived stream that breaks usually fails straight through to an error and is never resumed at all.
- Prometheus owns aggregation, alerting and retention.
- `pkg/collector` is an importable library and the standalone binary is thin wiring around it.
  The library takes a COSI state and returns a `prometheus.Collector`, so it can be embedded into any process with access to an Omni state, Omni itself included.
  Keeping its dependencies down to the Omni client SDK is what makes that possible, so adding a dependency there is a real decision.

## Domain invariants the repo leans on

- A COSI watch opened with bootstrap contents delivers the current contents first, then exactly one `Bootstrapped` event, then live events.
  The contract of record is the COSI runtime `state` package.
- `Bootstrapped` and `Noop` events carry a tombstone resource of an unrelated type.
  Event handling must therefore switch on the event type before it touches the resource, and doing it the other way round fails on a type assertion at bootstrap time rather than in some rare path.
- A COSI stream is never silently lossy.
  A resume from a bookmark either replays exactly the missed events or is refused outright, because the backend rejects a bookmark outside its history ring or from a previous run of the process, and the client turns that refusal into an error instead of retrying.
  A resumed stream is therefore behind, never wrong, which is why the exporter tolerates one instead of resynchronizing.
- Bookmarks only start once the bootstrap is complete: the initial contents events carry none, only the `Bootstrapped` event does.
  A stream broken mid-bootstrap therefore cannot be resumed at all and surfaces as an error, which is what keeps a partial view from ever being mistaken for a complete one.
- The Omni version resource is tiny, always present and readable by any authenticated identity, which is what makes it the reachability probe.
  A not-found answer still proves that Omni is reachable, so the probe treats it as success.
- Enum label values are derived from the generated protobuf `<Enum>_name` maps, so the set of known values is whatever this build's SDK was compiled against.
  That gap opens whenever Omni gains an enum value before this repo bumps its client.
- Cluster, machine set and machine IDs are user-chosen names.
  A cluster deleted and recreated under the same name continues the same series, and no metric can distinguish that from the cluster having never gone away.

## Behavior contracts

The exporter serves metrics rendered from an in-memory view maintained by watches, not from a read per scrape.
The guarantee is that object metrics are suppressed whenever serving them would be an outright wrong answer, and served otherwise, accepting that they can be stale.
Suppression is decided per resource type on every scrape, and needs either of two conditions: Omni did not answer this scrape's reachability probe, or the initial sync of that resource type has not completed.

Both conditions are about wrongness, not staleness.
An unreachable Omni puts no bound on how far the view has drifted, and a half-filled view renders as "these objects do not exist".
A watch failure after the initial sync is neither, so it suppresses nothing: the last complete view keeps being served, going stale, until the stream resumes or a fresh complete one is swapped in.
That is the kube-state-metrics posture, and it is deliberate, because an absent series is a worse failure mode for Prometheus than an old one.
The staleness it accepts has no upper bound, though.
`up` covers reachability and the initial sync, not ongoing watch health, so a reachable Omni with a persistently broken watch serves an ever older view with `up` at 1.
The signal for that is the watch attempt counter, since a quiet resource type legitimately leaves the last-sync timestamp untouched.

What follows from that:

- Suppression is per resource type, so one collector still bootstrapping leaves the rest serving.
  The `up` metric is the conjunction over reachability and every collector, so it is the gate alerts on object metrics should carry.
- A suppressed series is absent, which Prometheus marks stale immediately, so an Omni outage looks exactly like it would with a read-per-scrape exporter.
  Absence-based alerts cannot tell a deleted object from an unreachable Omni, which is the other reason for the `up` gate.
- Resource types are watched independently, so a single scrape can mix moments a fraction of a second apart.
  Nothing offers a cross-collector snapshot, and none is planned, so alerts spanning two resource types need a `for` duration longer than one scrape interval.
- Every known enum value is emitted as its own 0/1 series on every scrape, the kube-state-metrics pattern.
  Series therefore never appear or disappear on a state transition and alerting expressions can stay simple equality matches.
  A value unknown to this build is emitted as an extra series labeled with its number rather than dropped, so a newer Omni degrades into an unfamiliar label instead of a silent gap.
- Mutable and human-readable attributes live on info metrics, not on the state metrics, so a hostname or version change does not churn the state series.
  Joining them back together is the query's job.
- Failures degrade the exporter instead of terminating it.
  Watch failures retry forever with jittered backoff, panics in event handling and in rendering are contained to the collector they happen in, and a rendering failure emits no partial family.
  The service account key is validated locally at startup so malformed input fails fast, but authentication is deliberately not exercised there: an expired or revoked account at runtime must keep the exporter serving and report through the metrics.
- The scrape path never returns an Omni failure to the HTTP layer, so a gather error can only be an internal bug and is served as a 500.

## Code map

- `cmd/omni_exporter`: the entrypoint.
  Signal handling and nothing else.
- `internal/app`: the wiring.
  Flags, logger, client construction, the registry, the HTTP server and its landing page.
  TLS and basic authentication on the metrics endpoint come from the Prometheus exporter toolkit, so they are configured by its web config file rather than by flags of this repo.
- `pkg/collector`: everything real, and the public library surface.
  - `collector.go` holds the orchestration, the reachability probe and the exporter self-metrics.
  - `internal/watch` holds the generic per-resource-type watch collector: the retry loop, the bootstrap into a staging map that is swapped in atomically, and the rendering under a recovery boundary.
    It knows nothing about Omni, which is what lets it be tested against a stand-in resource type of the COSI runtime itself.
  - `enum.go` holds the enum expansion into 0/1 series.
  - One file per resource area holds the descriptors and the rendering: clusters, machines, cluster machines, machine sets, upgrades, etcd backups.
- Adding a resource type means one constructor returning a watch collector with its descriptors and render function, plus registering it in the collector list.
  The generic machinery is meant to make that the whole change.

Test conventions:

- Tests always live in an external `package <name>_test`, never in the package itself, and the `*_internal_test.go` escape hatch the `testpackage` linter allows is not used here.
  When a test needs something unexported, either drive it through the exported API or add a narrow hook to that package's `export_test.go`, which holds only hooks and aliases and never test functions.
  A package that is awkward to test from outside usually has the wrong boundary, which is worth fixing before adding hooks.
- The full metrics output for a fixed state is asserted against a golden file, regenerated with the suite's `-update` flag and never hand-edited.
  Read the resulting diff, it is where a change in exported output becomes visible.
- The same fixed state is exercised twice, once through direct state access and once over the real COSI gRPC protocol on an in-process listener, against the same golden file.
  That is what keeps the protocol-level behavior (bootstrap, tombstone events, server restart, recovery from a broken stream) honest without a live Omni.
  The server-restart test asserts that the view converges back, not how, because both of its servers share one backing state and the client therefore resumes from its bookmark rather than re-bootstrapping, which a real Omni restart would force.
- Promlint runs over the whole output, so naming and help-text conventions are enforced rather than reviewed.
- The concurrency test is only meaningful under the race detector, so `make unit-tests-race` is part of verifying a change here, not an optional extra.
- `internal/app` has an end-to-end smoke test that runs the real entrypoint against an endpoint where nothing listens and scrapes it over HTTP, which is the cheapest guard for the wiring and the degrade-do-not-terminate behavior.

## Verifying changes

Gates, in order, using the make targets, which are authoritative: `make lint-fmt`, `make lint` (golangci-lint, gofumpt, govulncheck and markdownlint) and `make unit-tests`, plus `make unit-tests-race` for anything touching the watch loops or the view.

## Generated files and rekres

Files generated by kres must not be hand-edited, and a generated file identifies itself with a generated-file comment near its top.
To change them, edit `.kres.yaml` and run `make rekres`.
kres scans the tracked source tree at generation time: it wires root-level `.md` files into the markdown-lint stage, and writes Dockerfile `COPY` lines only for source directories that exist, so `git add` first and re-run rekres after adding a top-level source directory or a root markdown file.
Markdown under subdirectories is not linted, only the root-level files are.

## Dependency bumps

Bump direct deps only: `go list -m -f '{{if and (not .Main) (not .Indirect)}}{{.Path}}@upgrade{{end}}' all | xargs go get`, then `go mod tidy`.
Never use `-u` or `all`, since those drag indirect deps past what direct deps require.
The `omni/client` module is versioned with Omni releases, and it is what defines the resource types and enum values this repo can expose, so a metric that depends on a new field has its floor there.
When `go.mod` carries a `replace` for `omni/client`, it is a temporary pin and the bump does not apply to it.

## Release process

Releases are cut with the repo's own tooling (`hack/release.sh` and `hack/release.toml`) in two phases.
First a release PR sets `hack/release.toml` `previous` to the latest tag, regenerates version files, runs `hack/release.sh changelog vX` to update the changelog, and creates the `release(vX): prepare release` commit via `hack/release.sh commit vX` (the script adds a DCO sign-off, and commits are GPG-signed when git is configured to sign).
Then, after merge, a signed tag is pushed, which triggers CI to build and push the release images and draft a GitHub release.
A deps bump and a release are separate PRs.

Metric names are a public contract once released.
Renaming or relabeling an existing series breaks other people's dashboards and alerts silently, so treat established names as frozen unless there is an explicit decision otherwise.

## Commit and PR conventions

Siderolabs PRs often contain a single commit, but multiple commits are fine for separate atomic logical changes (conform sets `maximumOfOneCommit: false`).
A single commit is usually still preferable, since the PR title and body then equal the commit title and body minus the DCO trailer.
Either way, keep PR titles and bodies simple like a commit message, without fancy markdown, and not long like a documentation page.
The commit title follows the conform rules: a `type[(scope)]: imperative summary` line, with types such as `feat`, `fix`, `chore`, `refactor`, `test`, `docs` and `release`.
Commits carry a DCO `Signed-off-by` trailer (`git commit -s`) and are GPG-signed.
Reference issues inline as sentences, for example `Closes siderolabs/omni#<n>.`

## Related projects and reference pointers

- `siderolabs/omni` is the instance this exporter reads, and the home of the client SDK, the resource model and the native `omni_` metrics.
- `siderolabs/omni-audit-log-exporter` is the sibling: it streams the audit event history, while this repo exposes current state.
- kube-state-metrics is the design precedent for the metric shape, the info-metric join pattern and the 0/1 enum series.
- `README.md` carries the user-facing metric reference, the failure behavior and the caveats, so it is the place to update when the exposed surface changes.
- `hack/compose/README.md` documents the local Prometheus and Grafana stack for developing against a real Omni instance.
