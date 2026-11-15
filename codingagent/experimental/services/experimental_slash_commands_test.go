// Ported from packages/coding-agent/test/experimental-slash-commands.test.ts (pi v1.0.0).

package services_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
)

func tescNames(cmds []services.SlashCommandContribution) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Name
	}
	return out
}

func TestSlashCommandRegistryRegistersAndRemovesContributions(t *testing.T) {
	registry := services.NewSlashCommandRegistry()
	var snapshots [][]string
	unsub := registry.Subscribe(func(commands []services.SlashCommandContribution) {
		snapshots = append(snapshots, tescNames(commands))
	})

	closeHello, err := registry.Register(services.SlashCommandContribution{
		Name:        "hello",
		Description: "Hello",
		Run:         func(_ string, _ chord.Context) (services.SlashCommandRunResult, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("Register hello failed: %v", err)
	}
	if got := tescNames(registry.List()); !reflect.DeepEqual(got, []string{"hello"}) {
		t.Fatalf("expected [hello], got %v", got)
	}
	if _, err := registry.Register(services.SlashCommandContribution{
		Name: "hello",
		Run:  func(_ string, _ chord.Context) (services.SlashCommandRunResult, error) { return nil, nil },
	}); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected already registered error, got %v", err)
	}

	closeHello()
	closeHello()
	if got := registry.List(); len(got) != 0 {
		t.Fatalf("expected empty list, got %v", got)
	}
	wantSnapshots := [][]string{{}, {"hello"}, {}}
	if !reflect.DeepEqual(snapshots, wantSnapshots) {
		t.Fatalf("expected snapshots %v, got %v", wantSnapshots, snapshots)
	}
	unsub()
}

func TestSlashCommandRegistryStagesReplacementsUntilPreviousRetires(t *testing.T) {
	registry := services.NewSlashCommandRegistry()
	first := services.SlashCommandContribution{
		Name:        "hello",
		Description: "First",
		Run:         func(_ string, _ chord.Context) (services.SlashCommandRunResult, error) { return nil, nil },
	}
	second := services.SlashCommandContribution{
		Name:        "hello",
		Description: "Second",
		Run:         func(_ string, _ chord.Context) (services.SlashCommandRunResult, error) { return nil, nil },
	}

	closeFirst, err := registry.Register(first)
	if err != nil {
		t.Fatalf("Register first failed: %v", err)
	}
	closeSecond, err := registry.Replace(second)
	if err != nil {
		t.Fatalf("Replace second failed: %v", err)
	}
	if got := registry.List(); len(got) != 1 || got[0].Description != "First" {
		t.Fatalf("expected First active, got %+v", got)
	}

	closeSecond()
	if got := registry.List(); len(got) != 1 || got[0].Description != "First" {
		t.Fatalf("expected First still active, got %+v", got)
	}

	closeReplacement, err := registry.Replace(second)
	if err != nil {
		t.Fatalf("Replace second again failed: %v", err)
	}
	closeFirst()
	if got := registry.List(); len(got) != 1 || got[0].Description != "Second" {
		t.Fatalf("expected Second active, got %+v", got)
	}
	closeReplacement()
	if got := registry.List(); len(got) != 0 {
		t.Fatalf("expected empty list, got %+v", got)
	}
}

func TestSlashCommandRegistryTracksPluginFacetReloadAndUnload(t *testing.T) {
	registry := services.NewSlashCommandRegistry()
	originalCalls := 0
	host, err := chord.CreateFacetHost(&chord.FacetOptions{
		Facets: []chord.Facet{
			services.CreateSlashCommandsRuntimeFacet(registry),
			chord.DefineFacet(chord.Facet{
				Id: "@test/example-hello",
				Setup: func(env chord.FacetEnvironment) error {
					commands := services.AsSlashCommands(env.Use(services.SlashCommandsService))
					env.OnActivate(func() error {
						unsub, err := commands.Replace(services.SlashCommandContribution{
							Name:        "hello",
							Description: "Original",
							Run: func(_ string, _ chord.Context) (services.SlashCommandRunResult, error) {
								originalCalls++
								return nil, nil
							},
						})
						if err != nil {
							return err
						}
						env.Own(func() error {
							unsub()
							return nil
						})
						return nil
					})
					return nil
				},
			}),
		},
	})
	if err != nil {
		t.Fatalf("CreateFacetHost failed: %v", err)
	}
	if got := tescNames(registry.List()); !reflect.DeepEqual(got, []string{"hello"}) {
		t.Fatalf("expected [hello], got %v", got)
	}

	failure := errors.New("replacement failed")
	if err := host.Reload([]chord.Facet{
		chord.DefineFacet(chord.Facet{
			Id: "@test/example-hello",
			Setup: func(env chord.FacetEnvironment) error {
				commands := services.AsSlashCommands(env.Use(services.SlashCommandsService))
				env.OnActivate(func() error {
					unsub, err := commands.Replace(services.SlashCommandContribution{
						Name:        "hello",
						Description: "Failing",
						Run:         func(_ string, _ chord.Context) (services.SlashCommandRunResult, error) { return nil, nil },
					})
					if err != nil {
						return err
					}
					env.Own(func() error {
						unsub()
						return nil
					})
					return failure
				})
				return nil
			},
		}),
	}); !errors.Is(err, failure) {
		t.Fatalf("expected reload error %v, got %v", failure, err)
	}

	list := registry.List()
	if len(list) != 1 || list[0].Description != "Original" {
		t.Fatalf("expected Original retained after failed reload, got %+v", list)
	}
	_, _ = list[0].Run("", chord.BackgroundContext)
	if originalCalls != 1 {
		t.Fatalf("expected original Run to be called, count=%d", originalCalls)
	}

	replacementCalls := 0
	if err := host.Reload([]chord.Facet{
		chord.DefineFacet(chord.Facet{
			Id: "@test/example-hello",
			Setup: func(env chord.FacetEnvironment) error {
				commands := services.AsSlashCommands(env.Use(services.SlashCommandsService))
				env.OnActivate(func() error {
					unsub, err := commands.Replace(services.SlashCommandContribution{
						Name:        "hello",
						Description: "Replacement",
						Run: func(_ string, _ chord.Context) (services.SlashCommandRunResult, error) {
							replacementCalls++
							return nil, nil
						},
					})
					if err != nil {
						return err
					}
					env.Own(func() error {
						unsub()
						return nil
					})
					return nil
				})
				return nil
			},
		}),
	}); err != nil {
		t.Fatalf("Reload replacement failed: %v", err)
	}

	list = registry.List()
	if len(list) != 1 || list[0].Description != "Replacement" {
		t.Fatalf("expected Replacement command, got %+v", list)
	}
	_, _ = list[0].Run("", chord.BackgroundContext)
	if replacementCalls != 1 {
		t.Fatalf("expected replacement Run to be called, count=%d", replacementCalls)
	}

	if err := host.Dispose(); err != nil {
		t.Fatalf("host.Dispose failed: %v", err)
	}
	if got := registry.List(); len(got) != 0 {
		t.Fatalf("expected empty registry after dispose, got %+v", got)
	}
}
