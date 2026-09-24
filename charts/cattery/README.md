# cattery

Helm chart for [Cattery](https://github.com/paritytech/cattery), a scheduler
and lifecycle manager for GitHub Actions self-hosted runners.

## TL;DR

```bash
helm install cattery ./charts/cattery -f my-values.yaml
```

## Prerequisites

- Kubernetes 1.24+
- Helm 3.8+
- A reachable MongoDB instance (this chart does **not** bundle one)
- A [GitHub App](https://docs.github.com/en/apps/creating-github-apps) with
  Actions read/write and Pull requests read permissions, installed on your
  organization
- Credentials for at least one provider (Docker socket access, a GCP service
  account for the `google` provider, or nothing extra for the `kubernetes`
  provider, which runs trays in this cluster)

## Installing

Minimal `values.yaml`:

```yaml
image:
  tag: "0.0.1"

config:
  server:
    advertiseUrl: https://cattery.example.com

  database:
    uri: mongodb://mongodb.default.svc:27017/
    database: cattery

  github:
    - name: my-org
      appId: 123456
      appClientId: Iv123abC
      installationId: 987654321
      privateKeyPath: /cattery/secrets/my-org/private-key.pem

  providers:
    - name: gce
      type: google
      project: my-gcp-project

  trayTypes:
    - name: my-runner
      provider: gce
      githubOrg: my-org
      runnerGroupId: 1
      maxTrays: 5
      shutdown: true
      config:
        project: my-gcp-project
        zones: [europe-west1-b, europe-west1-c]
        machineType: e2-standard-2
        instanceTemplate: global/instanceTemplates/my-runner

secretFiles:
  my-org-private-key:
    mountPath: /cattery/secrets/my-org/private-key.pem
    existingSecret: my-org-github-app
    existingSecretKey: private-key.pem
```

### GCP provider

When using the `google` provider outside of GKE Workload Identity, mount the
service account key JSON as a file and point `credentialsFile` at it:

```yaml
secretFiles:
  gcp-sa:
    mountPath: /cattery/secrets/gcp-sa.json
    existingSecret: my-gcp-sa
    existingSecretKey: key.json

config:
  providers:
    - name: gce
      type: google
      project: my-gcp-project
      credentialsFile: /cattery/secrets/gcp-sa.json
```

On GKE, skip the file and use Workload Identity via `serviceAccount.annotations`.

### Docker provider

The `docker` provider needs access to a Docker daemon. Typical pattern is a
hostPath mount of the socket (requires a privileged node configuration — not
recommended for multi-tenant clusters):

```yaml
extraVolumes:
  - name: docker-sock
    hostPath:
      path: /var/run/docker.sock
      type: Socket
extraVolumeMounts:
  - name: docker-sock
    mountPath: /var/run/docker.sock
```

### Kubernetes provider

The `kubernetes` provider runs each tray as a batch/v1 Job. It can target the
cluster the server runs in or any other cluster.

**Same cluster.** A provider without `kubeconfig`, `context` or `server` uses
the pod's service account. The chart then mounts the token and, with `rbac.create`,
creates a Role + RoleBinding in `runners.namespace` for Jobs
(create/get/delete), pods (get/list/watch) and podtemplates (get). The
`runners` block also provisions what the runner pods themselves need:

```yaml
config:
  server:
    # reachable from runner pods; in server mode the agent is downloaded from here
    advertiseUrl: http://cattery.cattery.svc:5137
  providers:
    - name: k8s
      type: kubernetes
      namespace: cattery-runners      # must equal runners.namespace
  trayTypes:
    - name: k8s-small
      provider: k8s
      githubOrg: my-org
      runnerGroupId: 1
      maxTrays: 10
      config:
        image: ghcr.io/actions/actions-runner:2.333.0
        serviceAccountName: cattery-runner
        resources:
          requests: {cpu: "2", memory: 4Gi}
    - name: k8s-dind
      provider: k8s
      githubOrg: my-org
      runnerGroupId: 1
      maxTrays: 5
      config:
        podTemplateRef: runner-dind

runners:
  namespace: cattery-runners
  createNamespace: true
  serviceAccount:
    create: true
    name: cattery-runner
    # rules: [...]   # optional Role for the runner pods, e.g. container hooks
  podTemplates:
    runner-dind:
      spec:
        serviceAccountName: cattery-runner
        containers:
          - name: runner
            image: ghcr.io/actions/actions-runner:2.333.0
            env:
              - name: DOCKER_HOST
                value: tcp://localhost:2375
          - name: dind
            image: docker:dind
            args: ["--host=tcp://0.0.0.0:2375", "--tls=false"]
            securityContext:
              privileged: true
```

`runners.podTemplates` renders core/v1 PodTemplate objects that tray types
reference through `podTemplateRef`; cattery injects its agent into the
container named `runner`. The chart refuses to render when an in-cluster
provider's `namespace` differs from `runners.namespace`, since the RBAC would
otherwise land in the wrong place.

**Another cluster.** Mount credentials for the target cluster through
`secretFiles` and point the provider at them, either as a kubeconfig
(`kubeconfig` + `context`) or as an API server with a bearer token, which
avoids cloud auth plugins the image does not ship:

```yaml
secretFiles:
  target-token:
    mountPath: /cattery/secrets/target/token
    existingSecret: target-cluster
    existingSecretKey: token
  target-ca:
    mountPath: /cattery/secrets/target/ca.crt
    existingSecret: target-cluster
    existingSecretKey: ca.crt

config:
  providers:
    - name: k8s-remote
      type: kubernetes
      server: https://10.0.0.1:6443
      tokenFile: /cattery/secrets/target/token
      caFile: /cattery/secrets/target/ca.crt
      namespace: cattery-runners
```

Such providers get no RBAC, token mount or `runners` objects from this chart;
create the equivalent of the `runner-jobs` Role for the token's service
account in the target cluster. See
[docs/configuration.md](https://github.com/paritytech/cattery/blob/main/docs/configuration.md)
for the full list of provider and tray type fields.

## Configuration

All fields under `config` are rendered verbatim into `/etc/cattery/config.yaml`
inside the pod. See
[docs/configuration.md](https://github.com/paritytech/cattery/blob/main/docs/configuration.md)
for the full reference.

### Secrets

`secretFiles` mounts arbitrary files (typically GitHub App private keys) at
the path `config.github[*].privateKeyPath` expects. Two modes per entry:

- **Inline** — set `value` with the file content. A Secret is created by
  this chart. Convenient for bootstrapping; don't commit production values.
- **External** — set `existingSecret` (and optionally `existingSecretKey`,
  default `content`) to reference a Secret you manage elsewhere (sealed
  secrets, external-secrets-operator, vault, etc).

### Status port

`config.server.statusListenAddress` can be set to serve `/status` and
`/metrics` on a separate port. When set, the chart opens a second Service
port and the `ServiceMonitor` scrapes that port. Probes hit `/healthcheck`,
which is registered on both ports; they target the status port when set,
otherwise the main http port.

### ServiceMonitor

Enable with `serviceMonitor.enabled: true` (requires the Prometheus Operator
CRDs installed in the cluster).

## Values

| Key                                   | Default                        | Description                                     |
| ------------------------------------- | ------------------------------ | ----------------------------------------------- |
| `replicaCount`                        | `1`                            | >1 requires `config.coordination.backend` mongo/k8s. |
| `updateStrategy`                      | `{type: Recreate}`             | Use RollingUpdate for multiple replicas.        |
| `image.repository`                    | `docker.io/paritytech/cattery` | Container image.                                |
| `image.tag`                           | `""` (uses `.Chart.AppVersion`)| Image tag.                                      |
| `image.pullPolicy`                    | `IfNotPresent`                 |                                                 |
| `config.server.listenAddress`         | `0.0.0.0:5137`                 | Agent/API listen address.                       |
| `config.server.statusListenAddress`   | `0.0.0.0:3925`                 | Status/metrics listen address. Empty to share.  |
| `config.server.advertiseUrl`          | `http://cattery.example.com`   | URL runners use to reach the server.            |
| `config.database.uri`                 | `mongodb://mongodb:27017/`     | MongoDB connection URI.                         |
| `config.github`                       | `[]`                           | List of GitHub App configs.                     |
| `config.providers`                    | `[]`                           | List of provider configs.                       |
| `config.trayTypes`                    | `[]`                           | List of tray type configs.                      |
| `config.coordination.backend`         | `memory`                       | Leader election: `memory` / `mongo` / `k8s`.    |
| `runners.namespace`                   | `""` (release namespace)       | Namespace of the runner Jobs; in-cluster providers must use the same. |
| `runners.createNamespace`             | `false`                        | Create `runners.namespace` when it differs from the release namespace. |
| `runners.serviceAccount.create`       | `false`                        | ServiceAccount for runner pods (`runners.serviceAccount.name`, default `<fullname>-runner`) with optional Role `rules`. |
| `runners.podTemplates`                | `{}`                           | core/v1 PodTemplates for `podTemplateRef` tray types, in `runners.namespace`. |
| `secretFiles`                         | `{}`                           | Files mounted into the container from Secrets.  |
| `env` / `envFrom`                     | `[]` / `[]`                    | Extra env vars on the container.                |
| `extraVolumes` / `extraVolumeMounts`  | `[]` / `[]`                    | Escape hatch for arbitrary volume mounts.       |
| `service.type`                        | `ClusterIP`                    |                                                 |
| `service.port`                        | `5137`                         | Service port mapped to `http`.                  |
| `service.statusPort`                  | `3925`                         | Service port for status/metrics (when enabled). |
| `ingress.enabled`                     | `false`                        |                                                 |
| `serviceMonitor.enabled`              | `false`                        | Requires Prometheus Operator.                   |
| `serviceAccount.create`               | `true`                         |                                                 |
| `serviceAccount.annotations`          | `{}`                           | e.g. GKE Workload Identity binding.             |
| `serviceAccount.automountServiceAccountToken` | `false`                | Forced on for the `k8s` coordination backend and in-cluster `kubernetes` providers. |
| `rbac.create`                         | `true`                         | Lease RBAC for the `k8s` backend; Job/pod RBAC in `runners.namespace` for in-cluster `kubernetes` providers. |
| `resources`                           | `{}`                           |                                                 |
| `livenessProbe.enabled`               | `true`                         |                                                 |
| `readinessProbe.enabled`              | `true`                         |                                                 |
| `nodeSelector` / `tolerations` / `affinity` | `{}` / `[]` / `{}`       |                                                 |
| `priorityClassName`                   | `""`                           |                                                 |
| `revisionHistoryLimit`                | `5`                            |                                                 |
| `terminationGracePeriodSeconds`       | `30`                           |                                                 |

## Testing

Template-level tests live in `tests/` and run with the
[helm-unittest](https://github.com/helm-unittest/helm-unittest) plugin
(`helm unittest charts/cattery`; plugin 1.x needs Helm 3.18 or newer), or
without installing anything:
`docker run --rm -v "$PWD/charts:/apps" helmunittest/helm-unittest cattery`.
CI also applies the rendered runner RBAC to a kind cluster and runs the
kubernetes provider's integration tests as that ServiceAccount.

## Upgrading

The Deployment defaults to `strategy: Recreate` with a single replica: each tray
type's poller holds a long-running GitHub session, so two overlapping pods would
double-poll. To run multiple replicas, set `config.coordination.backend` to
`mongo` or `k8s` — which leases each tray type's session, the workflow
restarter, and the stale tray cleanup to one replica at a time — and switch
`updateStrategy` to RollingUpdate. The tray API is served by
every replica regardless, so trays stay served throughout rollouts and
failovers. The `k8s` backend additionally needs `rbac.create` (default `true`)
for Lease access and mounts the service account token automatically.

Config changes are picked up automatically: the pod template carries a
checksum of the rendered `config` and restarts when it changes.
