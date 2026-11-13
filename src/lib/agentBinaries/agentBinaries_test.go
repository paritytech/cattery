package agentBinaries

import (
	"cattery/lib/config"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testVersion = "1.2.3"

// foreignPlatform returns a supported platform that is not the one the tests
// run on, so lookups go through the folder instead of the own executable.
func foreignPlatform() (string, string) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		return "linux", "amd64"
	}
	return "linux", "arm64"
}

func newTestStore(t *testing.T, download bool) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return NewStore(config.AgentBinariesConfig{Dir: dir, Download: download}, testVersion), dir
}

// releaseServer fakes the GitHub release download endpoint: it serves `body`
// for every path in `assets` and 404 otherwise, counting requests.
func releaseServer(t *testing.T, assets map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, ok := assets[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestAssetName(t *testing.T) {
	assert.Equal(t, "cattery_linux_arm64_1.2.3", AssetName("linux", "arm64", "1.2.3"))
	assert.Equal(t, "cattery_darwin_amd64_1.2.3", AssetName("darwin", "amd64", "1.2.3"))
	assert.Equal(t, "cattery_windows_amd64_1.2.3.exe", AssetName("windows", "amd64", "1.2.3"))
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		value    string
		aliases  map[string]string
		expected string
	}{
		{"linux", osAliases, "linux"},
		{"Linux", osAliases, "linux"},
		{"Darwin", osAliases, "darwin"},
		{"macOS", osAliases, "darwin"},
		{"Windows_NT", osAliases, "windows"},
		{"  linux ", osAliases, "linux"},
		{"", osAliases, "default"},
		{"amd64", archAliases, "amd64"},
		{"AMD64", archAliases, "amd64"},
		{"x86_64", archAliases, "amd64"},
		{"x64", archAliases, "amd64"},
		{"arm64", archAliases, "arm64"},
		{"aarch64", archAliases, "arm64"},
		{"", archAliases, "default"},
		{"riscv64", archAliases, "riscv64"},
	}

	for _, c := range cases {
		assert.Equal(t, c.expected, normalize(c.value, "default", c.aliases), "value %q", c.value)
	}
}

func TestPath_OwnPlatformIsOwnExecutable(t *testing.T) {
	store, _ := newTestStore(t, false)
	executable, err := os.Executable()
	require.NoError(t, err)

	t.Run("no parameters", func(t *testing.T) {
		path, err := store.Path(context.Background(), "", "")
		require.NoError(t, err)
		assert.Equal(t, executable, path)
	})

	t.Run("explicit own platform", func(t *testing.T) {
		path, err := store.Path(context.Background(), runtime.GOOS, runtime.GOARCH)
		require.NoError(t, err)
		assert.Equal(t, executable, path)
	})
}

func TestPath_UnsupportedPlatform(t *testing.T) {
	store, dir := newTestStore(t, true)
	srv, hits := releaseServer(t, nil)
	store.baseURL = srv.URL

	for _, c := range []struct{ os, arch string }{
		{"linux", "riscv64"},
		{"plan9", "amd64"},
		{"../../etc", "passwd"},
		{"linux", "arm64/../../x"},
	} {
		_, err := store.Path(context.Background(), c.os, c.arch)
		assert.ErrorIs(t, err, ErrUnsupportedPlatform, "os=%q arch=%q", c.os, c.arch)
	}

	assert.Zero(t, hits.Load(), "an unsupported platform must never reach the release server")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPath_FromFolder(t *testing.T) {
	store, dir := newTestStore(t, false)
	goos, goarch := foreignPlatform()
	expected := filepath.Join(dir, AssetName(goos, goarch, testVersion))
	require.NoError(t, os.WriteFile(expected, []byte("binary"), 0o755))

	path, err := store.Path(context.Background(), goos, goarch)
	require.NoError(t, err)
	assert.Equal(t, expected, path)
}

func TestPath_AcceptsUnameOutput(t *testing.T) {
	store, dir := newTestStore(t, false)

	// Both linux binaries are present, so whichever one is foreign to the test
	// host is served from the folder.
	for _, arch := range []string{"amd64", "arm64"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, AssetName("linux", arch, testVersion)), []byte(arch), 0o755))
	}

	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		path, err := store.Path(context.Background(), "Linux", "x86_64")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, "cattery_linux_amd64_1.2.3"), path)
		return
	}

	path, err := store.Path(context.Background(), "Linux", "aarch64")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "cattery_linux_arm64_1.2.3"), path)
}

func TestPath_OtherVersionInFolderIsNotServed(t *testing.T) {
	store, dir := newTestStore(t, false)
	goos, goarch := foreignPlatform()
	require.NoError(t, os.WriteFile(filepath.Join(dir, AssetName(goos, goarch, "1.2.2")), []byte("old"), 0o755))

	_, err := store.Path(context.Background(), goos, goarch)
	assert.ErrorIs(t, err, ErrNotAvailable)
}

func TestPath_MissingAndDownloadOff(t *testing.T) {
	store, _ := newTestStore(t, false)
	srv, hits := releaseServer(t, nil)
	store.baseURL = srv.URL
	goos, goarch := foreignPlatform()

	_, err := store.Path(context.Background(), goos, goarch)
	assert.ErrorIs(t, err, ErrNotAvailable)
	assert.Zero(t, hits.Load(), "download is off: the release server must not be contacted")
}

func TestPath_Download(t *testing.T) {
	store, dir := newTestStore(t, true)
	goos, goarch := foreignPlatform()
	name := AssetName(goos, goarch, testVersion)
	srv, hits := releaseServer(t, map[string]string{"/" + testVersion + "/" + name: "downloaded binary"})
	store.baseURL = srv.URL

	path, err := store.Path(context.Background(), goos, goarch)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, name), path)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "downloaded binary", string(content))

	// Second request is served from the folder.
	again, err := store.Path(context.Background(), goos, goarch)
	require.NoError(t, err)
	assert.Equal(t, path, again)
	assert.EqualValues(t, 1, hits.Load())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary files may be left behind")
}

func TestPath_DownloadCreatesMissingFolder(t *testing.T) {
	goos, goarch := foreignPlatform()
	name := AssetName(goos, goarch, testVersion)
	srv, _ := releaseServer(t, map[string]string{"/" + testVersion + "/" + name: "x"})

	dir := filepath.Join(t.TempDir(), "not", "yet", "there")
	store := NewStore(config.AgentBinariesConfig{Dir: dir, Download: true}, testVersion)
	store.baseURL = srv.URL

	path, err := store.Path(context.Background(), goos, goarch)
	require.NoError(t, err)
	assert.FileExists(t, path)
}

func TestPath_DownloadAssetMissingInRelease(t *testing.T) {
	store, dir := newTestStore(t, true)
	srv, hits := releaseServer(t, nil)
	store.baseURL = srv.URL
	goos, goarch := foreignPlatform()

	_, err := store.Path(context.Background(), goos, goarch)
	assert.ErrorIs(t, err, ErrNotAvailable)
	assert.EqualValues(t, 1, hits.Load())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a failed download must not leave files behind")
}

func TestPath_DownloadUpstreamError(t *testing.T) {
	store, dir := newTestStore(t, true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	store.baseURL = srv.URL
	goos, goarch := foreignPlatform()

	_, err := store.Path(context.Background(), goos, goarch)
	assert.ErrorIs(t, err, ErrDownloadFailed)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPath_DownloadTruncatedBodyIsNotServed(t *testing.T) {
	store, dir := newTestStore(t, true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Promise more than is sent, then drop the connection.
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("partial"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	store.baseURL = srv.URL
	goos, goarch := foreignPlatform()

	_, err := store.Path(context.Background(), goos, goarch)
	assert.ErrorIs(t, err, ErrDownloadFailed)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a partial download must not be kept")
}

func TestPath_ConcurrentRequestsDownloadOnce(t *testing.T) {
	store, _ := newTestStore(t, true)
	goos, goarch := foreignPlatform()
	name := AssetName(goos, goarch, testVersion)

	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release // hold the first download open until every caller is waiting
		_, _ = w.Write([]byte("binary"))
	}))
	t.Cleanup(srv.Close)
	store.baseURL = srv.URL

	const callers = 10
	var started, done sync.WaitGroup
	paths := make([]string, callers)
	errs := make([]error, callers)
	for i := range callers {
		started.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			started.Done()
			paths[i], errs[i] = store.Path(context.Background(), goos, goarch)
		}()
	}
	started.Wait()
	close(release)
	done.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, name, filepath.Base(paths[i]))
	}
	assert.EqualValues(t, 1, hits.Load(), "concurrent requests for one binary must share a single download")
}

// A caller that goes away mid-download is released at once, and the download
// it started still completes for whoever asks next.
func TestPath_CancelledRequestStopsWaitingButDownloadContinues(t *testing.T) {
	store, _ := newTestStore(t, true)
	goos, goarch := foreignPlatform()
	name := AssetName(goos, goarch, testVersion)

	var hits atomic.Int32
	arrived := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			close(arrived)
		}
		<-release // hold the download open while its caller goes away
		_, _ = w.Write([]byte("binary"))
	}))
	t.Cleanup(srv.Close)
	// Registered after srv.Close, so it runs first: Close waits for the handler.
	var releaseOnce sync.Once
	releaseDownload := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseDownload)
	store.baseURL = srv.URL

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		_, err := store.Path(ctx, goos, goarch)
		returned <- err
	}()

	<-arrived
	cancel()

	select {
	case err := <-returned:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled caller is still waiting for the download")
	}

	releaseDownload()

	path, err := store.Path(context.Background(), goos, goarch)
	require.NoError(t, err)
	assert.Equal(t, name, filepath.Base(path))
	assert.FileExists(t, path)
	assert.EqualValues(t, 1, hits.Load(), "the abandoned download must be reused, not restarted")
}
