// Ported from packages/coding-agent/src/extensions/codemode/tool.ts (pi v1.5.2).

package codemodeext

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codemode"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/extensions/toolsearchext"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

const (
	// CodemodeToolName is the registered tool name for codemode.
	CodemodeToolName = "codemode"
	// CodemodeStoreEntryType is the session custom entry type holding one script's store() writes.
	CodemodeStoreEntryType = "codemode-store"
)

// DefaultCodemodeInlineBudget is the default token budget for inline tool declarations.
var DefaultCodemodeInlineBudget = core.SettingsDefaults.Codemode.InlineBudget

const toolCharsPerToken = 4

// CodemodeStoreEntryData represents one script's persisted store() writes and deletes.
type CodemodeStoreEntryData struct {
	Set    *omap.Map[string, any] `json:"set"`
	Delete []string               `json:"delete"`
}

// CodemodeModelRuntime is the subset of *core.ModelRegistry exposed to scripts via the `models` global.
type CodemodeModelRuntime interface {
	GetModelsOfType(typ ai.ModelType, provider *string) []ai.AnyModel
	GetAvailableOfType(ctx context.Context, typ ai.ModelType, provider *string, options *ai.AuthOperationOptions) ([]ai.AnyModel, error)
	GetModelOfType(typ ai.ModelType, provider, modelId string) ai.AnyModel
	Classify(ctx context.Context, model *ai.ClassifierModel, c *ai.ClassifierContext, options *ai.ModelsClassifierOptions) (*ai.ClassifierResult, error)
	GenerateImages(ctx context.Context, model *ai.ImageModel, c *ai.ImagesContext, options *ai.ModelsImagesOptions) (*ai.AssistantImages, error)
}

// CodemodeToolOptions configures codemode tool declaration, descriptions, and persistence.
type CodemodeToolOptions struct {
	GetToolNamespace  func(toolName string) *core.ToolNamespace
	GetToolGuidelines func() map[string][]string
	Models            *bool
	AppendEntry       func(customType string, data CodemodeStoreEntryData)
	GetMode           func() core.CodemodeMode
	GetInlineBudget   func() *float64
}

// CodemodeSchema defines the parameters schema of the codemode tool.
var CodemodeSchema = func() *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("code", typebox.String(jsonx.ObjectOf("description", "Raw JavaScript source.")))
	return typebox.Object(props)
}()

// CodemodeToolInput is the decoded input passed to ExecuteCodemode.
type CodemodeToolInput struct {
	Code string `json:"code"`
}

// CodemodeNestedCallStatus represents the execution state of one nested call row.
type CodemodeNestedCallStatus string

const (
	CodemodeNestedCallRunning   CodemodeNestedCallStatus = "running"
	CodemodeNestedCallOk        CodemodeNestedCallStatus = "ok"
	CodemodeNestedCallError     CodemodeNestedCallStatus = "error"
	CodemodeNestedCallCancelled CodemodeNestedCallStatus = "cancelled"
)

// CodemodeNestedCall describes one nested tool or model call row rendered in tool details.
type CodemodeNestedCall struct {
	Id         string                   `json:"id"`
	Name       string                   `json:"name"`
	Args       string                   `json:"args"`
	Status     CodemodeNestedCallStatus `json:"status"`
	DurationMs *float64                 `json:"durationMs,omitzero"`
	Error      *string                  `json:"error,omitzero"`
	Cost       *float64                 `json:"cost,omitzero"`
}

// CodemodeToolDetails holds nested calls and optional temp path to full spilled output.
type CodemodeToolDetails struct {
	Calls          []CodemodeNestedCall `json:"calls"`
	FullOutputPath *string              `json:"fullOutputPath,omitzero"`
}

// CodemodeToolSystemPromptContribution carries codemode's default prompt snippet and guidelines.
var CodemodeToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet: "Run JavaScript that calls other tools",
	Guidelines: []string{
		"Use codemode to batch independent tool calls (Promise.allSettled), chain them, or filter large output, instead of many separate calls.",
	},
}

// CodemodeDocsPath is the filesystem path to the codemode reference documentation.
var CodemodeDocsPath = filepath.Join(utils.GetDocsPath(), "codemode.md")

const toolDescriptionIntro = `Run JavaScript that calls other tools. The input is raw JavaScript (not JSON, no code fence), run as an async function body in a QuickJS sandbox: top-level ` + "`await`" + ` and ` + "`return`" + ` work. No Node, file system, network, or timers.
- ` + "`await tools.<name>({ ...args })`" + ` resolves to a string, or an object if the tool's declaration says so, and rejects with an Error on failure. A tool whose declaration returns an object resolves on failure too when it returned that object; check its fields. Calls still running when the script ends are cancelled.
- Optional first line: ` + "`// @options: {\"max_output_tokens\": 10000, \"timeout_ms\": 60000}`"

var (
	toolDiscoveryGlobalsRe = regexp.MustCompile(`\b(searchTools|describeNamespace|describeTool|ALL_TOOLS)\b`)
	toolWordRe             = regexp.MustCompile(`\w+`)
)

func toolDescribeGlobals(models bool) string {
	lines := []string{
		"Globals:",
		"- `text(value)`, `image(dataUrlOrImageBlock)`, `console.log(...)`, and top-level `return` add output; `exit()` ends the script. With several text items, each starts with a `==> text N/M <==` line, and `console` lines follow the other output in one `<console_output>` block. `image()` also saves the image to a temp file and the result names its path.",
		"- `store(key, value)` and `load(key)` keep JSON values across codemode calls.",
		"- `ALL_TOOLS`, `await searchTools(query, { limit?, namespace? })`, `await describeTool(name)`, `await describeNamespace(name)`: find unlisted tools, such as MCP tools.",
	}
	if models {
		lines = append(lines, fmt.Sprintf("- `models`: classifiers and image generation. Read %s first.", CodemodeDocsPath))
	}
	return strings.Join(lines, "\n")
}

// toolSchemaToJSONSchema unwraps *typebox.Schema values into plain *jsonx.Object trees so
// codemode declarations and ref resolvers traverse nested object schemas uniformly.
func toolSchemaToJSONSchema(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case *typebox.Schema:
		if x == nil || x.Object == nil {
			return nil
		}
		return toolSchemaToJSONSchema(x.Object)
	case typebox.Schema:
		if x.Object == nil {
			return nil
		}
		return toolSchemaToJSONSchema(x.Object)
	case *jsonx.Object:
		if x == nil {
			return nil
		}
		out := jsonx.NewObject()
		for _, k := range x.Keys() {
			val, _ := x.Get(k)
			out.Set(k, toolSchemaToJSONSchema(val))
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = toolSchemaToJSONSchema(item)
		}
		return out
	case []*typebox.Schema:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = toolSchemaToJSONSchema(item)
		}
		return out
	case *omap.Map[string, *typebox.Schema]:
		if x == nil {
			return nil
		}
		out := jsonx.NewObject()
		for _, k := range x.Keys() {
			val, _ := x.Get(k)
			out.Set(k, toolSchemaToJSONSchema(val))
		}
		return out
	case *omap.Map[string, any]:
		if x == nil {
			return nil
		}
		out := jsonx.NewObject()
		for _, k := range x.Keys() {
			val, _ := x.Get(k)
			out.Set(k, toolSchemaToJSONSchema(val))
		}
		return out
	case map[string]any:
		if x == nil {
			return nil
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.SortFunc(keys, js.CompareUTF16)
		out := jsonx.NewObject()
		for _, k := range keys {
			out.Set(k, toolSchemaToJSONSchema(x[k]))
		}
		return out
	default:
		return v
	}
}

func toolTextOutputSchema() *jsonx.Object {
	return jsonx.ObjectOf("type", "string")
}

// ToCodemodeDeclaration adapts an *agent.AgentTool and optional guidelines into a codemode declaration.
func ToCodemodeDeclaration(tool *agent.AgentTool, guidelines ...[]string) *codemode.CodemodeTool {
	var guideList []string
	if len(guidelines) > 0 {
		guideList = guidelines[0]
	}
	var bullets []string
	for _, g := range guideList {
		if trimmed := js.Trim(g); trimmed != "" {
			bullets = append(bullets, "- "+trimmed)
		}
	}
	desc := tool.Description
	if len(bullets) > 0 {
		desc = js.Trim(tool.Description) + "\n\n" + strings.Join(bullets, "\n")
	}
	var outSchema codemode.CodemodeJsonSchema
	if tool.OutputSchema != nil {
		outSchema = toolSchemaToJSONSchema(tool.OutputSchema)
	}
	if outSchema == nil {
		outSchema = toolTextOutputSchema()
	}
	return &codemode.CodemodeTool{
		Name:         tool.Name,
		Description:  &desc,
		InputSchema:  toolSchemaToJSONSchema(tool.Parameters),
		OutputSchema: outSchema,
	}
}

// GetCodemodeCallableTools filters out the codemode tool itself from the callable list.
func GetCodemodeCallableTools(tools []*agent.AgentTool) []*agent.AgentTool {
	out := make([]*agent.AgentTool, 0, len(tools))
	for _, tool := range tools {
		if tool != nil && tool.Name != CodemodeToolName {
			out = append(out, tool)
		}
	}
	return out
}

// CodemodeDescriptionOptions configures CreateCodemodeDescription.
type CodemodeDescriptionOptions struct {
	Models       bool
	Namespaces   map[string]*core.ToolNamespace
	Deferred     map[string]struct{}
	Guidelines   map[string][]string
	InlineBudget *float64
}

func toolRenderToolSection(decl *codemode.CodemodeTool) string {
	id := codemode.ToCodemodeIdentifier(decl.Name)
	heading := fmt.Sprintf("### `%s`", id)
	if id != decl.Name {
		heading = fmt.Sprintf("### `%s` (`%s`)", id, decl.Name)
	}
	return heading + "\n" + js.Trim(codemode.RenderToolSample(decl, nil))
}

type toolCatalogEntry struct {
	name    string
	section string
	cost    int
}

type toolCatalogGroup struct {
	namespace *core.ToolNamespace
	entries   []toolCatalogEntry
}

func toolSelectCatalog(groups []toolCatalogGroup, budget *float64) map[string]struct{} {
	shown := make(map[string]struct{})
	if budget == nil {
		for _, group := range groups {
			for _, entry := range group.entries {
				shown[entry.name] = struct{}{}
			}
		}
		return shown
	}
	queues := make([][]toolCatalogEntry, 0, len(groups))
	for _, group := range groups {
		if len(group.entries) == 0 {
			continue
		}
		sorted := slices.Clone(group.entries)
		slices.SortStableFunc(sorted, func(a, b toolCatalogEntry) int {
			return a.cost - b.cost
		})
		queues = append(queues, sorted)
	}
	remaining := *budget
	for len(queues) > 0 {
		nextQueues := make([][]toolCatalogEntry, 0, len(queues))
		for _, queue := range queues {
			next := queue[0]
			if float64(next.cost) > remaining {
				continue
			}
			remaining -= float64(next.cost)
			shown[next.name] = struct{}{}
			if len(queue) > 1 {
				nextQueues = append(nextQueues, queue[1:])
			}
		}
		queues = nextQueues
	}
	return shown
}

// CreateCodemodeDescription renders the model-facing codemode tool description with
// globals, search helpers, and namespace-grouped tool declarations up to inlineBudget.
func CreateCodemodeDescription(tools []*agent.AgentTool, options ...*CodemodeDescriptionOptions) string {
	var opts *CodemodeDescriptionOptions
	if len(options) > 0 {
		opts = options[0]
	}

	callable := GetCodemodeCallableTools(tools)
	var declarations []*codemode.CodemodeTool
	for _, tool := range callable {
		if opts != nil && opts.Deferred != nil {
			if _, deferred := opts.Deferred[tool.Name]; deferred {
				continue
			}
		}
		var guides []string
		if opts != nil && opts.Guidelines != nil {
			guides = opts.Guidelines[tool.Name]
		}
		declarations = append(declarations, ToCodemodeDeclaration(tool, guides))
	}

	groupOrder := []string{""}
	groups := map[string]*toolCatalogGroup{
		"": {namespace: nil, entries: nil},
	}
	for _, decl := range declarations {
		var ns *core.ToolNamespace
		if opts != nil && opts.Namespaces != nil {
			ns = opts.Namespaces[decl.Name]
		}
		key := ""
		if ns != nil {
			key = "ns:" + ns.Name
		}
		group, exists := groups[key]
		if !exists {
			group = &toolCatalogGroup{namespace: ns}
			groups[key] = group
			groupOrder = append(groupOrder, key)
		}
		section := toolRenderToolSection(decl)
		cost := int(math.Ceil(float64(js.Len(section)) / toolCharsPerToken))
		group.entries = append(group.entries, toolCatalogEntry{
			name:    decl.Name,
			section: section,
			cost:    cost,
		})
	}

	ordered := make([]toolCatalogGroup, 0, len(groupOrder))
	for _, key := range groupOrder {
		ordered = append(ordered, *groups[key])
	}
	slices.SortStableFunc(ordered, func(a, b toolCatalogGroup) int {
		if a.namespace == nil && b.namespace == nil {
			return 0
		}
		if a.namespace == nil {
			return -1
		}
		if b.namespace == nil {
			return 1
		}
		return js.LocaleCompare(a.namespace.Name, b.namespace.Name)
	})

	var budget *float64
	if opts != nil {
		budget = opts.InlineBudget
	}
	shown := toolSelectCatalog(ordered, budget)

	models := opts != nil && opts.Models
	sections := []string{toolDescriptionIntro, toolDescribeGlobals(models)}
	if len(declarations) == 0 {
		return strings.Join(sections, "\n\n")
	}

	toolSections := []string{"Nested tools:"}
	for _, group := range ordered {
		var visible []toolCatalogEntry
		for _, entry := range group.entries {
			if _, ok := shown[entry.name]; ok {
				visible = append(visible, entry)
			}
		}
		if group.namespace != nil {
			listing := ""
			if len(visible) < len(group.entries) {
				if len(visible) == 0 {
					listing = " (tools not listed)"
				} else {
					listing = " (some tools not listed)"
				}
			}
			nsHeader := fmt.Sprintf("## %s%s", group.namespace.Name, listing)
			if group.namespace.Description != nil {
				if desc := js.Trim(*group.namespace.Description); desc != "" {
					nsHeader += "\n" + desc
				}
			}
			toolSections = append(toolSections, nsHeader)
		}
		for _, entry := range visible {
			toolSections = append(toolSections, entry.section)
		}
	}
	sections = append(sections, strings.Join(toolSections, "\n\n"))
	return strings.Join(sections, "\n\n")
}

func toolAsJSONObject(v any) *jsonx.Object {
	if v == nil {
		return nil
	}
	if obj, ok := v.(*jsonx.Object); ok {
		return obj
	}
	if sch, ok := v.(*typebox.Schema); ok && sch != nil {
		return sch.Object
	}
	return nil
}

func toolFollowRef(schema any, root codemode.CodemodeJsonSchema) any {
	seen := make(map[*jsonx.Object]struct{})
	current := schema
	for {
		obj := toolAsJSONObject(current)
		if obj == nil {
			break
		}
		if _, visited := seen[obj]; visited {
			break
		}
		rawRef, hasRef := obj.Get("$ref")
		ref, ok := rawRef.(string)
		if !hasRef || !ok {
			break
		}
		target := toolsearchext.ToolResolveSchemaRef(ref, root)
		if target == nil {
			break
		}
		seen[obj] = struct{}{}
		current = target
	}
	return current
}

func toolJSONString(v any) string {
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

func toolDescribeFields(schema any, root codemode.CodemodeJsonSchema, depth int) (string, bool) {
	resolved := toolAsJSONObject(toolFollowRef(schema, root))
	if resolved == nil {
		return "", false
	}
	typ, _ := resolved.Get("type")
	if typStr, ok := typ.(string); !ok || typStr != "object" {
		return "", false
	}
	rawProps, _ := resolved.Get("properties")
	props := toolAsJSONObject(rawProps)
	if props == nil {
		return "", false
	}

	requiredNames := make(map[string]struct{})
	if rawReq, hasReq := resolved.Get("required"); hasReq {
		if reqArr, ok := rawReq.([]any); ok {
			for _, item := range reqArr {
				if name, ok := item.(string); ok {
					requiredNames[name] = struct{}{}
				}
			}
		}
	}

	fields := make([]string, 0, props.Len())
	for _, name := range props.Keys() {
		field, _ := props.Get(name)
		target := toolAsJSONObject(toolFollowRef(field, root))
		nested := ""
		if target != nil && target.Has("const") {
			constVal, _ := target.Get("const")
			nested = toolJSONString(constVal)
		} else if depth > 0 {
			nested, _ = toolDescribeFields(field, root, depth-1)
		}
		opt := "?"
		if _, req := requiredNames[name]; req {
			opt = ""
		}
		entry := name + opt
		if nested != "" {
			entry += ": " + nested
		}
		fields = append(fields, entry)
	}
	return "{ " + strings.Join(fields, ", ") + " }", true
}

func toolDescribeUnionFields(schema any, root codemode.CodemodeJsonSchema) (string, bool) {
	resolved := toolAsJSONObject(toolFollowRef(schema, root))
	if resolved == nil {
		return "", false
	}
	var variants []any
	if raw, ok := resolved.Get("anyOf"); ok {
		variants, _ = raw.([]any)
	} else if raw, ok := resolved.Get("oneOf"); ok {
		variants, _ = raw.([]any)
	}
	if len(variants) == 0 {
		return "", false
	}
	described := make([]string, 0, len(variants))
	for _, variant := range variants {
		fields, ok := toolDescribeFields(variant, root, 0)
		if !ok {
			return "", false
		}
		described = append(described, fields)
	}
	return strings.Join(described, " | "), true
}

func toolDescribeOutput(name string, schema codemode.CodemodeJsonSchema) string {
	typeStr := codemode.RenderToolOutputType(schema)
	if typeStr == "string" {
		return "a string"
	}
	if schema != nil {
		if fields, ok := toolDescribeFields(schema, schema, 1); ok {
			return "`" + fields + "`"
		}
		if fields, ok := toolDescribeUnionFields(schema, schema); ok {
			return "`" + fields + "`"
		}
	}
	if !strings.Contains(typeStr, "\n") {
		return "`" + typeStr + "`"
	}
	return fmt.Sprintf("a value; `await describeTool(%s)` shows its type", toolJSONString(name))
}

func toolDescribeScriptCall(tool *agent.AgentTool) string {
	decl := ToCodemodeDeclaration(tool)
	return fmt.Sprintf(
		"%s\n\nCodemode: `tools.%s(args)` resolves to %s.",
		js.Trim(tool.Description),
		codemode.ToCodemodeIdentifier(tool.Name),
		toolDescribeOutput(tool.Name, decl.OutputSchema),
	)
}

func toolPrepareCodemodeLoadout(loadout core.ToolLoadout, options *CodemodeToolOptions) *core.ToolLoadoutChanges {
	mode := core.SettingsDefaults.Codemode.Mode
	if options != nil && options.GetMode != nil {
		mode = options.GetMode()
	}
	isDirect := func(tool *agent.AgentTool) bool {
		return loadout.GetExposure != nil && loadout.GetExposure(tool.Name) == core.ToolExposureDirect
	}

	callable := GetCodemodeCallableTools(loadout.Available)
	callableNames := make(map[string]struct{}, len(callable))
	for _, tool := range callable {
		callableNames[tool.Name] = struct{}{}
	}

	descriptions := make(map[string]string)
	if mode == core.CodemodeModeOn {
		for _, tool := range loadout.Declared {
			if tool == nil {
				continue
			}
			if _, ok := callableNames[tool.Name]; ok {
				descriptions[tool.Name] = toolDescribeScriptCall(tool)
			}
		}
	}

	listed := make([]*agent.AgentTool, 0, len(callable))
	for _, tool := range callable {
		if mode == core.CodemodeModeOnly || !isDirect(tool) {
			listed = append(listed, tool)
		}
	}

	namespaces := make(map[string]*core.ToolNamespace)
	guidelines := make(map[string][]string, len(listed))
	deferred := make(map[string]struct{})
	for _, tool := range listed {
		if loadout.GetNamespace != nil {
			if ns := loadout.GetNamespace(tool.Name); ns != nil {
				namespaces[tool.Name] = ns
			}
		}
		if loadout.GetPromptGuidelines != nil {
			guidelines[tool.Name] = loadout.GetPromptGuidelines(tool.Name)
		}
		if loadout.GetExposure != nil && loadout.GetExposure(tool.Name) == core.ToolExposureDeferred {
			deferred[tool.Name] = struct{}{}
		}
	}

	inlineBudget := &DefaultCodemodeInlineBudget
	if options != nil && options.GetInlineBudget != nil {
		inlineBudget = options.GetInlineBudget()
	}
	if inlineBudget == nil {
		b := DefaultCodemodeInlineBudget
		inlineBudget = &b
	}

	modelsEnabled := options != nil && options.Models != nil && *options.Models
	descriptions[CodemodeToolName] = CreateCodemodeDescription(listed, &CodemodeDescriptionOptions{
		Models:       modelsEnabled,
		Namespaces:   namespaces,
		Deferred:     deferred,
		Guidelines:   guidelines,
		InlineBudget: inlineBudget,
	})

	declaredNames := make(map[string]struct{}, len(loadout.Declared))
	for _, tool := range loadout.Declared {
		if tool != nil {
			declaredNames[tool.Name] = struct{}{}
		}
	}

	var hiddenDeclarations []string
	if mode == core.CodemodeModeOnly {
		for _, tool := range callable {
			if isDirect(tool) {
				if _, isDeclared := declaredNames[tool.Name]; isDeclared {
					hiddenDeclarations = append(hiddenDeclarations, tool.Name)
				}
			}
		}
	}

	return &core.ToolLoadoutChanges{
		Descriptions:     descriptions,
		HideDeclarations: hiddenDeclarations,
	}
}

func toolGetScriptReach(input *jsonx.Object) core.ToolReachesResult {
	if input == nil {
		return core.ToolReachesResult{Tools: []string{}}
	}
	raw, ok := input.Get("code")
	if !ok {
		return core.ToolReachesResult{Tools: []string{}}
	}
	code, ok := raw.(string)
	if !ok {
		return core.ToolReachesResult{Tools: []string{}}
	}
	if toolDiscoveryGlobalsRe.MatchString(code) {
		return core.ToolReachesResult{All: true}
	}
	words := toolWordRe.FindAllString(code, -1)
	if words == nil {
		words = []string{}
	}
	return core.ToolReachesResult{Tools: words}
}

// CreateCodemodeToolDefinition creates the core.ToolDefinition for codemode.
func CreateCodemodeToolDefinition(options ...*CodemodeToolOptions) core.ToolDefinition {
	var opts *CodemodeToolOptions
	if len(options) > 0 {
		opts = options[0]
	}
	modelsEnabled := opts != nil && opts.Models != nil && *opts.Models
	snippet := CodemodeToolSystemPromptContribution.Snippet
	grammar := codemode.CodemodeSourceGrammar

	return core.ToolDefinition{
		Name:             CodemodeToolName,
		Label:            CodemodeToolName,
		Description:      CreateCodemodeDescription(nil, &CodemodeDescriptionOptions{Models: modelsEnabled}),
		PromptSnippet:    &snippet,
		PromptGuidelines: slices.Clone(CodemodeToolSystemPromptContribution.Guidelines),
		Parameters:       CodemodeSchema,
		Exposure:         core.ToolExposureModelOnly,
		PrepareLoadout: func(loadout core.ToolLoadout) *core.ToolLoadoutChanges {
			return toolPrepareCodemodeLoadout(loadout, opts)
		},
		ReachesTools: toolGetScriptReach,
		ConstrainedSampling: ai.ToolConstrainedSamplingValue{
			V: &ai.ConstrainedSamplingGrammar{
				Type: "grammar",
				Variants: ai.GrammarVariants{
					OpenaiLark: &grammar,
				},
			},
		},
		Execute: func(
			ctx context.Context,
			toolCallId string,
			params *jsonx.Object,
			onUpdate agent.AgentToolUpdateCallback,
			ectx core.ExtensionToolContext,
		) (agent.AgentToolResult, error) {
			var code string
			if params != nil {
				if raw, ok := params.Get("code"); ok {
					if s, ok := raw.(string); ok {
						code = s
					}
				}
			}
			executor := LoadCodemodeExecutor()
			return executor(ctx, toolCallId, CodemodeToolInput{Code: code}, onUpdate, ectx, opts)
		},
		RenderCall:   CodemodeRenderers.RenderCall,
		RenderResult: CodemodeRenderers.RenderResult,
	}
}

// CreateCodemodeTool creates a ready-to-use *agent.AgentTool for codemode listing the given tools.
func CreateCodemodeTool(tools []*agent.AgentTool, options ...*CodemodeToolOptions) *agent.AgentTool {
	def := CreateCodemodeToolDefinition(options...)
	tool := core.WrapToolDefinition(def)
	var models bool
	if len(options) > 0 && options[0] != nil && options[0].Models != nil {
		models = *options[0].Models
	}
	tool.Description = CreateCodemodeDescription(tools, &CodemodeDescriptionOptions{Models: models})
	return tool
}
