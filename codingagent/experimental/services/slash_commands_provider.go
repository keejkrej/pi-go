// Ported from packages/coding-agent/src/experimental/services/slash-commands-provider.ts (pi v1.0.0).

package services

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
)

var scpCommandNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9:-]*$`)

var scpThinkingDescriptions = map[ai.ModelThinkingLevel]string{
	ai.ModelThinkingLevelOff:     "No reasoning",
	ai.ModelThinkingLevelMinimal: "Very brief reasoning",
	ai.ModelThinkingLevelLow:     "Light reasoning",
	ai.ModelThinkingLevelMedium:  "Moderate reasoning",
	ai.ModelThinkingLevelHigh:    "Deep reasoning",
	ai.ModelThinkingLevelXhigh:   "Extra-high reasoning",
	ai.ModelThinkingLevelMax:     "Maximum reasoning",
}

type scpRegisteredSlashCommand struct {
	command SlashCommandContribution
	closed  bool
}

type scpCommandList struct {
	entries []*scpRegisteredSlashCommand
}

// SlashCommandRegistry is a process-local ordered registry of slash command contributions.
type SlashCommandRegistry struct {
	mu         sync.Mutex
	order      []string
	commands   map[string]*scpCommandList
	listeners  map[uint64]func(commands []SlashCommandContribution)
	nextListen uint64
}

// NewSlashCommandRegistry creates an empty SlashCommandRegistry.
func NewSlashCommandRegistry() *SlashCommandRegistry {
	return &SlashCommandRegistry{
		commands:  make(map[string]*scpCommandList),
		listeners: make(map[uint64]func(commands []SlashCommandContribution)),
	}
}

func (r *SlashCommandRegistry) Register(command SlashCommandContribution) (func(), error) {
	if err := scpValidateCommand(command); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if _, exists := r.commands[command.Name]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("Slash command /%s is already registered", command.Name)
	}
	unsub, listeners, snapshot := r.addLocked(command)
	r.mu.Unlock()
	scpNotifyListeners(listeners, snapshot)
	return unsub, nil
}

func (r *SlashCommandRegistry) Replace(command SlashCommandContribution) (func(), error) {
	if err := scpValidateCommand(command); err != nil {
		return nil, err
	}
	r.mu.Lock()
	unsub, listeners, snapshot := r.addLocked(command)
	r.mu.Unlock()
	scpNotifyListeners(listeners, snapshot)
	return unsub, nil
}

func (r *SlashCommandRegistry) List() []SlashCommandContribution {
	r.mu.Lock()
	snapshot := r.listLocked()
	r.mu.Unlock()
	return snapshot
}

func (r *SlashCommandRegistry) Subscribe(listener func(commands []SlashCommandContribution)) func() {
	if listener == nil {
		return func() {}
	}
	r.mu.Lock()
	r.nextListen++
	id := r.nextListen
	r.listeners[id] = listener
	snapshot := r.listLocked()
	r.mu.Unlock()

	listener(snapshot)
	return func() {
		r.mu.Lock()
		delete(r.listeners, id)
		r.mu.Unlock()
	}
}

func (r *SlashCommandRegistry) addLocked(command SlashCommandContribution) (func(), []func(commands []SlashCommandContribution), []SlashCommandContribution) {
	entry := &scpRegisteredSlashCommand{command: command}
	list, ok := r.commands[command.Name]
	if !ok {
		list = &scpCommandList{}
		r.commands[command.Name] = list
		r.order = append(r.order, command.Name)
	}
	list.entries = append(list.entries, entry)

	var listeners []func(commands []SlashCommandContribution)
	var snapshot []SlashCommandContribution
	if len(list.entries) == 1 {
		listeners, snapshot = r.snapshotPublishLocked()
	}

	unsub := func() {
		r.mu.Lock()
		if entry.closed {
			r.mu.Unlock()
			return
		}
		entry.closed = true
		if len(list.entries) == 0 || list.entries[0] != entry {
			r.mu.Unlock()
			return
		}
		for len(list.entries) > 0 && list.entries[0].closed {
			list.entries = list.entries[1:]
		}
		if len(list.entries) == 0 {
			delete(r.commands, command.Name)
			r.removeOrderLocked(command.Name)
		}
		ls, snap := r.snapshotPublishLocked()
		r.mu.Unlock()
		scpNotifyListeners(ls, snap)
	}
	return unsub, listeners, snapshot
}

func (r *SlashCommandRegistry) removeOrderLocked(name string) {
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			return
		}
	}
}

func (r *SlashCommandRegistry) listLocked() []SlashCommandContribution {
	out := make([]SlashCommandContribution, 0, len(r.order))
	for _, name := range r.order {
		list := r.commands[name]
		if list != nil && len(list.entries) > 0 {
			out = append(out, list.entries[0].command)
		}
	}
	return out
}

func (r *SlashCommandRegistry) snapshotPublishLocked() ([]func(commands []SlashCommandContribution), []SlashCommandContribution) {
	ls := make([]func(commands []SlashCommandContribution), 0, len(r.listeners))
	for _, fn := range r.listeners {
		ls = append(ls, fn)
	}
	return ls, r.listLocked()
}

func scpNotifyListeners(listeners []func(commands []SlashCommandContribution), snapshot []SlashCommandContribution) {
	for _, fn := range listeners {
		copied := make([]SlashCommandContribution, len(snapshot))
		copy(copied, snapshot)
		fn(copied)
	}
}

func scpValidateCommand(command SlashCommandContribution) error {
	if !scpCommandNameRegex.MatchString(command.Name) {
		return fmt.Errorf("Invalid slash command name: %s", command.Name)
	}
	return nil
}

// BuiltInSlashCommandsFacetOptions configures CreateBuiltInSlashCommandsFacet.
type BuiltInSlashCommandsFacetOptions struct {
	ReloadPresentationPlugins func(data chord.JsonValue) error
}

// CreateSlashCommandsRuntimeFacet creates the facet providing SlashCommandsService.
func CreateSlashCommandsRuntimeFacet(registry ...*SlashCommandRegistry) chord.Facet {
	var reg *SlashCommandRegistry
	if len(registry) > 0 && registry[0] != nil {
		reg = registry[0]
	} else {
		reg = NewSlashCommandRegistry()
	}
	return chord.DefineFacet(chord.Facet{
		Id: "@pi/slash-commands-runtime",
		Setup: func(env chord.FacetEnvironment) error {
			env.Provide(SlashCommandsService, reg)
			return nil
		},
	})
}

// CreateBuiltInSlashCommandsFacet registers built-in /model, /thinking, /compact, and /reload commands.
func CreateBuiltInSlashCommandsFacet(options BuiltInSlashCommandsFacetOptions) chord.Facet {
	return chord.DefineFacet(chord.Facet{
		Id: "@pi/slash-commands-builtin",
		Setup: func(env chord.FacetEnvironment) error {
			commands := AsSlashCommands(env.Use(SlashCommandsService))
			models := AsModels(env.Use(ModelsService))
			controller := AsAgentController(env.Use(AgentControllerService))
			ui := AsPresentationUI(env.Use(PresentationUIService))
			presentationPlugins := AsPresentationPlugins(env.Use(PresentationPluginsService))
			sessionPlugins := AsSessionPlugins(env.Use(SessionPluginsService))

			env.OnActivate(func() error {
				for _, cmd := range []SlashCommandContribution{
					scpModelCommand(models, ui),
					scpThinkingCommand(models, ui),
					scpCompactCommand(controller, ui),
					{
						Name:        "reload",
						Description: "Reload server-selected plugins",
						Run: func(_ string, ctx chord.Context) (SlashCommandRunResult, error) {
							ui.ShowStatus("Reloading plugins…", ctx)
							data, err := presentationPlugins.Reload(ctx)
							if err != nil {
								return nil, err
							}
							if err := sessionPlugins.Reload(ctx); err != nil {
								return nil, err
							}
							if options.ReloadPresentationPlugins != nil {
								if err := options.ReloadPresentationPlugins(data); err != nil {
									return nil, err
								}
							}
							ui.ShowStatus("Reloaded plugins.", ctx)
							return nil, nil
						},
					},
				} {
					unsub, err := commands.Replace(cmd)
					if err != nil {
						return err
					}
					env.Own(func() error {
						unsub()
						return nil
					})
				}
				return nil
			})
			return nil
		},
	})
}

func scpModelCommand(models Models, ui PresentationUI) SlashCommandContribution {
	return SlashCommandContribution{
		Name:         "model",
		Description:  "Select model",
		ArgumentHint: "<provider/model>",
		GetArgumentCompletions: func(prefix string) ([]SlashCommandCompletion, error) {
			normalized := strings.ToLower(prefix)
			state, ok := models.State().Value()
			if !ok {
				return []SlashCommandCompletion{}, nil
			}
			var out []SlashCommandCompletion
			for _, m := range state.Catalog.AvailableModels {
				haystack := strings.ToLower(fmt.Sprintf("%s/%s %s", m.Provider, m.ModelId, m.Name))
				if strings.Contains(haystack, normalized) {
					out = append(out, SlashCommandCompletion{
						Value:       fmt.Sprintf("%s/%s", m.Provider, m.ModelId),
						Label:       m.ModelId,
						Description: m.Provider,
					})
				}
			}
			return out, nil
		},
		Run: func(args string, ctx chord.Context) (SlashCommandRunResult, error) {
			state, ok := models.State().Value()
			if !ok {
				return nil, errors.New("Models service is not ready")
			}
			selected, found := scpExactModel(state.Catalog.AvailableModels, args)
			if len(args) > 0 && !found {
				return nil, fmt.Errorf("Unknown model: %s", args)
			}
			if !found {
				items := make([]PresentationSelectItem, 0, len(state.Catalog.AvailableModels))
				for _, m := range state.Catalog.AvailableModels {
					label := m.Name
					if state.Configuration.Model != nil &&
						state.Configuration.Model.Provider == m.Provider &&
						state.Configuration.Model.ModelId == m.ModelId {
						label = fmt.Sprintf("%s (selected)", m.Name)
					}
					items = append(items, PresentationSelectItem{
						Value:       fmt.Sprintf("%s/%s", m.Provider, m.ModelId),
						Label:       label,
						Description: fmt.Sprintf("%s/%s", m.Provider, m.ModelId),
					})
				}
				var curSelected *string
				if state.Configuration.Model != nil {
					s := fmt.Sprintf("%s/%s", state.Configuration.Model.Provider, state.Configuration.Model.ModelId)
					curSelected = &s
				}
				val, err := ui.Select("Select model:", items, curSelected, ctx)
				if err != nil {
					return nil, err
				}
				if val == nil {
					return nil, nil
				}
				selected, found = scpExactModel(state.Catalog.AvailableModels, *val)
				if !found {
					return nil, fmt.Errorf("Unknown model: %s", *val)
				}
			}
			if err := models.Select(ModelRef{Provider: selected.Provider, ModelId: selected.ModelId}, ctx); err != nil {
				return nil, err
			}
			ui.ShowStatus(fmt.Sprintf("Selected %s/%s.", selected.Provider, selected.ModelId), ctx)
			return nil, nil
		},
	}
}

func scpThinkingCommand(models Models, ui PresentationUI) SlashCommandContribution {
	return SlashCommandContribution{
		Name:         "thinking",
		Description:  "Set thinking level",
		ArgumentHint: "<level>",
		Run: func(args string, ctx chord.Context) (SlashCommandRunResult, error) {
			levels, err := models.GetThinkingLevels(ctx)
			if err != nil {
				return nil, err
			}
			lowerArgs := strings.ToLower(args)
			var selected *ai.ModelThinkingLevel
			for _, lvl := range levels {
				if string(lvl) == lowerArgs {
					c := lvl
					selected = &c
					break
				}
			}
			if len(args) > 0 && selected == nil {
				names := make([]string, len(levels))
				for i, l := range levels {
					names[i] = string(l)
				}
				return nil, fmt.Errorf("Unknown thinking level %q. Available levels: %s.", args, strings.Join(names, ", "))
			}
			if selected == nil {
				state, _ := models.State().Value()
				curLevel := state.Configuration.ThinkingLevel
				items := make([]PresentationSelectItem, 0, len(levels))
				for _, lvl := range levels {
					label := string(lvl)
					if curLevel == lvl {
						label = fmt.Sprintf("%s (selected)", lvl)
					}
					items = append(items, PresentationSelectItem{
						Value:       string(lvl),
						Label:       label,
						Description: scpThinkingDescriptions[lvl],
					})
				}
				var curPtr *string
				if curLevel != "" {
					s := string(curLevel)
					curPtr = &s
				}
				val, err := ui.Select("Select thinking level:", items, curPtr, ctx)
				if err != nil {
					return nil, err
				}
				if val == nil {
					return nil, nil
				}
				for _, lvl := range levels {
					if string(lvl) == *val {
						c := lvl
						selected = &c
						break
					}
				}
				if selected == nil {
					return nil, fmt.Errorf("Unknown thinking level: %s", *val)
				}
			}
			if err := models.SelectThinking(*selected, ctx); err != nil {
				return nil, err
			}
			ui.ShowStatus(fmt.Sprintf("Thinking level: %s.", *selected), ctx)
			return nil, nil
		},
	}
}

func scpCompactCommand(controller AgentController, ui PresentationUI) SlashCommandContribution {
	return SlashCommandContribution{
		Name:         "compact",
		Description:  "Manually compact the session context",
		ArgumentHint: "<instructions>",
		Run: func(args string, ctx chord.Context) (SlashCommandRunResult, error) {
			ui.ShowStatus("Compacting…", ctx)
			var instructions *string
			if len(args) > 0 {
				s := args
				instructions = &s
			}
			res, err := controller.Compact(AgentCompactionRequest{CustomInstructions: instructions}, ctx)
			if err != nil {
				return nil, err
			}
			return res, nil
		},
	}
}

func scpExactModel(models []ModelSummary, query string) (ModelSummary, bool) {
	if len(query) == 0 {
		return ModelSummary{}, false
	}
	normalized := strings.ToLower(query)
	var matches []ModelSummary
	for _, m := range models {
		full := strings.ToLower(fmt.Sprintf("%s/%s", m.Provider, m.ModelId))
		if full == normalized || strings.ToLower(m.ModelId) == normalized {
			matches = append(matches, m)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return ModelSummary{}, false
}
