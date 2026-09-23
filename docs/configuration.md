# Configuration

Cattery reads a YAML configuration file. Pass its path with `cattery server -c /path/to/config.yaml`; without `-c`, cattery looks for `config.yaml` first in `/etc/cattery/` and then in the working directory, and uses the first one found.

### Example:

```yaml
server:
  listenAddress: "0.0.0.0:5137"
  statusListenAddress: "0.0.0.0:5138"
  advertiseUrl: https://example.org

database:
  uri: mongodb://localhost:27017/
  database: cattery

stale:
  pollInterval: 1m
  thresholds:
    creating: 5m
    registering: 5m
    registered: 15m
    deleting: 15m

coordination:
  backend: memory

github:
  - name: my-org
    appId: 123456
    appClientId: Iv123abC
    installationId: 987654321
    privateKeyPath: my-app.private-key.pem

providers:
  - name: docker-local
    type: docker

  - name: gce-prod
    type: google
    project: my-gcp-project
    credentialsFile: my-gcp-creds.json

  - name: nomad-scw
    type: nomad
    address: https://nomad.internal:4646
    token: <nomad-acl-token>
    namespace: runners

  - name: k8s-prod
    type: kubernetes

trayTypes:
  - name: cattery-docker-local
    provider: docker-local
    shutdown: false
    runnerGroupId: 1
    githubOrg: my-org
    maxTrays: 3
    maxParallelCreation: 5
    config:
      image: cattery-runner-tiny:latest

  - name: cattery-gce-prod
    provider: gce-prod
    shutdown: true
    githubOrg: my-org
    runnerGroupId: 3
    maxTrays: 10
    extraMetadata:
      cattery-agent-version: 0.0.4
    config:
      zones:
        - europe-west1-c
        - europe-west1-d
      machineType: e2-standard-4
      instanceTemplate: global/instanceTemplates/cattery-default

  - name: cattery-nomad
    provider: nomad-scw
    githubOrg: my-org
    runnerGroupId: 3
    maxTrays: 5
    shutdown: true
    config:
      jobId: scw-cattery-runner-tray
      runnerFolder: /cattery
      script: |
        echo "extra setup for $TRAY_NAME"

  - name: cattery-k8s
    provider: k8s-prod
    githubOrg: my-org
    runnerGroupId: 3
    maxTrays: 20
    shutdown: false
    config:
      image: ghcr.io/actions/actions-runner:2.333.0
      resources:
        requests:
          cpu: "2"
          memory: 4Gi
```

### Config sections

#### server

| Key                  | Type   | Required | Description                                                                                                              |
|----------------------|--------|----------|--------------------------------------------------------------------------------------------------------------------------|
| listenAddress        | string | yes      | Host:port for the HTTP server to bind (e.g., 0.0.0.0:5137).                                                             |
| statusListenAddress  | string | no       | Separate host:port for the /status and /metrics endpoints. If empty or equal to listenAddress, served on the agent port. |
| advertiseUrl         | string | yes      | Public base URL where the server is reachable. Passed to agents.                                                         |
| agentSecret          | string | no       | Bearer token the server requires on every `/agent/*` request. **Leave empty:** the agent does not send this token yet, so setting it rejects every agent registration. |

#### database

| Key      | Type   | Required | Description                                                   |
|----------|--------|----------|---------------------------------------------------------------|
| uri      | string | yes      | MongoDB connection string (e.g., mongodb://localhost:27017/). |
| database | string | yes      | Database name (e.g., cattery).                                |

#### stale
Optional. Configures the cleanup loop that deletes trays stuck in a non-running status. A tray whose status has not changed for longer than its threshold is deleted (the provider resource is cleaned up and the record removed). `running` trays are never stale.

| Key          | Type                    | Required | Description                                                                                                                                     |
|--------------|-------------------------|----------|-------------------------------------------------------------------------------------------------------------------------------------------------|
| pollInterval | duration                | no       | How often stale trays are looked for. Default `1m`.                                                                                             |
| thresholds   | map[status]duration     | no       | Per-status age after which a tray is stale. Keys: `creating`, `registering`, `registered`, `deleting`. Defaults: `5m`, `5m`, `15m`, `15m`. Statuses missing from the map are not checked. |

Durations use Go syntax (`30s`, `5m`, `1h30m`). Independently of this loop, an agent whose tray has sat in `registered` (idle, no job) for 15 minutes is told to shut down on its next ping.

#### coordination
Optional. Selects the leader-election backend for running more than one server replica. Each tray type's scale set poller (the GitHub session), the workflow restarter and the stale tray cleanup each run on exactly one replica at a time; every replica serves the agent HTTP API regardless.

| Key                         | Type     | Required | Description                                                                                                                      |
|-----------------------------|----------|----------|----------------------------------------------------------------------------------------------------------------------------------|
| backend                     | enum     | no       | `memory` (default), `mongo` or `k8s`. `memory` always leads and is correct **only for a single replica**. `mongo` stores leases in the configured database. `k8s` uses `coordination.k8s.io` Leases and must run in-cluster with RBAC to get/create/update leases. |
| lease.ttl                   | duration | no       | Lease validity; bounds worst-case failover after a leader dies. Default `30s`. Ignored by `memory`.                              |
| lease.renewInterval         | duration | no       | How often a leader renews. Default `ttl/3`. Must stay well below `ttl`.                                                          |
| lease.retryInterval         | duration | no       | How often a non-leader retries acquisition. Default `5s`.                                                                        |
| kubernetes.namespace        | string   | no       | `k8s` only. Namespace for Lease objects. Defaults to the pod's namespace.                                                        |
| kubernetes.leaseNamePrefix  | string   | no       | `k8s` only. Prefix prepended to the sanitized lease key to form the Lease name.                                                  |

Lease keys are the tray type names plus `cattery-restarter` and `cattery-stale-trays`; do not name a tray type after those two.

#### github
A list of GitHub organizations/accounts the server manages via a GitHub App.

| Key            | Type   | Required | Description                                             |
|----------------|--------|----------|---------------------------------------------------------|
| name           | string | yes      | Name of the GitHub organization                         |
| appId          | int    | yes      | GitHub App ID                                           |
| appClientId    | string | yes      | GitHub App Client ID                                    |
| installationId | int    | yes      | Installation ID of that App in the organization/account |
| privateKeyPath | string | no       | Path to the App private key PEM on disk                 |

#### providers
Providers define how trays (runner machines) are provisioned. At least one provider is required.

Common fields for all providers:

| Key  | Type   | Required | Description                                         |
|------|--------|----------|-----------------------------------------------------|
| name | string | yes      | Provider name to reference from trayTypes.          |
| type | enum   | yes      | Provider type. Currently implemented: docker, google (GCE), nomad, kubernetes. |

Provider-specific fields:

- docker

  The docker provider has no extra fields.

- google (GCE)
  
  | Key             | Type   | Required | Description                                  |
  |-----------------|--------|----------|----------------------------------------------|
  | project         | string | yes      | GCP project ID                               |
  | credentialsFile | string | no       | Path to GCP service account JSON credentials. If omitted, uses Application Default Credentials. |

- nomad

  Cattery dispatches each tray as a child of a **parameterized parent job** that must already be registered in your Nomad cluster. The provider supplies `tray_name`, `bootstrap_token` and `cattery_url` as dispatch meta plus a generated bash payload that downloads and execs the cattery agent. Resources, driver and constraints come from the parent job spec — Nomad does not allow overriding them at dispatch time, so use distinct parameterized jobs for distinct resource shapes.

  | Key       | Type   | Required | Description                                                                                       |
  |-----------|--------|----------|---------------------------------------------------------------------------------------------------|
  | address   | string | yes      | Nomad agent HTTP(S) address, e.g. `https://nomad.internal:4646`.                                  |
  | token     | string | no       | Nomad ACL token. Needs `dispatch-job` (StartDeploy), `read-job` (WaitDeploy reads the evaluation), `list-jobs` (CleanTray's leaked-child recovery scan), and `deregister-job`/`purge-job` (CleanTray purges the dispatched child) on the parent job's namespace. See [Nomad ACL policies](https://developer.hashicorp.com/nomad/docs/secure/acl/policies) for the exact capability names in your Nomad version. |
  | namespace | string | no       | Nomad namespace to dispatch into. Defaults to `default`.                                          |
  | region    | string | no       | Nomad region. Defaults to the agent's region.                                                     |
  | tlsCaFile | string | no       | Path to a PEM CA bundle for verifying the Nomad agent's TLS certificate.                          |
  | insecure  | bool   | no       | Skip TLS verification. Dev-only. Accepts `true`/`false`, quoted or not.                          |

- kubernetes

  Cattery runs each tray as a **batch/v1 Job** in a Kubernetes cluster: the one it runs in or any other. The Job's pod runs the cattery agent, which an init container injects, so stock runner images such as `ghcr.io/actions/actions-runner` work unmodified. The pod shape is described per tray type (see the kubernetes config section below). All keys are optional.

  Cluster access is one of three modes:

  - **In-cluster** (nothing set): the pod's service account. Outside a cluster, e.g. on a dev box, the ambient kubeconfig (`KUBECONFIG` or `~/.kube/config`) is used instead.
  - **Kubeconfig** (`kubeconfig` and/or `context`): a kubeconfig file, for example one mounted from a Secret.
  - **Server** (`server` plus `token`/`tokenFile` and `caFile`/`insecure`): a target cluster's API server with a bearer token, typically a service account token from that cluster. Use it when a kubeconfig would need a cloud auth plugin the cattery image does not ship.

  | Key           | Type     | Required | Description                                                                                       |
  |---------------|----------|----------|---------------------------------------------------------------------------------------------------|
  | kubeconfig    | string   | no       | Path to a kubeconfig file.                                                                        |
  | context       | string   | no       | Kubeconfig context to use (with `kubeconfig`, or with the ambient kubeconfig).                    |
  | server        | string   | no       | API server URL of the target cluster, e.g. `https://10.0.0.1:6443`. Mutually exclusive with `kubeconfig`/`context`. |
  | tokenFile     | string   | no       | `server` only. Path to a file holding the bearer token; re-read by the client, so rotated tokens keep working. |
  | token         | string   | no       | `server` only. Inline bearer token; prefer `tokenFile`.                                           |
  | caFile        | string   | no       | `server` only. PEM bundle to verify the API server's certificate.                                 |
  | insecure      | bool     | no       | `server` only. Skip TLS verification. Dev-only. Mutually exclusive with `caFile`.                 |
  | namespace     | string   | no       | Namespace the Jobs are created in. Defaults to the server's own namespace in-cluster, to the context's namespace with a kubeconfig, and to `default` with `server`. Tray types can override it. |
  | deployTimeout | duration | no       | How long cattery waits for a tray's pod to be `Running` before giving up on the tray. Default `5m`. Keep it at or below `stale.thresholds.creating`. |

  **RBAC.** In every namespace trays are created in, the identity cattery uses (its service account, the kubeconfig user, or the token's service account) needs `create`, `get` and `delete` on `jobs.batch`, `get`, `list` and `watch` on `pods`, and `get` on `podtemplates` when tray types use `podTemplateRef`. The Helm chart provisions this, and a ServiceAccount for the runner pods, for in-cluster providers in its `runners.namespace`.

#### trayTypes
Defines one or more tray "profiles" that the Tray Manager can maintain.

| Key                 | Type               | Required | Description                                                                    |
|---------------------|--------------------|----------|--------------------------------------------------------------------------------|
| name                | string             | yes      | Unique name for the tray type. Also used as the runner scale set name/label.   |
| description         | string             | no       | Free-text description shown on the status page (Tray Types tab). Use it to document what the type provides: machine size, image, intended workloads. |
| provider            | string             | yes      | Name of a provider defined in `providers`.                                     |
| runnerGroupId       | int                | yes      | GitHub Runner Group ID to register runners into.                               |
| githubOrg           | string             | yes      | The GitHub org key, matching one of the entries under `github`.                |
| shutdown            | bool               | no       | Whether instances should self-terminate when the job completes.                |
| maxTrays            | int                | yes      | Maximum number of concurrent trays of this type. Also the scale set's runner capacity reported to GitHub. Must be greater than 0: with the default of 0 no trays are ever created. |
| maxParallelCreation | int                | no       | Maximum number of trays to create in parallel. Defaults to 10.                 |
| extraMetadata       | map[string]string  | no       | Extra key-value metadata passed to the provider (GCE instance metadata, Nomad dispatch meta; ignored by docker). Keys are lowercased when the file is read. |
| config              | provider-dependent | yes      | Provider-specific configuration for how to create a tray (see below).          |

Provider-specific config under trayType.config:

- docker config

  Containers are named after the tray id and started as `<image> /action-runner/cattery/cattery agent -i <tray-id> -s <advertiseUrl> --runner-folder /action-runner` with `host.docker.internal` mapped to the host, so the image must contain the cattery binary and the Actions runner at those paths (see [examples/Dockerfile-cattery-tiny](../examples/Dockerfile-cattery-tiny)).

  | Key        | Type   | Required | Description                                                                 |
  |------------|--------|----------|-----------------------------------------------------------------------------|
  | image      | string | yes      | Docker image to run for the agent/runner (e.g., cattery-runner-tiny:latest) |

- google (GCE) config

  Instances are named after the tray id and receive `cattery-url` (the `advertiseUrl`) and `cattery-agent-id` (the tray id) as instance metadata, plus any `extraMetadata`. The GCP project comes from the provider.

  | Key              | Type     | Required | Description                                                                     |
  |------------------|----------|----------|---------------------------------------------------------------------------------|
  | zones            | []string | yes      | List of zones to create instances in (e.g. `europe-west1-c`); one is picked at random per tray |
  | machineType      | string   | yes      | Instance machine type (e.g. `e2-standard-4`)                                    |
  | instanceTemplate | string   | yes      | Template to base instances on (e.g. `global/instanceTemplates/cattery-default`) |

- nomad config

  | Key          | Type   | Required | Description                                                                                          |
  |--------------|--------|----------|------------------------------------------------------------------------------------------------------|
  | jobId        | string | yes      | ID of a parameterized parent job already registered in Nomad. Cattery dispatches one child per tray. |
  | runnerFolder | string | no       | Path inside the guest where the GitHub Actions runner distribution lives. Passed as `--runner-folder` to `cattery agent`. Defaults to `/cattery`. |
  | script       | string | no       | Inline bash, executed after the agent binary is downloaded and before the agent is exec'd. Use YAML's `\|` block scalar for multi-line. |

  **Bootstrap composition.** The provider builds the dispatched payload from three pieces:

  1. A fixed prelude that downloads the cattery agent binary from `$CATTERY_URL/agent/download` (the server serves its own executable) to `/usr/local/bin/cattery`.
  2. The optional `script` field, executed as a pre-agent hook.
  3. An `exec /usr/local/bin/cattery agent -i "$TRAY_NAME" -s "$CATTERY_URL" --runner-folder <runnerFolder>`, where `<runnerFolder>` defaults to `/cattery`.

  To take over the agent invocation entirely (e.g. when the image starts the agent itself via systemd), put your own `exec ...` at the end of `script` — the default exec emitted afterwards becomes unreachable.

  **Parent-job contract.** The parameterized parent job must declare the `parameterized` stanza at the job level *and* materialize the dispatched payload at the task level via `dispatch_payload`. Without `dispatch_payload`, Nomad accepts the dispatch but never writes the payload bytes anywhere the task can read.

  ```hcl
  job "my-runner-tray" {
    type = "batch"

    parameterized {
      payload       = "required"
      meta_required = ["tray_name", "bootstrap_token", "cattery_url"]
    }

    group "g" {
      task "t" {
        // Nomad writes the dispatched payload to ${NOMAD_TASK_DIR}/bootstrap.sh
        // before the task starts.
        dispatch_payload {
          file = "bootstrap.sh"
        }

        // ... driver, config, resources ...
      }
    }
  }
  ```

  The dispatched bytes land at `${NOMAD_TASK_DIR}/bootstrap.sh`. Your task is responsible for executing that file *with the dispatch meta values exported as env vars* — the script generated by cattery references `$CATTERY_URL`, `$TRAY_NAME` and `$BOOTSTRAP_TOKEN`. Two common ways to wire that up:

  - For raw_exec / exec drivers running directly on the host: source a small env file and exec the payload, e.g.
    ```
    set -a; . /etc/cattery/bootstrap.env; set +a
    bash "$NOMAD_TASK_DIR/bootstrap.sh"
    ```
  - For VM-style drivers (qemu, firecracker, custom `nomad-runner-vm` wrappers): render a cloud-init userdata template that uses `write_files` to drop the meta values into an env file (e.g. `/etc/cattery/bootstrap.env`) and a `runcmd` that sources it before exec'ing the dispatched payload. The wrapper bakes the rendered userdata into the guest's cidata seed iso.

  Either approach must produce an environment where `TRAY_NAME`, `BOOTSTRAP_TOKEN` and `CATTERY_URL` are exported when the payload script runs.

  **Lifecycle.**

  - Cattery dispatches the parent job with `idPrefixTemplate = tray.Id` and `IdempotencyToken = tray.Id`, and stores `dispatchedJobId` + `evalId` + `parentJobId` in the tray's provider data. The provider stages `parentJobId` and `namespace` in memory before the dispatch call; the trayManager persists provider data once `StartDeploy` returns (success path) or right before cleanup (error path). This recovers the case where Dispatch creates the child but the HTTP response is lost — it does *not* recover a process crash mid-dispatch (parentJobId never reaches the database in that window).
  - Cattery blocks until the dispatch evaluation leaves `pending`. `complete` → success; `blocked` → returned as `ErrCapacityBlocked` (Nomad has no capacity for this alloc); `failed`/`canceled` → error.
  - On tray cleanup, the dispatched child job is deregistered with `purge=true`. If `dispatchedJobId` is missing (e.g., the dispatch response was lost in transit), cattery lists the parent's dispatched children with the prefix `<parentJobId>/dispatch-` and deregisters any whose ID starts with `<parentJobId>/dispatch-<trayId>-` (the shape Nomad assigns when `idPrefixTemplate = tray.Id`).

  **Resource shapes.** Resources, driver, constraints and reschedule policy are baked into the parent job spec — they cannot be set per-dispatch. To run trays at different sizes, register multiple parameterized parent jobs and reference them by `jobId` from different trayTypes.

  **`extraMetadata` and Nomad meta.** Any keys in the trayType's `extraMetadata` are forwarded as Nomad dispatch meta alongside `tray_name` / `bootstrap_token` / `cattery_url`. The provider-owned keys are written *last* and cannot be clobbered by `extraMetadata`. Nomad rejects dispatch meta keys that are not declared in the parent job's `meta_required` or `meta_optional`, so any keys you add via `extraMetadata` must also be declared `meta_optional` in the parameterized parent job.

- kubernetes config

  Each tray is a Job named after the tray id with `backoffLimit: 0`, `podReplacementPolicy: Failed` and `restartPolicy: Never`: a pod that fails or is evicted is never replaced, because a second agent registering under the same tray id would break the one-tray-one-job model (jobs lost that way are re-run by the workflow restarter on a new tray). `ttlSecondsAfterFinished` lets Kubernetes garbage-collect finished Jobs that cattery failed to delete.

  The pod shape comes from one of two mutually exclusive sources: the typed fields below (`image` required), or `podTemplateRef`, the name of a `core/v1` PodTemplate object in the tray namespace whose `template` is used as-is (labels, annotations and spec). With `podTemplateRef` every typed pod-shape field is ignored (a warning is logged at start-up). Use a PodTemplate for anything the typed fields cannot express — sidecars (e.g. Docker-in-Docker for jobs that use `container:` or `services:`), volumes, affinity, security contexts, runtime classes — and whenever the case of map keys matters: cattery's config loader lowercases every map key in this file (`nodeSelector`, `labels`, `annotations`, `resources`), while a PodTemplate object is stored exactly as written. The Helm chart renders PodTemplates from its `runnerPodTemplates` value.

  | Key                          | Type     | Required      | Description                                                                                        |
  |------------------------------|----------|---------------|----------------------------------------------------------------------------------------------------|
  | agentVersion                 | string   | no            | How the agent binary gets into the pod. `server` (default): an init container downloads it from `<advertiseUrl>/agent/download`, so the agent always matches the running server. Any other value is a tag of `docker.io/paritytech/cattery` whose `/usr/local/bin/cattery` the init container copies (e.g. `0.2.0`, `latest`). |
  | agentImage                   | string   | no            | Init container image override. Server mode needs `sh`, `wget` and CA certificates (default `docker.io/paritytech/cattery:latest`); tag mode needs `/usr/local/bin/cattery` and `cp` (default `docker.io/paritytech/cattery:<agentVersion>`). Builds from `main` are published as `docker.io/paritypr/cattery:<commit sha>`; pin those here. |
  | runnerFolder                 | string   | no            | Where `<folder>/bin/Runner.Listener` lives in the runner image; also the runner container's working directory unless the template sets one. Default `/home/runner` (the `ghcr.io/actions/actions-runner` layout). |
  | runnerContainer              | string   | no            | `podTemplateRef` only: the container that runs the agent when the template has several. Default `runner`; a template with exactly one container always uses that one. |
  | namespace                    | string   | no            | Overrides the provider namespace for this tray type. Needs RBAC (and PodTemplates) in that namespace. |
  | ttlSecondsAfterFinished      | int      | no            | Job TTL after it finishes. Default `3600`; `0` deletes immediately.                               |
  | activeDeadlineSeconds        | int      | no            | Hard bound on the whole tray lifetime, idle time included; a job still running when it expires is killed. Unset (`0`) by default. |
  | image                        | string   | typed mode    | Runner image. Must contain the Actions runner under `runnerFolder` and `pkill` (procps), which the agent uses to stop the runner. |
  | imagePullPolicy              | string   | no            | `Always`, `IfNotPresent` or `Never`.                                                              |
  | imagePullSecrets             | []string | no            | Names of image pull secrets in the tray namespace.                                                |
  | serviceAccountName           | string   | no            | Service account of the runner pod.                                                                |
  | automountServiceAccountToken | bool     | no            | Default `false`: jobs run third-party code and get no API token unless asked for.                 |
  | resources                    | object   | no            | `requests` / `limits` maps of resource name to quantity. Quantities are strings in Kubernetes syntax: `cpu: "2"`, `cpu: 500m`, `memory: 4Gi`. |
  | nodeSelector                 | map      | no            | Node selector.                                                                                    |
  | tolerations                  | list     | no            | Tolerations with `key`, `operator` (`Exists` or `Equal`), `value`, `effect` (`NoSchedule`, `PreferNoSchedule` or `NoExecute`) and `tolerationSeconds`. |
  | labels, annotations          | map      | no            | Extra pod labels and annotations. Cattery's own labels win on conflict.                          |
  | env                          | list     | no            | Literal env vars for the runner container as `name` / `value` entries; quote values that look like numbers or booleans (`value: "1"`). Values from Secrets or ConfigMaps need a PodTemplate. |
  | runtimeClassName             | string   | no            | Runtime class, e.g. `gvisor`.                                                                     |
  | priorityClassName            | string   | no            | Pod priority class.                                                                               |
  | terminationGracePeriodSeconds | int     | no            | Grace period on pod deletion. Leave the agent a few seconds to unregister.                        |
  | podTemplateRef               | string   | template mode | Name of a `core/v1` PodTemplate in the tray namespace.                                            |

  **What cattery adds to the pod.** An `emptyDir` volume `cattery-agent` mounted at `/cattery-agent`; an init container `cattery-agent` that stages the binary there; and, on the runner container, `command: ["/cattery-agent/cattery"]`, `args: ["agent", "-i", <tray id>, "-s", <advertiseUrl>, "--runner-folder", <runnerFolder>]`, the env vars `CATTERY_URL` and `CATTERY_AGENT_ID`, the volume mount and, when unset, `workingDir`. The labels `app.kubernetes.io/managed-by=cattery`, `app.kubernetes.io/component=runner`, `cattery.io/tray-id`, `cattery.io/tray-type` and `cattery.io/provider` go on the Job and its pod (`kubectl get pods -l cattery.io/tray-type=<name>`). Typed-mode pods also get `enableServiceLinks: false`.

  **Lifecycle.** `namespace` and `jobName` are stored in the tray's provider data before the Job is created, `jobUid` afterwards. Cattery then watches the pod: `Running` is success (agent registration is the readiness signal from there); an image pull or container configuration error, a `Failed` or `Succeeded` pod and a deleted pod fail the tray immediately; otherwise cattery gives up after `deployTimeout` and reports the last obstacle (typically the scheduler's `0/N nodes are available` message). A failed tray is deleted and recreated on the next demand signal. On cleanup the Job is deleted with background propagation, which removes its pod too; a Job that is already gone is not an error. Deleting the pod sends the agent SIGTERM, so it unregisters with the SigTerm reason and the workflow restarter re-runs the job on a new tray.

  **Notes.**
  - The tray type name becomes the Job name: it must be a DNS-1123 label of at most 40 characters, so the generated pod name fits in 63.
  - `extraMetadata` is ignored by this provider; use `env`, `labels` or `annotations`.
  - `shutdown` has no effect inside a container; set it to `false`.
  - In server mode the init container downloads with busybox `wget`, which does not verify TLS certificates. Pin `agentVersion` (or `agentImage`) where that matters; note that a `latest` tag also implies `imagePullPolicy: Always` on the init container.
  - The config is validated at start-up (name, mode, pull policy, quantities, tolerations, env names, durations); a broken tray type refuses to load instead of failing every tray it creates. Like the other providers, unknown keys are ignored.


Notes:
- Ensure runnerGroupId corresponds to an existing Runner Group in your GitHub org and that your GitHub App has permission to register runners.
  To find the runner group id go to org Settings -> Actions -> Runner Groups -> your runner group, the id will be in the page URL: `https://github.com/organizations/<org_name>/settings/actions/runner-groups/<group_id>`
- Ensure that the repository/workflow has access to the runner group (runner group repository access).
