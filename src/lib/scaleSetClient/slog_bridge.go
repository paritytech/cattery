package scaleSetClient

import (
	"context"
	"log/slog"

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
func newRetryableClient(entry *log.Entry) *retryablehttp.Client {
	rc := retryablehttp.NewClient()
	rc.Logger = newSlogLogger(entry)
	return rc
}

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
