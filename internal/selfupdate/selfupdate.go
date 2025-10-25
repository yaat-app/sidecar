package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repository = "yaat-app/sidecar"
	apiURL     = "https://api.github.com/repos/" + repository + "/releases/latest"
)

// Result describes the outcome of a self-update attempt.
type Result struct {
	Updated     bool
	FromVersion string
	ToVersion   string
}

// Run downloads the latest release and replaces the running binary when an update is available.
func Run(currentVersion string) (*Result, error) {
	release, err := fetchLatestRelease()
	if err != nil {
		return nil, err
	}

	result := &Result{
		FromVersion: currentVersion,
		ToVersion:   release.TagName,
	}

	if sameVersion(currentVersion, release.TagName) {
		return result, nil
	}

	assetURL, err := findAssetURL(release)
	if err != nil {
		return nil, err
	}

	if err := downloadAndInstall(assetURL); err != nil {
		return nil, err
	}

	result.Updated = true
	return result, nil
}

type releaseResponse struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchLatestRelease() (*releaseResponse, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "yaat-sidecar-selfupdate")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to contact GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected GitHub response: %s", resp.Status)
	}

	var release releaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release data: %w", err)
	}

	return &release, nil
}

func sameVersion(current, remote string) bool {
	clean := func(v string) string {
		return strings.TrimPrefix(strings.TrimSpace(v), "v")
	}
	return clean(current) == clean(remote)
}

func findAssetURL(release *releaseResponse) (string, error) {
	target := fmt.Sprintf("yaat-sidecar-%s-%s.tar.gz", goOS(), goArch())
	for _, asset := range release.Assets {
		if asset.Name == target {
			return asset.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no binary available for %s/%s", goOS(), goArch())
}

func downloadAndInstall(url string) error {
	tmpDir, err := os.MkdirTemp("", "yaat-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, "sidecar.tar.gz")
	if err := downloadFile(url, archivePath); err != nil {
		return err
	}

	extracted, err := extractBinary(archivePath, tmpDir)
	if err != nil {
		return err
	}

	return replaceCurrentBinary(extracted)
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("failed to save download: %w", err)
	}
	return nil
}

func extractBinary(archivePath, destDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("invalid archive: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		switch {
		case errors.Is(err, io.EOF):
			return "", fmt.Errorf("binary not found in archive")
		case err != nil:
			return "", fmt.Errorf("failed to extract archive: %w", err)
		case header.Typeflag != tar.TypeReg:
			continue
		}

		target := filepath.Join(destDir, filepath.Base(header.Name))
		out, err := os.Create(target)
		if err != nil {
			return "", err
		}

		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return "", fmt.Errorf("failed to write binary: %w", err)
		}
		out.Close()

		if err := os.Chmod(target, 0o755); err != nil {
			return "", err
		}

		return target, nil
	}
}

func replaceCurrentBinary(newBinary string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}

	destDir := filepath.Dir(exePath)
	tempDest := filepath.Join(destDir, ".yaat-sidecar.tmp")
	if err := copyFile(newBinary, tempDest); err != nil {
		return err
	}

	if err := os.Chmod(tempDest, 0o755); err != nil {
		return err
	}

	if err := os.Rename(tempDest, exePath); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("permission denied replacing %s (try running with sudo)", exePath)
		}
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	return nil
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer target.Close()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return nil
}

func goOS() string {
	switch runtime.GOOS {
	case "darwin", "linux":
		return runtime.GOOS
	default:
		return runtime.GOOS
	}
}

func goArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}
