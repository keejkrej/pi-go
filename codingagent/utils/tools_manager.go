// Ported from packages/coding-agent/src/utils/tools-manager.ts (pi v1.5.2).

package utils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ToolStatus reports progress or warnings during managed binary tool resolution/download.
type ToolStatus struct {
	Type    string `json:"type"` // "info" | "warning" | "error"
	Message string `json:"message"`
}

type tmToolConfig struct {
	Name              string
	Repo              string
	BinaryName        string
	SystemBinaryNames []string
	TagPrefix         string
	GetAssetUrl       func(version, plat, arch string) string
}

var (
	tmPlatform   = func() string { return runtime.GOOS }
	tmArch       = tmDefaultArch
	tmLookPath   = exec.LookPath
	tmStatExists = tmDefaultExists
	tmFetch      = FetchWithRetry
	tmBinDir     = GetBinDir
	tmGetenv     = os.Getenv
)

func tmDefaultArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return runtime.GOARCH
	}
}

func tmDefaultExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func tmNormalizePlatform(plat string) string {
	if plat == "windows" {
		return "win32"
	}
	return plat
}

var tmTools = map[string]tmToolConfig{
	"fd": {
		Name:              "fd",
		Repo:              "sharkdp/fd",
		BinaryName:        "fd",
		SystemBinaryNames: []string{"fd", "fdfind"},
		TagPrefix:         "v",
		GetAssetUrl: func(version, plat, arch string) string {
			tag := "v" + version
			switch {
			case plat == "darwin" && arch == "arm64":
				return fmt.Sprintf("https://github.com/sharkdp/fd/releases/download/%s/fd-%s-aarch64-apple-darwin.tar.gz", tag, tag)
			case plat == "darwin" && arch == "x64":
				return fmt.Sprintf("https://github.com/sharkdp/fd/releases/download/%s/fd-%s-x86_64-apple-darwin.tar.gz", tag, tag)
			case plat == "linux" && arch == "arm64":
				return fmt.Sprintf("https://github.com/sharkdp/fd/releases/download/%s/fd-%s-aarch64-unknown-linux-musl.tar.gz", tag, tag)
			case plat == "linux" && arch == "x64":
				return fmt.Sprintf("https://github.com/sharkdp/fd/releases/download/%s/fd-%s-x86_64-unknown-linux-musl.tar.gz", tag, tag)
			case plat == "win32" && arch == "x64":
				return fmt.Sprintf("https://github.com/sharkdp/fd/releases/download/%s/fd-%s-x86_64-pc-windows-msvc.zip", tag, tag)
			case plat == "win32" && arch == "arm64":
				return fmt.Sprintf("https://github.com/sharkdp/fd/releases/download/%s/fd-%s-aarch64-pc-windows-msvc.zip", tag, tag)
			default:
				return ""
			}
		},
	},
	"rg": {
		Name:              "ripgrep",
		Repo:              "BurntSushi/ripgrep",
		BinaryName:        "rg",
		SystemBinaryNames: []string{"rg"},
		GetAssetUrl: func(version, plat, arch string) string {
			switch {
			case plat == "darwin" && arch == "arm64":
				return fmt.Sprintf("https://github.com/BurntSushi/ripgrep/releases/download/%s/ripgrep-%s-aarch64-apple-darwin.tar.gz", version, version)
			case plat == "darwin" && arch == "x64":
				return fmt.Sprintf("https://github.com/BurntSushi/ripgrep/releases/download/%s/ripgrep-%s-x86_64-apple-darwin.tar.gz", version, version)
			case plat == "linux" && arch == "arm64":
				return fmt.Sprintf("https://github.com/BurntSushi/ripgrep/releases/download/%s/ripgrep-%s-aarch64-unknown-linux-musl.tar.gz", version, version)
			case plat == "linux" && arch == "x64":
				return fmt.Sprintf("https://github.com/BurntSushi/ripgrep/releases/download/%s/ripgrep-%s-x86_64-unknown-linux-musl.tar.gz", version, version)
			case plat == "win32" && arch == "x64":
				return fmt.Sprintf("https://github.com/BurntSushi/ripgrep/releases/download/%s/ripgrep-%s-x86_64-pc-windows-msvc.zip", version, version)
			default:
				return ""
			}
		},
	},
}

var tmReleaseTagRe = regexp.MustCompile(`/releases/tag/v?([^/?#]+)$`)

// GetToolPath returns the path to an installed tool binary (checking bundled bin dir first,
// then system PATH), or nil if not found.
func GetToolPath(tool string) *string {
	cfg, ok := tmTools[tool]
	if !ok {
		return nil
	}
	binName := cfg.BinaryName
	if tmNormalizePlatform(tmPlatform()) == "win32" {
		binName += ".exe"
	}
	localPath := filepath.Join(tmBinDir(), binName)
	if tmStatExists(localPath) {
		return &localPath
	}
	candidates := cfg.SystemBinaryNames
	if len(candidates) == 0 {
		candidates = []string{cfg.BinaryName}
	}
	for _, candidate := range candidates {
		if systemPath, err := tmLookPath(candidate); err == nil && systemPath != "" {
			return &systemPath
		}
	}
	return nil
}

// GetLatestVersion resolves the latest release tag version for a GitHub owner/repo
// via the /releases/latest HTTP redirect without invoking the rate-limited JSON API.
func GetLatestVersion(ctx context.Context, repo string) (string, error) {
	latestURL := fmt.Sprintf("https://github.com/%s/releases/latest", repo)
	follow := false
	resp, err := tmFetch(ctx, latestURL, &FetchWithRetryOptions{
		Headers:         http.Header{"User-Agent": {GetPiUserAgent(VERSION)}},
		FollowRedirects: &follow,
	})
	if err != nil {
		return "", err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	location := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode >= 400 || location == "" {
		return "", fmt.Errorf("Failed to resolve latest %s release: HTTP %d without redirect", repo, resp.StatusCode)
	}

	baseParsed, _ := url.Parse(latestURL)
	locParsed, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("Failed to resolve latest %s release: unexpected redirect to %s", repo, location)
	}
	resolved := baseParsed.ResolveReference(locParsed)
	m := tmReleaseTagRe.FindStringSubmatch(resolved.Path)
	if len(m) != 2 {
		return "", fmt.Errorf("Failed to resolve latest %s release: unexpected redirect to %s", repo, resolved.String())
	}
	return m[1], nil
}

func tmFormatDownloadErr(err error) string {
	var parts []string
	for cur := err; cur != nil; {
		msg := strings.TrimSpace(cur.Error())
		unwrapper, ok := cur.(interface{ Unwrap() error })
		var next error
		if ok {
			next = unwrapper.Unwrap()
		}
		if next != nil && strings.HasSuffix(msg, ": "+next.Error()) {
			msg = strings.TrimSuffix(msg, ": "+next.Error())
		}
		if msg != "" {
			parts = append(parts, msg)
		}
		cur = next
	}
	return strings.Join(parts, ": ")
}

func tmExtractTarGzBinary(archiveBytes []byte, binaryName, destPath string) error {
	gz, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == binaryName {
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return err
			}
			tmpPath := destPath + ".tmp"
			f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				_ = os.Remove(tmpPath)
				return copyErr
			}
			if closeErr != nil {
				_ = os.Remove(tmpPath)
				return closeErr
			}
			return os.Rename(tmpPath, destPath)
		}
	}
	return fmt.Errorf("binary %s not found in archive", binaryName)
}

func tmExtractZipBinary(archivePath, binaryName, destPath string) error {
	tmpDir, err := os.MkdirTemp(filepath.Dir(destPath), ".tm-extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	if err := ExtractZip(archivePath, tmpDir); err != nil {
		return err
	}
	var found string
	_ = filepath.WalkDir(tmpDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr == nil && !d.IsDir() && strings.EqualFold(d.Name(), binaryName) {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	if found == "" {
		return fmt.Errorf("binary %s not found in archive", binaryName)
	}
	return os.Rename(found, destPath)
}

func tmIsOffline() bool {
	v := strings.ToLower(strings.TrimSpace(tmGetenv("PI_OFFLINE")))
	return v == "1" || v == "true" || v == "yes"
}

// EnsureTool locates tool ("fd" or "rg") or downloads it into GetBinDir when supported
// and online. Progress and non-fatal failures are reported to onStatus without stdout noise.
func EnsureTool(ctx context.Context, tool string, onStatus func(ToolStatus)) *string {
	if existing := GetToolPath(tool); existing != nil {
		return existing
	}

	cfg, ok := tmTools[tool]
	if !ok {
		return nil
	}

	if tmIsOffline() {
		if onStatus != nil {
			onStatus(ToolStatus{
				Type:    "warning",
				Message: fmt.Sprintf("%s not found. Offline mode enabled, skipping download.", cfg.Name),
			})
		}
		return nil
	}

	plat := tmNormalizePlatform(tmPlatform())
	arch := tmArch()
	if plat == "android" {
		pkgName := cfg.BinaryName
		if tool == "rg" {
			pkgName = "ripgrep"
		}
		if onStatus != nil {
			onStatus(ToolStatus{
				Type:    "warning",
				Message: fmt.Sprintf("%s not found. Install with: pkg install %s", cfg.Name, pkgName),
			})
		}
		return nil
	}

	// Verify platform/arch before attempting network requests.
	if cfg.GetAssetUrl("0.0.0", plat, arch) == "" {
		if onStatus != nil {
			onStatus(ToolStatus{
				Type: "warning",
				Message: fmt.Sprintf(
					"%s not found. Automatic download is not supported on %s/%s. Install %s with your system package manager.",
					cfg.Name, plat, arch, cfg.Name,
				),
			})
		}
		return nil
	}

	if onStatus != nil {
		onStatus(ToolStatus{
			Type:    "info",
			Message: fmt.Sprintf("%s not found. Downloading...", cfg.Name),
		})
	}

	var version string
	var err error
	if plat == "darwin" && arch == "x64" && tool == "fd" {
		// fd v10.3.0 is the last release publishing x86_64-apple-darwin prebuilt archives.
		version = "10.3.0"
	} else {
		version, err = GetLatestVersion(ctx, cfg.Repo)
		if err != nil {
			if onStatus != nil {
				onStatus(ToolStatus{
					Type:    "warning",
					Message: fmt.Sprintf("Failed to download %s: %s", cfg.Name, tmFormatDownloadErr(err)),
				})
			}
			return nil
		}
	}

	assetURL := cfg.GetAssetUrl(version, plat, arch)
	if assetURL == "" {
		return nil
	}

	resp, err := tmFetch(ctx, assetURL, &FetchWithRetryOptions{
		Headers: http.Header{"User-Agent": {GetPiUserAgent(VERSION)}},
	})
	if err != nil {
		if onStatus != nil {
			onStatus(ToolStatus{
				Type:    "warning",
				Message: fmt.Sprintf("Failed to download %s: %s", cfg.Name, tmFormatDownloadErr(err)),
			})
		}
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		if onStatus != nil {
			onStatus(ToolStatus{
				Type:    "warning",
				Message: fmt.Sprintf("Failed to download %s: %s", cfg.Name, errors.New(resp.Status)),
			})
		}
		return nil
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		if onStatus != nil {
			onStatus(ToolStatus{
				Type:    "warning",
				Message: fmt.Sprintf("Failed to download %s: %s", cfg.Name, tmFormatDownloadErr(err)),
			})
		}
		return nil
	}

	binDir := tmBinDir()
	binName := cfg.BinaryName
	if plat == "win32" {
		binName += ".exe"
	}
	destPath := filepath.Join(binDir, binName)

	if strings.HasSuffix(assetURL, ".zip") {
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return nil
		}
		tmpZip := destPath + ".zip"
		if err := os.WriteFile(tmpZip, data, 0o600); err != nil {
			return nil
		}
		extractErr := tmExtractZipBinary(tmpZip, binName, destPath)
		_ = os.Remove(tmpZip)
		if extractErr != nil {
			if onStatus != nil {
				onStatus(ToolStatus{
					Type:    "warning",
					Message: fmt.Sprintf("Failed to download %s: %s", cfg.Name, tmFormatDownloadErr(extractErr)),
				})
			}
			return nil
		}
	} else {
		if err := tmExtractTarGzBinary(data, binName, destPath); err != nil {
			if onStatus != nil {
				onStatus(ToolStatus{
					Type:    "warning",
					Message: fmt.Sprintf("Failed to download %s: %s", cfg.Name, tmFormatDownloadErr(err)),
				})
			}
			return nil
		}
	}

	if onStatus != nil {
		onStatus(ToolStatus{
			Type:    "info",
			Message: fmt.Sprintf("%s installed to %s", cfg.Name, destPath),
		})
	}
	return &destPath
}
