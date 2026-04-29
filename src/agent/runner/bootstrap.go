// Package runner handles ensuring the GitHub Actions runner distribution is
// present on disk before the agent launches Runner.Listener.
//
// If the runner is already installed (e.g. baked into the VM image), EnsureRunner
// is a no-op. Otherwise it downloads the runner tarball from GitHub releases.
package runner

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// httpClient is package-scoped so tests can swap it out.
var httpClient = &http.Client{Timeout: 5 * time.Minute}

// latestReleaseURL returns the GitHub API endpoint that yields the latest
// actions/runner release. Overridable for tests.
var latestReleaseURL = "https://api.github.com/repos/actions/runner/releases/latest"

// runnerDownloadURL builds the tarball URL for a given version + platform.
// Overridable for tests.
var runnerDownloadURL = func(version, osName, arch string) string {
	return fmt.Sprintf(
		"https://github.com/actions/runner/releases/download/v%s/actions-runner-%s-%s-%s.tar.gz",
		version, osName, arch, version,
	)
}

// EnsureRunner makes sure Runner.Listener exists under runnerFolder/bin.
// If it doesn't, the GH Actions runner tarball is downloaded and extracted.
// When runnerVersion is empty, the latest release tag is fetched from GitHub.
func EnsureRunner(runnerFolder, runnerVersion string) error {
	listenerPath := filepath.Join(runnerFolder, "bin", "Runner.Listener")
	if _, err := os.Stat(listenerPath); err == nil {
		log.Infof("Runner.Listener already present at %s, skipping download", listenerPath)
		return nil
	}

	version := runnerVersion
	if version == "" {
		latest, err := fetchLatestRunnerVersion()
		if err != nil {
			return fmt.Errorf("resolve latest runner version: %w", err)
		}
		version = latest
		log.Infof("Resolved latest GH runner version: %s", version)
	}
	// strip optional leading 'v'
	version = strings.TrimPrefix(version, "v")

	osName, arch, err := runnerPlatform()
	if err != nil {
		return err
	}

	url := runnerDownloadURL(version, osName, arch)
	log.Infof("Downloading GH Actions runner %s/%s v%s from %s", osName, arch, version, url)

	if err := os.MkdirAll(runnerFolder, 0o755); err != nil {
		return fmt.Errorf("create runner folder: %w", err)
	}

	resp, err := httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("download runner: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download runner: HTTP %d from %s", resp.StatusCode, url)
	}

	if err := extractTarGz(resp.Body, runnerFolder); err != nil {
		return fmt.Errorf("extract runner: %w", err)
	}

	if _, err := os.Stat(listenerPath); err != nil {
		return fmt.Errorf("Runner.Listener missing after extraction at %s: %w", listenerPath, err)
	}
	log.Infof("GH Actions runner installed at %s", runnerFolder)
	return nil
}

// fetchLatestRunnerVersion queries the GH API for the latest runner release tag.
func fetchLatestRunnerVersion() (string, error) {
	req, err := http.NewRequest(http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("latest release: empty tag_name")
	}
	return payload.TagName, nil
}

// runnerPlatform maps Go runtime info to the actions/runner naming scheme.
func runnerPlatform() (osName, arch string, err error) {
	switch runtime.GOOS {
	case "linux":
		osName = "linux"
	case "darwin":
		osName = "osx"
	case "windows":
		osName = "win"
	default:
		return "", "", fmt.Errorf("unsupported runtime.GOOS %q for GH runner", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	case "arm":
		arch = "arm"
	default:
		return "", "", fmt.Errorf("unsupported runtime.GOARCH %q for GH runner", runtime.GOARCH)
	}
	return osName, arch, nil
}

// extractTarGz unpacks a gzipped tar stream into destDir. Entries that would
// escape destDir via path traversal are rejected.
func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		target := filepath.Join(absDest, header.Name)
		// Path traversal protection: target must stay inside destDir.
		rel, err := filepath.Rel(absDest, target)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("tar entry escapes destination: %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Resolve the symlink target relative to destDir and reject escapes.
			linkTarget := header.Linkname
			absLink := linkTarget
			if !filepath.IsAbs(absLink) {
				absLink = filepath.Join(filepath.Dir(target), linkTarget)
			}
			rel, err := filepath.Rel(absDest, absLink)
			if err != nil || strings.HasPrefix(rel, "..") {
				return fmt.Errorf("symlink entry escapes destination: %q -> %q", header.Name, linkTarget)
			}
			_ = os.Remove(target) // overwrite if exists
			if err := os.Symlink(linkTarget, target); err != nil {
				return err
			}
		default:
			// Skip other entry types (block/char devices, etc.)
		}
	}
}
