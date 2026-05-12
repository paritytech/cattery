package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/nomad/api"
	"github.com/sirupsen/logrus"
)

// ErrCapacityBlocked indicates Nomad accepted the dispatch but cannot place
// the alloc due to insufficient capacity or unsatisfied constraints. The eval
// remains queued; a future fallback provider will use this sentinel to
// reroute to another provider.
var ErrCapacityBlocked = errors.New("nomad: dispatch blocked, no capacity")

const (
	nomadProviderDataDispatchedJobID = "dispatchedJobId"
	nomadProviderDataEvalID          = "evalId"
	nomadProviderDataNamespace       = "namespace"
	nomadProviderDataParentJobID     = "parentJobId"
)

// defaultRunnerFolder is used when NomadTrayConfig.RunnerFolder is empty.
// It is the path inside the guest where the GitHub Actions runner
// distribution is expected to live and is passed as `--runner-folder` to
// `cattery agent` (which is required by the agent CLI). To take over the
// agent invocation (e.g. when the image starts the agent itself), put your
// own `exec ...` at the end of NomadTrayConfig.Script — the default exec
// emitted afterwards becomes unreachable.
const defaultRunnerFolder = "/cattery"

// The provider synthesizes the dispatched payload from three pieces:
//
//  1. A fixed prelude that downloads the cattery agent binary.
//  2. The user's optional Script, executed as a pre-agent hook.
//  3. An exec of `cattery agent ... --runner-folder <RunnerFolder>`, using
//     defaultRunnerFolder when RunnerFolder is empty.
//
// The script assumes meta values TRAY_NAME, BOOTSTRAP_TOKEN, CATTERY_URL are
// exported in the environment. The parameterized parent job is responsible
// for sourcing them from /etc/cattery/bootstrap.env (or equivalent) before
// exec'ing this script — see the "Parent-job contract" section in
// docs/configuration.md for the wiring patterns.

type NomadProvider struct {
	name string

	client    *api.Client
	namespace string

	logger *logrus.Entry
}

func NewNomadProvider(name string, providerConfig config.ProviderConfig) *NomadProvider {
	logger := logrus.WithFields(logrus.Fields{
		"name":         "NomadProvider",
		"providerName": name,
		"providerType": "nomad",
	})

	address := providerConfig.Get("address")
	if address == "" {
		logger.Error("nomad provider missing required 'address'")
		return nil
	}

	cfg := api.DefaultConfig()
	cfg.Address = address
	if region := providerConfig.Get("region"); region != "" {
		cfg.Region = region
	}
	if ns := providerConfig.Get("namespace"); ns != "" {
		cfg.Namespace = ns
	}
	if token := providerConfig.Get("token"); token != "" {
		cfg.SecretID = token
	}
	if caFile := providerConfig.Get("tlscafile"); caFile != "" {
		if cfg.TLSConfig == nil {
			cfg.TLSConfig = &api.TLSConfig{}
		}
		cfg.TLSConfig.CACert = caFile
	}
	if strings.EqualFold(providerConfig.Get("insecure"), "true") {
		if cfg.TLSConfig == nil {
			cfg.TLSConfig = &api.TLSConfig{}
		}
		cfg.TLSConfig.Insecure = true
	}

	client, err := api.NewClient(cfg)
	if err != nil {
		logger.Errorf("failed to create nomad client: %v", err)
		return nil
	}

	return &NomadProvider{
		name:      name,
		client:    client,
		namespace: cfg.Namespace,
		logger:    logger,
	}
}

func (n *NomadProvider) GetProviderName() string {
	return n.name
}

// StartDeploy submits a parameterized-job dispatch to Nomad.
//
// ProviderData ordering matters for cleanup recovery:
//
//   - parentJobId and namespace are staged on tray.ProviderData *before* the
//     Dispatch call, so that when trayManager persists ProviderData (after
//     StartDeploy returns, on either the success or error path) those keys
//     are durable. CleanTray uses them to scan for leaked children when
//     dispatchedJobId is missing — recovering the case where Dispatch
//     created the child but the HTTP response was lost. This does NOT
//     recover a process crash *during* the in-flight Dispatch, since
//     ProviderData hasn't been persisted yet at that point.
//   - dispatchedJobId and evalId are written from the Dispatch response.
//
// To make this safe under retry, the dispatch sets:
//
//   - idPrefixTemplate = tray.Id, so the child job's ID has the shape
//     "<parent>/dispatch-<tray.Id>-<timestamp>-<uuid>" and can be located
//     by prefix scan.
//   - IdempotencyToken = tray.Id, so a retried Dispatch with the same token
//     does not create a duplicate child.
func (n *NomadProvider) StartDeploy(ctx context.Context, tray *trays.Tray) error {
	trayConfig, ok := tray.TrayConfig().(config.NomadTrayConfig)
	if !ok {
		return fmt.Errorf("unexpected tray config type for nomad provider, tray %s", tray.Id)
	}
	if trayConfig.JobId == "" {
		return fmt.Errorf("nomad tray config missing jobId, tray %s", tray.Id)
	}

	// bootstrapToken is forwarded as a meta value and surfaces inside the
	// guest as $BOOTSTRAP_TOKEN. It is not validated by the cattery server
	// today; the field is plumbed through so a future change can adopt
	// per-dispatch token validation without touching the parent-job contract.
	bootstrapToken, err := generateBootstrapToken()
	if err != nil {
		return fmt.Errorf("failed to generate bootstrap token: %w", err)
	}

	payload := buildBootstrapPayload(trayConfig.Script, trayConfig.RunnerFolder)

	// Provider-owned keys are written *last* so that user-supplied
	// extraMetadata cannot accidentally clobber the bootstrap contract.
	meta := map[string]string{}
	if tt := tray.TrayType(); tt != nil {
		for k, v := range tt.ExtraMetadata {
			meta[k] = v
		}
	}
	meta["tray_name"] = tray.Id
	meta["bootstrap_token"] = bootstrapToken
	meta["cattery_url"] = config.Get().Server.AdvertiseUrl

	// Staged on tray.ProviderData before the Dispatch call so that when
	// trayManager persists ProviderData after StartDeploy returns, cleanup
	// can recover a leaked child via the parent-job prefix scan. See the
	// StartDeploy doc comment above for the recovery model and its limits.
	tray.ProviderData[nomadProviderDataNamespace] = n.namespace
	tray.ProviderData[nomadProviderDataParentJobID] = trayConfig.JobId

	resp, _, err := n.client.Jobs().Dispatch(
		trayConfig.JobId,
		meta,
		payload,
		tray.Id,
		(&api.WriteOptions{
			Namespace:        n.namespace,
			IdempotencyToken: tray.Id,
		}).WithContext(ctx),
	)
	if err != nil {
		n.logger.Errorf("Failed to dispatch nomad job %s for tray %s: %v", trayConfig.JobId, tray.Id, err)
		return err
	}

	tray.ProviderData[nomadProviderDataDispatchedJobID] = resp.DispatchedJobID
	tray.ProviderData[nomadProviderDataEvalID] = resp.EvalID

	n.logger.Infof("Dispatched nomad job %s for tray %s (dispatchedJobId=%s, evalId=%s)",
		trayConfig.JobId, tray.Id, resp.DispatchedJobID, resp.EvalID)

	return nil
}

// WaitDeploy blocks until the dispatch evaluation leaves the `pending` state.
// Mapping:
//
//   - complete:           nil (Nomad scheduled the alloc; agent registration
//     is the readiness signal from here)
//   - blocked:            ErrCapacityBlocked
//   - failed/canceled:    plain error
//   - ctx cancellation:   ctx error (caller-imposed timeout)
func (n *NomadProvider) WaitDeploy(ctx context.Context, tray *trays.Tray) error {
	evalID := tray.ProviderData[nomadProviderDataEvalID]
	if evalID == "" {
		n.logger.Tracef("No eval id stored for tray %s; skipping wait", tray.Id)
		return nil
	}

	var waitIndex uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		q := (&api.QueryOptions{
			Namespace: n.namespace,
			WaitIndex: waitIndex,
		}).WithContext(ctx)

		eval, meta, err := n.client.Evaluations().Info(evalID, q)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("failed to query eval %s: %w", evalID, err)
		}

		switch eval.Status {
		case "complete":
			return nil
		case "blocked":
			n.logger.Warnf("Nomad eval %s blocked for tray %s: %s", evalID, tray.Id, eval.StatusDescription)
			return fmt.Errorf("%w: %s", ErrCapacityBlocked, formatBlockedReason(eval))
		case "failed", "canceled":
			return fmt.Errorf("nomad eval %s ended with status %s: %s", evalID, eval.Status, eval.StatusDescription)
		case "pending", "":
			// keep waiting; advance WaitIndex so the next call blocks server-side
			if meta != nil && meta.LastIndex > waitIndex {
				waitIndex = meta.LastIndex
			}
		default:
			return fmt.Errorf("nomad eval %s returned unexpected status %q", evalID, eval.Status)
		}
	}
}

// CleanTray deregisters the dispatched child job. Safe to call on a tray
// that StartDeploy never finished:
//
//   - If dispatchedJobId is stored, deregister it directly (fast path).
//   - Otherwise, if parentJobId is stored, scan the parent's dispatched
//     children for any whose ID matches the prefix
//     "<parentJobId>/dispatch-<tray.Id>-" and deregister them. This
//     recovers the leaked-child scenario where Dispatch succeeded on the
//     server but the response was lost (network error / timeout) and
//     dispatchedJobId never made it into ProviderData.
//   - If neither is stored, nothing to do.
func (n *NomadProvider) CleanTray(ctx context.Context, tray *trays.Tray) error {
	ns := tray.ProviderData[nomadProviderDataNamespace]
	if ns == "" {
		ns = n.namespace
	}

	dispatchedJobID := tray.ProviderData[nomadProviderDataDispatchedJobID]
	if dispatchedJobID != "" {
		return n.deregister(ctx, ns, dispatchedJobID)
	}

	parentJobID := tray.ProviderData[nomadProviderDataParentJobID]
	if parentJobID == "" {
		n.logger.Warnf("CleanTray called without dispatchedJobId or parentJobId for tray %s; nothing to do", tray.Id)
		return nil
	}

	return n.cleanupLeakedDispatch(ctx, ns, parentJobID, tray.Id)
}

func (n *NomadProvider) deregister(ctx context.Context, ns, jobID string) error {
	_, _, err := n.client.Jobs().Deregister(
		jobID,
		true,
		(&api.WriteOptions{Namespace: ns}).WithContext(ctx),
	)
	if err != nil {
		if isNomad404(err) {
			n.logger.Tracef("Dispatched job %s already gone; nothing to do", jobID)
			return nil
		}
		return err
	}
	return nil
}

// cleanupLeakedDispatch finds child jobs of parentJobID that were dispatched
// for trayID and deregisters each. Used when StartDeploy could not persist
// the returned DispatchedJobID. The provider always dispatches with
// idPrefixTemplate = tray.Id, so a leaked child's ID has the shape
// "<parentJobID>/dispatch-<trayID>-<timestamp>-<uuid>" — matched by prefix.
func (n *NomadProvider) cleanupLeakedDispatch(ctx context.Context, ns, parentJobID, trayID string) error {
	expectedPrefix := parentJobID + "/dispatch-" + trayID + "-"

	q := (&api.QueryOptions{
		Namespace: ns,
		Prefix:    parentJobID + "/dispatch-",
	}).WithContext(ctx)

	stubs, _, err := n.client.Jobs().List(q)
	if err != nil {
		if isNomad404(err) {
			return nil
		}
		return fmt.Errorf("failed to list nomad jobs for cleanup recovery: %w", err)
	}

	matched := 0
	var firstErr error
	for _, stub := range stubs {
		if stub.ParentID != parentJobID {
			continue
		}
		if !strings.HasPrefix(stub.ID, expectedPrefix) {
			continue
		}
		matched++
		if err := n.deregister(ctx, ns, stub.ID); err != nil {
			n.logger.Errorf("Failed to deregister leaked dispatched job %s for tray %s: %v", stub.ID, trayID, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		n.logger.Infof("Deregistered leaked dispatched job %s for tray %s", stub.ID, trayID)
	}
	if matched == 0 {
		n.logger.Tracef("No leaked dispatched children found for tray %s under parent %s", trayID, parentJobID)
	}
	return firstErr
}

func generateBootstrapToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// buildBootstrapPayload composes the dispatched bash payload. runnerFolder
// defaults to defaultRunnerFolder when empty.
func buildBootstrapPayload(userScript, runnerFolder string) []byte {
	if runnerFolder == "" {
		runnerFolder = defaultRunnerFolder
	}
	var sb strings.Builder
	sb.WriteString("#!/bin/bash\n")
	sb.WriteString("set -euo pipefail\n\n")
	sb.WriteString(`curl -fsSL "$CATTERY_URL/agent/download" -o /usr/local/bin/cattery` + "\n")
	sb.WriteString("chmod +x /usr/local/bin/cattery\n\n")
	if userScript != "" {
		sb.WriteString(userScript)
		if !strings.HasSuffix(userScript, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "exec /usr/local/bin/cattery agent -i \"$TRAY_NAME\" -s \"$CATTERY_URL\" --runner-folder %q\n", runnerFolder)
	return []byte(sb.String())
}

// formatBlockedReason summarizes the first FailedTGAllocs entry into something
// loggable. Nomad's AllocationMetric carries node counters
// (NodesExhausted/ConstraintFiltered/etc.) that almost always answer "why
// didn't this place" without parsing the entire structure.
func formatBlockedReason(eval *api.Evaluation) string {
	if len(eval.FailedTGAllocs) == 0 {
		return eval.StatusDescription
	}
	parts := make([]string, 0, len(eval.FailedTGAllocs))
	for tg, m := range eval.FailedTGAllocs {
		if m == nil {
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"%s: nodesEvaluated=%d nodesFiltered=%d nodesExhausted=%d classFiltered=%d constraintFiltered=%d",
			tg, m.NodesEvaluated, m.NodesFiltered, m.NodesExhausted,
			len(m.ClassFiltered), len(m.ConstraintFiltered),
		))
	}
	return strings.Join(parts, "; ")
}

// isNomad404 returns true if err is a Nomad "job not found" response. Nomad's
// api package returns plain errors with the HTTP status embedded in the
// message; there is no typed 404 to match against.
func isNomad404(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "404")
}
