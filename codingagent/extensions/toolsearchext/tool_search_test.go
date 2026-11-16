// Ported from packages/coding-agent/test/tool-search.test.ts (pi v1.5.2).

package toolsearchext_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/extensions/toolsearchext"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func ttsTool(name, description string, properties ...*jsonx.Object) core.ToolInfo {
	propsObj := jsonx.NewObject()
	if len(properties) > 0 && properties[0] != nil {
		propsObj = properties[0]
	}
	return core.ToolInfo{
		Name:        name,
		Description: description,
		Parameters: typebox.Unsafe(jsonx.ObjectOf(
			"type", "object",
			"properties", propsObj,
		)),
	}
}

func ttsMatchNames(matches []toolsearchext.ToolSearchMatch) []string {
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.Name
	}
	return out
}

func TestTokenize(t *testing.T) {
	t.Run("splits camelCase and snake_case, drops stop words, and folds plurals", func(t *testing.T) {
		if got, want := toolsearchext.Tokenize("listIssues for the GitHub_repo"), []string{"list", "issue", "git", "hub", "repo"}; !slices.Equal(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
		if got, want := toolsearchext.Tokenize("searches queries HTTPServer"), []string{"search", "query", "http", "server"}; !slices.Equal(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
		if got, want := toolsearchext.Tokenize("repo.issues/search:queries"), []string{"repo", "issue", "search", "query"}; !slices.Equal(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("segments spaced non-ASCII scripts cleanly", func(t *testing.T) {
		cases := []struct {
			text string
			want []string
		}{
			{"문서 검색", []string{"문서", "검색"}},
			{"Поиск документов", []string{"поиск", "документов"}},
			{"البحث عن الملفات", []string{"البحث", "عن", "الملفات"}},
			{"फ़ाइल खोजें", []string{"फ़ाइल", "खोजें"}},
			{"Éléments cafés", []string{"éléments", "cafés"}},
		}
		for _, tc := range cases {
			if got := toolsearchext.Tokenize(tc.text); !slices.Equal(got, tc.want) {
				t.Fatalf("Tokenize(%q) = %q, want %q", tc.text, got, tc.want)
			}
		}
	})

	t.Run("segments CJK and Southeast Asian scripts via UAX#29 word boundaries without C/ICU dictionary", func(t *testing.T) {
		for _, text := range []string{"搜索仓库问题", "文書を検索", "ค้นหาเอกสาร"} {
			got := toolsearchext.Tokenize(text)
			if len(got) < 2 {
				t.Fatalf("Tokenize(%q) = %q, expected multiple tokens", text, got)
			}
		}
	})

	t.Run("preserves non-ASCII words and splits mixed-language identifiers", func(t *testing.T) {
		got := toolsearchext.Tokenize("搜索GitHub_repoIssues résumé cafés")
		tail := []string{"git", "hub", "repo", "issue", "résumé", "cafés"}
		if len(got) < len(tail)+1 {
			t.Fatalf("Tokenize mixed = %q, too short", got)
		}
		if gotTail := got[len(got)-len(tail):]; !slices.Equal(gotTail, tail) {
			t.Fatalf("Tokenize mixed tail = %q, want %q", gotTail, tail)
		}
		if strings.Join(got[:len(got)-len(tail)], "") != "搜索" {
			t.Fatalf("Tokenize mixed CJK prefix = %q, expected 搜/索", got[:len(got)-len(tail)])
		}
	})

	t.Run("normalizes canonically equivalent Unicode text", func(t *testing.T) {
		if got, want := toolsearchext.Tokenize("CAFÉ cafe\u0301"), []string{"café", "café"}; !slices.Equal(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("ignores whitespace, punctuation, and emoji", func(t *testing.T) {
		if got := toolsearchext.Tokenize(" \t，。!? 😀 "); len(got) != 0 {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

func TestBm25Ranker(t *testing.T) {
	tools := []core.ToolInfo{
		ttsTool("mcp__github__list_issues", "List issues in a repository.", jsonx.ObjectOf(
			"state", jsonx.ObjectOf("type", "string", "description", "open or closed"),
		)),
		ttsTool("mcp__github__create_pull_request", "Open a pull request."),
		ttsTool("mcp__linear__search_issues", "Search Linear issues by text."),
		ttsTool("mcp__docs__search", "Search the documentation."),
	}
	documents := make([]toolsearchext.ToolSearchDocument, len(tools))
	for i, entry := range tools {
		documents[i] = toolsearchext.CreateToolSearchDocument(entry)
	}

	t.Run("ranks by term relevance and respects the limit", func(t *testing.T) {
		ranker := toolsearchext.NewBm25Ranker()
		if got, want := ttsMatchNames(ranker.Rank("issue", documents, 8)), []string{"mcp__linear__search_issues", "mcp__github__list_issues"}; !slices.Equal(got, want) {
			t.Fatalf("issue ranking = %q, want %q", got, want)
		}
		prMatches := ranker.Rank("pull requests", documents, 8)
		if len(prMatches) == 0 || prMatches[0].Name != "mcp__github__create_pull_request" {
			t.Fatalf("pull requests top match = %+v", prMatches)
		}
		if got := ranker.Rank("search", documents, 1); len(got) != 1 {
			t.Fatalf("limit=1 returned %d items", len(got))
		}
		if got, want := ttsMatchNames(ranker.Rank("closed", documents, 8)), []string{"mcp__github__list_issues"}; !slices.Equal(got, want) {
			t.Fatalf("property description ranking = %q, want %q", got, want)
		}
	})

	t.Run("matches Unicode queries against localized descriptions", func(t *testing.T) {
		cases := []struct {
			description string
			query       string
		}{
			{"搜索仓库问题", "问题"},
			{"文書を検索", "検索"},
			{"문서 검색", "검색"},
			{"ค้นหาเอกสาร", "เอกสาร"},
			{"Поиск документов", "документов"},
			{"البحث عن الملفات", "الملفات"},
			{"फ़ाइल खोजें", "खोजें"},
			{"Rechercher un café", "cafe\u0301"},
		}
		for _, tc := range cases {
			localized := toolsearchext.CreateToolSearchDocument(ttsTool("localized", tc.description))
			docs := append(slices.Clone(documents), localized)
			matches := toolsearchext.NewBm25Ranker().Rank(tc.query, docs, 8)
			if len(matches) != 1 || matches[0].Name != "localized" || matches[0].Score <= 0 {
				t.Fatalf("Rank(%q, desc=%q) = %+v, want [{Name:localized Score:>0}]", tc.query, tc.description, matches)
			}
		}
	})

	t.Run("returns nothing for unknown or empty queries", func(t *testing.T) {
		ranker := toolsearchext.NewBm25Ranker()
		if got := ranker.Rank("kubernetes", documents, 8); len(got) != 0 {
			t.Fatalf("unknown query = %+v", got)
		}
		if got := ranker.Rank("the", documents, 8); len(got) != 0 {
			t.Fatalf("stopword query = %+v", got)
		}
		if got := ranker.Rank("tickets", documents, 8); len(got) != 0 {
			t.Fatalf("synonym query = %+v", got)
		}
	})

	t.Run("ranks a tool first when the query is its name", func(t *testing.T) {
		named := []toolsearchext.ToolSearchDocument{
			toolsearchext.CreateToolSearchDocument(ttsTool("mcp__app__queue_read", "Read a queued message of this thread.")),
			toolsearchext.CreateToolSearchDocument(ttsTool(
				"mcp__dev-app__thread_read",
				"Read durable state and a paginated timeline from any thread. The default view returns user messages, assistant messages and plans. Continue with afterPosition until hasMore is false.",
			)),
		}
		ranker := toolsearchext.NewBm25Ranker()
		for _, query := range []string{
			"thread_read",
			"dev_app__thread_read",
			"mcp__dev-app__thread_read",
			"Thread_Read",
			"thread read",
		} {
			got := ttsMatchNames(ranker.Rank(query, named, 1))
			if !slices.Equal(got, []string{"mcp__dev-app__thread_read"}) {
				t.Fatalf("Rank(%q) = %q, want [mcp__dev-app__thread_read]", query, got)
			}
		}
		got := ttsMatchNames(ranker.Rank("read", named, 1))
		if !slices.Equal(got, []string{"mcp__app__queue_read"}) {
			t.Fatalf("partial name Rank(read) = %q, want [mcp__app__queue_read]", got)
		}
	})

	t.Run("ranks a name match above mentions in descriptions", func(t *testing.T) {
		docs := []toolsearchext.ToolSearchDocument{
			toolsearchext.CreateToolSearchDocument(ttsTool("mcp__ci__status", "Deploy status: last deploy, deploy log, deploy errors.")),
			toolsearchext.CreateToolSearchDocument(ttsTool(
				"mcp__ci__deploy_release",
				"Start a release of the current branch to an environment, run the migrations, switch traffic over, and wait until the new version is live and healthy.",
			)),
		}
		got := ttsMatchNames(toolsearchext.NewBm25Ranker().Rank("deploy", docs, 1))
		if !slices.Equal(got, []string{"mcp__ci__deploy_release"}) {
			t.Fatalf("got %q, want [mcp__ci__deploy_release]", got)
		}
	})

	t.Run("ranks a tool first when the query contains its whole name", func(t *testing.T) {
		inst := "Peekaboo controls macOS apps by app name."
		peekaboo := &core.ToolNamespace{Name: "mcp__peekaboo", Instructions: &inst}
		rawTools := []core.ToolInfo{
			ttsTool("mcp__peekaboo__see", "Capture the app window of the peekaboo app and describe the app."),
			ttsTool("mcp__peekaboo__menu", "Click a menu item of an app."),
			ttsTool("mcp__peekaboo__app", "Launch, quit, relaunch, focus, hide, or switch applications. Targets an application by name, bundle identifier, or process ID, and can wait until it is ready."),
			ttsTool("mcp__app__queue_read", "Read a queued message of this thread."),
			ttsTool("mcp__dev-app__thread_read", "Read durable state and a paginated timeline from any thread. The default view returns user messages, assistant messages and plans."),
		}
		docs := make([]toolsearchext.ToolSearchDocument, len(rawTools))
		for i, entry := range rawTools {
			var ns *core.ToolNamespace
			if strings.HasPrefix(entry.Name, "mcp__peekaboo__") {
				ns = peekaboo
			}
			docs[i] = toolsearchext.CreateToolSearchDocument(entry, ns)
		}
		ranker := toolsearchext.NewBm25Ranker()
		first := func(query string) []string {
			return ttsMatchNames(ranker.Rank(query, docs, 1))
		}
		if got := first("peekaboo app"); !slices.Equal(got, []string{"mcp__peekaboo__app"}) {
			t.Fatalf("peekaboo app = %q", got)
		}
		if got := first("open the peekaboo app tool"); !slices.Equal(got, []string{"mcp__peekaboo__app"}) {
			t.Fatalf("open the peekaboo app tool = %q", got)
		}
		if got := first("read thread_read state"); !slices.Equal(got, []string{"mcp__dev-app__thread_read"}) {
			t.Fatalf("read thread_read state = %q", got)
		}
		if got := first("describe window"); !slices.Equal(got, []string{"mcp__peekaboo__see"}) {
			t.Fatalf("describe window = %q", got)
		}
	})

	t.Run("ranks a tool matching more query terms first, whatever the scores", func(t *testing.T) {
		docs := []toolsearchext.ToolSearchDocument{
			toolsearchext.CreateToolSearchDocument(ttsTool("mcp__peekaboo__app", "Launch, quit, focus, or switch applications by name or bundle identifier.")),
			toolsearchext.CreateToolSearchDocument(ttsTool("mcp__peekaboo__see", "Capture the app window and describe the UI elements of the app.")),
			toolsearchext.CreateToolSearchDocument(ttsTool("mcp__peekaboo__window", "Move, resize, or close a window of an app.")),
			toolsearchext.CreateToolSearchDocument(ttsTool("mcp__peekaboo__image", "Take a screenshot of the screen, an app, or a window.")),
		}
		ranker := toolsearchext.NewBm25Ranker()
		first := func(query string) []string {
			return ttsMatchNames(ranker.Rank(query, docs, 1))
		}
		if got := first("screenshot window"); !slices.Equal(got, []string{"mcp__peekaboo__image"}) {
			t.Fatalf("screenshot window = %q", got)
		}
		if got := first("describe app"); !slices.Equal(got, []string{"mcp__peekaboo__see"}) {
			t.Fatalf("describe app = %q", got)
		}
		if got := first("window"); !slices.Equal(got, []string{"mcp__peekaboo__window"}) {
			t.Fatalf("window = %q", got)
		}
	})

	t.Run("includes the namespace in the search text", func(t *testing.T) {
		desc := "Kubernetes cluster tools"
		doc := toolsearchext.CreateToolSearchDocument(
			ttsTool("mcp__x__run", "Run it."),
			&core.ToolNamespace{Name: "mcp__x", Description: &desc},
		)
		matches := toolsearchext.NewBm25Ranker().Rank("kubernetes", []toolsearchext.ToolSearchDocument{doc}, 8)
		if len(matches) != 1 || matches[0].Name != "mcp__x__run" || matches[0].Score <= 0 {
			t.Fatalf("matches = %+v", matches)
		}
	})
}

func TestSchemaReferencesInSearchText(t *testing.T) {
	searchText := func(parameters *jsonx.Object) string {
		entry := core.ToolInfo{
			Name:        "mcp__ledger__inspect",
			Description: "Inspect records",
			Parameters:  typebox.Unsafe(parameters),
		}
		return toolsearchext.CreateToolSearchDocument(entry).Text
	}

	t.Run("reads names and descriptions behind local references", func(t *testing.T) {
		got := searchText(jsonx.ObjectOf(
			"type", "object",
			"properties", jsonx.ObjectOf(
				"options", jsonx.ObjectOf("$ref", "#/$defs/options"),
			),
			"$defs", jsonx.ObjectOf(
				"options", jsonx.ObjectOf(
					"type", "object",
					"properties", jsonx.ObjectOf(
						"mode", jsonx.ObjectOf("type", "string", "description", "reconciliation"),
					),
				),
			),
		))
		if want := "Inspect records options mode reconciliation"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("follows escaped pointer segments", func(t *testing.T) {
		got := searchText(jsonx.ObjectOf(
			"type", "object",
			"properties", jsonx.ObjectOf(
				"range", jsonx.ObjectOf("$ref", "#/$defs/date~1range"),
			),
			"$defs", jsonx.ObjectOf(
				"date/range", jsonx.ObjectOf("description", "fiscal period"),
			),
		))
		if want := "Inspect records range fiscal period"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("reads recursive and shared definitions once", func(t *testing.T) {
		got := searchText(jsonx.ObjectOf(
			"type", "object",
			"properties", jsonx.ObjectOf(
				"root", jsonx.ObjectOf("$ref", "#/$defs/node"),
				"other", jsonx.ObjectOf("$ref", "#/$defs/node"),
				"self", jsonx.ObjectOf("$ref", "#"),
			),
			"$defs", jsonx.ObjectOf(
				"node", jsonx.ObjectOf(
					"description", "tree node",
					"properties", jsonx.ObjectOf(
						"children", jsonx.ObjectOf(
							"type", "array",
							"items", jsonx.ObjectOf("$ref", "#/$defs/node"),
						),
					),
				),
			),
		))
		if want := "Inspect records root tree node children other self"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("leaves out unreferenced definitions and remote or missing references", func(t *testing.T) {
		got := searchText(jsonx.ObjectOf(
			"type", "object",
			"properties", jsonx.ObjectOf(
				"remote", jsonx.ObjectOf("$ref", "https://example.com/schema.json#/$defs/x"),
				"missing", jsonx.ObjectOf("$ref", "#/$defs/missing"),
				"malformed", jsonx.ObjectOf("$ref", "#/$defs/%E0%A4%A"),
			),
			"$defs", jsonx.ObjectOf(
				"unused", jsonx.ObjectOf("description", "reconciliation"),
			),
		))
		if want := "Inspect records remote missing malformed"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

type ttsFakeToolsAPI struct {
	all    []core.ToolInfo
	active []string
}

func (f *ttsFakeToolsAPI) GetAllTools() []core.ToolInfo {
	return slices.Clone(f.all)
}

func (f *ttsFakeToolsAPI) GetActiveTools() []string {
	return slices.Clone(f.active)
}

func (f *ttsFakeToolsAPI) SetActiveTools(toolNames []string) {
	f.active = slices.Clone(toolNames)
}

func TestToolSearchToolDefinition(t *testing.T) {
	ctx := context.Background()

	t.Run("validates query and limit", func(t *testing.T) {
		def := toolsearchext.CreateToolSearchToolDefinition()
		if def.Exposure != core.ToolExposureModelOnly {
			t.Fatalf("Exposure = %q, want model-only", def.Exposure)
		}
		if !def.ReachesTools(nil).All {
			t.Fatalf("ReachesTools = %+v, want All=true", def.ReachesTools(nil))
		}

		for _, bad := range []*jsonx.Object{
			jsonx.ObjectOf("query", "   "),
			jsonx.ObjectOf("query", "issues", "limit", float64(0)),
			jsonx.ObjectOf("query", "issues", "limit", float64(-1)),
			jsonx.ObjectOf("query", "issues", "limit", 1.5),
		} {
			if _, err := def.Execute(ctx, "call-1", bad, nil, nil); err == nil {
				t.Fatalf("expected error for params %v", bad)
			}
		}

		res, err := def.Execute(ctx, "call-2", jsonx.ObjectOf("query", "anything"), nil, nil)
		if err != nil {
			t.Fatalf("Execute without Tools: %v", err)
		}
		txt := res.Content[0].(*ai.TextContent).Text
		if txt != "No matching tools found." {
			t.Fatalf("text = %q", txt)
		}
	})

	t.Run("activates only codemode and deferred inactive tools and formats first-line summary", func(t *testing.T) {
		api := &ttsFakeToolsAPI{
			all: []core.ToolInfo{
				{Name: "read", Description: "Read issues in files.", Exposure: core.ToolExposureDirect},
				{Name: "secret", Description: "Secret issue tool.", Exposure: core.ToolExposureHidden},
				{Name: "model_only", Description: "Model only issue tool.", Exposure: core.ToolExposureModelOnly},
				{Name: "already_active", Description: "Already active issue tool.", Exposure: core.ToolExposureDeferred},
				{Name: "mcp__github__list_issues", Description: "List issues in a repo.\nSecond line omitted.", Exposure: core.ToolExposureCodemode},
				{Name: "mcp__linear__find_issue", Description: "Find Linear issues.", Exposure: core.ToolExposureDeferred},
			},
			active: []string{"read", "already_active"},
		}
		def := toolsearchext.CreateToolSearchToolDefinition(&toolsearchext.ToolSearchToolOptions{Tools: api})
		res, err := def.Execute(ctx, "call-1", jsonx.ObjectOf("query", "issue", "limit", float64(1)), nil, nil)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		details := res.Details.(toolsearchext.ToolSearchToolDetails)
		if len(details.Loaded) != 1 {
			t.Fatalf("Loaded = %q, want 1 match", details.Loaded)
		}
		loadedName := details.Loaded[0]
		if !slices.Contains(api.active, loadedName) {
			t.Fatalf("expected %q to be added to active tools: %q", loadedName, api.active)
		}
		text := res.Content[0].(*ai.TextContent).Text
		if !strings.HasPrefix(text, "Loaded 1 tool. They are available from your next call:\n- "+loadedName+": ") || strings.Contains(text, "Second line omitted.") {
			t.Fatalf("unexpected text output: %q", text)
		}
	})
}

func TestToolSearchExtensionAgentSessionIntegration(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	agentDir := t.TempDir()

	faux := compat.RegisterFauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{{Id: "faux-1"}},
	})
	defer faux.Unregister()

	authStorage := core.AuthStorageInMemory()
	fauxKey := "faux-key"
	if _, err := authStorage.Modify(ctx, string(faux.GetModel().Provider), func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &fauxKey}, nil
	}, nil); err != nil {
		t.Fatalf("AuthStorage.Modify: %v", err)
	}
	modelRegistry, err := testkit.CreateInMemoryModelRegistry(ctx, authStorage)
	if err != nil {
		t.Fatalf("CreateInMemoryModelRegistry: %v", err)
	}
	modelRuntime, err := testkit.GetModelRuntime(modelRegistry)
	if err != nil {
		t.Fatalf("GetModelRuntime: %v", err)
	}
	defer modelRuntime.Close()

	model := faux.GetModel()
	apiStr := string(model.Api)
	baseUrl := model.BaseUrl
	if err := modelRegistry.RegisterProvider(string(model.Provider), core.ProviderConfigInput{
		BaseUrl: &baseUrl,
		ApiKey:  &fauxKey,
		Api:     &apiStr,
		Models: []core.ProviderModelConfig{
			&core.ProviderChatModelConfig{
				ProviderModelConfigBase: core.ProviderModelConfigBase{
					Id:      model.Id,
					Name:    model.Name,
					Api:     &apiStr,
					BaseUrl: &baseUrl,
					Input:   model.Input,
					Cost:    model.Cost,
				},
				Reasoning:     model.Reasoning,
				ContextWindow: model.ContextWindow,
				MaxTokens:     model.MaxTokens,
			},
		},
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	deferredExt := func(pi core.ExtensionAPI) error {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("repo", typebox.String())
		pi.RegisterTool(core.ToolDefinition{
			Name:        "mcp__github__list_issues",
			Label:       "mcp__github__list_issues",
			Description: "List GitHub repository issues",
			Parameters:  typebox.Object(props),
			Exposure:    core.ToolExposureDeferred,
			Execute: func(_ context.Context, _ string, params *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
				repo, _ := params.GetString("repo")
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "issues:" + repo}},
				}, nil
			},
		})
		return nil
	}

	rl := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
		Cwd:               cwd,
		AgentDir:          agentDir,
		NoExtensions:      true,
		NoSkills:          true,
		NoPromptTemplates: true,
		NoThemes:          true,
		ExtensionFactories: []core.InlineExtension{
			{Name: "tool-search", Factory: toolsearchext.Extension},
			{Name: "deferred-tools", Factory: deferredExt},
		},
	})
	if err := rl.Reload(ctx); err != nil {
		t.Fatalf("ResourceLoader.Reload: %v", err)
	}

	sm, err := core.SessionManagerCreate(cwd, filepath.Join(agentDir, "sessions"))
	if err != nil {
		t.Fatalf("SessionManagerCreate: %v", err)
	}
	res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		AuthStorage:     authStorage,
		ModelRegistry:   modelRegistry,
		SettingsManager: core.SettingsManagerInMemory(),
		SessionManager:  sm,
		ResourceLoader:  rl,
		Model:           model,
	})
	if err != nil {
		t.Fatalf("CreateAgentSession: %v", err)
	}
	session := res.Session
	defer session.Dispose()

	// Verify tool_search and deferred tool are registered, while tool_search starts inactive (DefaultActive: false).
	allToolNames := make([]string, len(session.GetAllTools()))
	for i, info := range session.GetAllTools() {
		allToolNames[i] = info.Name
	}
	if !slices.Contains(allToolNames, toolsearchext.ToolSearchToolName) || !slices.Contains(allToolNames, "mcp__github__list_issues") {
		t.Fatalf("missing expected registered tools: %q", allToolNames)
	}
	if slices.Contains(session.GetActiveToolNames(), toolsearchext.ToolSearchToolName) {
		t.Fatalf("expected tool_search to start inactive, active=%q", session.GetActiveToolNames())
	}
	// Activate tool_search while leaving deferred tool unloaded.
	session.SetActiveToolsByName([]string{"read", toolsearchext.ToolSearchToolName})
	if slices.Contains(session.GetActiveToolNames(), "mcp__github__list_issues") {
		t.Fatalf("deferred tool should not be active initially")
	}

	// Capture declared tool names sent on each provider call.
	var declaredPerTurn [][]string
	stopTool := ai.StopReasonToolUse
	faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			declaredPerTurn = append(declaredPerTurn, ttsAiToolNames(ai.GetCurrentTools(transcript.Messages)))
			return providers.FauxAssistantMessage(
				providers.FauxAssistantBlock{Block: providers.FauxToolCall(
					toolsearchext.ToolSearchToolName,
					jsonx.ObjectOf("query", "github issues"),
					nil,
				)},
				&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
			), nil
		}),
		providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			declaredPerTurn = append(declaredPerTurn, ttsAiToolNames(ai.GetCurrentTools(transcript.Messages)))
			return providers.FauxAssistantMessage(
				providers.FauxAssistantBlock{Block: providers.FauxToolCall(
					"mcp__github__list_issues",
					jsonx.ObjectOf("repo", "pi-go"),
					nil,
				)},
				&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
			), nil
		}),
		providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			declaredPerTurn = append(declaredPerTurn, ttsAiToolNames(ai.GetCurrentTools(transcript.Messages)))
			return providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil), nil
		}),
	})

	if err := session.Prompt(ctx, "find and call github issue tool"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if len(declaredPerTurn) != 3 {
		t.Fatalf("declaredPerTurn len = %d, want 3", len(declaredPerTurn))
	}
	if slices.Contains(declaredPerTurn[0], "mcp__github__list_issues") {
		t.Fatalf("turn 0 should not declare deferred tool yet: %q", declaredPerTurn[0])
	}
	if !slices.Contains(declaredPerTurn[1], "mcp__github__list_issues") {
		t.Fatalf("turn 1 should declare loaded tool after tool_search: %q", declaredPerTurn[1])
	}
	msgs := session.Messages()
	var sawToolResult bool
	for _, m := range msgs {
		if tr, ok := m.(*ai.ToolResultMessage); ok && tr.ToolName == "mcp__github__list_issues" {
			sawToolResult = true
			if tr.IsError || len(tr.Content) == 0 || tr.Content[0].(*ai.TextContent).Text != "issues:pi-go" {
				t.Fatalf("unexpected deferred tool result: %+v", tr)
			}
		}
	}
	if !sawToolResult {
		t.Fatalf("expected ToolResultMessage from mcp__github__list_issues")
	}

	// Reopening the persisted session restores loaded active tools via custom_message / tools change entry.
	sessionFile := sm.GetSessionFile()
	if sessionFile == nil {
		t.Fatalf("expected persisted session file")
	}
	reloadedSM, err := core.SessionManagerOpen(*sessionFile)
	if err != nil {
		t.Fatalf("SessionManagerOpen: %v", err)
	}
	rl2 := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
		Cwd:               cwd,
		AgentDir:          agentDir,
		NoExtensions:      true,
		NoSkills:          true,
		NoPromptTemplates: true,
		NoThemes:          true,
		ExtensionFactories: []core.InlineExtension{
			{Name: "tool-search", Factory: toolsearchext.Extension},
			{Name: "deferred-tools", Factory: deferredExt},
		},
	})
	if err := rl2.Reload(ctx); err != nil {
		t.Fatalf("ResourceLoader.Reload 2: %v", err)
	}
	res2, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		AuthStorage:     authStorage,
		ModelRegistry:   modelRegistry,
		SettingsManager: core.SettingsManagerInMemory(),
		SessionManager:  reloadedSM,
		ResourceLoader:  rl2,
		Model:           model,
	})
	if err != nil {
		t.Fatalf("CreateAgentSession reloaded: %v", err)
	}
	defer res2.Session.Dispose()
	if !slices.Contains(res2.Session.GetActiveToolNames(), "mcp__github__list_issues") {
		t.Fatalf("expected reloaded session to keep loaded tool active, got %q", res2.Session.GetActiveToolNames())
	}
}

func ttsAiToolNames(tools []*ai.Tool) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	return out
}
