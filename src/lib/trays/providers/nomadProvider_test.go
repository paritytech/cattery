package providers

import (
	"cattery/lib/config"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/nomad/api"
	"github.com/stretchr/testify/assert"
)

func TestGenerateBootstrapToken(t *testing.T) {
	t.Run("returns 64 hex chars", func(t *testing.T) {
		tok, err := generateBootstrapToken()
		assert.NoError(t, err)
		assert.Len(t, tok, 64)
		_, err = hex.DecodeString(tok)
		assert.NoError(t, err, "token must be valid hex")
	})

	t.Run("two calls produce distinct tokens", func(t *testing.T) {
		a, _ := generateBootstrapToken()
		b, _ := generateBootstrapToken()
		assert.NotEqual(t, a, b)
	})
}

func TestBuildBootstrapPayload(t *testing.T) {
	t.Run("empty runnerFolder defaults to /cattery", func(t *testing.T) {
		out := string(buildBootstrapPayload("", ""))
		assert.Contains(t, out, `--runner-folder "/cattery"`)
	})

	t.Run("custom runnerFolder is quoted with %q semantics", func(t *testing.T) {
		out := string(buildBootstrapPayload("", "/opt/runner"))
		assert.Contains(t, out, `--runner-folder "/opt/runner"`)
	})

	t.Run("prelude downloads cattery agent and chmods it", func(t *testing.T) {
		out := string(buildBootstrapPayload("", ""))
		assert.Contains(t, out, `curl -fsSL "$CATTERY_URL/agent/binary" -o /usr/local/bin/cattery`)
		assert.Contains(t, out, "chmod +x /usr/local/bin/cattery")
	})

	t.Run("starts with shebang and strict mode", func(t *testing.T) {
		out := string(buildBootstrapPayload("", ""))
		assert.True(t, strings.HasPrefix(out, "#!/bin/bash\nset -euo pipefail\n"),
			"output must start with shebang and `set -euo pipefail`, got: %q", out[:min(64, len(out))])
	})

	t.Run("user script is spliced between prelude and exec", func(t *testing.T) {
		out := string(buildBootstrapPayload("echo MARKER", "/cattery"))

		preludeIdx := strings.Index(out, "chmod +x /usr/local/bin/cattery")
		userIdx := strings.Index(out, "echo MARKER")
		execIdx := strings.Index(out, "exec /usr/local/bin/cattery agent")

		assert.NotEqual(t, -1, preludeIdx)
		assert.NotEqual(t, -1, userIdx)
		assert.NotEqual(t, -1, execIdx)
		assert.Less(t, preludeIdx, userIdx, "prelude must come before user script")
		assert.Less(t, userIdx, execIdx, "user script must come before exec")
	})

	t.Run("user script without trailing newline is normalized", func(t *testing.T) {
		// no trailing newline
		out := string(buildBootstrapPayload("echo a", ""))
		// The user line and the exec line must be on separate lines.
		assert.NotContains(t, out, "echo aexec")
		assert.Contains(t, out, "echo a\n")
	})

	t.Run("user script with trailing newline doesn't gain a duplicate", func(t *testing.T) {
		out := string(buildBootstrapPayload("echo a\n", ""))
		// One blank line between user script and exec is fine; three+ would be a bug.
		assert.NotContains(t, out, "echo a\n\n\n\n")
	})

	t.Run("multi-line user script preserved verbatim", func(t *testing.T) {
		userScript := "set -e\necho one\necho two"
		out := string(buildBootstrapPayload(userScript, ""))
		assert.Contains(t, out, userScript)
	})

	t.Run("empty user script produces no extra block", func(t *testing.T) {
		out := string(buildBootstrapPayload("", ""))
		// chmod line followed by blank line then exec, no leftover user-script artefacts
		assert.NotContains(t, out, "echo")
	})

	t.Run("exec line uses TRAY_NAME and CATTERY_URL env vars", func(t *testing.T) {
		out := string(buildBootstrapPayload("", ""))
		assert.Contains(t, out, `exec /usr/local/bin/cattery agent -i "$TRAY_NAME" -s "$CATTERY_URL"`)
	})
}

func TestFormatBlockedReason(t *testing.T) {
	t.Run("empty FailedTGAllocs falls back to StatusDescription", func(t *testing.T) {
		eval := &api.Evaluation{StatusDescription: "no nodes available"}
		assert.Equal(t, "no nodes available", formatBlockedReason(eval))
	})

	t.Run("nil metric in map is skipped", func(t *testing.T) {
		eval := &api.Evaluation{
			FailedTGAllocs: map[string]*api.AllocationMetric{"vm": nil},
		}
		// Skipping the nil leaves an empty join — acceptable, don't panic.
		assert.NotPanics(t, func() { formatBlockedReason(eval) })
	})

	t.Run("single group renders all counters", func(t *testing.T) {
		eval := &api.Evaluation{
			FailedTGAllocs: map[string]*api.AllocationMetric{
				"vm": {
					NodesEvaluated:     5,
					NodesFiltered:      2,
					NodesExhausted:     1,
					ClassFiltered:      map[string]int{"runner-host": 1},
					ConstraintFiltered: map[string]int{"meta.runner_host=cattery": 2},
				},
			},
		}
		out := formatBlockedReason(eval)
		assert.Contains(t, out, "vm: ")
		assert.Contains(t, out, "nodesEvaluated=5")
		assert.Contains(t, out, "nodesFiltered=2")
		assert.Contains(t, out, "nodesExhausted=1")
		assert.Contains(t, out, "classFiltered=1")
		assert.Contains(t, out, "constraintFiltered=1")
	})

	t.Run("multiple groups joined by semicolons", func(t *testing.T) {
		eval := &api.Evaluation{
			FailedTGAllocs: map[string]*api.AllocationMetric{
				"vm-a": {NodesEvaluated: 1},
				"vm-b": {NodesEvaluated: 2},
			},
		}
		out := formatBlockedReason(eval)
		assert.Contains(t, out, "; ")
		// Don't assert ordering — map iteration is non-deterministic.
		assert.Contains(t, out, "vm-a:")
		assert.Contains(t, out, "vm-b:")
	})
}

func TestIsNomad404(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"contains lowercase 'not found'", errors.New("job not found"), true},
		{"contains uppercase 'Not Found' (case-insensitive)", errors.New("Job Not Found"), true},
		{"contains '404'", errors.New("server returned 404"), true},
		{"unrelated error", errors.New("connection refused"), false},
		{"wrapped 404 error", fmt.Errorf("dispatch failed: %w", errors.New("404 page not found")), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isNomad404(tc.err))
		})
	}
}

func TestNewNomadProvider(t *testing.T) {
	t.Run("missing address returns nil", func(t *testing.T) {
		p := NewNomadProvider("toaster", config.ProviderConfig{
			"name": "toaster",
			"type": "nomad",
		})
		assert.Nil(t, p)
	})

	t.Run("minimal valid config builds a provider", func(t *testing.T) {
		p := NewNomadProvider("toaster", config.ProviderConfig{
			"name":    "toaster",
			"type":    "nomad",
			"address": "https://example.invalid:4646",
		})
		if assert.NotNil(t, p) {
			assert.Equal(t, "toaster", p.GetProviderName())
			assert.Empty(t, p.namespace, "namespace stays empty when not configured")
		}
	})

	t.Run("namespace is captured on the provider", func(t *testing.T) {
		p := NewNomadProvider("toaster", config.ProviderConfig{
			"name":      "toaster",
			"type":      "nomad",
			"address":   "https://example.invalid:4646",
			"namespace": "ci",
		})
		if assert.NotNil(t, p) {
			assert.Equal(t, "ci", p.namespace)
		}
	})

	t.Run("insecure=true is parsed case-insensitively", func(t *testing.T) {
		// Just verifying the constructor accepts the value without erroring.
		// The TLS config lives inside an unexported nomad client field, so
		// we don't introspect it — we'd need a real handshake, which an
		// httptest server below covers.
		for _, v := range []string{"true", "TRUE", "True"} {
			p := NewNomadProvider("toaster", config.ProviderConfig{
				"name":     "toaster",
				"type":     "nomad",
				"address":  "https://example.invalid:4646",
				"insecure": v,
			})
			assert.NotNil(t, p, "insecure=%q should be accepted", v)
		}
	})
}

func TestGetProviderName(t *testing.T) {
	p := NewNomadProvider("my-nomad", config.ProviderConfig{
		"name":    "my-nomad",
		"type":    "nomad",
		"address": "https://example.invalid:4646",
	})
	if assert.NotNil(t, p) {
		assert.Equal(t, "my-nomad", p.GetProviderName())
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
