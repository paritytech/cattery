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
// exec'ing this script — see scw-cattery-runner-tray.nomad.hcl for the
// canonical setup.

type NomadProvider struct {
	name           string
	providerConfig config.ProviderConfig

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
		name:           name,
		providerConfig: providerConfig,
		client:         client,
		namespace:      cfg.Namespace,
		logger:         logger,
	}
}

func (n *NomadProvider) GetProviderName() string {
	return n.name
}

// StartDeploy submits a parameterized-job dispatch to Nomad. ProviderData is
// populated *before* the call returns so a partial failure (or a process
// restart between StartDeploy and CleanTray) still leaves enough context for
// CleanTray to attempt cleanup.
func (n *NomadProvider) StartDeploy(ctx context.Context, tray *trays.Tray) error {
	trayConfig, ok := tray.TrayConfig().(config.NomadTrayConfig)
	if !ok {
		return fmt.Errorf("unexpected tray config type for nomad provider, tray %s", tray.Id)
	}
	if trayConfig.JobId == "" {
		return fmt.Errorf("nomad tray config missing jobId, tray %s", tray.Id)
	}

	bootstrapToken, err := generateBootstrapToken()
	if err != nil {
		return fmt.Errorf("failed to generate bootstrap token: %w", err)
	}

	payload := buildBootstrapPayload(trayConfig.Script, trayConfig.RunnerFolder)

	meta := map[string]string{
		"tray_name":       tray.Id,
		"bootstrap_token": bootstrapToken,
		"cattery_url":     config.Get().Server.AdvertiseUrl,
	}
	if tt := tray.TrayType(); tt != nil {
		for k, v := range tt.ExtraMetadata {
			meta[k] = v
		}
	}

	tray.ProviderData[nomadProviderDataNamespace] = n.namespace

	resp, _, err := n.client.Jobs().Dispatch(
		trayConfig.JobId,
		meta,
		payload,
		"",
		(&api.WriteOptions{Namespace: n.namespace}).WithContext(ctx),
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

// CleanTray deregisters the dispatched child job. Safe to call on a tray that
// StartDeploy never finished — missing DispatchedJobID is treated as a no-op.
func (n *NomadProvider) CleanTray(ctx context.Context, tray *trays.Tray) error {
	dispatchedJobID := tray.ProviderData[nomadProviderDataDispatchedJobID]
	if dispatchedJobID == "" {
		n.logger.Warnf("CleanTray called without dispatchedJobId for tray %s; nothing to deregister", tray.Id)
		return nil
	}

	ns := tray.ProviderData[nomadProviderDataNamespace]
	if ns == "" {
		ns = n.namespace
	}

	_, _, err := n.client.Jobs().Deregister(
		dispatchedJobID,
		true,
		(&api.WriteOptions{Namespace: ns}).WithContext(ctx),
	)
	if err != nil {
		if isNomad404(err) {
			n.logger.Tracef("Dispatched job %s already gone; nothing to do", dispatchedJobID)
			return nil
		}
		return err
	}
	return nil
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
	sb.WriteString(`curl -fsSL "$CATTERY_URL/agent/binary" -o /usr/local/bin/cattery` + "\n")
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
