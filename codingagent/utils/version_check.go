// Ported from packages/coding-agent/src/utils/version-check.ts (pi v1.5.2).

package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/semver"
)

// VersionCheckUrl is the endpoint queried to check for newer Pi releases.
const VersionCheckUrl = "https://pi.dev/api/latest-version"

// PiReleaseInfo holds release metadata returned by the Pi version-check API.
type PiReleaseInfo struct {
	PackageName string `json:"packageName,omitzero"`
	Version     string `json:"version"`
	Note        string `json:"note,omitzero"`
}

// VersionCheckOptions configures version check requests.
type VersionCheckOptions struct {
	Retry bool
}

var vcFetch = FetchWithRetry

// ComparePackageVersions compares two semantic package versions and returns
// >0 if a > b, 0 if a == b, and <0 if a < b.
func ComparePackageVersions(a, b string) int {
	va, okA := semver.Valid(a)
	vb, okB := semver.Valid(b)
	if okA && okB {
		return semver.Compare(va, vb)
	}
	if a == b {
		return 0
	}
	if a > b {
		return 1
	}
	return -1
}

// IsNewerPackageVersion reports whether latest represents a newer version than current.
func IsNewerPackageVersion(latest, current string) bool {
	return ComparePackageVersions(latest, current) > 0
}

// FormatVersionCheckError formats a network/version-check error with any unique nested
// error codes or root cause descriptions in parentheses.
func FormatVersionCheckError(err error) string {
	if err == nil {
		return ""
	}
	top := err.Error()
	var details []string
	seen := make(map[string]struct{})
	addDetail := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || s == top {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		details = append(details, s)
	}

	var walk func(cur error)
	walk = func(cur error) {
		if cur == nil {
			return
		}
		type multiUnwrapper interface {
			Unwrap() []error
		}
		type singleUnwrapper interface {
			Unwrap() error
		}
		type coded interface {
			ErrorCode() string
		}

		if c, ok := cur.(coded); ok {
			addDetail(c.ErrorCode())
			return
		}
		if m, ok := cur.(multiUnwrapper); ok {
			for _, child := range m.Unwrap() {
				walk(child)
			}
			return
		}
		if s, ok := cur.(singleUnwrapper); ok {
			next := s.Unwrap()
			if next != nil {
				walk(next)
				return
			}
		}
		if cur != err {
			addDetail("cause: " + cur.Error())
		}
	}
	walk(err)

	if len(details) == 0 {
		return top
	}
	return fmt.Sprintf("%s (%s)", top, strings.Join(details, ", "))
}

// GetLatestPiRelease queries the Pi version check endpoint and returns release metadata.
func GetLatestPiRelease(ctx context.Context, currentVersion string, options *VersionCheckOptions) (*PiReleaseInfo, error) {
	if os.Getenv("PI_OFFLINE") != "" {
		return nil, nil
	}
	retries := 0
	if options != nil && options.Retry {
		retries = ManagementHttpDefaultRetries
	}
	headers := http.Header{
		"User-Agent": {GetPiUserAgent(currentVersion)},
		"Accept":     {"application/json"},
	}
	resp, err := vcFetch(ctx, VersionCheckUrl, &FetchWithRetryOptions{
		Headers:   headers,
		Retries:   &retries,
		TimeoutMs: 10000,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var release PiReleaseInfo
	if err := jsonx.Unmarshal(body, &release); err != nil {
		return nil, err
	}
	release.Version = strings.TrimSpace(release.Version)
	release.PackageName = strings.TrimSpace(release.PackageName)
	release.Note = strings.TrimSpace(release.Note)
	if release.Version == "" {
		return nil, errors.New("missing version field in release response")
	}
	return &release, nil
}

// GetLatestPiVersion returns the latest Pi version string from the version check endpoint.
func GetLatestPiVersion(ctx context.Context, currentVersion string, options *VersionCheckOptions) (string, error) {
	release, err := GetLatestPiRelease(ctx, currentVersion, options)
	if err != nil || release == nil {
		return "", err
	}
	return release.Version, nil
}

// CheckForNewPiVersion checks once whether a newer Pi release is available unless
// automatic version checks are disabled via PI_SKIP_VERSION_CHECK. Returns nil when up to
// date, disabled, or if the check fails.
func CheckForNewPiVersion(ctx context.Context, currentVersion string) *PiReleaseInfo {
	if os.Getenv("PI_SKIP_VERSION_CHECK") != "" {
		return nil
	}
	release, err := GetLatestPiRelease(ctx, currentVersion, nil)
	if err != nil || release == nil {
		return nil
	}
	if !IsNewerPackageVersion(release.Version, currentVersion) {
		return nil
	}
	return release
}
