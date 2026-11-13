// Package agentBinaries resolves the cattery binary the server hands out to a
// tray of a given platform through GET /agent/download.
//
// The server's own platform is always served from its own executable. Every
// other platform comes from a folder on disk holding release assets under their
// release names (see AssetName), optionally filled on demand from the GitHub
// release matching the server's version.
package agentBinaries

import (
	"cattery/lib/config"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
)

// releaseBaseURL is where release assets are downloaded from:
// <releaseBaseURL>/<version>/<asset name>.
const releaseBaseURL = "https://github.com/paritytech/cattery/releases/download"

// downloadTimeout bounds a single on-demand download.
const downloadTimeout = 5 * time.Minute

var (
	// ErrUnsupportedPlatform means the requested os/arch is not a platform
	// cattery is released for.
	ErrUnsupportedPlatform = errors.New("unsupported platform")
	// ErrNotAvailable means the platform is supported but this server has no
	// binary for it: it is not in the folder and either on-demand download is
	// off or the release has no such asset.
	ErrNotAvailable = errors.New("agent binary not available")
	// ErrDownloadFailed means the on-demand download from the release failed.
	ErrDownloadFailed = errors.New("agent binary download failed")
)

// supportedPlatforms lists the platforms cattery is released for. Keep it in
// sync with the build matrix in .github/workflows/build.yml.
var supportedPlatforms = map[string]struct{}{
	"linux/amd64":   {},
	"linux/arm64":   {},
	"darwin/amd64":  {},
	"darwin/arm64":  {},
	"windows/amd64": {},
	"windows/arm64": {},
}

// osAliases and archAliases map what a tray can cheaply find out about itself
// (`uname -s`, `uname -m`, %OS%) to Go's names, so a bootstrap script can pass
// those values through unchanged. Keys are lowercase.
var osAliases = map[string]string{
	"macos":      "darwin",
	"windows_nt": "windows",
}

var archAliases = map[string]string{
	"x86_64":  "amd64",
	"x64":     "amd64",
	"aarch64": "arm64",
}

// AssetName returns the file name of the cattery binary for a platform and
// version. The same name is used for the release asset, for the file in the
// binaries folder and in the download URL.
func AssetName(goos, goarch, version string) string {
	name := fmt.Sprintf("cattery_%s_%s_%s", goos, goarch, version)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Store locates cattery binaries for the platforms trays run on.
type Store struct {
	dir      string
	download bool
	version  string

	baseURL string
	client  *http.Client
	// downloads collapses concurrent requests for the same missing binary into
	// one download.
	downloads singleflight.Group
	logger    *log.Entry
}

// NewStore creates a Store serving binaries of the given cattery version.
func NewStore(cfg config.AgentBinariesConfig, version string) *Store {
	return &Store{
		dir:      cfg.Dir,
		download: cfg.Download,
		version:  version,
		baseURL:  releaseBaseURL,
		client:   &http.Client{},
		logger:   log.WithField("name", "agentBinaries"),
	}
}

// Path returns the local path of the cattery binary for the requested platform.
//
// osName and archName are what the tray reported about itself: Go names
// (linux, arm64), `uname` output (Linux, aarch64, x86_64) or empty for the
// server's own. Matching is case-insensitive.
//
// Lookup order:
//  1. the server's own platform is its own executable;
//  2. a file named AssetName in the binaries folder;
//  3. if on-demand download is enabled, the release asset for the server's
//     version, saved into the folder.
//
// Errors wrap ErrUnsupportedPlatform, ErrNotAvailable or ErrDownloadFailed. A
// caller whose ctx ends while a download is in progress gets the ctx error
// right away; the download itself carries on for the next caller.
func (s *Store) Path(ctx context.Context, osName, archName string) (string, error) {
	goos := normalize(osName, runtime.GOOS, osAliases)
	goarch := normalize(archName, runtime.GOARCH, archAliases)

	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		return os.Executable()
	}

	// From here on goos/goarch end up in a file name and a URL, so only
	// allowlisted values may pass.
	if _, ok := supportedPlatforms[goos+"/"+goarch]; !ok {
		return "", fmt.Errorf("%w: os=%q arch=%q", ErrUnsupportedPlatform, osName, archName)
	}

	name := AssetName(goos, goarch, s.version)
	path := filepath.Join(s.dir, name)

	if isRegularFile(path) {
		return path, nil
	}

	if !s.download {
		return "", fmt.Errorf("%w: %s (not in the binaries folder, on-demand download is off)", ErrNotAvailable, name)
	}

	result := s.downloads.DoChan(name, func() (any, error) {
		// A previous download may have finished between the check above and
		// this call starting.
		if isRegularFile(path) {
			return nil, nil
		}

		// Detached from the request: other trays may be waiting on this same
		// download, and the caller that started it going away must not abort it.
		downloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), downloadTimeout)
		defer cancel()

		err := s.fetch(downloadCtx, name, path)
		if err != nil {
			// Logged here because every caller may have gone away by now.
			s.logger.Warnf("Download of %s failed: %v", name, err)
		}
		return nil, err
	})

	// The download is shared and detached, waiting for it is not: a caller
	// that went away must not stay parked here until the download ends.
	select {
	case downloaded := <-result:
		if downloaded.Err != nil {
			return "", downloaded.Err
		}
		return path, nil
	case <-ctx.Done():
		return "", fmt.Errorf("gave up waiting for the download of %s: %w", name, ctx.Err())
	}
}

// fetch downloads the release asset `name` into dest. The body goes to a
// temporary file first and is renamed into place, so a partial download is
// never served.
func (s *Store) fetch(ctx context.Context, name, dest string) (err error) {
	assetURL := s.baseURL + "/" + url.PathEscape(s.version) + "/" + url.PathEscape(name)
	s.logger.Infof("Downloading %s", assetURL)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}

	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s (no such asset in release %s)", ErrNotAvailable, name, s.version)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s responded %s", ErrDownloadFailed, assetURL, response.Status)
	}

	if err = os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create agent binaries folder: %w", err)
	}

	tmp, err := os.CreateTemp(s.dir, name+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err = io.Copy(tmp, response.Body); err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err = os.Chmod(tmp.Name(), 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp.Name(), err)
	}
	if err = os.Rename(tmp.Name(), dest); err != nil {
		return fmt.Errorf("move %s into place: %w", name, err)
	}

	s.logger.Infof("Downloaded %s to %s", name, dest)
	return nil
}

// normalize lowercases value, applies aliases, and falls back to def when
// value is empty.
func normalize(value, def string, aliases map[string]string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return def
	}
	if alias, ok := aliases[value]; ok {
		return alias
	}
	return value
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
