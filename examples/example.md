### Local docker

A minimal end-to-end setup: cattery server on your machine, MongoDB in docker
compose, and runners as local docker containers.

1. Start MongoDB (single-node replica set):

   ```bash
   docker compose -f examples/docker-compose.yaml up -d
   ```

2. Build the runner image. The build context is the repository root, because
   the image compiles the cattery agent from `src/`:

   ```bash
   docker build -t cattery-runner-tiny:latest -f examples/Dockerfile-cattery-tiny .
   ```

3. Create `config.yaml`. The docker provider starts each container with
   `--add-host=host.docker.internal:host-gateway`, so `advertiseUrl` must point
   at that name for the agent inside the container to reach the server:

   ```yaml
   server:
     listenAddress: "0.0.0.0:5137"
     advertiseUrl: http://host.docker.internal:5137

   database:
     uri: mongodb://localhost:27017/
     database: cattery

   github:
     - name: my-org
       appId: 123456
       appClientId: Iv123abC
       installationId: 987654321
       privateKeyPath: /path/to/private-key.pem

   providers:
     - name: docker-local
       type: docker

   trayTypes:
     - name: cattery-tiny
       provider: docker-local
       githubOrg: my-org
       runnerGroupId: 1
       maxTrays: 2
       config:
         image: cattery-runner-tiny:latest
   ```

4. Run the server:

   ```bash
   cd src && go build -o cattery && ./cattery server -c ../config.yaml
   ```

5. Queue a job in a workflow that targets the runner group with
   `runs-on: cattery-tiny` (the tray type name is the scale set name). The
   server picks the job up through the scale set API, starts a container, the
   agent registers and runs the job, and the container is removed when the
   job completes. Progress is visible on `http://localhost:5137/status`.
