// Ported from packages/durable/test/harness-registry.test.ts (pi v1.0.0).

package durable_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type thrgDummyReader struct{}

func (thrgDummyReader) Snapshot(_ chord.Context, _ durable.AnyDocToken, _ ...any) (durable.JsonObject, error) {
	return nil, nil
}

func (thrgDummyReader) SnapshotAsOf(_ chord.Context, _ durable.AnyDocToken, _ durable.ConversationId, _ ...any) (durable.JsonObject, error) {
	return nil, nil
}

func thrgTool(name string, desc ...string) *durable.ToolRegistration {
	d := name + " tool"
	if len(desc) > 0 && desc[0] != "" {
		d = desc[0]
	}
	return &durable.ToolRegistration{
		Name:        name,
		Description: d,
		Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
		Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{Content: ai.ContentList{}, HasContent: true}, nil
		},
	}
}

func thrgTask(name string, version ...int) durable.AnyTask {
	ver := 1
	if len(version) > 0 {
		ver = version[0]
	}
	return durable.DefineTask[any, durable.JsonObject, any, any](durable.TaskDefinition[any, durable.JsonObject, any, any]{
		Name:    name,
		Version: ver,
		Initial: func(_ any) durable.JsonObject { return jsonx.ObjectOf("phase", "run") },
		Phases: map[string]durable.PhaseHandler[any, durable.JsonObject, any, any]{
			"run": func(_ chord.Context, _ durable.RunningTask[any, durable.JsonObject], _ *durable.TaskRuntime[any, durable.JsonObject, any, any]) error {
				return nil
			},
		},
		Abort: func(_ chord.Context, _ durable.RunningTask[any, durable.JsonObject], _ *durable.TaskRuntime[any, durable.JsonObject, any, any]) error {
			return nil
		},
	})
}

func thrgExtNames(exts []*durable.Extension) []string {
	out := make([]string, len(exts))
	for i, ext := range exts {
		out[i] = ext.Name
	}
	return out
}

func thrgToolNames(tools []*durable.ToolRegistration) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	return out
}

func thrgResolve(
	state durable.JsonObject,
	snapshot durable.RegistrySnapshot,
	settings *durable.HarnessSettings,
	reports *[]error,
) *durable.Agent {
	return durable.ResolveAgent(state, snapshot, durable.ResolveSettings(settings), func(err error) {
		if reports != nil {
			*reports = append(*reports, err)
		}
	})
}

func thrgRendered(t *testing.T, agent *durable.Agent) [][2]string {
	t.Helper()
	input := durable.PromptInput{
		ConversationId: durable.IdFromNumber[durable.ConversationId](1),
		Agent:          agent,
		Shown:          map[string]string{},
		Read:           thrgDummyReader{},
	}
	out := make([][2]string, len(agent.Sections))
	for i, item := range agent.Sections {
		text, ok, err := item.Render(input, testkit.TestContext)
		if err != nil {
			t.Fatalf("section %s render failed: %v", item.Key, err)
		}
		if !ok {
			out[i] = [2]string{item.Key, ""}
		} else {
			out[i] = [2]string{item.Key, text}
		}
	}
	return out
}

func TestHarnessRegistry(t *testing.T) {
	t.Run("installs, replaces in place, and uninstalls extensions by name", func(t *testing.T) {
		registry := durable.CreateRegistry()
		var listener []string
		registry.Subscribe(func() {
			listener = append(listener, strings.Join(thrgExtNames(registry.Snapshot().Installed()), ","))
		})
		a := durable.DefineExtension(&durable.Extension{
			Name:  "a",
			Tools: []*durable.ToolRegistration{thrgTool("read", "Read files")},
		})
		b := durable.DefineExtension(&durable.Extension{
			Name:  "b",
			Tools: []*durable.ToolRegistration{thrgTool("read"), thrgTool("bash")},
		})
		if err := registry.Install(a); err != nil {
			t.Fatalf("install a failed: %v", err)
		}
		if err := registry.Install(b); err != nil {
			t.Fatalf("install b failed: %v", err)
		}
		before := registry.Snapshot()

		// A new object with an installed name replaces it at its position.
		a2 := durable.DefineExtension(&durable.Extension{
			Name:  "a",
			Tools: []*durable.ToolRegistration{thrgTool("grep")},
		})
		if err := registry.Install(a2); err != nil {
			t.Fatalf("install a2 failed: %v", err)
		}
		if got := thrgExtNames(registry.Snapshot().Installed()); !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Fatalf("installed = %v", got)
		}
		if registry.Snapshot().Extension("a") != a2 {
			t.Fatalf("expected extension a == a2")
		}
		var toolsAt []string
		for _, rt := range registry.Snapshot().Tools() {
			toolsAt = append(toolsAt, rt.Tool.Name+"@"+rt.Extension.Name)
		}
		if !reflect.DeepEqual(toolsAt, []string{"grep@a", "read@b", "bash@b"}) {
			t.Fatalf("toolsAt = %v", toolsAt)
		}
		// Old snapshots stay as they were.
		if before.Extension("a") != a {
			t.Fatalf("before.Extension(a) mutated")
		}
		if before.Tools()[0].Tool.Description != "Read files" {
			t.Fatalf("before tool desc = %q", before.Tools()[0].Tool.Description)
		}
		// Uninstall matches the name, whichever object; a later install appends.
		registry.Uninstall(a)
		registry.Uninstall(a)
		if got := thrgExtNames(registry.Snapshot().Installed()); !reflect.DeepEqual(got, []string{"b"}) {
			t.Fatalf("after uninstall = %v", got)
		}
		if err := registry.Install(a); err != nil {
			t.Fatalf("reinstall a failed: %v", err)
		}
		if got := thrgExtNames(registry.Snapshot().Installed()); !reflect.DeepEqual(got, []string{"b", "a"}) {
			t.Fatalf("after reinstall = %v", got)
		}
		if !reflect.DeepEqual(listener, []string{"a", "a,b", "a,b", "b", "b,a"}) {
			t.Fatalf("listener = %v", listener)
		}
	})

	t.Run("validates the registry as it would be after an install and publishes nothing when invalid", func(t *testing.T) {
		registry := durable.CreateRegistry()
		tasksExt := durable.DefineExtension(&durable.Extension{
			Name:  "tasks",
			Tasks: []durable.AnyTask{thrgTask("app.index")},
		})
		if err := registry.Install(tasksExt); err != nil {
			t.Fatalf("install tasks failed: %v", err)
		}
		var published []int
		registry.Subscribe(func() { published = append(published, 1) })
		before := registry.Snapshot()

		cases := []struct {
			ext     *durable.Extension
			message string
		}{
			{durable.DefineExtension(&durable.Extension{Name: "x", Tools: []*durable.ToolRegistration{thrgTool("read"), thrgTool("read")}}), "two tools named read"},
			{durable.DefineExtension(&durable.Extension{Name: "x", Sections: []*durable.PromptSection{durable.SimplePromptSection("a", "1"), durable.SimplePromptSection("a", "2")}}), "two sections"},
			{durable.DefineExtension(&durable.Extension{Name: "x", Sections: []*durable.PromptSection{durable.SimplePromptSection("Bad Key", "")}}), "must match"},
			{durable.DefineExtension(&durable.Extension{Name: "x", Sections: []*durable.PromptSection{durable.SimplePromptSection("instructions", "")}}), "reserved"},
			{durable.DefineExtension(&durable.Extension{Name: "x", Tasks: []durable.AnyTask{thrgTask("pi.generation")}}), "already installed"},
			{durable.DefineExtension(&durable.Extension{Name: "x", Tasks: []durable.AnyTask{thrgTask("app.index")}}), "already installed"},
		}
		for _, tc := range cases {
			err := registry.Install(tc.ext)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected error containing %q, got %v", tc.message, err)
			}
		}
		if registry.Snapshot() != before {
			t.Fatalf("snapshot changed after rejected installs")
		}
		if len(published) != 0 {
			t.Fatalf("published = %v", published)
		}
		// Replacing the extension that holds a task name is valid: the check runs on the state after replacement.
		if err := registry.Install(durable.DefineExtension(&durable.Extension{
			Name:  "tasks",
			Tasks: []durable.AnyTask{thrgTask("app.index", 2)},
		})); err != nil {
			t.Fatalf("replace tasks failed: %v", err)
		}
		if got := registry.Snapshot().Task("app.index"); got == nil || got.TaskVersion() != 2 {
			t.Fatalf("replaced task = %v", got)
		}
	})

	t.Run("always holds the built-in tasks, which are not an extension", func(t *testing.T) {
		registry := durable.CreateRegistry()
		if len(registry.Snapshot().Installed()) != 0 {
			t.Fatalf("expected empty installed extensions")
		}
		builtins := []durable.AnyTask{durable.GenerationTask, durable.ToolTask, durable.CompactionTask}
		if !reflect.DeepEqual(registry.Snapshot().Tasks(), builtins) {
			t.Fatalf("tasks = %v", registry.Snapshot().Tasks())
		}
		custom := thrgTask("app.custom")
		if err := registry.Install(durable.DefineExtension(&durable.Extension{Name: "custom", Tasks: []durable.AnyTask{custom}})); err != nil {
			t.Fatalf("install custom failed: %v", err)
		}
		want := append(append([]durable.AnyTask(nil), builtins...), custom)
		if !reflect.DeepEqual(registry.Snapshot().Tasks(), want) {
			t.Fatalf("with custom = %v", registry.Snapshot().Tasks())
		}
		if registry.Snapshot().Task("app.custom") != custom {
			t.Fatalf("expected custom task")
		}
		registry.Uninstall(durable.DefineExtension(&durable.Extension{Name: "custom"}))
		if registry.Snapshot().Task("app.custom") != nil {
			t.Fatalf("expected custom task uninstalled")
		}
	})
}

func TestHarnessAgentResolution(t *testing.T) {
	read := thrgTool("read")
	bash := thrgTool("bash")
	edit := thrgTool("edit")
	coding := durable.DefineExtension(&durable.Extension{
		Name:  "coding",
		Tools: []*durable.ToolRegistration{read, bash, edit},
		Sections: []*durable.PromptSection{
			durable.SimplePromptSection("preamble", "You code.", false),
			durable.SimplePromptSection("cwd", "/repo"),
		},
	})
	skills := durable.DefineExtension(&durable.Extension{
		Name:     "skills",
		Sections: []*durable.PromptSection{durable.SimplePromptSection("skills", "S")},
	})
	reviewer := durable.DefineExtension(&durable.Extension{
		Name:     "reviewer",
		Sections: []*durable.PromptSection{durable.SimplePromptSection("role", "Review.")},
	})
	registry := durable.CreateRegistry()
	for _, ext := range []*durable.Extension{coding, skills, reviewer} {
		_ = registry.Install(ext)
	}
	snapshot := registry.Snapshot()

	t.Run("selects the default, an array, or the default edited by add and remove", func(t *testing.T) {
		if got := thrgExtNames(thrgResolve(nil, snapshot, nil, nil).Extensions); !reflect.DeepEqual(got, []string{"coding", "skills", "reviewer"}) {
			t.Fatalf("default extensions = %v", got)
		}
		settings := &durable.HarnessSettings{Extensions: []*durable.Extension{coding, skills}, HasExtensions: true}
		if got := thrgExtNames(thrgResolve(jsonx.NewObject(), snapshot, settings, nil).Extensions); !reflect.DeepEqual(got, []string{"coding", "skills"}) {
			t.Fatalf("settings extensions = %v", got)
		}
		if got := thrgExtNames(thrgResolve(jsonx.ObjectOf("extensions", []any{"reviewer", "coding"}), snapshot, settings, nil).Extensions); !reflect.DeepEqual(got, []string{"reviewer", "coding"}) {
			t.Fatalf("array extensions = %v", got)
		}
		// Add appends, remove drops, duplicates keep their first position, uninstalled names are skipped.
		edited := jsonx.ObjectOf("extensions", jsonx.ObjectOf(
			"add", []any{"reviewer", "coding", "gone"},
			"remove", []any{"skills"},
		))
		if got := thrgExtNames(thrgResolve(edited, snapshot, settings, nil).Extensions); !reflect.DeepEqual(got, []string{"coding", "reviewer"}) {
			t.Fatalf("edited extensions = %v", got)
		}
		// An old object stands for its name: the installed extension is selected.
		stale := &durable.HarnessSettings{
			Extensions:    []*durable.Extension{durable.DefineExtension(&durable.Extension{Name: "skills"}), skills},
			HasExtensions: true,
		}
		if got := thrgResolve(jsonx.NewObject(), snapshot, stale, nil).Extensions; !reflect.DeepEqual(got, []*durable.Extension{skills}) {
			t.Fatalf("stale resolved = %v", got)
		}
	})

	t.Run("skips uninstalled names and resolves them again once they are installed", func(t *testing.T) {
		local := durable.CreateRegistry()
		_ = local.Install(coding)
		state := jsonx.ObjectOf("extensions", []any{"coding", "skills"})
		if got := thrgExtNames(thrgResolve(state, local.Snapshot(), nil, nil).Extensions); !reflect.DeepEqual(got, []string{"coding"}) {
			t.Fatalf("before install = %v", got)
		}
		_ = local.Install(skills)
		if got := thrgExtNames(thrgResolve(state, local.Snapshot(), nil, nil).Extensions); !reflect.DeepEqual(got, []string{"coding", "skills"}) {
			t.Fatalf("after install = %v", got)
		}
	})

	t.Run("replaces same-name tools in place, wraps the winner, then applies the filter", func(t *testing.T) {
		local := durable.CreateRegistry()
		venvBash := thrgTool("bash", "venv bash")
		var calls []string
		venv := durable.DefineExtension(&durable.Extension{Name: "venv", Tools: []*durable.ToolRegistration{venvBash}})
		timing := durable.DefineExtension(&durable.Extension{
			Name: "timing",
			Wraps: []durable.Wrap{
				durable.WrapTool(bash, func(inner *durable.ToolRegistration) *durable.ToolRegistration {
					c := *inner
					c.Description = inner.Description + " (timed)"
					return &c
				}),
				durable.WrapTool(bash, func(inner *durable.ToolRegistration) *durable.ToolRegistration {
					c := *inner
					c.Description = inner.Description + " [2]"
					return &c
				}),
				// No `grep` is selected: the wrapper does nothing and reports nothing.
				durable.WrapTool(thrgTool("grep"), func(inner *durable.ToolRegistration) *durable.ToolRegistration {
					calls = append(calls, "grep")
					return thrgTool("grep")
				}),
			},
		})
		for _, ext := range []*durable.Extension{coding, venv, timing} {
			_ = local.Install(ext)
		}
		var reports []error
		agent := thrgResolve(nil, local.Snapshot(), nil, &reports)
		var pairs [][2]string
		for _, tl := range agent.Tools {
			pairs = append(pairs, [2]string{tl.Name, tl.Description})
		}
		want := [][2]string{
			{"read", "read tool"},
			{"bash", "venv bash (timed) [2]"},
			{"edit", "edit tool"},
		}
		if !reflect.DeepEqual(pairs, want) || len(reports) != 0 || len(calls) != 0 {
			t.Fatalf("pairs = %v, reports = %v, calls = %v", pairs, reports, calls)
		}
		// An array keeps exactly these names in its order, a repeated name at its first position.
		filtered := thrgResolve(jsonx.ObjectOf("tools", []any{"edit", "missing", "read", "edit"}), local.Snapshot(), nil, nil)
		if got := thrgToolNames(filtered.Tools); !reflect.DeepEqual(got, []string{"edit", "read"}) {
			t.Fatalf("filtered = %v", got)
		}
		removed := thrgResolve(jsonx.ObjectOf("tools", jsonx.ObjectOf("remove", []any{"bash"})), local.Snapshot(), nil, nil)
		if got := thrgToolNames(removed.Tools); !reflect.DeepEqual(got, []string{"read", "edit"}) {
			t.Fatalf("removed = %v", got)
		}
	})

	t.Run("drops a tool or section whose wrapper panics or renames it and reports the failure", func(t *testing.T) {
		local := durable.CreateRegistry()
		broken := durable.DefineExtension(&durable.Extension{
			Name: "broken",
			Wraps: []durable.Wrap{
				durable.WrapTool(read, func(_ *durable.ToolRegistration) *durable.ToolRegistration {
					panic(errors.New("wrapper failed"))
				}),
				durable.WrapTool(edit, func(inner *durable.ToolRegistration) *durable.ToolRegistration {
					c := *inner
					c.Name = "renamed"
					return &c
				}),
				durable.WrapSection("cwd", func(_ *durable.PromptSection) *durable.PromptSection {
					panic(errors.New("section wrapper failed"))
				}),
			},
		})
		_ = local.Install(coding)
		_ = local.Install(broken)
		var reports []error
		agent := thrgResolve(nil, local.Snapshot(), nil, &reports)
		if got := thrgToolNames(agent.Tools); !reflect.DeepEqual(got, []string{"bash"}) {
			t.Fatalf("tools = %v", got)
		}
		var secKeys []string
		for _, s := range agent.Sections {
			secKeys = append(secKeys, s.Key)
		}
		if !reflect.DeepEqual(secKeys, []string{"preamble"}) {
			t.Fatalf("sections = %v", secKeys)
		}
		var msgs []string
		for _, err := range reports {
			msgs = append(msgs, err.Error())
		}
		wantMsgs := []string{"wrapper failed", "Wrapper renamed edit to renamed", "section wrapper failed"}
		if !reflect.DeepEqual(msgs, wantMsgs) {
			t.Fatalf("reports = %v", msgs)
		}
	})

	t.Run("orders sections by extension, replaces same keys in place, and renders instructions last and unwrapped", func(t *testing.T) {
		local := durable.CreateRegistry()
		override := durable.DefineExtension(&durable.Extension{
			Name:     "override",
			Sections: []*durable.PromptSection{durable.SimplePromptSection("preamble", "You review.", false)},
			Wraps: []durable.Wrap{
				durable.WrapSection("cwd", func(inner *durable.PromptSection) *durable.PromptSection {
					return durable.Section(inner.Key, func(input durable.PromptInput, ctx chord.Context) (string, bool, error) {
						txt, ok, err := inner.Render(input, ctx)
						if err != nil || !ok {
							return txt, ok, err
						}
						return txt + "!", true, nil
					})
				}),
				// Instructions are not wrapped.
				durable.WrapSection(durable.InstructionsKey, func(_ *durable.PromptSection) *durable.PromptSection {
					panic(errors.New("never"))
				}),
			},
		})
		for _, ext := range []*durable.Extension{coding, skills, override} {
			_ = local.Install(ext)
		}
		var reports []error
		agent := thrgResolve(jsonx.ObjectOf("instructions", "Be terse."), local.Snapshot(), nil, &reports)
		got := thrgRendered(t, agent)
		want := [][2]string{
			{"preamble", "You review."},
			{"cwd", "/repo!"},
			{"skills", "S"},
			{"instructions", "Be terse."},
		}
		if !reflect.DeepEqual(got, want) || agent.Sections[len(agent.Sections)-1].Tag != nil || len(reports) != 0 {
			t.Fatalf("rendered = %v, tag = %v, reports = %v", got, agent.Sections[len(agent.Sections)-1].Tag, reports)
		}
	})

	t.Run("collects hooks of the selected extensions in extension order and applies field defaults", func(t *testing.T) {
		local := durable.CreateRegistry()
		var order []string
		first := durable.ToolHooks{
			BeforeTool: func(_ *ai.ToolCall, _ durable.HookApi, _ chord.Context) (*durable.BeforeToolDecision, error) {
				order = append(order, "first")
				return nil, nil
			},
		}
		second := durable.ToolHooks{
			BeforeTool: func(_ *ai.ToolCall, _ durable.HookApi, _ chord.Context) (*durable.BeforeToolDecision, error) {
				order = append(order, "second")
				return nil, nil
			},
		}
		onYield := durable.GenerationHooks{
			OnYield: func(_ *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) (*durable.GenerationYieldDecision, error) {
				return nil, nil
			},
		}
		_ = local.Install(durable.DefineExtension(&durable.Extension{
			Name:  "a",
			Hooks: []durable.HookRegistration{durable.Hook(durable.ToolTask, first), durable.Hook(durable.GenerationTask, onYield)},
		}))
		_ = local.Install(durable.DefineExtension(&durable.Extension{
			Name:  "b",
			Hooks: []durable.HookRegistration{durable.Hook(durable.ToolTask, second)},
		}))

		callOrder := func(hooks []any) []string {
			order = nil
			for _, h := range hooks {
				_, _ = h.(durable.ToolHooks).BeforeTool(nil, nil, testkit.TestContext)
			}
			return order
		}

		if got := callOrder(durable.AgentHooks(thrgResolve(nil, local.Snapshot(), nil, nil), "pi.tool")); !reflect.DeepEqual(got, []string{"first", "second"}) {
			t.Fatalf("default hook order = %v", got)
		}
		if got := callOrder(durable.AgentHooks(thrgResolve(jsonx.ObjectOf("extensions", []any{"b", "a"}), local.Snapshot(), nil, nil), "pi.tool")); !reflect.DeepEqual(got, []string{"second", "first"}) {
			t.Fatalf("reordered hook order = %v", got)
		}
		if got := durable.AgentHooks(thrgResolve(jsonx.ObjectOf("extensions", []any{"b"}), local.Snapshot(), nil, nil), "pi.generation"); len(got) != 0 {
			t.Fatalf("expected no generation hooks, got %v", got)
		}

		defaults := thrgResolve(nil, local.Snapshot(), nil, nil)
		if defaults.Model != nil || defaults.ThinkingLevel != ai.ModelThinkingLevelOff || defaults.Cwd != nil {
			t.Fatalf("defaults = %+v", defaults)
		}
		explicit := thrgResolve(jsonx.ObjectOf(
			"model", jsonx.ObjectOf("provider", "p", "modelId", "m"),
			"thinkingLevel", "high",
			"cwd", "/w",
		), local.Snapshot(), nil, nil)
		if explicit.Model == nil || *explicit.Model != (durable.ModelRef{Provider: "p", ModelId: "m"}) || explicit.ThinkingLevel != ai.ModelThinkingLevelHigh || explicit.Cwd == nil || *explicit.Cwd != "/w" {
			t.Fatalf("explicit = %+v", explicit)
		}
	})
}
