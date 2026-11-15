// Ported from packages/coding-agent/src/extensions/codemode/execute.ts (pi v1.5.2).

package codemodeext

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codemode"
	codemoderuntime "github.com/keejkrej/pi-go/codemode/runtime"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/extensions/toolsearchext"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	execArgsPreviewChars     = 200
	execErrorPreviewChars    = 500
	execMaxConcurrentModels  = 4
	execMemoryLimitBytes     = 256 * 1024 * 1024
	execDefaultMaxWorkers    = 2
	execWorkerWaitMs         = 60_000
	execDefaultMaxOutputToks = 10_000
	execCharsPerToken        = 4
)

var (
	execVowelPrefixRe = regexp.MustCompile(`^[aeiou]`)
	execImageExts     = map[string]string{
		"image/png":  ".png",
		"image/jpeg": ".jpg",
		"image/gif":  ".gif",
		"image/webp": ".webp",
	}
	execModelTypes = map[ai.ModelType]struct{}{
		ai.ModelTypeChat:       {},
		ai.ModelTypeImage:      {},
		ai.ModelTypeClassifier: {},
	}
	execOrderedModelTypes = []ai.ModelType{
		ai.ModelTypeChat,
		ai.ModelTypeImage,
		ai.ModelTypeClassifier,
	}
)

type execWaiter struct {
	ch      chan struct{}
	granted bool
}

// execGate bounds concurrent entry in arrival order without holding a mutex across
// channel waits or the guarded work itself.
type execGate struct {
	mu      sync.Mutex
	active  int
	limitFn func() int
	waiters []*execWaiter
}

func execNewGate(limitFn func() int) *execGate {
	return &execGate{limitFn: limitFn}
}

func (g *execGate) admitLocked() {
	limit := g.limitFn()
	for len(g.waiters) > 0 && g.active < limit {
		w := g.waiters[0]
		g.waiters = g.waiters[1:]
		w.granted = true
		g.active++
		close(w.ch)
	}
}

func (g *execGate) releaseLocked() {
	g.active--
	g.admitLocked()
}

func (g *execGate) removeWaiterLocked(target *execWaiter) {
	for i, w := range g.waiters {
		if w == target {
			g.waiters = append(g.waiters[:i], g.waiters[i+1:]...)
			return
		}
	}
}

func (g *execGate) acquire(ctx context.Context, timeout *time.Duration) string {
	if ctx != nil && ctx.Err() != nil {
		return "aborted"
	}
	w := &execWaiter{ch: make(chan struct{})}
	g.mu.Lock()
	g.waiters = append(g.waiters, w)
	g.admitLocked()
	granted := w.granted
	g.mu.Unlock()
	if granted {
		return "reserved"
	}

	var timer *time.Timer
	var timerCh <-chan time.Time
	if timeout != nil {
		timer = time.NewTimer(*timeout)
		defer timer.Stop()
		timerCh = timer.C
	}

	var doneCh <-chan struct{}
	if ctx != nil {
		doneCh = ctx.Done()
	}

	select {
	case <-w.ch:
		return "reserved"
	case <-doneCh:
		g.mu.Lock()
		if w.granted {
			g.releaseLocked()
		} else {
			g.removeWaiterLocked(w)
		}
		g.mu.Unlock()
		return "aborted"
	case <-timerCh:
		g.mu.Lock()
		if w.granted {
			g.releaseLocked()
		} else {
			g.removeWaiterLocked(w)
		}
		g.mu.Unlock()
		return "timeout"
	}
}

func (g *execGate) release() {
	g.mu.Lock()
	g.releaseLocked()
	g.mu.Unlock()
}

func execMaxCodemodeWorkers() int {
	raw := strings.TrimSpace(os.Getenv("PI_CODEMODE_MAX_WORKERS"))
	if raw == "" {
		return execDefaultMaxWorkers
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return execDefaultMaxWorkers
	}
	return n
}

var execWorkerGate = execNewGate(execMaxCodemodeWorkers)

func execTruncateText(text string, maxChars int) string {
	if js.Len(text) > maxChars {
		return core.SliceHead(text, maxChars-3) + "..."
	}
	return text
}

func execPreviewArgs(args any) string {
	if args == nil {
		return ""
	}
	raw, err := jsonx.Marshal(args)
	if err != nil {
		return ""
	}
	return execTruncateText(string(raw), execArgsPreviewChars)
}

func execTextOf(result agent.AgentToolResult) string {
	var parts []string
	for _, block := range result.Content {
		if txt, ok := block.(*ai.TextContent); ok && txt != nil {
			parts = append(parts, txt.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func execToModelType(value any) (ai.ModelType, error) {
	if s, ok := value.(string); ok {
		mt := ai.ModelType(s)
		if _, valid := execModelTypes[mt]; valid {
			return mt, nil
		}
	}
	return "", fmt.Errorf(`Unknown model type %s. Use "chat", "image", or "classifier".`, toolJSONString(value))
}

func execToProvider(value any) (*string, error) {
	if value == nil {
		return nil, nil
	}
	s, ok := value.(string)
	if !ok {
		return nil, errors.New("provider must be a string")
	}
	return &s, nil
}

// execToModelInfo clones a model summary without headers because models.json headers can carry credentials.
func execToModelInfo(model ai.AnyModel) any {
	switch m := model.(type) {
	case *ai.Model:
		if m == nil {
			return nil
		}
		cp := *m
		cp.Headers = nil
		return &cp
	case *ai.ImageModel:
		if m == nil {
			return nil
		}
		cp := *m
		cp.Headers = nil
		return &cp
	case *ai.ClassifierModel:
		if m == nil {
			return nil
		}
		cp := *m
		cp.Headers = nil
		return &cp
	default:
		encoded, err := jsonx.Encode(model)
		if err != nil {
			return model
		}
		if obj, ok := encoded.(*jsonx.Object); ok && obj != nil {
			obj.Delete("headers")
			return obj
		}
		return encoded
	}
}

func execWithArticle(word string) string {
	article := "a"
	if execVowelPrefixRe.MatchString(word) {
		article = "an"
	}
	return article + " " + word
}

type execObjectView struct {
	keys   []string
	values []any
	byKey  map[string]any
}

func execAsObject(v any) (*execObjectView, bool) {
	switch o := v.(type) {
	case *jsonx.Object:
		if o == nil {
			return nil, false
		}
		keys := o.Keys()
		view := &execObjectView{
			keys:   keys,
			values: make([]any, len(keys)),
			byKey:  make(map[string]any, len(keys)),
		}
		for i, k := range keys {
			val, _ := o.Get(k)
			view.values[i] = val
			view.byKey[k] = val
		}
		return view, true
	case map[string]any:
		if o == nil {
			return nil, false
		}
		keys := make([]string, 0, len(o))
		for k := range o {
			keys = append(keys, k)
		}
		slices.SortFunc(keys, js.CompareUTF16)
		view := &execObjectView{
			keys:   keys,
			values: make([]any, len(keys)),
			byKey:  make(map[string]any, len(keys)),
		}
		for i, k := range keys {
			val := o[k]
			view.values[i] = val
			view.byKey[k] = val
		}
		return view, true
	default:
		return nil, false
	}
}

func (v *execObjectView) get(key string) (any, bool) {
	if v == nil {
		return nil, false
	}
	val, ok := v.byKey[key]
	return val, ok
}

func execDescribeValue(value any) string {
	if value == nil {
		return "null"
	}
	if arr, ok := value.([]any); ok {
		if len(arr) == 0 {
			return "an empty array"
		}
		return "an array"
	}
	if obj, ok := execAsObject(value); ok {
		if len(obj.keys) == 0 {
			return "{}"
		}
		limit := len(obj.keys)
		suffix := ""
		if limit > 6 {
			limit = 6
			suffix = ", ..."
		}
		return "{ " + strings.Join(obj.keys[:limit], ", ") + suffix + " }"
	}
	switch value.(type) {
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case float64, int, int64:
		return "a number"
	default:
		return fmt.Sprintf("a %T", value)
	}
}

func execDescribeField(obj *execObjectView, key string) string {
	val, has := obj.get(key)
	if !has {
		return "undefined"
	}
	return execDescribeValue(val)
}

const execClassifierContextShape = `{ state: { ... }, images?: [{ type: "image", data: <base64>, mimeType }], questions: { <id>: { type: "choice", instructions, criteria: { <label>: <meaning> } } | { type: "score", instructions, criteria: [<lowest level>, ..., <highest level>] } | { type: "bool", instructions, criteria: { true: <meaning>, false: <meaning> } } } }`

func execIsNonEmptyStringSlice(values []any) bool {
	if len(values) == 0 {
		return false
	}
	for _, v := range values {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

func execCheckClassifierContext(raw any) (*ai.ClassifierContext, error) {
	fail := func(problem string) error {
		return fmt.Errorf(
			"models.classify() %s. Expected context: %s. See \"Classify\" in %s.",
			problem,
			execClassifierContextShape,
			CodemodeDocsPath,
		)
	}
	rootObj, ok := execAsObject(raw)
	if !ok {
		return nil, fail("expects a context object as its second argument, got " + execDescribeValue(raw))
	}
	stateRaw, hasState := rootObj.get("state")
	if _, ok := execAsObject(stateRaw); !hasState || !ok {
		return nil, fail("context.state must be an object, got " + execDescribeField(rootObj, "state"))
	}
	if imagesRaw, hasImages := rootObj.get("images"); hasImages {
		imgs, ok := imagesRaw.([]any)
		if !ok {
			return nil, fail("context.images must be an array, got " + execDescribeValue(imagesRaw))
		}
		for i, imgRaw := range imgs {
			imgObj, ok := execAsObject(imgRaw)
			if !ok {
				return nil, fail(fmt.Sprintf("context.images[%d] must be an image block, got %s", i, execDescribeValue(imgRaw)))
			}
			t, _ := imgObj.get("type")
			d, _ := imgObj.get("data")
			m, _ := imgObj.get("mimeType")
			tStr, tok := t.(string)
			_, dok := d.(string)
			_, mok := m.(string)
			if !tok || tStr != "image" || !dok || !mok {
				return nil, fail(fmt.Sprintf("context.images[%d] must be an image block, got %s", i, execDescribeValue(imgRaw)))
			}
		}
	}
	questionsRaw, hasQuestions := rootObj.get("questions")
	questionsObj, ok := execAsObject(questionsRaw)
	if !hasQuestions || !ok || len(questionsObj.keys) == 0 {
		return nil, fail("context.questions must map question IDs to questions, got " + execDescribeField(rootObj, "questions"))
	}
	for i, id := range questionsObj.keys {
		qRaw := questionsObj.values[i]
		at := "context.questions." + id
		qObj, ok := execAsObject(qRaw)
		if !ok {
			return nil, fail(at + " must be a question object, got " + execDescribeValue(qRaw))
		}
		instRaw, _ := qObj.get("instructions")
		if _, ok := instRaw.(string); !ok {
			return nil, fail(at + ".instructions must be a string")
		}
		qType, hasType := qObj.get("type")
		criteriaRaw, _ := qObj.get("criteria")
		switch qType {
		case "choice":
			critObj, ok := execAsObject(criteriaRaw)
			if !ok || !execIsNonEmptyStringSlice(critObj.values) {
				return nil, fail(at + ` is a "choice" question, so criteria must map each label to its meaning`)
			}
		case "score":
			critArr, ok := criteriaRaw.([]any)
			if !ok || !execIsNonEmptyStringSlice(critArr) {
				return nil, fail(at + ` is a "score" question, so criteria must list the levels as strings, lowest first`)
			}
		case "bool":
			critObj, ok := execAsObject(criteriaRaw)
			trueVal, hasTrue := critObj.get("true")
			falseVal, hasFalse := critObj.get("false")
			_, tStr := trueVal.(string)
			_, fStr := falseVal.(string)
			if !ok || !hasTrue || !hasFalse || !tStr || !fStr {
				return nil, fail(at + ` is a "bool" question, so criteria must be { true: string, false: string }`)
			}
		default:
			gotType := "undefined"
			if hasType {
				gotType = toolJSONString(qType)
			}
			return nil, fail(fmt.Sprintf(`%s.type must be "choice", "score", or "bool", got %s`, at, gotType))
		}
	}

	encoded, err := jsonx.Marshal(raw)
	if err != nil {
		return nil, fail(err.Error())
	}
	var ctx ai.ClassifierContext
	if err := jsonx.Unmarshal(encoded, &ctx); err != nil {
		return nil, fail(err.Error())
	}
	return &ctx, nil
}

func execCheckImagesContext(raw any) (*ai.ImagesContext, error) {
	fail := func(problem string) error {
		return fmt.Errorf(
			`models.generateImages() %s. Expected context: { input: [{ type: "text", text: <prompt> }, ...optional { type: "image", data: <base64>, mimeType } references] }. See "Generate images" in %s.`,
			problem,
			CodemodeDocsPath,
		)
	}
	rootObj, ok := execAsObject(raw)
	if !ok {
		return nil, fail("expects a context object as its second argument, got " + execDescribeValue(raw))
	}
	inputRaw, hasInput := rootObj.get("input")
	inputArr, ok := inputRaw.([]any)
	if !hasInput || !ok || len(inputArr) == 0 {
		return nil, fail("context.input must be a non-empty array of blocks, got " + execDescribeField(rootObj, "input"))
	}
	for i, blockRaw := range inputArr {
		blockObj, ok := execAsObject(blockRaw)
		if ok {
			bType, _ := blockObj.get("type")
			if bType == "text" {
				if txt, _ := blockObj.get("text"); txt != nil {
					if _, isStr := txt.(string); isStr {
						continue
					}
				}
			}
			if bType == "image" {
				d, dok := blockObj.get("data")
				m, mok := blockObj.get("mimeType")
				_, dStr := d.(string)
				_, mStr := m.(string)
				if dok && mok && dStr && mStr {
					continue
				}
			}
		}
		return nil, fail(fmt.Sprintf("context.input[%d] must be a text or image block, got %s", i, execDescribeValue(blockRaw)))
	}

	encoded, err := jsonx.Marshal(raw)
	if err != nil {
		return nil, fail(err.Error())
	}
	var ctx ai.ImagesContext
	if err := jsonx.Unmarshal(encoded, &ctx); err != nil {
		return nil, fail(err.Error())
	}
	return &ctx, nil
}

func execDecodeStoreEntryData(raw any) (*CodemodeStoreEntryData, bool) {
	switch d := raw.(type) {
	case *CodemodeStoreEntryData:
		if d == nil || d.Set == nil || d.Delete == nil {
			return nil, false
		}
		return d, true
	case CodemodeStoreEntryData:
		if d.Set == nil || d.Delete == nil {
			return nil, false
		}
		cp := d
		return &cp, true
	case *jsonx.Object:
		if d == nil {
			return nil, false
		}
		setRaw, _ := d.Get("set")
		setObj, ok := setRaw.(*jsonx.Object)
		if !ok || setObj == nil {
			return nil, false
		}
		delRaw, _ := d.Get("delete")
		delArr, ok := delRaw.([]any)
		if !ok {
			return nil, false
		}
		deleted := make([]string, 0, len(delArr))
		for _, item := range delArr {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			deleted = append(deleted, s)
		}
		setMap := omap.NewMap[string, any]()
		for _, k := range setObj.Keys() {
			val, _ := setObj.Get(k)
			setMap.Set(k, val)
		}
		return &CodemodeStoreEntryData{Set: setMap, Delete: deleted}, true
	case map[string]any:
		if d == nil {
			return nil, false
		}
		setObj, hasSet := execAsObject(d["set"])
		delArr, hasDel := d["delete"].([]any)
		if !hasSet || !hasDel {
			return nil, false
		}
		deleted := make([]string, 0, len(delArr))
		for _, item := range delArr {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			deleted = append(deleted, s)
		}
		setMap := omap.NewMap[string, any]()
		for i, k := range setObj.keys {
			setMap.Set(k, setObj.values[i])
		}
		return &CodemodeStoreEntryData{Set: setMap, Delete: deleted}, true
	default:
		return nil, false
	}
}

// ReadCodemodeStore reconstructs the script load() key-value store from codemode-store entries on branch.
func ReadCodemodeStore(branch []core.SessionEntry) map[string]any {
	store := make(map[string]any)
	for _, entry := range branch {
		ce, ok := entry.(*core.CustomEntry)
		if !ok || ce == nil || ce.CustomType != CodemodeStoreEntryType {
			continue
		}
		data, ok := execDecodeStoreEntryData(ce.Data)
		if !ok {
			continue
		}
		for _, key := range data.Delete {
			delete(store, key)
		}
		for _, key := range data.Set.Keys() {
			val, _ := data.Set.Get(key)
			store[key] = val
		}
	}
	return store
}

func execValueText(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	raw, err := jsonx.Marshal(value)
	if err == nil && len(raw) > 0 {
		return string(raw)
	}
	return fmt.Sprint(value)
}

func execFormatOutput(output codemode.CodemodeOutputItemList) ai.ContentList {
	totalText := 0
	for _, item := range output {
		if t, ok := item.(*codemode.CodemodeTextOutput); ok && t != nil {
			totalText++
		}
	}
	items := make(ai.ContentList, 0, len(output))
	index := 0
	for _, item := range output {
		switch x := item.(type) {
		case *codemode.CodemodeImageOutput:
			if x != nil {
				items = append(items, &ai.ImageContent{
					Type:     "image",
					Data:     x.Data,
					MimeType: x.MimeType,
				})
			}
		case *codemode.CodemodeTextOutput:
			if x != nil {
				index++
				text := x.Text
				if totalText > 1 {
					text = fmt.Sprintf("==> text %d/%d <==\n%s", index, totalText, text)
				}
				items = append(items, &ai.TextContent{
					Type: "text",
					Text: text,
				})
			}
		}
	}
	return items
}

func execJoinAdjacentText(items ai.ContentList) ai.ContentList {
	joined := make(ai.ContentList, 0, len(items))
	var parts []string
	lastPart := ""
	flush := func() {
		if len(parts) > 0 {
			joined = append(joined, &ai.TextContent{
				Type: "text",
				Text: strings.Join(parts, ""),
			})
		}
		parts = nil
		lastPart = ""
	}
	for _, item := range items {
		txt, ok := item.(*ai.TextContent)
		if !ok || txt == nil {
			flush()
			joined = append(joined, item)
			continue
		}
		if lastPart != "" && !strings.HasSuffix(lastPart, "\n") {
			parts = append(parts, "\n")
			lastPart = "\n"
		}
		parts = append(parts, txt.Text)
		if txt.Text != "" {
			lastPart = txt.Text
		}
	}
	flush()
	return joined
}

func execFormatCallSummary(calls []CodemodeNestedCall) string {
	if len(calls) == 0 {
		return "No tool calls were made."
	}
	parts := make([]string, len(calls))
	for i, call := range calls {
		parts[i] = fmt.Sprintf("%s (%s)", call.Name, call.Status)
	}
	return "Tool calls made before the failure (they are not undone): " + strings.Join(parts, ", ")
}

func execFormatError(failure *codemode.CodemodeFailure, calls []CodemodeNestedCall) string {
	e := failure.Error
	var head string
	switch e.Kind {
	case codemode.CodemodeErrorKindScript:
		if e.Stack != nil {
			head = *e.Stack
		} else {
			errName := "Error"
			if e.Name != nil && *e.Name != "" {
				errName = *e.Name
			}
			head = fmt.Sprintf("%s: %s", errName, e.Message)
		}
	case codemode.CodemodeErrorKindTimeout:
		head = "Script timed out: " + e.Message
	case codemode.CodemodeErrorKindAborted:
		head = "Script aborted: " + e.Message
	default:
		head = "Script sandbox failed: " + e.Message
	}
	return head + "\n\n" + execFormatCallSummary(calls)
}

func execSpillOutput(text string) (path string, err error) {
	return utils.WriteOutputFile("pi-codemode", ".txt", []byte(text))
}

func execSaveImages(items ai.ContentList) (ai.ContentList, error) {
	labels := make(map[string]string)
	out := make(ai.ContentList, 0, len(items)*2)
	for _, item := range items {
		img, ok := item.(*ai.ImageContent)
		if !ok || img == nil {
			out = append(out, item)
			continue
		}
		label, seen := labels[img.Data]
		if !seen {
			ext, ok := execImageExts[img.MimeType]
			if !ok {
				return nil, fmt.Errorf("No file extension for image type %s", img.MimeType)
			}
			bytes, err := base64.StdEncoding.DecodeString(img.Data)
			if err != nil {
				kind := fmt.Sprintf("%s, %s", img.MimeType, core.FormatSize(0))
				label = fmt.Sprintf("[Image (%s) could not be saved: %s]", kind, err.Error())
			} else {
				kind := fmt.Sprintf("%s, %s", img.MimeType, core.FormatSize(len(bytes)))
				if savedPath, writeErr := utils.WriteOutputFile("pi-codemode", ext, bytes); writeErr != nil {
					label = fmt.Sprintf("[Image (%s) could not be saved: %s]", kind, writeErr.Error())
				} else {
					label = fmt.Sprintf("[Image saved to %s (%s)]", savedPath, kind)
				}
			}
			labels[img.Data] = label
		}
		out = append(out, &ai.TextContent{Type: "text", Text: label}, img)
	}
	return out, nil
}

func execTruncateOutput(items ai.ContentList, maxTokens int) (ai.ContentList, *string) {
	var texts []string
	var images ai.ContentList
	for _, item := range items {
		switch x := item.(type) {
		case *ai.TextContent:
			if x != nil {
				texts = append(texts, x.Text)
			}
		case *ai.ImageContent:
			if x != nil {
				images = append(images, x)
			}
		}
	}
	if len(texts) == 0 {
		return items, nil
	}
	combined := strings.Join(texts, "\n")
	combinedLen := js.Len(combined)
	budget := maxTokens * execCharsPerToken
	if combinedLen <= budget {
		return items, nil
	}

	headChars := budget / 2
	tailChars := budget - headChars
	removed := combinedLen - headChars - tailChars
	head := core.SliceHead(combined, headChars)
	tail := core.SliceTail(combined, tailChars)
	origTokens := int(math.Ceil(float64(combinedLen) / execCharsPerToken))
	truncatedTokens := int(math.Ceil(float64(removed) / execCharsPerToken))
	totalLines := len(strings.Split(combined, "\n"))

	text := fmt.Sprintf(
		"Warning: truncated output (original token count: %d)\nTotal output lines: %d\n\n%s…%d tokens truncated…%s",
		origTokens,
		totalLines,
		head,
		truncatedTokens,
		tail,
	)

	var fullOutputPath *string
	spilledPath, err := execSpillOutput(combined)
	if err == nil {
		text += fmt.Sprintf("\n\n[Full output: %s (read with offset/limit)]", spilledPath)
		fullOutputPath = &spilledPath
	} else {
		text += fmt.Sprintf("\n\n[Could not save the full output: %s]", err.Error())
	}

	resultItems := make(ai.ContentList, 0, 1+len(images))
	resultItems = append(resultItems, &ai.TextContent{Type: "text", Text: text})
	resultItems = append(resultItems, images...)
	return resultItems, fullOutputPath
}

func execToScriptValue(tool *agent.AgentTool, outcome agent.AgentToolCallOutcome) (any, error) {
	if tool.OutputSchema != nil && outcome.Result.StructuredContent != nil {
		return outcome.Result.StructuredContent, nil
	}
	text := execTextOf(outcome.Result)
	if outcome.IsError {
		if text == "" {
			text = fmt.Sprintf(`Tool "%s" failed`, tool.Name)
		}
		return nil, errors.New(text)
	}
	return text, nil
}

type execRunState struct {
	mu              sync.Mutex
	calls           []CodemodeNestedCall
	modelUsage      *ai.Usage
	generatedImages int
	modelCallCount  int
	onUpdate        agent.AgentToolUpdateCallback
}

func (s *execRunState) snapshot() CodemodeToolDetails {
	s.mu.Lock()
	copied := slices.Clone(s.calls)
	s.mu.Unlock()
	if copied == nil {
		copied = []CodemodeNestedCall{}
	}
	return CodemodeToolDetails{Calls: copied}
}

func (s *execRunState) publish() {
	if s.onUpdate == nil {
		return
	}
	details := s.snapshot()
	s.onUpdate(agent.AgentToolResult{
		Content: ai.ContentList{},
		Details: details,
	})
}

func (s *execRunState) addCall(call CodemodeNestedCall) int {
	s.mu.Lock()
	idx := len(s.calls)
	s.calls = append(s.calls, call)
	s.mu.Unlock()
	s.publish()
	return idx
}

func (s *execRunState) updateCall(idx int, fn func(call *CodemodeNestedCall)) {
	s.mu.Lock()
	if idx >= 0 && idx < len(s.calls) {
		fn(&s.calls[idx])
	}
	s.mu.Unlock()
	s.publish()
}

func (s *execRunState) nextModelCallId(toolCallId, name string) string {
	s.mu.Lock()
	s.modelCallCount++
	n := s.modelCallCount
	s.mu.Unlock()
	return fmt.Sprintf("%s/%s/%d", toolCallId, name, n)
}

func (s *execRunState) addModelUsage(usage ai.Usage) {
	s.mu.Lock()
	if s.modelUsage == nil {
		cp := usage
		s.modelUsage = &cp
	} else {
		combined := core.CombineUsage(*s.modelUsage, usage)
		s.modelUsage = &combined
	}
	s.mu.Unlock()
}

func (s *execRunState) addGeneratedImages(count int) {
	s.mu.Lock()
	s.generatedImages += count
	s.mu.Unlock()
}

func (s *execRunState) cancelRunningCalls() {
	s.mu.Lock()
	for i := range s.calls {
		if s.calls[i].Status == CodemodeNestedCallRunning {
			s.calls[i].Status = CodemodeNestedCallCancelled
		}
	}
	s.mu.Unlock()
}

// ExecuteCodemode runs a single codemode script inside the QuickJS sandbox and returns the tool result.
func ExecuteCodemode(
	ctx context.Context,
	toolCallId string,
	input CodemodeToolInput,
	onUpdate agent.AgentToolUpdateCallback,
	ectx core.ExtensionToolContext,
	options ...*CodemodeToolOptions,
) (agent.AgentToolResult, error) {
	startedAt := time.Now()
	var opts *CodemodeToolOptions
	if len(options) > 0 {
		opts = options[0]
	}

	parsed, err := codemode.ParseCodemodeSource(input.Code)
	if err != nil {
		return agent.AgentToolResult{}, err
	}

	state := &execRunState{onUpdate: onUpdate}
	// Show the script as running before its first nested call, so the row's timer starts.
	state.publish()

	var callable []*agent.AgentTool
	if ectx != nil {
		callable = GetCodemodeCallableTools(ectx.Tools())
	}

	var guidelines map[string][]string
	if opts != nil && opts.GetToolGuidelines != nil {
		guidelines = opts.GetToolGuidelines()
	}

	samples := make(map[string]string, len(callable))
	sandboxTools := make([]*codemode.CodemodeTool, 0, len(callable))
	for _, t := range callable {
		tool := t
		var guide []string
		if guidelines != nil {
			guide = guidelines[tool.Name]
		}
		sample := codemode.RenderToolSample(ToCodemodeDeclaration(tool, guide), nil)
		samples[tool.Name] = sample
		sampleCopy := sample
		sandboxTools = append(sandboxTools, &codemode.CodemodeTool{
			Name:        tool.Name,
			Description: &sampleCopy,
			Execute: func(callCtx context.Context, args any) (any, error) {
				idx := state.addCall(CodemodeNestedCall{
					Id:     toolCallId + "/?",
					Name:   tool.Name,
					Args:   execPreviewArgs(args),
					Status: CodemodeNestedCallRunning,
				})
				callStartedAt := time.Now()
				if ectx == nil {
					errMsg := "Tool calls need a session"
					state.updateCall(idx, func(c *CodemodeNestedCall) {
						d := float64(time.Since(callStartedAt).Nanoseconds()) / float64(time.Millisecond)
						c.DurationMs = &d
						c.Status = CodemodeNestedCallError
						trunc := execTruncateText(errMsg, execErrorPreviewChars)
						c.Error = &trunc
					})
					return nil, errors.New(errMsg)
				}
				outcome, execErr := ectx.ExecuteTool(callCtx, tool.Name, args, &core.ExecuteToolOptions{
					Signal: callCtx,
				})
				dur := float64(time.Since(callStartedAt).Nanoseconds()) / float64(time.Millisecond)
				if execErr != nil && outcome.ToolCall == nil {
					errMsg := execErr.Error()
					state.updateCall(idx, func(c *CodemodeNestedCall) {
						c.DurationMs = &dur
						if callCtx.Err() != nil {
							c.Status = CodemodeNestedCallCancelled
						} else {
							c.Status = CodemodeNestedCallError
						}
						trunc := execTruncateText(errMsg, execErrorPreviewChars)
						c.Error = &trunc
					})
					return nil, execErr
				}
				callID := toolCallId + "/?"
				if outcome.ToolCall != nil && outcome.ToolCall.Id != "" {
					callID = outcome.ToolCall.Id
				}
				state.updateCall(idx, func(c *CodemodeNestedCall) {
					c.Id = callID
					c.DurationMs = &dur
					if outcome.IsError {
						if callCtx.Err() != nil {
							c.Status = CodemodeNestedCallCancelled
						} else {
							c.Status = CodemodeNestedCallError
						}
						errText := execTextOf(outcome.Result)
						if errText == "" {
							errText = fmt.Sprintf(`Tool "%s" failed`, tool.Name)
						}
						trunc := execTruncateText(errText, execErrorPreviewChars)
						c.Error = &trunc
					} else {
						c.Status = CodemodeNestedCallOk
					}
				})
				return execToScriptValue(tool, outcome)
			},
		})
	}

	// The wait for a worker counts toward the script's deadline.
	hasDeadline := parsed.Options.TimeoutMs != nil
	deadlineMs := math.Inf(1)
	if hasDeadline {
		deadlineMs = float64(*parsed.Options.TimeoutMs)
	}
	waitLimitMs := float64(execWorkerWaitMs)
	if hasDeadline && deadlineMs < waitLimitMs {
		waitLimitMs = deadlineMs
	}
	waitTimeout := time.Duration(waitLimitMs * float64(time.Millisecond))
	waitStartedAt := time.Now()
	admission := execWorkerGate.acquire(ctx, &waitTimeout)
	waitedMs := float64(time.Since(waitStartedAt).Nanoseconds()) / float64(time.Millisecond)

	if admission != "reserved" {
		waited := fmt.Sprintf("%.1f", time.Since(startedAt).Seconds())
		var errMsg string
		if admission == "aborted" {
			reason := "Execution aborted"
			if ctx != nil {
				if cause := context.Cause(ctx); cause != nil && cause != context.Canceled && cause != context.DeadlineExceeded {
					reason = cause.Error()
				}
			}
			errMsg = "Script aborted: " + reason
		} else if hasDeadline && deadlineMs <= float64(execWorkerWaitMs) {
			errMsg = fmt.Sprintf(
				"Script timed out: Execution timed out after %s ms while waiting for a free codemode worker",
				js.NumberToString(deadlineMs),
			)
		} else {
			errMsg = fmt.Sprintf(
				"No codemode worker became free within %d seconds. All %d workers, shared by every session in this process, are held by other scripts. Retry after they finish.",
				execWorkerWaitMs/1000,
				execMaxCodemodeWorkers(),
			)
		}
		isErr := true
		return agent.AgentToolResult{
			Content: ai.ContentList{
				&ai.TextContent{Type: "text", Text: fmt.Sprintf("Script failed\nWall time %s seconds\nOutput:\n", waited)},
				&ai.TextContent{Type: "text", Text: fmt.Sprintf("Script error:\n%s\n\nThe script did not start.", errMsg)},
			},
			Details: state.snapshot(),
			IsError: &isErr,
		}, nil
	}

	globals := execCreateDiscoveryGlobals(callable, samples, opts)
	if opts != nil && opts.Models != nil && *opts.Models && ectx != nil {
		if mr := ectx.ModelRegistry(); mr != nil {
			globals = append(globals, execCreateModelGlobals(mr, toolCallId, state)...)
		}
	}

	var execTimeout float64
	if hasDeadline {
		rem := math.Round(deadlineMs - waitedMs)
		if rem < 1 {
			rem = 1
		}
		execTimeout = rem
	} else {
		execTimeout = math.Inf(1)
	}
	memLimit := execMemoryLimitBytes

	wasmPath := utils.GetQuickJSWasmPath()
	wasmMod, wasmErr := codemode.LoadQuickJSWasm(&wasmPath)
	if wasmErr != nil {
		execWorkerGate.release()
		return agent.AgentToolResult{}, wasmErr
	}

	var sandbox *codemoderuntime.CodemodeSandbox
	var cmResult codemode.CodemodeResult
	var runErr error

	func() {
		defer func() {
			if sandbox != nil {
				_ = sandbox.Close()
			}
			execWorkerGate.release()
		}()

		sandbox, runErr = codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
			Tools:            sandboxTools,
			Globals:          globals,
			TimeoutMs:        &execTimeout,
			MemoryLimitBytes: &memLimit,
			Wasm:             wasmMod,
		})
		if runErr != nil {
			return
		}
		var store map[string]any
		if ectx != nil && ectx.SessionManager() != nil {
			store = ReadCodemodeStore(ectx.SessionManager().GetBranch())
		}
		if ctx == nil {
			ctx = context.Background()
		}
		cmResult, runErr = sandbox.Execute(ctx, parsed.Code, &codemode.CodemodeExecuteOptions{
			Store: store,
		})
	}()

	if runErr != nil {
		return agent.AgentToolResult{}, runErr
	}

	// Calls still marked running were cut off by the script ending, a timeout, or an abort.
	state.cancelRunningCalls()

	var okFlag bool
	var scriptOutput codemode.CodemodeOutputItemList
	var failure *codemode.CodemodeFailure

	switch r := cmResult.(type) {
	case *codemode.CodemodeSuccess:
		okFlag = true
		scriptOutput = slices.Clone(r.Output)
		writes := r.StoreWrites
		setNonEmpty := writes.Set != nil && writes.Set.Len() > 0
		delNonEmpty := len(writes.Delete) > 0
		if (setNonEmpty || delNonEmpty) && opts != nil && opts.AppendEntry != nil {
			setCopy := writes.Set
			if setCopy == nil {
				setCopy = omap.NewMap[string, any]()
			}
			delCopy := writes.Delete
			if delCopy == nil {
				delCopy = []string{}
			}
			opts.AppendEntry(CodemodeStoreEntryType, CodemodeStoreEntryData{
				Set:    setCopy,
				Delete: delCopy,
			})
		}
		if !r.Value.IsZero() {
			val := r.Value.OrElse(nil)
			scriptOutput = append(scriptOutput, &codemode.CodemodeTextOutput{
				Type: "text",
				Text: execValueText(val),
			})
		}
	case *codemode.CodemodeFailure:
		okFlag = false
		failure = r
		scriptOutput = slices.Clone(r.Output)
	default:
		okFlag = false
		failure = &codemode.CodemodeFailure{
			Ok: false,
			Error: codemode.CodemodeError{
				Kind:    codemode.CodemodeErrorKindSandbox,
				Message: "Unexpected codemode result",
			},
		}
	}

	items := execFormatOutput(scriptOutput)
	if !okFlag && failure != nil {
		detailsSnap := state.snapshot()
		items = append(items, &ai.TextContent{
			Type: "text",
			Text: "Script error:\n" + execFormatError(failure, detailsSnap.Calls),
		})
	}

	state.mu.Lock()
	genImages := state.generatedImages
	finalUsage := state.modelUsage
	state.mu.Unlock()

	if genImages > 0 {
		hasImage := false
		for _, item := range items {
			if _, isImg := item.(*ai.ImageContent); isImg {
				hasImage = true
				break
			}
		}
		if !hasImage {
			plural := "s"
			if genImages == 1 {
				plural = ""
			}
			items = append(items, &ai.TextContent{
				Type: "text",
				Text: fmt.Sprintf(
					"Note: models.generateImages() returned %d image%s that the script did not show. Show each image block of result.output with image(block).",
					genImages,
					plural,
				),
			})
		}
	}

	maxOutputTokens := execDefaultMaxOutputToks
	if parsed.Options.MaxOutputTokens != nil {
		maxOutputTokens = *parsed.Options.MaxOutputTokens
	}
	truncatedItems, fullOutputPath := execTruncateOutput(execJoinAdjacentText(items), maxOutputTokens)
	savedItems, saveErr := execSaveImages(truncatedItems)
	if saveErr != nil {
		return agent.AgentToolResult{}, saveErr
	}
	output := execJoinAdjacentText(savedItems)

	wallTime := fmt.Sprintf("%.1f", time.Since(startedAt).Seconds())
	statusWord := "completed"
	if !okFlag {
		statusWord = "failed"
	}
	header := fmt.Sprintf("Script %s\nWall time %s seconds\nOutput:\n", statusWord, wallTime)
	details := state.snapshot()
	details.FullOutputPath = fullOutputPath

	content := make(ai.ContentList, 0, 1+len(output))
	content = append(content, &ai.TextContent{Type: "text", Text: header})
	content = append(content, output...)

	res := agent.AgentToolResult{
		Content: content,
		Details: details,
		Usage:   finalUsage,
	}
	if !okFlag {
		isErr := true
		res.IsError = &isErr
	}
	return res, nil
}

func execIsNamespaceName(namespace *core.ToolNamespace, query string) bool {
	if namespace == nil {
		return false
	}
	queryId := codemode.ToCodemodeIdentifier(query)
	if namespace.Name == query || codemode.ToCodemodeIdentifier(namespace.Name) == queryId {
		return true
	}
	for _, alias := range namespace.Aliases {
		if alias == query || codemode.ToCodemodeIdentifier(alias) == queryId {
			return true
		}
	}
	return false
}

type execSearchMatchEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type execNamespaceSummary struct {
	Name         string   `json:"name"`
	Description  *string  `json:"description,omitzero"`
	Instructions *string  `json:"instructions,omitzero"`
	Tools        []string `json:"tools"`
}

func execCreateDiscoveryGlobals(
	tools []*agent.AgentTool,
	samples map[string]string,
	options *CodemodeToolOptions,
) []*codemode.CodemodeTool {
	ranker := toolsearchext.NewBm25Ranker()
	spreadTrue := true

	makeEntry := func(name string) execSearchMatchEntry {
		return execSearchMatchEntry{
			Name:        codemode.ToCodemodeIdentifier(name),
			Description: samples[name],
		}
	}

	return []*codemode.CodemodeTool{
		{
			Name:   "searchTools",
			Spread: &spreadTrue,
			Execute: func(_ context.Context, rawArgs any) (any, error) {
				args, _ := rawArgs.([]any)
				var queryVal any
				var optsVal any
				if len(args) > 0 {
					queryVal = args[0]
				}
				if len(args) > 1 {
					optsVal = args[1]
				}
				query, ok := queryVal.(string)
				if !ok {
					return nil, errors.New("searchTools() expects a query string")
				}
				limit := toolsearchext.DefaultToolSearchLimit
				var namespace string
				if optsVal != nil {
					optsObj, _ := execAsObject(optsVal)
					if optsObj != nil {
						if limitRaw, hasLimit := optsObj.get("limit"); hasLimit && limitRaw != nil {
							limitNum, isFloat := limitRaw.(float64)
							if !isFloat || math.IsNaN(limitNum) || math.IsInf(limitNum, 0) || math.Trunc(limitNum) != limitNum || limitNum <= 0 {
								return nil, errors.New("searchTools() limit must be a positive integer")
							}
							limit = int(limitNum)
						}
						if nsRaw, hasNs := optsObj.get("namespace"); hasNs && nsRaw != nil {
							nsStr, isStr := nsRaw.(string)
							if !isStr {
								return nil, errors.New("searchTools() namespace must be a string")
							}
							namespace = nsStr
						}
					}
				}

				docs := make([]toolsearchext.ToolSearchDocument, 0, len(tools))
				for _, tool := range tools {
					var ns *core.ToolNamespace
					if options != nil && options.GetToolNamespace != nil {
						ns = options.GetToolNamespace(tool.Name)
					}
					if namespace != "" && (ns == nil || !execIsNamespaceName(ns, namespace)) {
						continue
					}
					docs = append(docs, toolsearchext.CreateToolSearchDocument(core.ToolInfo{
						Name:        tool.Name,
						Description: tool.Description,
						Parameters:  tool.Parameters,
					}, ns))
				}
				matches := ranker.Rank(query, docs, limit)
				out := make([]execSearchMatchEntry, len(matches))
				for i, match := range matches {
					out[i] = makeEntry(match.Name)
				}
				return out, nil
			},
		},
		{
			Name:   "describeTool",
			Spread: &spreadTrue,
			Execute: func(_ context.Context, rawArgs any) (any, error) {
				args, _ := rawArgs.([]any)
				var nameVal any
				if len(args) > 0 {
					nameVal = args[0]
				}
				name, ok := nameVal.(string)
				if !ok {
					return nil, errors.New("describeTool() expects a tool name")
				}
				for _, candidate := range tools {
					if candidate.Name == name || codemode.ToCodemodeIdentifier(candidate.Name) == name {
						return samples[candidate.Name], nil
					}
				}
				return nil, nil
			},
		},
		{
			Name:   "describeNamespace",
			Spread: &spreadTrue,
			Execute: func(_ context.Context, rawArgs any) (any, error) {
				args, _ := rawArgs.([]any)
				var nameVal any
				if len(args) > 0 {
					nameVal = args[0]
				}
				name, ok := nameVal.(string)
				if !ok {
					return nil, errors.New("describeNamespace() expects a namespace name")
				}
				var matchedNs *core.ToolNamespace
				var names []string
				for _, tool := range tools {
					if options == nil || options.GetToolNamespace == nil {
						continue
					}
					toolNs := options.GetToolNamespace(tool.Name)
					if toolNs == nil || !execIsNamespaceName(toolNs, name) {
						continue
					}
					if matchedNs == nil {
						matchedNs = toolNs
					}
					names = append(names, codemode.ToCodemodeIdentifier(tool.Name))
				}
				if matchedNs == nil {
					return nil, nil
				}
				return execNamespaceSummary{
					Name:         matchedNs.Name,
					Description:  matchedNs.Description,
					Instructions: matchedNs.Instructions,
					Tools:        names,
				}, nil
			},
		},
	}
}

type execModelRef struct {
	Provider string
	Id       string
}

func execExtractModelRef(v any) (execModelRef, bool) {
	obj, ok := execAsObject(v)
	if !ok {
		return execModelRef{}, false
	}
	provRaw, _ := obj.get("provider")
	idRaw, _ := obj.get("id")
	prov, pOk := provRaw.(string)
	id, iOk := idRaw.(string)
	if !pOk || !iOk {
		return execModelRef{}, false
	}
	return execModelRef{Provider: prov, Id: id}, true
}

func execCreateModelGlobals(
	models CodemodeModelRuntime,
	toolCallId string,
	state *execRunState,
) []*codemode.CodemodeTool {
	modelGate := execNewGate(func() int { return execMaxConcurrentModels })
	spreadTrue := true

	resolveRef := func(name string, typ ai.ModelType, modelArg any) (ai.AnyModel, execModelRef, error) {
		listHint := fmt.Sprintf(`List the %s models you can use with models.getAvailableOfType("%s").`, typ, typ)
		given, ok := execExtractModelRef(modelArg)
		if !ok {
			undefinedHint := ""
			if modelArg == nil {
				undefinedHint = " models.getModelOfType() returns undefined for an unknown provider or id."
			}
			return nil, execModelRef{}, fmt.Errorf(
				"%s() expects %s model as its first argument, got %s.%s %s",
				name,
				execWithArticle(string(typ)),
				execDescribeValue(modelArg),
				undefinedHint,
				listHint,
			)
		}
		ref := given.Provider + "/" + given.Id
		resolved := models.GetModelOfType(typ, given.Provider, given.Id)
		if resolved == nil {
			for _, other := range execOrderedModelTypes {
				if other != typ && models.GetModelOfType(other, given.Provider, given.Id) != nil {
					return nil, execModelRef{}, fmt.Errorf(
						`"%s" is %s model, not %s model. %s`,
						ref,
						execWithArticle(string(other)),
						execWithArticle(string(typ)),
						listHint,
					)
				}
			}
			return nil, execModelRef{}, fmt.Errorf(`Unknown %s model "%s". %s`, typ, ref, listHint)
		}
		return resolved, given, nil
	}

	return []*codemode.CodemodeTool{
		{
			Name:   "models.getModelsOfType",
			Spread: &spreadTrue,
			Execute: func(_ context.Context, rawArgs any) (any, error) {
				args, _ := rawArgs.([]any)
				var typArg, provArg any
				if len(args) > 0 {
					typArg = args[0]
				}
				if len(args) > 1 {
					provArg = args[1]
				}
				typ, err := execToModelType(typArg)
				if err != nil {
					return nil, err
				}
				prov, err := execToProvider(provArg)
				if err != nil {
					return nil, err
				}
				found := models.GetModelsOfType(typ, prov)
				out := make([]any, len(found))
				for i, m := range found {
					out[i] = execToModelInfo(m)
				}
				return out, nil
			},
		},
		{
			Name:   "models.getAvailableOfType",
			Spread: &spreadTrue,
			Execute: func(callCtx context.Context, rawArgs any) (any, error) {
				args, _ := rawArgs.([]any)
				var typArg, provArg any
				if len(args) > 0 {
					typArg = args[0]
				}
				if len(args) > 1 {
					provArg = args[1]
				}
				typ, err := execToModelType(typArg)
				if err != nil {
					return nil, err
				}
				prov, err := execToProvider(provArg)
				if err != nil {
					return nil, err
				}
				available, err := models.GetAvailableOfType(callCtx, typ, prov, &ai.AuthOperationOptions{})
				if err != nil {
					return nil, err
				}
				out := make([]any, len(available))
				for i, m := range available {
					out[i] = execToModelInfo(m)
				}
				return out, nil
			},
		},
		{
			Name:   "models.getModelOfType",
			Spread: &spreadTrue,
			Execute: func(_ context.Context, rawArgs any) (any, error) {
				args, _ := rawArgs.([]any)
				var typArg, provArg, idArg any
				if len(args) > 0 {
					typArg = args[0]
				}
				if len(args) > 1 {
					provArg = args[1]
				}
				if len(args) > 2 {
					idArg = args[2]
				}
				prov, pOk := provArg.(string)
				id, iOk := idArg.(string)
				if !pOk || !iOk {
					described := make([]string, len(args))
					for i, a := range args {
						described[i] = execDescribeValue(a)
					}
					return nil, fmt.Errorf(
						`models.getModelOfType(type, provider, id) expects three strings, got (%s). The provider and the id are separate arguments, for example models.getModelOfType("classifier", "typesafe", "jev-latest").`,
						strings.Join(described, ", "),
					)
				}
				typ, err := execToModelType(typArg)
				if err != nil {
					return nil, err
				}
				model := models.GetModelOfType(typ, prov, id)
				if model == nil {
					return nil, nil
				}
				return execToModelInfo(model), nil
			},
		},
		{
			Name:   "models.classify",
			Spread: &spreadTrue,
			Execute: func(callCtx context.Context, rawArgs any) (any, error) {
				args, _ := rawArgs.([]any)
				var modelArg, ctxArg any
				if len(args) > 0 {
					modelArg = args[0]
				}
				if len(args) > 1 {
					ctxArg = args[1]
				}
				resolved, ref, err := resolveRef("models.classify", ai.ModelTypeClassifier, modelArg)
				if err != nil {
					return nil, err
				}
				clsModel, ok := resolved.(*ai.ClassifierModel)
				if !ok || clsModel == nil {
					return nil, fmt.Errorf(`Unknown classifier model "%s/%s".`, ref.Provider, ref.Id)
				}
				clsCtx, err := execCheckClassifierContext(ctxArg)
				if err != nil {
					return nil, err
				}

				idx := state.addCall(CodemodeNestedCall{
					Id:     state.nextModelCallId(toolCallId, "models.classify"),
					Name:   "models.classify",
					Args:   ref.Provider + "/" + ref.Id,
					Status: CodemodeNestedCallRunning,
				})
				startedAt := time.Now()
				if adm := modelGate.acquire(callCtx, nil); adm != "reserved" {
					dur := float64(time.Since(startedAt).Nanoseconds()) / float64(time.Millisecond)
					state.updateCall(idx, func(c *CodemodeNestedCall) {
						c.DurationMs = &dur
						c.Status = CodemodeNestedCallCancelled
					})
					return nil, callCtx.Err()
				}
				var res *ai.ClassifierResult
				var callErr error
				func() {
					defer modelGate.release()
					res, callErr = models.Classify(callCtx, clsModel, clsCtx, &ai.ModelsClassifierOptions{})
				}()
				dur := float64(time.Since(startedAt).Nanoseconds()) / float64(time.Millisecond)
				if callErr != nil {
					trunc := execTruncateText(callErr.Error(), execErrorPreviewChars)
					state.updateCall(idx, func(c *CodemodeNestedCall) {
						c.DurationMs = &dur
						c.Status = CodemodeNestedCallError
						c.Error = &trunc
					})
					return nil, callErr
				}
				state.updateCall(idx, func(c *CodemodeNestedCall) {
					c.DurationMs = &dur
					switch res.StopReason {
					case ai.ClassifierStopReasonStop:
						c.Status = CodemodeNestedCallOk
					case ai.ClassifierStopReasonAborted:
						c.Status = CodemodeNestedCallCancelled
					default:
						c.Status = CodemodeNestedCallError
					}
					if res.ErrorMessage != nil && *res.ErrorMessage != "" {
						trunc := execTruncateText(*res.ErrorMessage, execErrorPreviewChars)
						c.Error = &trunc
					}
					if res.Usage != nil {
						cost := res.Usage.Cost.Total
						c.Cost = &cost
					}
				})
				if res.Usage != nil {
					state.addModelUsage(*res.Usage)
				}
				return res, nil
			},
		},
		{
			Name:   "models.generateImages",
			Spread: &spreadTrue,
			Execute: func(callCtx context.Context, rawArgs any) (any, error) {
				args, _ := rawArgs.([]any)
				var modelArg, ctxArg any
				if len(args) > 0 {
					modelArg = args[0]
				}
				if len(args) > 1 {
					ctxArg = args[1]
				}
				resolved, ref, err := resolveRef("models.generateImages", ai.ModelTypeImage, modelArg)
				if err != nil {
					return nil, err
				}
				imgModel, ok := resolved.(*ai.ImageModel)
				if !ok || imgModel == nil {
					return nil, fmt.Errorf(`Unknown image model "%s/%s".`, ref.Provider, ref.Id)
				}
				imgCtx, err := execCheckImagesContext(ctxArg)
				if err != nil {
					return nil, err
				}

				idx := state.addCall(CodemodeNestedCall{
					Id:     state.nextModelCallId(toolCallId, "models.generateImages"),
					Name:   "models.generateImages",
					Args:   ref.Provider + "/" + ref.Id,
					Status: CodemodeNestedCallRunning,
				})
				startedAt := time.Now()
				if adm := modelGate.acquire(callCtx, nil); adm != "reserved" {
					dur := float64(time.Since(startedAt).Nanoseconds()) / float64(time.Millisecond)
					state.updateCall(idx, func(c *CodemodeNestedCall) {
						c.DurationMs = &dur
						c.Status = CodemodeNestedCallCancelled
					})
					return nil, callCtx.Err()
				}
				var res *ai.AssistantImages
				var callErr error
				func() {
					defer modelGate.release()
					res, callErr = models.GenerateImages(callCtx, imgModel, imgCtx, &ai.ModelsImagesOptions{})
				}()
				dur := float64(time.Since(startedAt).Nanoseconds()) / float64(time.Millisecond)
				if callErr != nil {
					trunc := execTruncateText(callErr.Error(), execErrorPreviewChars)
					state.updateCall(idx, func(c *CodemodeNestedCall) {
						c.DurationMs = &dur
						c.Status = CodemodeNestedCallError
						c.Error = &trunc
					})
					return nil, callErr
				}
				imgCount := 0
				for _, block := range res.Output {
					if _, isImg := block.(*ai.ImageContent); isImg {
						imgCount++
					}
				}
				if imgCount > 0 {
					state.addGeneratedImages(imgCount)
				}
				state.updateCall(idx, func(c *CodemodeNestedCall) {
					c.DurationMs = &dur
					switch res.StopReason {
					case ai.ImagesStopReasonStop:
						c.Status = CodemodeNestedCallOk
					case ai.ImagesStopReasonAborted:
						c.Status = CodemodeNestedCallCancelled
					default:
						c.Status = CodemodeNestedCallError
					}
					if res.ErrorMessage != nil && *res.ErrorMessage != "" {
						trunc := execTruncateText(*res.ErrorMessage, execErrorPreviewChars)
						c.Error = &trunc
					}
					if res.Usage != nil {
						cost := res.Usage.Cost.Total
						c.Cost = &cost
					}
				})
				if res.Usage != nil {
					state.addModelUsage(*res.Usage)
				}
				return res, nil
			},
		},
	}
}
