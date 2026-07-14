# Configuration

Cattery uses YAML configuration files and searches for the file named config.yaml first in the working directory and then in the /etc/cattery directory.
Cattery will use the first config file found.

### Example:

```yaml
server:
  listenAddress: "0.0.0.0:5137"
  statusListenAddress: "0.0.0.0:5138"
  advertiseUrl: https://example.org
  agentSecret: my-secret-token

database:
  uri: mongodb://localhost:27017/
  database: cattery

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
      namePrefix: cattery

  - name: cattery-gce-prod
    provider: gce-prod
    shutdown: true
    githubOrg: my-org
    runnerGroupId: 3
    maxTrays: 10
    extraMetadata:
      cattery-agent-version: 0.0.4
    config:
      project: my-gcp-project
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
```

### Config sections

#### server

| Key                  | Type   | Required | Description                                                                                                              |
|----------------------|--------|----------|--------------------------------------------------------------------------------------------------------------------------|
| listenAddress        | string | yes      | Host:port for the HTTP server to bind (e.g., 0.0.0.0:5137).                                                             |
| statusListenAddress  | string | no       | Separate host:port for the /status and /metrics endpoints. If empty or equal to listenAddress, served on the agent port. |
| advertiseUrl         | string | yes      | Public base URL where the server is reachable. Passed to agents.                                                         |
| agentSecret          | string | no       | Bearer token that agents must present to register/unregister. If empty, agent auth is disabled.                          |

#### database

| Key      | Type   | Required | Description                                                   |
|----------|--------|----------|---------------------------------------------------------------|
| uri      | string | yes      | MongoDB connection string (e.g., mongodb://localhost:27017/). |
| database | string | yes      | Database name (e.g., cattery).                                |

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
| type | enum   | yes      | Provider type. Currently implemented: docker, google (GCE), nomad. |

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
  | insecure  | bool   | no       | Skip TLS verification. Dev-only.                                                                  |

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
| maxTrays            | int                | no       | Maximum number of concurrent trays of this type.                               |
| maxParallelCreation | int                | no       | Maximum number of trays to create in parallel. Defaults to 10.                 |
| extraMetadata       | map[string]string  | no       | Extra key-value metadata passed to the provider (e.g., GCE instance metadata). |
| config              | provider-dependent | yes      | Provider-specific configuration for how to create a tray (see below).          |

Provider-specific config under trayType.config:

- docker config
  
  | Key        | Type   | Required | Description                                                                 |
  |------------|--------|----------|-----------------------------------------------------------------------------|
  | image      | string | yes      | Docker image to run for the agent/runner (e.g., cattery-runner-tiny:latest) |
  | namePrefix | string | no       | Prefix for container names                                                  |

- google (GCE) config
  
  | Key              | Type     | Required | Description                                                                     |
  |------------------|----------|----------|---------------------------------------------------------------------------------|
  | project          | string   | no       | GCP project ID (can also be set at provider level)                              |
  | zones            | []string | yes      | List of zones to create instances in (e.g. `europe-west1-c`)                    |
  | machineType      | string   | yes      | Instance machine type (e.g. `e2-standard-4`)                                    |
  | instanceTemplate | string   | yes      | Template to base instances on (e.g. `global/instanceTemplates/cattery-default`) |
  | namePrefix       | string   | no       | Prefix for VM names                                                             |

- nomad config

  | Key          | Type   | Required | Description                                                                                          |
  |--------------|--------|----------|------------------------------------------------------------------------------------------------------|
  | jobId        | string | yes      | ID of a parameterized parent job already registered in Nomad. Cattery dispatches one child per tray. |
  | runnerFolder | string | no       | Path inside the guest where the GitHub Actions runner distribution lives. Passed as `--runner-folder` to `cattery agent`. Defaults to `/cattery`. |
  | script       | string | no       | Inline bash, executed after the agent binary is downloaded and before the agent is exec'd. Use YAML's `\|` block scalar for multi-line. |

  **Bootstrap composition.** The provider builds the dispatched payload from three pieces:

  1. A fixed prelude that downloads the cattery agent binary from `$CATTERY_URL/agent/binary` to `/usr/local/bin/cattery`.
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


Notes:
- Ensure runnerGroupId corresponds to an existing Runner Group in your GitHub org and that your GitHub App has permission to register runners.
  To find the runner group id go to org Settings -> Actions -> Runner Groups -> your runner group, the id will be in the page URL: `https://github.com/organizations/<org_name>/settings/actions/runner-groups/<group_id>`
- Ensure that the repository/workflow has access to the runner group (runner group repository access).
