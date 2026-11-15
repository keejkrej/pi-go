// Ported from packages/coding-agent/src/experimental/plugins/package.ts (pi v1.0.0).

package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/keejkrej/pi-go/chord/node"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/protocol"
)

const packPluginPackageProfileVersion = 1

var packDefaultPluginFacets = func() *omap.Map[string, string] {
	m := omap.NewMap[string, string]()
	m.Set("session", "src/session.ts")
	m.Set("tui", "src/tui.ts")
	return m
}()

var packBuildNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// ConfiguredServerPluginPackage represents a server-owned builder for one configured plugin package.
type ConfiguredServerPluginPackage interface {
	ManifestPath() string
	Build() ([]node.FacetBundleArtifact, error)
}

type packServerPluginPackage struct {
	mu           sync.Mutex
	packagePath  string
	outdir       string
	manifestPath string
}

func (p *packServerPluginPackage) ManifestPath() string {
	return p.manifestPath
}

func (p *packServerPluginPackage) Build() ([]node.FacetBundleArtifact, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	result, err := node.BundleFacetPackage(&node.BundleFacetPackageOptions{
		PackagePath:   p.packagePath,
		Outdir:        p.outdir,
		DefaultFacets: packDefaultPluginFacets,
	})
	if err != nil {
		return nil, err
	}
	if _, hasTui := result.Manifest.Entries.Get("tui"); !hasTui {
		return nil, nil
	}
	artifact, err := node.ReadFacetBundleArtifact(&node.ReadFacetBundleArtifactOptions{
		ManifestPath: node.FacetBundlePathString(result.ManifestPath),
		Entry:        "tui",
	})
	if err != nil {
		return nil, err
	}
	if artifact == nil {
		return nil, nil
	}
	return []node.FacetBundleArtifact{*artifact}, nil
}

// RestoreServerPluginPackageProfile persists an explicit plugin package selection or restores it for a later server generation.
// Omitting configuredPackagePaths (or passing nil) restores the persisted selection; passing an explicit empty slice clears it.
func RestoreServerPluginPackageProfile(directory string, serverId protocol.ServerId, configuredPackagePaths ...[]string) ([]string, error) {
	profilePath := filepath.Join(directory, fmt.Sprintf("plugin-packages-%s.json", serverId))
	if len(configuredPackagePaths) == 0 || configuredPackagePaths[0] == nil {
		restored, exists, err := packReadPluginPackageProfile(profilePath, false, "")
		if err != nil {
			return nil, err
		}
		if !exists {
			return []string{}, nil
		}
		return restored, nil
	}
	packagePaths, err := NormalizePluginPackagePaths(configuredPackagePaths[0])
	if err != nil {
		return nil, err
	}
	if len(packagePaths) == 0 {
		if err := os.Remove(profilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return []string{}, nil
	}
	if err := packWritePluginPackageProfile(profilePath, packagePaths, ""); err != nil {
		return nil, err
	}
	return packagePaths, nil
}

// ReadSessionPluginPackageProfile reads the explicit plugin selection stored for one durable Session.
// Returns (nil, false, nil) when no profile exists yet.
func ReadSessionPluginPackageProfile(directory string, serverId protocol.ServerId, sessionPath string) ([]string, bool, error) {
	return packReadPluginPackageProfile(packSessionPluginProfilePath(directory, serverId, sessionPath), true, sessionPath)
}

// RemoveSessionPluginPackageProfile removes the plugin selection stored for one deleted Session.
func RemoveSessionPluginPackageProfile(directory string, serverId protocol.ServerId, sessionPath string) error {
	err := os.Remove(packSessionPluginProfilePath(directory, serverId, sessionPath))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// WriteSessionPluginPackageProfile persists the plugin selection for one durable Session.
func WriteSessionPluginPackageProfile(directory string, serverId protocol.ServerId, sessionPath string, packagePaths []string) error {
	normalized, err := NormalizePluginPackagePaths(packagePaths)
	if err != nil {
		return err
	}
	return packWritePluginPackageProfile(packSessionPluginProfilePath(directory, serverId, sessionPath), normalized, sessionPath)
}

// CreateServerPluginPackage creates a serialized server-owned builder for one configured plugin package.
func CreateServerPluginPackage(directory string, serverId protocol.ServerId, packagePath string) (ConfiguredServerPluginPackage, error) {
	normalizedPackagePath, err := filepath.Abs(packagePath)
	if err != nil {
		return nil, err
	}
	outdir := filepath.Join(directory, "plugin-builds", string(serverId), packPluginBuildDirectoryName(normalizedPackagePath))
	manifestPath := filepath.Join(outdir, node.FacetBundleManifestFile)
	return &packServerPluginPackage{
		packagePath:  normalizedPackagePath,
		outdir:       outdir,
		manifestPath: manifestPath,
	}, nil
}

// NormalizePluginPackagePaths resolves and validates uniqueness of plugin package paths.
func NormalizePluginPackagePaths(packagePaths []string) ([]string, error) {
	normalized := make([]string, 0, len(packagePaths))
	seen := make(map[string]struct{}, len(packagePaths))
	for _, pkgPath := range packagePaths {
		if pkgPath == "" {
			return nil, errors.New("Plugin package path must not be empty")
		}
		absPath, err := filepath.Abs(pkgPath)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[absPath]; dup {
			return nil, errors.New("Plugin package paths must be unique")
		}
		seen[absPath] = struct{}{}
		normalized = append(normalized, absPath)
	}
	return normalized, nil
}

func packReadPluginPackageProfile(path string, allowEmpty bool, expectedSessionPath string) ([]string, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("Could not read experimental plugin package profile %s: %w", path, err)
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s: %w", path, err)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s", path)
	}
	for _, key := range obj.Keys() {
		if key != "version" && key != "packagePaths" && !(key == "sessionPath" && expectedSessionPath != "") {
			return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s", path)
		}
	}
	versionVal, hasVersion := obj.Get("version")
	if !hasVersion {
		return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s", path)
	}
	versionNum, ok := versionVal.(float64)
	if !ok || versionNum != float64(packPluginPackageProfileVersion) {
		return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s", path)
	}
	sessionVal, hasSession := obj.Get("sessionPath")
	if expectedSessionPath == "" {
		if hasSession {
			return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s", path)
		}
	} else {
		sessionStr, ok := sessionVal.(string)
		if !hasSession || !ok || sessionStr != expectedSessionPath {
			return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s", path)
		}
	}
	pathsVal, hasPaths := obj.Get("packagePaths")
	if !hasPaths {
		return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s", path)
	}
	pathsArr, ok := pathsVal.([]any)
	if !ok || (!allowEmpty && len(pathsArr) == 0) {
		return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s", path)
	}
	rawPaths := make([]string, 0, len(pathsArr))
	for _, entry := range pathsArr {
		s, ok := entry.(string)
		if !ok || s == "" {
			return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s", path)
		}
		rawPaths = append(rawPaths, s)
	}
	normalized, err := NormalizePluginPackagePaths(rawPaths)
	if err != nil {
		return nil, false, fmt.Errorf("Invalid experimental plugin package profile %s: %w", path, err)
	}
	return normalized, true, nil
}

func packWritePluginPackageProfile(path string, packagePaths []string, sessionPath string) error {
	obj := jsonx.NewObject()
	obj.Set("version", float64(packPluginPackageProfileVersion))
	if sessionPath != "" {
		obj.Set("sessionPath", sessionPath)
	}
	items := make([]any, 0, len(packagePaths))
	for _, p := range packagePaths {
		items = append(items, p)
	}
	obj.Set("packagePaths", items)
	data, err := jsonx.MarshalIndent(obj, "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func packSessionPluginProfilePath(directory string, serverId protocol.ServerId, sessionPath string) string {
	sum := sha256.Sum256([]byte(sessionPath))
	hash := hex.EncodeToString(sum[:])[:24]
	return filepath.Join(directory, fmt.Sprintf("session-plugin-packages-%s-%s.json", serverId, hash))
}

func packPluginBuildDirectoryName(packagePath string) string {
	packageDirectory := packagePath
	if filepath.Base(packagePath) == "package.json" {
		packageDirectory = filepath.Dir(packagePath)
	}
	base := packBuildNameSanitizer.ReplaceAllString(filepath.Base(packageDirectory), "-")
	if base == "" {
		base = "plugin"
	}
	label := base
	if len(base) < len("-plugin") || base[len(base)-len("-plugin"):] != "-plugin" {
		label = base + "-plugin"
	}
	sum := sha256.Sum256([]byte(packagePath))
	hash := hex.EncodeToString(sum[:])[:12]
	return fmt.Sprintf("%s-%s", label, hash)
}
