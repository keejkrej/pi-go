// Ported from packages/coding-agent/src/core/package-manager.ts (pi v1.5.2).
// Barebone build stub: external package/plugin management is unsupported.

package core

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

// ErrPackagesUnsupported is returned by package mutation operations in the barebone build.
var ErrPackagesUnsupported = errors.New("pi-go barebone build does not support external packages or plugins")

// PathMetadata records source provenance for a discovered resource path.
type PathMetadata = utils.PathMetadata

// ResolvedResource describes a discovered resource path paired with enablement and provenance.
type ResolvedResource struct {
	Path     string       `json:"path"`
	Enabled  bool         `json:"enabled"`
	Metadata PathMetadata `json:"metadata"`
}

// ResolvedPaths groups resolved resource lists by category.
type ResolvedPaths struct {
	Extensions []ResolvedResource `json:"extensions"`
	Skills     []ResolvedResource `json:"skills"`
	Prompts    []ResolvedResource `json:"prompts"`
	Themes     []ResolvedResource `json:"themes"`
}

// PackageInstallFailure describes a failed package installation attempt.
type PackageInstallFailure struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

// MissingSourceAction determines how PackageManager reacts to missing remote sources.
type MissingSourceAction = string

const (
	MissingSourceActionInstall MissingSourceAction = "install"
	MissingSourceActionSkip    MissingSourceAction = "skip"
	MissingSourceActionError   MissingSourceAction = "error"
)

// ProgressEvent reports package resolution/installation progress.
type ProgressEvent struct {
	Type    string  `json:"type"`
	Action  string  `json:"action"`
	Source  string  `json:"source"`
	Message *string `json:"message,omitzero"`
}

// ProgressCallback receives package manager progress updates.
type ProgressCallback func(event ProgressEvent)

// PackageUpdate describes an available package update.
type PackageUpdate struct {
	Source         string  `json:"source"`
	DisplayName    string  `json:"displayName"`
	Type           string  `json:"type"`
	Scope          string  `json:"scope"`
	CurrentVersion *string `json:"currentVersion,omitzero"`
	LatestVersion  *string `json:"latestVersion,omitzero"`
}

// ConfiguredPackage describes a configured package source entry.
type ConfiguredPackage struct {
	Source        string  `json:"source"`
	Scope         string  `json:"scope"`
	InstalledPath *string `json:"installedPath,omitzero"`
}

// PackageInstallOptions configures package installation.
type PackageInstallOptions struct {
	Local bool
}

// PackageRemoveOptions configures package removal.
type PackageRemoveOptions struct {
	Local bool
}

// ResolveExtensionSourcesOptions configures temporary extension/package source resolution.
type ResolveExtensionSourcesOptions struct {
	Temporary bool
	Local     bool
}

// PackageManager defines the resource package management contract.
type PackageManager interface {
	Resolve(ctx context.Context, onMissing ...func(source string) (MissingSourceAction, error)) (ResolvedPaths, error)
	ResolveExtensionSources(ctx context.Context, sources []string, options ...*ResolveExtensionSourcesOptions) (ResolvedPaths, error)
	Install(ctx context.Context, source string, options ...*PackageInstallOptions) error
	Remove(ctx context.Context, source string, options ...*PackageRemoveOptions) error
	Update(ctx context.Context, source ...string) error
	GetInstalledPackages() []ConfiguredPackage
	GetInstalledPath(source string, scope ...string) *string
	SetProgressCallback(cb ProgressCallback)
	AddSourceToSettings(source string, local bool) bool
	RemoveSourceFromSettings(source string, local bool) bool
}

// DefaultPackageManagerOptions configures NewDefaultPackageManager.
type DefaultPackageManagerOptions struct {
	Cwd             string
	AgentDir        string
	SettingsManager *SettingsManager
}

// DefaultPackageManager is the barebone stub PackageManager implementation.
type DefaultPackageManager struct {
	cwd             string
	agentDir        string
	settingsManager *SettingsManager
	progressCb      ProgressCallback
}

// NewDefaultPackageManager creates a barebone DefaultPackageManager stub.
func NewDefaultPackageManager(options DefaultPackageManagerOptions) *DefaultPackageManager {
	return &DefaultPackageManager{
		cwd:             options.Cwd,
		agentDir:        options.AgentDir,
		settingsManager: options.SettingsManager,
	}
}

// IsOfflineModeEnabled reports whether PI_OFFLINE is enabled in the environment.
func IsOfflineModeEnabled() bool {
	val := strings.TrimSpace(os.Getenv("PI_OFFLINE"))
	return val == "1" || strings.EqualFold(val, "true")
}

// SetProgressCallback stores an optional progress listener.
func (pm *DefaultPackageManager) SetProgressCallback(cb ProgressCallback) {
	pm.progressCb = cb
}

// Resolve returns empty resource lists in the barebone build (no npm/git packages).
func (pm *DefaultPackageManager) Resolve(_ context.Context, _ ...func(source string) (MissingSourceAction, error)) (ResolvedPaths, error) {
	return ResolvedPaths{
		Extensions: []ResolvedResource{},
		Skills:     []ResolvedResource{},
		Prompts:    []ResolvedResource{},
		Themes:     []ResolvedResource{},
	}, nil
}

// ResolveExtensionSources returns empty resource lists in the barebone build.
func (pm *DefaultPackageManager) ResolveExtensionSources(_ context.Context, _ []string, _ ...*ResolveExtensionSourcesOptions) (ResolvedPaths, error) {
	return ResolvedPaths{
		Extensions: []ResolvedResource{},
		Skills:     []ResolvedResource{},
		Prompts:    []ResolvedResource{},
		Themes:     []ResolvedResource{},
	}, nil
}

// Install returns ErrPackagesUnsupported in the barebone build.
func (pm *DefaultPackageManager) Install(_ context.Context, _ string, _ ...*PackageInstallOptions) error {
	return ErrPackagesUnsupported
}

// Remove returns ErrPackagesUnsupported in the barebone build.
func (pm *DefaultPackageManager) Remove(_ context.Context, _ string, _ ...*PackageRemoveOptions) error {
	return ErrPackagesUnsupported
}

// Update returns ErrPackagesUnsupported in the barebone build.
func (pm *DefaultPackageManager) Update(_ context.Context, _ ...string) error {
	return ErrPackagesUnsupported
}

// GetInstalledPackages returns an empty slice in the barebone build.
func (pm *DefaultPackageManager) GetInstalledPackages() []ConfiguredPackage {
	return []ConfiguredPackage{}
}

// GetInstalledPath always returns nil in the barebone build.
func (pm *DefaultPackageManager) GetInstalledPath(_ string, _ ...string) *string {
	return nil
}

// AddSourceToSettings is unsupported in the barebone build and returns false.
func (pm *DefaultPackageManager) AddSourceToSettings(_ string, _ bool) bool {
	return false
}

// RemoveSourceFromSettings is unsupported in the barebone build and returns false.
func (pm *DefaultPackageManager) RemoveSourceFromSettings(_ string, _ bool) bool {
	return false
}
