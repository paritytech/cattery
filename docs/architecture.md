# Architecture

Cattery is composed of a control-plane (server) and data-plane (providers and agents). It integrates with GitHub via the Actions Runner Scale Set SDK and a GitHub App, and persists state in MongoDB.

Main components (code pointers included):
- Server
  - Entry points: src/main.go, src/cmd/server.go, src/server/server.go
  - HTTP handlers: src/server/handlers
    - Root: src/server/handlers/rootHandler.go
    - Agent API: src/server/handlers/agentHandler.go
  - Metrics: Prometheus endpoint at /metrics
- Scale Set Integration
  - Scale Set Client: src/lib/scaleSetClient/scaleSetClient.go (session management, polling, JIT runner config)
  - Scale Set Poller: src/lib/scaleSetPoller/poller.go (long-polls GitHub for job demand and lifecycle events)
  - Poller Manager: src/lib/scaleSetPoller/manager.go (manages pollers per tray type)
  - Uses the github.com/actions/scaleset SDK and its listener package
- Configuration
  - Types and loading: src/lib/config/config.go
  - Examples: bin/config.yaml, examples/example-config.yaml
- GitHub Integration
  - Client wrapper: src/lib/githubClient/githubClient.go
  - Requires GitHub App credentials (appClientId, installationId, privateKeyPath)
- Tray Management
  - Domain model: src/lib/trays/tray.go, trayStatus.go
  - Repository: src/lib/trays/repositories (MongoDB implementation)
  - Provider interface: src/lib/trays/providers/iTrayProvider.go
  - Providers: src/lib/trays/providers/dockerProvider.go, gceProvider.go
  - Provider factory: src/lib/trays/providers/trayProviderFactory.go
  - Tray Manager: src/lib/trayManager/trayManager.go
- Agents
  - Agent binary: src/agent/agent.go, src/cmd/agent.go
  - Cattery client: src/agent/catteryClient/
  - GitHub listener: src/agent/githubListener/
  - Tools: src/agent/tools/shutdown.go (+ platform-specific)
- Restarter
  - Workflow restarts: src/lib/restarter/*.go
- Data stores
  - MongoDB repositories for trays and restarter

Data flow (simplified):
1) Server starts, loads configuration, and connects to MongoDB.
2) For each configured tray type, a Scale Set Poller is started. It creates a message session with GitHub and long-polls for events.
3) When GitHub signals job demand (desired runner count), the Tray Manager scales up by provisioning trays via the appropriate provider.
4) Providers create infrastructure resources (Docker containers, GCE instances) and start the agent with a JIT runner configuration.
5) Agent registers with the Cattery server and manages the runner process on the provisioned machine.
6) When a job completes, the poller receives a job-completed event and the tray is deleted.
7) On shutdown, pollers close their sessions with GitHub and wait for cleanup before the process exits.

Extensibility:
- Add providers by implementing iTrayProvider and registering in trayProviderFactory.go.
- Extend repositories by adding new interfaces and MongoDB implementations.
- Add handlers or background workers as needed in the server package.
