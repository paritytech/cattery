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
  Actions read/write permissions, installed on your organization
- Credentials for at least one provider (Docker socket access, or a GCP
  service account for the `google` provider)

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

```bash
helm install cattery ./charts/cattery -f values.yaml
```

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

Webhook secrets are handled the same way: inline `webhookSecret` on a
`config.github[*]` entry renders a Secret, or reference an existing one via
the top-level `webhookSecrets` map.

### Status port

`config.server.statusListenAddress` can be set to serve `/status` and
`/metrics` on a separate port. When set, the chart opens a second Service
port and the `ServiceMonitor` scrapes that port. Probes always target
whichever port `/status` is on.

### ServiceMonitor

Enable with `serviceMonitor.enabled: true` (requires the Prometheus Operator
CRDs installed in the cluster).

## Values

| Key                                   | Default                        | Description                                     |
| ------------------------------------- | ------------------------------ | ----------------------------------------------- |
| `replicaCount`                        | `1`                            | Runs as a singleton; don't raise without care.  |
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
| `secretFiles`                         | `{}`                           | Files mounted into the container from Secrets.  |
| `webhookSecrets`                      | `{}`                           | References to existing webhook Secrets.         |
| `service.type`                        | `ClusterIP`                    |                                                 |
| `service.port`                        | `5137`                         | Service port mapped to `http`.                  |
| `service.statusPort`                  | `3925`                         | Service port for status/metrics (when enabled). |
| `ingress.enabled`                     | `false`                        |                                                 |
| `serviceMonitor.enabled`              | `false`                        | Requires Prometheus Operator.                   |
| `serviceAccount.create`               | `true`                         |                                                 |
| `serviceAccount.annotations`          | `{}`                           | e.g. GKE Workload Identity binding.             |
| `resources`                           | `{}`                           |                                                 |
| `livenessProbe.enabled`               | `true`                         |                                                 |
| `readinessProbe.enabled`              | `true`                         |                                                 |
| `nodeSelector` / `tolerations` / `affinity` | `{}` / `[]` / `{}`       |                                                 |

## Upgrading

The Deployment uses `strategy: Recreate` — cattery is a singleton and the
pod holds long-running leases against GitHub, so rolling updates would
produce a double-poll window.

Config changes are picked up automatically: the pod template carries a
checksum of the rendered `config` and restarts when it changes.
