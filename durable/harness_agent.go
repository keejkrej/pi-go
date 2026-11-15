// Ported from packages/durable/src/harness/agent.ts (pi v1.0.0).

package durable

import (
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// DefaultRetryPolicy provides the built-in generation retry defaults.
var DefaultRetryPolicy = ConversationRetryPolicy{
	Enabled:         true,
	MaxRetries:      3,
	BaseDelayMs:     2000,
	MaxAgentDelayMs: haInt64Ptr(60000),
}

// DefaultCompactionPolicy provides the built-in automatic compaction defaults.
var DefaultCompactionPolicy = CompactionPolicy{
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
	BackgroundTokens: 32768,
}

// InstructionsKey is the reserved prompt section key for the agent's instructions.
const InstructionsKey = "instructions"

// AgentDoc is the built-in rewindable per-conversation agent document ("pi.agent").
var AgentDoc = docuMustDefineDoc(DocDefinition{
	Kind:    "pi.agent",
	Version: 1,
	Scope:   DocumentScopeConversation,
	History: DocumentHistoryRewindable,
	Fork:    DocumentForkAsOf,
	Initial: func() JsonObject {
		return jsonx.NewObject()
	},
	CheckpointWhen: func(_ JsonObject, _ []chord.Op, _ CheckpointInfo) bool {
		return true
	},
})

// ResolveSettings resolves host settings over built-in defaults, merging nested policy objects.
func ResolveSettings(settings *HarnessSettings) Settings {
	out := Settings{
		Retry:         haCopyRetryPolicy(DefaultRetryPolicy),
		Compaction:    DefaultCompactionPolicy,
		ToolExecution: ToolExecutionModeParallel,
		SteeringMode:  QueueModeOneAtATime,
		FollowUpMode:  QueueModeOneAtATime,
	}
	if settings == nil {
		return out
	}
	if settings.HasExtensions || settings.Extensions != nil {
		out.HasExtensions = true
		out.Extensions = append([]*Extension(nil), settings.Extensions...)
	}
	if settings.Stream != nil {
		out.Stream = haCopyStreamOptions(*settings.Stream)
	}
	if settings.Retry != nil {
		r := settings.Retry
		if r.Enabled != nil {
			out.Retry.Enabled = *r.Enabled
		}
		if r.MaxRetries != nil {
			out.Retry.MaxRetries = *r.MaxRetries
		}
		if r.BaseDelayMs != nil {
			out.Retry.BaseDelayMs = *r.BaseDelayMs
		}
		if r.MaxAgentDelayMs != nil {
			v := *r.MaxAgentDelayMs
			out.Retry.MaxAgentDelayMs = &v
		}
	}
	if settings.Compaction != nil {
		c := settings.Compaction
		if c.Enabled != nil {
			out.Compaction.Enabled = *c.Enabled
		}
		if c.ReserveTokens != nil {
			out.Compaction.ReserveTokens = *c.ReserveTokens
		}
		if c.KeepRecentTokens != nil {
			out.Compaction.KeepRecentTokens = *c.KeepRecentTokens
		}
		if c.BackgroundTokens != nil {
			out.Compaction.BackgroundTokens = *c.BackgroundTokens
		}
	}
	if settings.ToolExecution != "" {
		out.ToolExecution = settings.ToolExecution
	}
	if settings.SteeringMode != "" {
		out.SteeringMode = settings.SteeringMode
	}
	if settings.FollowUpMode != "" {
		out.FollowUpMode = settings.FollowUpMode
	}
	return out
}

// Configure applies an AgentChange to conversationId's pi.agent document inside tx.
func Configure(tx Tx, conversationId ConversationId, change AgentChange) error {
	draft, err := tx.Doc(AgentDoc, conversationId)
	if err != nil {
		return err
	}
	return haApplyChange(draft.Value, change)
}

// AddTools records post-round tool additions in pi.agent.tools: explicit lists get new names appended,
// {remove} filters lose added names, and unset tools already expose everything.
func AddTools(tx Tx, conversationId ConversationId, added []string) error {
	if len(added) == 0 {
		return nil
	}
	draft, err := tx.Doc(AgentDoc, conversationId)
	if err != nil {
		return err
	}
	rawTools, ok := draft.Value.Get("tools")
	if !ok || rawTools == nil {
		return nil
	}
	switch t := rawTools.(type) {
	case []any:
		existing := make(map[string]struct{}, len(t))
		for _, item := range t {
			if name, isStr := item.(string); isStr {
				existing[name] = struct{}{}
			}
		}
		next := append([]any(nil), t...)
		changed := false
		for _, name := range added {
			if _, seen := existing[name]; !seen {
				existing[name] = struct{}{}
				next = append(next, name)
				changed = true
			}
		}
		if changed {
			draft.Value.Set("tools", next)
		}
	case *jsonx.Object:
		rawRemove, hasRemove := t.Get("remove")
		if !hasRemove {
			return nil
		}
		arr, isArr := rawRemove.([]any)
		if !isArr {
			return nil
		}
		addedSet := make(map[string]struct{}, len(added))
		for _, name := range added {
			addedSet[name] = struct{}{}
		}
		anyRemoved := false
		filtered := make([]any, 0, len(arr))
		for _, item := range arr {
			name, _ := item.(string)
			if _, drop := addedSet[name]; drop {
				anyRemoved = true
				continue
			}
			filtered = append(filtered, item)
		}
		if anyRemoved {
			draft.Value.Set("tools", jsonx.ObjectOf("remove", filtered))
		}
	}
	return nil
}

func haApplyChange(state *jsonx.Object, change AgentChange) error {
	if !change.Model.IsZero() {
		if change.Model.IsNull() {
			state.Delete("model")
		} else if model, ok := change.Model.Get(); ok {
			state.Set("model", jsonx.ObjectOf("provider", model.Provider, "modelId", model.ModelId))
		}
	}
	if !change.ThinkingLevel.IsZero() {
		if change.ThinkingLevel.IsNull() {
			state.Delete("thinkingLevel")
		} else if level, ok := change.ThinkingLevel.Get(); ok {
			state.Set("thinkingLevel", string(level))
		}
	}
	if !change.Extensions.IsZero() {
		if change.Extensions.IsNull() {
			state.Delete("extensions")
		} else if extChange, ok := change.Extensions.Get(); ok {
			if extChange.List != nil {
				list := make([]any, len(extChange.List))
				for i, ext := range extChange.List {
					list[i] = ext.Name
				}
				state.Set("extensions", list)
			} else {
				obj := jsonx.NewObject()
				if extChange.Add != nil {
					add := make([]any, len(extChange.Add))
					for i, ext := range extChange.Add {
						add[i] = ext.Name
					}
					obj.Set("add", add)
				}
				if extChange.Remove != nil {
					rem := make([]any, len(extChange.Remove))
					for i, ext := range extChange.Remove {
						rem[i] = ext.Name
					}
					obj.Set("remove", rem)
				}
				state.Set("extensions", obj)
			}
		}
	}
	if !change.Tools.IsZero() {
		if change.Tools.IsNull() {
			state.Delete("tools")
		} else if toolChange, ok := change.Tools.Get(); ok {
			if toolChange.List != nil {
				list := make([]any, len(toolChange.List))
				for i, tool := range toolChange.List {
					list[i] = tool.Name
				}
				state.Set("tools", list)
			} else {
				rem := make([]any, len(toolChange.Remove))
				for i, tool := range toolChange.Remove {
					rem[i] = tool.Name
				}
				state.Set("tools", jsonx.ObjectOf("remove", rem))
			}
		}
	}
	if !change.Instructions.IsZero() {
		if change.Instructions.IsNull() {
			state.Delete("instructions")
		} else if instr, ok := change.Instructions.Get(); ok {
			state.Set("instructions", instr)
		}
	}
	if !change.Cwd.IsZero() {
		if change.Cwd.IsNull() {
			state.Delete("cwd")
		} else if cwd, ok := change.Cwd.Get(); ok {
			state.Set("cwd", cwd)
		}
	}
	return nil
}

// CreateAgent initializes pi.agent in every conversation creation commit: forks keep their asOf copy;
// task-owned conversations copy the owner task's conversation agent; ownerless ones start empty.
func CreateAgent(tx Tx, conversation ConversationRecord) error {
	if conversation.Parent != nil {
		return nil
	}
	draft, err := tx.Doc(AgentDoc, conversation.Id)
	if err != nil {
		return err
	}
	if conversation.Owner == nil {
		return nil
	}
	ownerDraft, err := tx.Doc(AgentDoc, conversation.Owner.ConversationId)
	if err != nil {
		return err
	}
	draft.Value.Assign(ownerDraft.Value.Clone())
	return nil
}

// AgentHooks returns handlers for taskName from the agent's selected extensions in extension order.
func AgentHooks(agent *Agent, taskName string) []any {
	if agent == nil {
		return nil
	}
	var handlers []any
	for _, ext := range agent.Extensions {
		for _, hook := range ext.Hooks {
			if hook.Task == taskName {
				handlers = append(handlers, hook.Handlers)
			}
		}
	}
	return handlers
}

// ResolveAgent resolves an agent from stored pi.agent JSON state, a registry snapshot, and settings.
// Wrappers that fail or rename their target drop the target and report via report.
func ResolveAgent(
	rawState JsonObject,
	snapshot RegistrySnapshot,
	settings Settings,
	report func(error),
) *Agent {
	state := haDecodeAgentState(rawState)
	extensions := haSelectExtensions(state.Extensions, snapshot, settings)

	composedTools := make(map[string]*ToolRegistration)
	var toolOrder []string
	for _, ext := range extensions {
		for _, tool := range ext.Tools {
			if _, exists := composedTools[tool.Name]; !exists {
				toolOrder = append(toolOrder, tool.Name)
			}
			composedTools[tool.Name] = tool
		}
	}

	composedSections := make(map[string]*PromptSection)
	var sectionOrder []string
	for _, ext := range extensions {
		for _, sec := range ext.Sections {
			if _, exists := composedSections[sec.Key]; !exists {
				sectionOrder = append(sectionOrder, sec.Key)
			}
			composedSections[sec.Key] = sec
		}
	}

	for _, ext := range extensions {
		for _, wrap := range ext.Wraps {
			if wrap.WrapTool != nil {
				haApplyWrap(composedTools, wrap.Tool, wrap.WrapTool, func(t *ToolRegistration) string { return t.Name }, report)
			} else if wrap.WrapSection != nil {
				haApplyWrap(composedSections, wrap.Section, wrap.WrapSection, func(s *PromptSection) string { return s.Key }, report)
			}
		}
	}

	var tools []*ToolRegistration
	if state.Tools == nil {
		for _, name := range toolOrder {
			if t, ok := composedTools[name]; ok {
				tools = append(tools, t)
			}
		}
	} else if state.Tools.List != nil {
		seen := make(map[string]struct{}, len(state.Tools.List))
		for _, name := range state.Tools.List {
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			if t, ok := composedTools[name]; ok {
				tools = append(tools, t)
			}
		}
	} else {
		removed := make(map[string]struct{}, len(state.Tools.Remove))
		for _, name := range state.Tools.Remove {
			removed[name] = struct{}{}
		}
		for _, name := range toolOrder {
			if _, drop := removed[name]; drop {
				continue
			}
			if t, ok := composedTools[name]; ok {
				tools = append(tools, t)
			}
		}
	}

	var agentSections []*PromptSection
	for _, key := range sectionOrder {
		if sec, ok := composedSections[key]; ok {
			agentSections = append(agentSections, sec)
		}
	}
	if state.Instructions != nil {
		instr := *state.Instructions
		agentSections = append(agentSections, Section(InstructionsKey, func(_ PromptInput, _ chord.Context) (string, bool, error) {
			return instr, true, nil
		}))
	}

	thinking := ai.ModelThinkingLevelOff
	if state.ThinkingLevel != nil && *state.ThinkingLevel != "" {
		thinking = *state.ThinkingLevel
	}

	return &Agent{
		Model:         state.Model,
		ThinkingLevel: thinking,
		Extensions:    extensions,
		Tools:         tools,
		Sections:      agentSections,
		Instructions:  state.Instructions,
		Cwd:           state.Cwd,
	}
}

func haDecodeAgentState(raw JsonObject) AgentState {
	var state AgentState
	if raw == nil {
		return state
	}
	if rawModel, ok := raw.Get("model"); ok && rawModel != nil {
		var m ModelRef
		if err := jsonx.Decode(rawModel, &m); err == nil {
			state.Model = &m
		}
	}
	if rawLevel, ok := raw.Get("thinkingLevel"); ok {
		if s, isStr := rawLevel.(string); isStr {
			lvl := ai.ModelThinkingLevel(s)
			state.ThinkingLevel = &lvl
		}
	}
	if rawExt, ok := raw.Get("extensions"); ok && rawExt != nil {
		switch v := rawExt.(type) {
		case []any:
			list := make([]string, 0, len(v))
			for _, item := range v {
				if name, isStr := item.(string); isStr {
					list = append(list, name)
				}
			}
			state.Extensions = &AgentExtensionsState{List: list}
		case *jsonx.Object:
			extState := &AgentExtensionsState{}
			if addRaw, ok := v.Get("add"); ok {
				if addArr, isArr := addRaw.([]any); isArr {
					for _, item := range addArr {
						if name, isStr := item.(string); isStr {
							extState.Add = append(extState.Add, name)
						}
					}
				}
			}
			if remRaw, ok := v.Get("remove"); ok {
				if remArr, isArr := remRaw.([]any); isArr {
					for _, item := range remArr {
						if name, isStr := item.(string); isStr {
							extState.Remove = append(extState.Remove, name)
						}
					}
				}
			}
			state.Extensions = extState
		}
	}
	if rawTools, ok := raw.Get("tools"); ok && rawTools != nil {
		switch v := rawTools.(type) {
		case []any:
			list := make([]string, 0, len(v))
			for _, item := range v {
				if name, isStr := item.(string); isStr {
					list = append(list, name)
				}
			}
			state.Tools = &AgentToolsState{List: list}
		case *jsonx.Object:
			toolState := &AgentToolsState{}
			if remRaw, ok := v.Get("remove"); ok {
				if remArr, isArr := remRaw.([]any); isArr {
					for _, item := range remArr {
						if name, isStr := item.(string); isStr {
							toolState.Remove = append(toolState.Remove, name)
						}
					}
				}
			}
			state.Tools = toolState
		}
	}
	if rawInstr, ok := raw.Get("instructions"); ok {
		if s, isStr := rawInstr.(string); isStr {
			state.Instructions = &s
		}
	}
	if rawCwd, ok := raw.Get("cwd"); ok {
		if s, isStr := rawCwd.(string); isStr {
			state.Cwd = &s
		}
	}
	return state
}

func haSelectExtensions(stored *AgentExtensionsState, snapshot RegistrySnapshot, settings Settings) []*Extension {
	var selected []string
	if stored != nil && stored.List != nil {
		selected = stored.List
	} else {
		var base []string
		if settings.HasExtensions {
			base = make([]string, len(settings.Extensions))
			for i, ext := range settings.Extensions {
				base[i] = ext.Name
			}
		} else {
			installed := snapshot.Installed()
			base = make([]string, len(installed))
			for i, ext := range installed {
				base[i] = ext.Name
			}
		}
		removed := make(map[string]struct{})
		var added []string
		if stored != nil {
			for _, name := range stored.Remove {
				removed[name] = struct{}{}
			}
			added = stored.Add
		}
		combined := make([]string, 0, len(base)+len(added))
		combined = append(combined, base...)
		combined = append(combined, added...)
		for _, name := range combined {
			if _, drop := removed[name]; !drop {
				selected = append(selected, name)
			}
		}
	}

	extensions := make([]*Extension, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, name := range selected {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		if ext := snapshot.Extension(name); ext != nil {
			extensions = append(extensions, ext)
		}
	}
	return extensions
}

func haApplyWrap[T any](items map[string]T, target string, wrap func(T) T, nameOf func(T) string, report func(error)) {
	item, exists := items[target]
	if !exists {
		return
	}
	var wrapped T
	var panicErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					panicErr = err
				} else {
					panicErr = fmt.Errorf("wrapper panic: %v", r)
				}
			}
		}()
		wrapped = wrap(item)
	}()
	if panicErr != nil {
		delete(items, target)
		if report != nil {
			report(panicErr)
		}
		return
	}
	if nameOf(wrapped) != target {
		delete(items, target)
		if report != nil {
			report(fmt.Errorf("Wrapper renamed %s to %s", target, nameOf(wrapped)))
		}
		return
	}
	items[target] = wrapped
}

func haCopyRetryPolicy(p ConversationRetryPolicy) ConversationRetryPolicy {
	out := p
	if p.MaxAgentDelayMs != nil {
		v := *p.MaxAgentDelayMs
		out.MaxAgentDelayMs = &v
	}
	return out
}

func haCopyStreamOptions(s ConversationStreamOptions) ConversationStreamOptions {
	out := s
	if s.Transport != nil {
		v := *s.Transport
		out.Transport = &v
	}
	if s.TimeoutMs != nil {
		v := *s.TimeoutMs
		out.TimeoutMs = &v
	}
	if s.MaxRetries != nil {
		v := *s.MaxRetries
		out.MaxRetries = &v
	}
	if s.MaxRetryDelayMs != nil {
		v := *s.MaxRetryDelayMs
		out.MaxRetryDelayMs = &v
	}
	if s.Headers != nil {
		out.Headers = make(map[string]string, len(s.Headers))
		for k, v := range s.Headers {
			out.Headers[k] = v
		}
	}
	if s.Metadata != nil {
		out.Metadata = s.Metadata.Clone()
	}
	if s.CacheRetention != nil {
		v := *s.CacheRetention
		out.CacheRetention = &v
	}
	if s.Deferred != nil {
		d := *s.Deferred
		out.Deferred = &d
	}
	return out
}

func haInt64Ptr(v int64) *int64 {
	return &v
}
