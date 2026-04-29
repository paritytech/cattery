package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureRunner_AlreadyPresent_NoDownload(t *testing.T) {
	// Pre-create the listener; downloader must not be touched.
	folder := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(folder, "bin"), 0o755))
	listener := filepath.Join(folder, "bin", "Runner.Listener")
	require.NoError(t, os.WriteFile(listener, []byte("preexisting"), 0o755))

	withDownloadServer(t, "should-not-be-called", nil, func() {
		err := EnsureRunner(folder, "2.300.0")
		require.NoError(t, err)
	})

	got, err := os.ReadFile(listener)
	require.NoError(t, err)
	assert.Equal(t, "preexisting", string(got), "existing listener must not be overwritten")
}

func TestEnsureRunner_DownloadsPinnedVersion(t *testing.T) {
	folder := t.TempDir()

	tarball := makeRunnerTarball(t, "fake-listener-bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	}))
	defer srv.Close()

	origURLFn := runnerDownloadURL
	t.Cleanup(func() { runnerDownloadURL = origURLFn })
	runnerDownloadURL = func(version, osName, arch string) string {
		return srv.URL + "/" + version
	}

	require.NoError(t, EnsureRunner(folder, "2.300.0"))

	listener := filepath.Join(folder, "bin", "Runner.Listener")
	got, err := os.ReadFile(listener)
	require.NoError(t, err)
	assert.Equal(t, "fake-listener-bytes", string(got))
}

func TestEnsureRunner_ResolvesLatestWhenVersionEmpty(t *testing.T) {
	folder := t.TempDir()

	tarball := makeRunnerTarball(t, "latest-listener")
	var requestedVersion string

	tarSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	}))
	defer tarSrv.Close()
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v2.999.0"}`))
	}))
	defer apiSrv.Close()

	origAPI := latestReleaseURL
	origURLFn := runnerDownloadURL
	t.Cleanup(func() {
		latestReleaseURL = origAPI
		runnerDownloadURL = origURLFn
	})
	latestReleaseURL = apiSrv.URL
	runnerDownloadURL = func(version, osName, arch string) string {
		requestedVersion = version
		return tarSrv.URL + "/" + version
	}

	require.NoError(t, EnsureRunner(folder, ""))
	assert.Equal(t, "2.999.0", requestedVersion, "must strip leading v and pass to URL builder")
}

func TestEnsureRunner_DownloadFailureSurfaces(t *testing.T) {
	folder := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	origURLFn := runnerDownloadURL
	t.Cleanup(func() { runnerDownloadURL = origURLFn })
	runnerDownloadURL = func(version, osName, arch string) string { return srv.URL }

	err := EnsureRunner(folder, "2.300.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestExtractTarGz_RejectsPathTraversal(t *testing.T) {
	// Build a tarball with a malicious entry that escapes destDir.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "../escape.txt",
		Mode:     0o644,
		Size:     5,
		Typeflag: tar.TypeReg,
	}))
	_, _ = tw.Write([]byte("hello"))
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	dest := t.TempDir()
	err := extractTarGz(&buf, dest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes destination")
}

func TestRunnerPlatform(t *testing.T) {
	osName, arch, err := runnerPlatform()
	require.NoError(t, err)
	// Spot-check: on the test host these should resolve to *something*; we
	// can't assert exact values portably, but we can assert non-empty.
	assert.NotEmpty(t, osName)
	assert.NotEmpty(t, arch)
	// And on common dev machines (linux/amd64, darwin/arm64) we get sane mappings.
	if runtime.GOOS == "linux" {
		assert.Equal(t, "linux", osName)
	}
	if runtime.GOARCH == "amd64" {
		assert.Equal(t, "x64", arch)
	}
}

// --- helpers ---

// makeRunnerTarball creates a tar.gz with a single entry bin/Runner.Listener
// containing the provided body. Mirrors the layout the agent expects.
func makeRunnerTarball(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "bin/",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
	}))
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "bin/Runner.Listener",
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// withDownloadServer replaces the URL builder for the duration of fn. The
// supplied 'shouldNotCall' string is irrelevant to behaviour; it documents
// intent at the call site (the server is expected not to receive a request).
func withDownloadServer(t *testing.T, _ string, _ http.Handler, fn func()) {
	t.Helper()
	orig := runnerDownloadURL
	t.Cleanup(func() { runnerDownloadURL = orig })
	runnerDownloadURL = func(version, osName, arch string) string {
		t.Fatalf("runnerDownloadURL should not have been called for version=%s", version)
		return ""
	}
	fn()
}
