// Unit tests for codingagent/experimental/plugins.

package plugins_test

import (
	"crypto/sha256"
	"encoding/base64"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/keejkrej/pi-go/chord/node"
	"github.com/keejkrej/pi-go/codingagent/experimental/plugins"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
)

func TestPluginProfilesAndBundledData(t *testing.T) {
	serverId := protocol.ServerId("00000000-0000-4000-8000-000000000001")

	t.Run("restores and clears server plugin package profiles", func(t *testing.T) {
		dir := t.TempDir()
		pkgA := filepath.Join(dir, "plugin-a")
		pkgB := filepath.Join(dir, "plugin-b")
		want := []string{pkgA, pkgB}

		got, err := plugins.RestoreServerPluginPackageProfile(dir, serverId, want)
		if err != nil {
			t.Fatalf("Restore set failed: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}

		restored, err := plugins.RestoreServerPluginPackageProfile(dir, serverId)
		if err != nil {
			t.Fatalf("Restore get failed: %v", err)
		}
		if !reflect.DeepEqual(restored, want) {
			t.Fatalf("got %v, want %v", restored, want)
		}

		cleared, err := plugins.RestoreServerPluginPackageProfile(dir, serverId, []string{})
		if err != nil {
			t.Fatalf("Restore clear failed: %v", err)
		}
		if len(cleared) != 0 {
			t.Fatalf("expected empty slice, got %v", cleared)
		}

		afterClear, err := plugins.RestoreServerPluginPackageProfile(dir, serverId)
		if err != nil {
			t.Fatalf("Restore after clear failed: %v", err)
		}
		if len(afterClear) != 0 {
			t.Fatalf("expected empty slice after clear, got %v", afterClear)
		}
	})

	t.Run("persists and removes per-session plugin package profiles", func(t *testing.T) {
		dir := t.TempDir()
		sessionPath := filepath.Join(dir, "sessions", "demo-1")
		if err := plugins.WriteSessionPluginPackageProfile(dir, serverId, sessionPath, []string{}); err != nil {
			t.Fatalf("Write empty profile failed: %v", err)
		}
		got, exists, err := plugins.ReadSessionPluginPackageProfile(dir, serverId, sessionPath)
		if err != nil || !exists || len(got) != 0 {
			t.Fatalf("Read empty profile = (%v, %v, %v)", got, exists, err)
		}

		pkg := filepath.Join(dir, "my-plugin")
		if err := plugins.WriteSessionPluginPackageProfile(dir, serverId, sessionPath, []string{pkg}); err != nil {
			t.Fatalf("Write profile failed: %v", err)
		}
		got, exists, err = plugins.ReadSessionPluginPackageProfile(dir, serverId, sessionPath)
		if err != nil || !exists || !reflect.DeepEqual(got, []string{pkg}) {
			t.Fatalf("Read profile = (%v, %v, %v)", got, exists, err)
		}

		if err := plugins.RemoveSessionPluginPackageProfile(dir, serverId, sessionPath); err != nil {
			t.Fatalf("Remove profile failed: %v", err)
		}
		_, exists, err = plugins.ReadSessionPluginPackageProfile(dir, serverId, sessionPath)
		if err != nil || exists {
			t.Fatalf("expected removed profile, got (%v, %v)", exists, err)
		}
	})

	t.Run("encodes and creates presentation facet loaders", func(t *testing.T) {
		source := "module.exports = {};"
		sum := sha256.Sum256([]byte(source))
		data, err := plugins.CreatePresentationFacetData([]node.FacetBundleArtifact{
			{
				Format:        node.FacetBundleArtifactFormat,
				FormatVersion: node.FacetBundleArtifactFormatVersion,
				Plugin:        node.FacetBundlePlugin{Id: "test-plugin"},
				EntryName:     "tui",
				Entry: node.FacetBundleEntry{
					File:            "tui.cjs",
					Integrity:       "sha256-" + base64.StdEncoding.EncodeToString(sum[:]),
					ExternalImports: []string{},
				},
				Source: source,
			},
		})
		if err != nil {
			t.Fatalf("CreatePresentationFacetData failed: %v", err)
		}
		loaders, err := plugins.CreatePresentationFacetLoaders(data)
		if err != nil {
			t.Fatalf("CreatePresentationFacetLoaders failed: %v", err)
		}
		if len(loaders) != 1 {
			t.Fatalf("expected 1 loader, got %d", len(loaders))
		}
		emptyLoaders, err := plugins.CreatePresentationFacetLoaders(jsonx.NewObject())
		if err != nil || len(emptyLoaders) != 0 {
			t.Fatalf("empty data loaders = (%d, %v)", len(emptyLoaders), err)
		}
		if loader, err := plugins.CreateSessionPluginFacetLoader(nil); err != nil || loader != nil {
			t.Fatalf("empty manifestPaths = (%v, %v)", loader, err)
		}
	})
}
