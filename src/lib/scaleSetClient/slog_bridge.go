package scaleSetClient

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	log "github.com/sirupsen/logrus"
)

// newRetryableClient builds a retryablehttp.Client whose Logger is our slog
// bridge so that retry/debug HTTP lines flow through logrus instead of the
// stdlib stderr default.
//
// Why this exists: actions/scaleset@v0.3.0/common_client.go:123 only sets the
// retry client's Logger if it is nil — but retryablehttp.NewClient() always
// populates Logger with a default `log.New(os.Stderr, "", log.LstdFlags)`.
// Result: scaleset.WithLogger(...) is silently dropped for the inner retry
// client, and you see lines like `2026/05/09 23:04:30 [DEBUG] GET ...`
// bypassing logrus formatting.
//
// Pre-setting Logger here makes scaleset's nil guard a no-op and our bridge
// stays in place. *slog.Logger satisfies retryablehttp.LeveledLogger.
//
// The timeout and retry budget are set here too, because the library defaults
// leave the listener blind for far too long. See pollRequestTimeout.
func newRetryableClient(entry *log.Entry) *retryablehttp.Client {
	rc := retryablehttp.NewClient()
	rc.Logger = newSlogLogger(entry)
	rc.HTTPClient.Timeout = pollRequestTimeout
	rc.RetryMax = pollRetryMax
	rc.RetryWaitMin = pollRetryWaitMin
	rc.RetryWaitMax = pollRetryWaitMax
	rc.Backoff = boundedBackoff
	return rc
}

// boundedBackoff is retryablehttp's DefaultBackoff with its one unbounded path
// closed off.
//
// DefaultBackoff returns a Retry-After header verbatim for 429 and 503,
// skipping the RetryWaitMax clamp that governs every other case. GitHub sends
// Retry-After on rate limits, and the header may be an HTTP-date minutes or
// hours in the future, so a single GetMessage could otherwise block far past
// MaxPollDuration — the bound config.DefaultStalePollerGrace is derived from.
//
// Clamping means we retry sooner than GitHub asked. That is acceptable here
// because the budget is only pollRetryMax retries: once they are spent the
// listener returns an error and runPoller waits 30s before recreating the
// session, which backs off far more than the clamped wait saved.
func boundedBackoff(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration {
	wait := retryablehttp.DefaultBackoff(min, max, attemptNum, resp)
	if wait > max {
		return max
	}
	return wait
}

const (
	// pollRequestTimeout caps a single HTTP attempt against the Actions
	// broker.
	//
	// Left at zero, actions/scaleset applies its own 5-minute default
	// (common_client.go), which is six times the ~50s GitHub actually holds an
	// idle long-poll open. A connection that dies silently therefore burns a
	// full 5 minutes before the client gives up.
	//
	// 90s is comfortably above the observed hold time and well under it.
	pollRequestTimeout = 90 * time.Second

	// pollRetryMax caps retries within one request.
	//
	// retryablehttp defaults to 4, so a single GetMessage could spend
	// 5 attempts x 5 minutes before the listener loop saw any error at all.
	// Nothing upstream reports a retry, so the listener looked healthy
	// throughout while the tray it was tracking sat untouched and was reaped
	// as stale.
	pollRetryMax = 2

	// Backoff between attempts. Pinned rather than left to the library so the
	// worst-case budget below is a property of this file, not of a dependency
	// default that can change under us.
	pollRetryWaitMin = time.Second
	pollRetryWaitMax = 5 * time.Second

	// MaxPollDuration is the worst case for one GetMessage call:
	// (pollRetryMax+1) attempts at pollRequestTimeout, plus the backoff waits
	// between them. boundedBackoff caps every wait at pollRetryWaitMax, so
	// pollRetryMax waits of that size is the ceiling — about 4m40s.
	//
	// config.DefaultStalePollerGrace must stay above this, or a single
	// legitimately-exhausted call would be misread as a dead listener.
	MaxPollDuration = (pollRetryMax+1)*pollRequestTimeout + pollRetryMax*pollRetryWaitMax
)

// logrusHandler bridges slog into logrus so that third-party libraries using
// slog (e.g. actions/scaleset / go-retryablehttp) respect cattery's log level
// and emit records in the same format as the rest of the application.
type logrusHandler struct {
	entry *log.Entry
}

func newSlogLogger(entry *log.Entry) *slog.Logger {
	return slog.New(&logrusHandler{entry: entry})
}

func (h *logrusHandler) Enabled(_ context.Context, level slog.Level) bool {
	return h.entry.Logger.IsLevelEnabled(slogToLogrus(level))
}

func (h *logrusHandler) Handle(_ context.Context, r slog.Record) error {
	entry := h.entry
	if r.NumAttrs() > 0 {
		fields := make(log.Fields, r.NumAttrs())
		r.Attrs(func(a slog.Attr) bool {
			fields[a.Key] = a.Value.Any()
			return true
		})
		entry = entry.WithFields(fields)
	}
	entry.Log(slogToLogrus(r.Level), r.Message)
	return nil
}

func (h *logrusHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	fields := make(log.Fields, len(attrs))
	for _, a := range attrs {
		fields[a.Key] = a.Value.Any()
	}
	return &logrusHandler{entry: h.entry.WithFields(fields)}
}

// WithGroup is a no-op: logrus has no concept of groups and retryablehttp
// does not use them, so we flatten by ignoring the group prefix.
func (h *logrusHandler) WithGroup(_ string) slog.Handler {
	return h
}

func slogToLogrus(level slog.Level) log.Level {
	switch {
	case level >= slog.LevelError:
		return log.ErrorLevel
	case level >= slog.LevelWarn:
		return log.WarnLevel
	case level >= slog.LevelInfo:
		return log.InfoLevel
	default:
		return log.DebugLevel
	}
}
