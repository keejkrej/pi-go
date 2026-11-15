package gui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	tvReadPreviewLines  = 12
	tvWritePreviewLines = 10
	tvBashPreviewLines  = 15
	tvGrepPreviewLines  = 15
	tvFindPreviewLines  = 20
	tvLsPreviewLines    = 20
)

// ToolExecutionSnapshot captures read-only state of a ToolExecutionCard for custom GUI renderers.
type ToolExecutionSnapshot struct {
	ToolCallId     string
	ToolName       string
	Label          string
	Cwd            string
	Args           *jsonx.Object
	ArgsComplete   bool
	Result         agent.AgentToolResult
	HasResult      bool
	Pending        bool
	IsError        bool
	Expanded       bool
	ShowImages     bool
	DiffSideBySide bool
	Duration       time.Duration
}

// ToolGuiRenderer builds a custom MyGo element for a ToolExecutionSnapshot.
// Returning ui.Element{} falls back to the built-in tool card renderer.
type ToolGuiRenderer func(c *ui.Context, snap ToolExecutionSnapshot, th *gui.GuiTheme) ui.Element

// ToolCardOption configures a ToolExecutionCard.
type ToolCardOption func(*ToolExecutionCard)

// WithToolLabel sets a display label shown instead of raw ToolName when non-empty.
func WithToolLabel(label string) ToolCardOption {
	return func(tc *ToolExecutionCard) { tc.label = label }
}

// WithToolCwd sets the working directory used to shorten relative/repo paths.
func WithToolCwd(cwd string) ToolCardOption {
	return func(tc *ToolExecutionCard) { tc.cwd = cwd }
}

// WithToolExpanded sets the initial expanded state of the card body.
func WithToolExpanded(expanded bool) ToolCardOption {
	return func(tc *ToolExecutionCard) { tc.expanded = expanded }
}

// WithToolShowImages controls whether tool image attachments render inline.
func WithToolShowImages(show bool) ToolCardOption {
	return func(tc *ToolExecutionCard) { tc.showImages = show }
}

// WithToolDiffSideBySide defaults `edit` diff cards to side-by-side (`editor.DiffSplit`)
// rather than inline (`editor.DiffUnified`).
func WithToolDiffSideBySide(sideBySide bool) ToolCardOption {
	return func(tc *ToolExecutionCard) { tc.diffSideBySide = sideBySide }
}

// WithToolTheme applies a *gui.GuiTheme override.
func WithToolTheme(th *gui.GuiTheme) ToolCardOption {
	return func(tc *ToolExecutionCard) { tc.theme = th }
}

// WithToolOnToggle registers a callback invoked when expand/collapse changes.
func WithToolOnToggle(fn func(bool)) ToolCardOption {
	return func(tc *ToolExecutionCard) { tc.onToggle = fn }
}

// WithToolOnCopy registers a callback invoked when the user clicks Copy.
func WithToolOnCopy(fn func(string)) ToolCardOption {
	return func(tc *ToolExecutionCard) { tc.onCopy = fn }
}

// WithToolCustomRenderer installs a custom GUI renderer for non-standard or overridden tools.
func WithToolCustomRenderer(fn ToolGuiRenderer) ToolCardOption {
	return func(tc *ToolExecutionCard) { tc.customRenderer = fn }
}

// ToolExecutionCard renders a live or completed tool call (`read`, `write`, `edit`,
// `bash`, `grep`, `find`, `ls`, or generic JSON fallback) with pending spinner,
// duration/byte badges, diff viewer, code viewer, and expand/collapse toggle.
type ToolExecutionCard struct {
	mu             sync.Mutex
	toolCallId     string
	toolName       string
	label          string
	cwd            string
	args           *jsonx.Object
	argsComplete   bool
	result         agent.AgentToolResult
	hasResult      bool
	pending        bool
	isError        bool
	expanded       bool
	showImages     bool
	diffSideBySide bool
	startedAt      time.Time
	duration       time.Duration
	hasDuration    bool
	theme          *gui.GuiTheme
	onToggle       func(bool)
	onCopy         func(string)
	customRenderer ToolGuiRenderer

	diffViewer *gui.DiffViewer
	diffSig    string
	codeViewer *gui.CodeViewer
	codeSig    string
	imgCache   map[string]msgDecodedImage
}

// NewToolExecutionCard creates a pending ToolExecutionCard for toolCallId and toolName.
func NewToolExecutionCard(toolCallId, toolName string, args *jsonx.Object, opts ...ToolCardOption) *ToolExecutionCard {
	tc := &ToolExecutionCard{
		toolCallId: toolCallId,
		toolName:   toolName,
		args:       tvCloneArgs(args),
		pending:    true,
		showImages: true,
		startedAt:  time.Now(),
		imgCache:   make(map[string]msgDecodedImage),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(tc)
		}
	}
	return tc
}

func tvCloneArgs(args *jsonx.Object) *jsonx.Object {
	if args == nil {
		return nil
	}
	return args.Clone()
}

// UpdateArgs updates streamed tool arguments while the tool call is still parsing/running.
func (tc *ToolExecutionCard) UpdateArgs(args *jsonx.Object) {
	tc.mu.Lock()
	tc.args = tvCloneArgs(args)
	tc.mu.Unlock()
}

// SetArgsComplete marks whether tool argument streaming has completed.
func (tc *ToolExecutionCard) SetArgsComplete(complete bool) {
	tc.mu.Lock()
	tc.argsComplete = complete
	tc.mu.Unlock()
}

// UpdateResult updates partial or final tool execution output.
func (tc *ToolExecutionCard) UpdateResult(result agent.AgentToolResult, isPartial bool) {
	tc.mu.Lock()
	tc.result = tvCloneResult(result)
	tc.hasResult = true
	if !isPartial {
		tc.pending = false
		if result.IsError != nil && *result.IsError {
			tc.isError = true
		}
		if !tc.hasDuration {
			tc.duration = time.Since(tc.startedAt)
			tc.hasDuration = true
		}
	}
	tc.mu.Unlock()
}

// Complete marks the tool execution finished with final result, error state, and optional durationMs.
func (tc *ToolExecutionCard) Complete(result agent.AgentToolResult, isError bool, durationMs *int64) {
	tc.mu.Lock()
	tc.result = tvCloneResult(result)
	tc.hasResult = true
	tc.pending = false
	tc.argsComplete = true
	tc.isError = isError || (result.IsError != nil && *result.IsError)
	if durationMs != nil {
		tc.duration = time.Duration(*durationMs) * time.Millisecond
		tc.hasDuration = true
	} else if !tc.hasDuration {
		tc.duration = time.Since(tc.startedAt)
		tc.hasDuration = true
	}
	tc.mu.Unlock()
}

func tvCloneResult(res agent.AgentToolResult) agent.AgentToolResult {
	cp := res
	if len(res.Content) > 0 {
		cp.Content = append(ai.ContentList(nil), res.Content...)
	}
	return cp
}

// SetExpanded expands or collapses the tool body.
func (tc *ToolExecutionCard) SetExpanded(expanded bool) {
	tc.mu.Lock()
	tc.expanded = expanded
	tc.mu.Unlock()
}

// IsExpanded reports whether the card body is expanded.
func (tc *ToolExecutionCard) IsExpanded() bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.expanded
}

// SetDiffLayout sets side-by-side (`true`) or unified inline (`false`) diff mode for `edit`.
func (tc *ToolExecutionCard) SetDiffLayout(sideBySide bool) {
	tc.mu.Lock()
	tc.diffSideBySide = sideBySide
	if tc.diffViewer != nil {
		if sideBySide {
			tc.diffViewer.SetLayout(editor.DiffSplit)
		} else {
			tc.diffViewer.SetLayout(editor.DiffUnified)
		}
	}
	tc.mu.Unlock()
}

// DiffSideBySide reports whether `edit` renders side-by-side diffs.
func (tc *ToolExecutionCard) DiffSideBySide() bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.diffSideBySide
}

// SetShowImages enables or disables inline image rendering inside tool outputs.
func (tc *ToolExecutionCard) SetShowImages(show bool) {
	tc.mu.Lock()
	tc.showImages = show
	tc.mu.Unlock()
}

// SetCwd sets the working directory used for display path shortening.
func (tc *ToolExecutionCard) SetCwd(cwd string) {
	tc.mu.Lock()
	tc.cwd = cwd
	tc.mu.Unlock()
}

// SetTheme overrides the GuiTheme used by this card and nested viewers.
func (tc *ToolExecutionCard) SetTheme(th *gui.GuiTheme) {
	tc.mu.Lock()
	tc.theme = th
	if tc.diffViewer != nil {
		tc.diffViewer.SetTheme(th)
	}
	if tc.codeViewer != nil {
		tc.codeViewer.SetTheme(th)
	}
	tc.mu.Unlock()
}

// ToolCallId returns the tool call ID.
func (tc *ToolExecutionCard) ToolCallId() string {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.toolCallId
}

// ToolName returns the tool name.
func (tc *ToolExecutionCard) ToolName() string {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.toolName
}

// IsPending reports whether the tool call has not finished yet.
func (tc *ToolExecutionCard) IsPending() bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.pending
}

// IsError reports whether the tool finished with an error.
func (tc *ToolExecutionCard) IsError() bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.isError
}

// Duration returns the measured or elapsed tool duration.
func (tc *ToolExecutionCard) Duration() time.Duration {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.hasDuration {
		return tc.duration
	}
	if tc.pending {
		return time.Since(tc.startedAt)
	}
	return 0
}

func tvArgVal(args *jsonx.Object, keys ...string) (any, bool) {
	if args == nil {
		return nil, false
	}
	for _, k := range keys {
		if v, ok := args.Get(k); ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

func tvArgStr(args *jsonx.Object, keys ...string) string {
	v, ok := tvArgVal(args, keys...)
	if !ok {
		return ""
	}
	if s := core.Str(v); s != nil {
		return *s
	}
	return ""
}

func tvDisplayPath(rawPath, cwd string) string {
	if rawPath == "" {
		return ""
	}
	p := rawPath
	if cwd != "" && filepath.IsAbs(p) {
		if rel, err := filepath.Rel(cwd, p); err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
			p = rel
		}
	}
	return core.ShortenPath(p)
}

func tvDecodeDetails[T any](raw any) (*T, bool) {
	if raw == nil {
		return nil, false
	}
	switch v := raw.(type) {
	case *T:
		if v == nil {
			return nil, false
		}
		return v, true
	case T:
		cp := v
		return &cp, true
	}
	encoded, err := jsonx.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var out T
	if err := jsonx.Unmarshal(encoded, &out); err != nil {
		return nil, false
	}
	return &out, true
}

func tvExtractImages(res agent.AgentToolResult) []ai.ImageContent {
	var imgs []ai.ImageContent
	for _, blk := range res.Content {
		if img, ok := blk.(*ai.ImageContent); ok && img != nil {
			imgs = append(imgs, *img)
		}
	}
	return imgs
}

func tvPreviewLines(text string, maxLines int, fromTail, expanded bool) (shown string, hiddenCount int, totalLines int) {
	if text == "" {
		return "", 0, 0
	}
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	totalLines = len(lines)
	if expanded || len(lines) <= maxLines {
		return strings.Join(lines, "\n"), 0, totalLines
	}
	hiddenCount = len(lines) - maxLines
	if fromTail {
		return strings.Join(lines[hiddenCount:], "\n"), hiddenCount, totalLines
	}
	return strings.Join(lines[:maxLines], "\n"), hiddenCount, totalLines
}

func tvExtractEdits(args *jsonx.Object) []core.EditReplacement {
	if args == nil {
		return nil
	}
	if rawEdits, ok := args.Get("edits"); ok && rawEdits != nil {
		if b, err := jsonx.Marshal(rawEdits); err == nil {
			var parsed []core.EditReplacement
			if err := jsonx.Unmarshal(b, &parsed); err == nil && len(parsed) > 0 {
				return parsed
			}
		}
	}
	oldText := tvArgStr(args, "oldText", "old_text")
	newText := tvArgStr(args, "newText", "new_text")
	if oldText != "" || newText != "" {
		return []core.EditReplacement{{OldText: oldText, NewText: newText}}
	}
	return nil
}

func tvToolIcon(name string) *ui.SVG {
	switch strings.ToLower(name) {
	case "read":
		return gui.IconFile
	case "write", "edit":
		return gui.IconFileDiff
	case "bash":
		return gui.IconTerminal
	case "grep", "find":
		return gui.IconSearch
	case "ls":
		return gui.IconFolder
	default:
		return gui.IconWrench
	}
}

func tvCountDiffStats(patch, oldText, newText string) (adds, dels int) {
	if patch != "" {
		for _, ln := range strings.Split(patch, "\n") {
			if strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---") {
				continue
			}
			if strings.HasPrefix(ln, "+") {
				adds++
			} else if strings.HasPrefix(ln, "-") {
				dels++
			}
		}
		if adds > 0 || dels > 0 {
			return adds, dels
		}
	}
	if oldText != "" {
		dels = strings.Count(oldText, "\n") + 1
	}
	if newText != "" {
		adds = strings.Count(newText, "\n") + 1
	}
	return adds, dels
}

// ModifiedFileStats returns the normalized workspace path and (+adds, -dels) line counts
// if this card represents a completed `edit` or `write` file modification.
func (tc *ToolExecutionCard) ModifiedFileStats() (path string, adds, dels int) {
	if tc == nil {
		return "", 0, 0
	}
	tc.mu.Lock()
	name := strings.ToLower(tc.toolName)
	args := tvCloneArgs(tc.args)
	res := tvCloneResult(tc.result)
	cwd := tc.cwd
	isErr := tc.isError
	tc.mu.Unlock()

	if isErr || (name != "edit" && name != "write") {
		return "", 0, 0
	}
	rawPath := tvArgStr(args, "path", "file_path")
	if rawPath == "" {
		return "", 0, 0
	}
	disp := tvDisplayPath(rawPath, cwd)
	if name == "write" {
		content := tvArgStr(args, "content")
		if content == "" {
			return disp, 0, 0
		}
		return disp, strings.Count(content, "\n") + 1, 0
	}
	var patch string
	if det, ok := tvDecodeDetails[core.EditToolDetails](res.Details); ok {
		patch = det.Patch
		if patch == "" {
			patch = det.Diff
		}
	}
	edits := tvExtractEdits(args)
	var oldB, newB strings.Builder
	for _, ed := range edits {
		oldB.WriteString(ed.OldText)
		newB.WriteString(ed.NewText)
	}
	a, d := tvCountDiffStats(patch, oldB.String(), newB.String())
	return disp, a, d
}

func tvMicroBadge(c *ui.Context, th *gui.GuiTheme, label string, textColor ui.Color) {
	ui.Box(c).
		Background(th.Inset).
		Border(1, th.Border).
		Radius(5).
		Padding(1, 6).
		Children(func() {
			ui.Text(c, label).TextColor(textColor).FontSize(10).SingleLine()
		})
}

func tvGhostActionButton(c *ui.Context, th *gui.GuiTheme, label string) bool {
	btn := ui.ButtonBase(c).
		Height(20).
		Padding(0, 6, 0, 6).
		Radius(5).
		AlignItems(ui.Center).
		Justify(ui.Center).
		Cursor(ui.CursorPointer).
		Label(label)
	if btn.Hovered() {
		btn.Background(th.Overlay)
	}
	btn.Children(func() {
		ui.Text(c, label).TextColor(th.TextTertiary).FontSize(11).SingleLine()
	})
	return btn.Clicked()
}

// View renders the immediate-mode MyGo UI tree for the ToolExecutionCard.
func (tc *ToolExecutionCard) View(c *ui.Context) ui.Element {
	tc.mu.Lock()
	snap := ToolExecutionSnapshot{
		ToolCallId:     tc.toolCallId,
		ToolName:       tc.toolName,
		Label:          tc.label,
		Cwd:            tc.cwd,
		Args:           tvCloneArgs(tc.args),
		ArgsComplete:   tc.argsComplete,
		Result:         tvCloneResult(tc.result),
		HasResult:      tc.hasResult,
		Pending:        tc.pending,
		IsError:        tc.isError,
		Expanded:       tc.expanded,
		ShowImages:     tc.showImages,
		DiffSideBySide: tc.diffSideBySide,
	}
	if tc.hasDuration {
		snap.Duration = tc.duration
	}
	th := gui.ResolveGuiTheme(c, tc.theme)
	custom := tc.customRenderer
	onToggle := tc.onToggle
	onCopy := tc.onCopy
	imgCache := tc.imgCache
	tc.mu.Unlock()

	if custom != nil {
		if el := custom(c, snap, th); el != (ui.Element{}) {
			return el
		}
	}

	bg := th.Raised
	border := th.Border
	iconCol := th.TextSecondary
	if snap.Pending {
		border = th.BorderAccent
		iconCol = th.Accent
	} else if snap.IsError {
		bg = th.ToolErrorBg
		border = th.Error.Alpha(0.55)
		iconCol = th.Error
	} else if snap.HasResult {
		iconCol = th.Success
	}

	title, subtitle, extraBadges, copyText := tvBuildHeaderMeta(snap)
	_, modAdds, modDels := tc.ModifiedFileStats()

	var toggleClicked, toggleDiffClicked, copyClicked bool

	card := ui.Column(c).
		FillWidth().
		MinWidth(0).
		ClipX().
		Background(bg).
		Border(1, border).
		Radius(8).
		Padding(6, 10).
		Gap(6)

	card.Children(func() {
		// Compact 28px header row
		ui.Row(c).FillWidth().MinWidth(0).MinHeight(22).AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
			titleTrigger := ui.Row(c).Shrink(1).Grow(1).MinWidth(0).ClipX().AlignItems(ui.Center).Gap(7).Cursor(ui.CursorPointer)
			titleTrigger.Children(func() {
				if snap.Pending {
					ui.Spinner(c).TextColor(th.Accent).Label(snap.ToolName)
				} else {
					gui.IconView(c, tvToolIcon(snap.ToolName), 13, iconCol)
				}
				ui.Text(c, title).TextColor(th.Text).FontWeight(600).FontSize(12).SingleLine().Shrink(0)
				if subtitle != "" {
					ui.Text(c, subtitle).TextColor(th.TextSecondary).Font("monospace").FontSize(12).SingleLine().Shrink(1).MinWidth(0)
				}
			})
			if titleTrigger.Clicked() {
				toggleClicked = true
			}

			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(5).Children(func() {
				if modAdds > 0 {
					ui.Textf(c, "+%d", modAdds).TextColor(th.DiffAdded).Font("monospace").FontSize(11).SingleLine()
				}
				if modDels > 0 {
					ui.Textf(c, "-%d", modDels).TextColor(th.DiffRemoved).Font("monospace").FontSize(11).SingleLine()
				}
				for _, b := range extraBadges {
					bCol := th.TextTertiary
					if b == "truncated" || b == "lines truncated" {
						bCol = th.Warning
					}
					tvMicroBadge(c, th, b, bCol)
				}
				if snap.Duration > 0 {
					tvMicroBadge(c, th, FormatDuration(snap.Duration), th.TextTertiary)
				}
				if strings.EqualFold(snap.ToolName, "edit") {
					layoutLbl := "Side-by-side"
					if snap.DiffSideBySide {
						layoutLbl = "Inline"
					}
					if tvGhostActionButton(c, th, layoutLbl) {
						toggleDiffClicked = true
					}
				}
				expandLbl := "Expand"
				if snap.Expanded {
					expandLbl = "Collapse"
				}
				if tvGhostActionButton(c, th, expandLbl) {
					toggleClicked = true
				}
				if copyText != "" {
					if tvGhostActionButton(c, th, "Copy") {
						copyClicked = true
					}
				}
			})
		})

		// Specialized body
		tc.renderBody(c, snap, th, imgCache)
	})

	if toggleClicked {
		tc.mu.Lock()
		tc.expanded = !tc.expanded
		newState := tc.expanded
		tc.mu.Unlock()
		if onToggle != nil {
			onToggle(newState)
		}
	}
	if toggleDiffClicked {
		tc.SetDiffLayout(!snap.DiffSideBySide)
	}
	if copyClicked && copyText != "" {
		if onCopy != nil {
			onCopy(copyText)
		} else {
			_ = utils.CopyToClipboard(copyText)
		}
	}

	return card
}

func tvBuildHeaderMeta(snap ToolExecutionSnapshot) (title, subtitle string, badges []string, copyText string) {
	displayName := snap.ToolName
	if snap.Label != "" {
		displayName = snap.Label
	}
	textOut := ""
	if snap.HasResult {
		textOut = core.GetTextOutput(&snap.Result, false)
	}

	switch strings.ToLower(snap.ToolName) {
	case "read":
		title = "read"
		rawPath := tvArgStr(snap.Args, "path", "file_path")
		subtitle = tvDisplayPath(rawPath, snap.Cwd)
		offset, _ := tvArgVal(snap.Args, "offset")
		limit, _ := tvArgVal(snap.Args, "limit")
		if rng := core.FormatReadLineRange(offset, limit); rng != "" {
			subtitle += rng
		}
		if det, ok := tvDecodeDetails[core.ReadToolDetails](snap.Result.Details); ok {
			if det.Truncation != nil && det.Truncation.Truncated {
				badges = append(badges, "truncated")
			}
			if det.ImageUnsupportedByModel != nil && *det.ImageUnsupportedByModel != "" {
				badges = append(badges, "image unsupported")
			}
		}
		if len(textOut) > 0 {
			badges = append(badges, FormatBytes(int64(len(textOut))))
		}
		copyText = textOut

	case "write":
		title = "write"
		rawPath := tvArgStr(snap.Args, "path", "file_path")
		subtitle = tvDisplayPath(rawPath, snap.Cwd)
		content := tvArgStr(snap.Args, "content")
		if content != "" {
			badges = append(badges, FormatBytes(int64(len(content))))
			copyText = content
		} else {
			copyText = textOut
		}

	case "edit":
		title = "edit"
		rawPath := tvArgStr(snap.Args, "path", "file_path")
		subtitle = tvDisplayPath(rawPath, snap.Cwd)
		if det, ok := tvDecodeDetails[core.EditToolDetails](snap.Result.Details); ok {
			if det.FirstChangedLine != nil {
				subtitle += fmt.Sprintf(":L%d", *det.FirstChangedLine)
			}
			copyText = det.Patch
			if copyText == "" {
				copyText = det.Diff
			}
		}
		if copyText == "" {
			copyText = textOut
		}

	case "bash":
		title = "bash"
		cmd := tvArgStr(snap.Args, "command")
		if cmd != "" {
			subtitle = "$ " + msgSingleLinePreview(cmd, 80)
		}
		if det, ok := tvDecodeDetails[core.BashToolDetails](snap.Result.Details); ok {
			if det.Truncation != nil && det.Truncation.Truncated {
				badges = append(badges, "truncated")
			}
		}
		cleanOut := utils.StripAnsi(textOut)
		if len(cleanOut) > 0 {
			badges = append(badges, FormatBytes(int64(len(cleanOut))))
		}
		copyText = cleanOut

	case "grep":
		title = "grep"
		pattern := tvArgStr(snap.Args, "pattern")
		searchPath := tvDisplayPath(tvArgStr(snap.Args, "path"), snap.Cwd)
		if pattern != "" {
			subtitle = "/" + pattern + "/"
		}
		if searchPath != "" {
			subtitle = strings.TrimSpace(subtitle + " in " + searchPath)
		}
		if det, ok := tvDecodeDetails[core.GrepToolDetails](snap.Result.Details); ok {
			if det.MatchLimitReached != nil {
				badges = append(badges, fmt.Sprintf("limit %d", *det.MatchLimitReached))
			}
			if det.LinesTruncated != nil && *det.LinesTruncated {
				badges = append(badges, "lines truncated")
			}
		}
		copyText = textOut

	case "find":
		title = "find"
		pattern := tvArgStr(snap.Args, "pattern")
		searchPath := tvDisplayPath(tvArgStr(snap.Args, "path"), snap.Cwd)
		if pattern != "" {
			subtitle = pattern
		}
		if searchPath != "" {
			subtitle = strings.TrimSpace(subtitle + " in " + searchPath)
		}
		if det, ok := tvDecodeDetails[core.FindToolDetails](snap.Result.Details); ok && det.ResultLimitReached != nil {
			badges = append(badges, fmt.Sprintf("limit %d", *det.ResultLimitReached))
		}
		copyText = textOut

	case "ls":
		title = "ls"
		p := tvDisplayPath(tvArgStr(snap.Args, "path"), snap.Cwd)
		if p == "" {
			p = "."
		}
		subtitle = p
		if det, ok := tvDecodeDetails[core.LsToolDetails](snap.Result.Details); ok && det.EntryLimitReached != nil {
			badges = append(badges, fmt.Sprintf("limit %d", *det.EntryLimitReached))
		}
		copyText = textOut

	default:
		title = displayName
		subtitle = tvFormatGenericArgsInline(snap.Args)
		if len(textOut) > 0 {
			badges = append(badges, FormatBytes(int64(len(textOut))))
		}
		copyText = textOut
	}

	return title, subtitle, badges, copyText
}

func tvFormatGenericArgsInline(args *jsonx.Object) string {
	if args == nil || args.Len() == 0 {
		return ""
	}
	keys := args.Keys()
	sort.Strings(keys)
	parts := make([]string, 0, min(3, len(keys)))
	for _, k := range keys {
		if len(parts) >= 3 {
			parts = append(parts, "...")
			break
		}
		v, _ := args.Get(k)
		if s := core.Str(v); s != nil {
			parts = append(parts, fmt.Sprintf("%s=%q", k, msgSingleLinePreview(*s, 28)))
		} else {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(parts, " ")
}

func (tc *ToolExecutionCard) ensureCodeViewer(filename, content string, th *gui.GuiTheme) *gui.CodeViewer {
	langHint := filename
	if langHint == "" {
		langHint = "file.txt"
	}
	sig := fmt.Sprintf("%s:%d:%s", langHint, len(content), msgPrefix(content, 32))
	tc.mu.Lock()
	cv := tc.codeViewer
	if cv == nil {
		cv = gui.NewCodeViewer(langHint, content)
		cv.SetReadOnly(true)
		cv.SetFitContent(true)
		cv.SetShowHeader(false)
		tc.codeViewer = cv
		tc.codeSig = sig
	} else if tc.codeSig != sig {
		cv.SetContent(langHint, content)
		tc.codeSig = sig
	}
	cv.SetTheme(th)
	tc.mu.Unlock()
	return cv
}

func (tc *ToolExecutionCard) ensureDiffViewer(
	filename, patch, oldText, newText string,
	sideBySide bool,
	th *gui.GuiTheme,
) (*gui.DiffViewer, bool) {
	sig := fmt.Sprintf("%s:%d:%d:%d:%s", filename, len(patch), len(oldText), len(newText), msgPrefix(patch+oldText, 32))
	layout := editor.DiffUnified
	if sideBySide {
		layout = editor.DiffSplit
	}

	tc.mu.Lock()
	dv := tc.diffViewer
	needUpdate := dv == nil || tc.diffSig != sig
	if dv == nil {
		dv = gui.NewDiffViewer(oldText, newText)
		dv.SetShowToolbar(true)
		dv.SetFitContent(true)
		tc.diffViewer = dv
	}
	tc.mu.Unlock()

	ok := false
	if needUpdate {
		if patch != "" {
			if err := dv.ParseAndSetPatch(patch); err == nil {
				ok = true
			}
		}
		if !ok && (oldText != "" || newText != "") {
			dv.SetTexts(oldText, newText)
			ok = true
		}
		if filename != "" {
			dv.SetLanguageForFile(filename)
		}
		tc.mu.Lock()
		tc.diffSig = sig
		tc.mu.Unlock()
	} else {
		ok = patch != "" || oldText != "" || newText != ""
	}
	dv.SetLayout(layout)
	dv.SetTheme(th)
	return dv, ok
}

func (tc *ToolExecutionCard) renderBody(c *ui.Context, snap ToolExecutionSnapshot, th *gui.GuiTheme, imgCache map[string]msgDecodedImage) {
	textOut := ""
	if snap.HasResult {
		textOut = core.GetTextOutput(&snap.Result, false)
	}
	imgs := tvExtractImages(snap.Result)

	if snap.IsError && textOut != "" {
		ui.Box(c).
			FillWidth().
			MinWidth(0).
			ClipX().
			Background(th.Background).
			Border(1, th.Error).
			Radius(th.Radius-2).
			Padding(8, 10).
			Children(func() {
				ui.Text(c, textOut).FillWidth().MinWidth(0).TextColor(th.Error).Font("monospace").FontSize(12).Selectable()
			})
		if snap.ShowImages && len(imgs) > 0 {
			tc.mu.Lock()
			msgRenderImageThumbs(c, th, imgs, imgCache)
			tc.mu.Unlock()
		}
		return
	}

	switch strings.ToLower(snap.ToolName) {
	case "read":
		tc.renderReadBody(c, snap, textOut, imgs, th, imgCache)
	case "write":
		tc.renderWriteBody(c, snap, textOut, th)
	case "edit":
		tc.renderEditBody(c, snap, textOut, th)
	case "bash":
		tc.renderBashBody(c, snap, textOut, th)
	case "grep":
		tvRenderLinesBody(c, textOut, tvGrepPreviewLines, false, snap.Expanded, "(no matches)", th)
	case "find":
		tvRenderLinesBody(c, textOut, tvFindPreviewLines, false, snap.Expanded, "(no files)", th)
	case "ls":
		tvRenderLinesBody(c, textOut, tvLsPreviewLines, false, snap.Expanded, "(empty directory)", th)
	default:
		tc.renderGenericBody(c, snap, textOut, imgs, th, imgCache)
	}
}

func (tc *ToolExecutionCard) renderReadBody(
	c *ui.Context,
	snap ToolExecutionSnapshot,
	textOut string,
	imgs []ai.ImageContent,
	th *gui.GuiTheme,
	imgCache map[string]msgDecodedImage,
) {
	if snap.ShowImages && len(imgs) > 0 {
		tc.mu.Lock()
		msgRenderImageThumbs(c, th, imgs, imgCache)
		tc.mu.Unlock()
	}
	if !snap.HasResult {
		return
	}
	filename := tvArgStr(snap.Args, "path", "file_path")
	if snap.Expanded && textOut != "" {
		cv := tc.ensureCodeViewer(filename, textOut, th)
		ui.Box(c).FillWidth().MinWidth(0).ClipX().Children(func() {
			cv.View(c)
		})
	} else {
		tvRenderLinesBody(c, textOut, tvReadPreviewLines, false, false, "(empty file)", th)
	}
	if det, ok := tvDecodeDetails[core.ReadToolDetails](snap.Result.Details); ok && det.Truncation != nil && det.Truncation.Truncated {
		ui.Textf(c, "[Truncated: showing %d/%d lines (%s/%s)]",
			det.Truncation.OutputLines, det.Truncation.TotalLines,
			FormatBytes(int64(det.Truncation.OutputBytes)), FormatBytes(int64(det.Truncation.TotalBytes))).
			TextColor(th.Warning).FontSize(12)
	}
}

func (tc *ToolExecutionCard) renderWriteBody(c *ui.Context, snap ToolExecutionSnapshot, textOut string, th *gui.GuiTheme) {
	content := tvArgStr(snap.Args, "content")
	filename := tvArgStr(snap.Args, "path", "file_path")
	if content == "" && snap.HasResult {
		content = textOut
	}
	if content == "" {
		return
	}
	if lang := theme.GetLanguageFromPath(filename); lang != nil && *lang != "" {
		ui.Textf(c, "language: %s", *lang).TextColor(th.Dim).FontSize(11)
	}
	if snap.Expanded {
		cv := tc.ensureCodeViewer(filename, content, th)
		ui.Box(c).FillWidth().MinWidth(0).ClipX().Children(func() {
			cv.View(c)
		})
		return
	}
	tvRenderLinesBody(c, content, tvWritePreviewLines, false, false, "", th)
}

func (tc *ToolExecutionCard) renderEditBody(c *ui.Context, snap ToolExecutionSnapshot, textOut string, th *gui.GuiTheme) {
	filename := tvArgStr(snap.Args, "path", "file_path")
	var patch, diffFallback string
	if det, ok := tvDecodeDetails[core.EditToolDetails](snap.Result.Details); ok {
		patch = det.Patch
		diffFallback = det.Diff
	}

	var oldCombined, newCombined strings.Builder
	edits := tvExtractEdits(snap.Args)
	for i, ed := range edits {
		if i > 0 {
			oldCombined.WriteString("\n---\n")
			newCombined.WriteString("\n---\n")
		}
		oldCombined.WriteString(ed.OldText)
		newCombined.WriteString(ed.NewText)
	}

	dv, hasStructuredDiff := tc.ensureDiffViewer(
		filename,
		patch,
		oldCombined.String(),
		newCombined.String(),
		snap.DiffSideBySide,
		th,
	)
	if hasStructuredDiff {
		dv.SetHeight(0)
		if !snap.Expanded {
			dv.SetHeight(200)
		}
		ui.Box(c).FillWidth().MinWidth(0).ClipX().Children(func() {
			dv.View(c)
		})
		return
	}

	if diffFallback != "" {
		tvRenderLinesBody(c, diffFallback, 24, false, snap.Expanded, "(no diff)", th)
		return
	}
	if textOut != "" {
		tvRenderLinesBody(c, textOut, 20, false, snap.Expanded, "", th)
	}
}

func (tc *ToolExecutionCard) renderBashBody(c *ui.Context, snap ToolExecutionSnapshot, textOut string, th *gui.GuiTheme) {
	clean := utils.StripAnsi(textOut)
	if clean != "" {
		tvRenderLinesBody(c, clean, tvBashPreviewLines, true, snap.Expanded, "(no output)", th)
	} else if !snap.Pending {
		ui.Text(c, "(no output)").TextColor(th.Dim).FontSize(12)
	}
	if det, ok := tvDecodeDetails[core.BashToolDetails](snap.Result.Details); ok && det.Truncation != nil && det.Truncation.Truncated {
		msgText := fmt.Sprintf("Output truncated (%d/%d lines)", det.Truncation.OutputLines, det.Truncation.TotalLines)
		if det.FullOutputPath != nil && *det.FullOutputPath != "" {
			msgText += " · Full log: " + *det.FullOutputPath
		}
		ui.Text(c, msgText).TextColor(th.Warning).FontSize(12).Wrap()
	}
}

func (tc *ToolExecutionCard) renderGenericBody(
	c *ui.Context,
	snap ToolExecutionSnapshot,
	textOut string,
	imgs []ai.ImageContent,
	th *gui.GuiTheme,
	imgCache map[string]msgDecodedImage,
) {
	if snap.Expanded && snap.Args != nil && snap.Args.Len() > 0 {
		if pretty, err := jsonx.MarshalIndent(snap.Args, "  "); err == nil {
			ui.Column(c).
				FillWidth().
				MinWidth(0).
				ClipX().
				Background(th.Inset).
				Border(1, th.Border).
				Radius(7).
				Padding(6, 10).
				Gap(4).
				Children(func() {
					ui.Text(c, "Arguments").TextColor(th.TextTertiary).FontSize(11).FontWeight(600)
					ui.Text(c, string(pretty)).FillWidth().MinWidth(0).TextColor(th.ToolOutput).Font("monospace").FontSize(12).Selectable()
				})
		}
	}
	if snap.HasResult {
		if cleanOut := utils.StripAnsi(textOut); cleanOut != "" {
			tvRenderLinesBody(c, cleanOut, 15, false, snap.Expanded, "(no output)", th)
		}
		if snap.Expanded && snap.Result.Details != nil {
			if pretty, err := jsonx.MarshalIndent(snap.Result.Details, "  "); err == nil && len(pretty) > 2 {
				ui.Column(c).
					FillWidth().
					MinWidth(0).
					ClipX().
					Background(th.Inset).
					Border(1, th.Border).
					Radius(7).
					Padding(6, 10).
					Gap(4).
					Children(func() {
						ui.Text(c, "Details").TextColor(th.TextTertiary).FontSize(11).FontWeight(600)
						ui.Text(c, string(pretty)).FillWidth().MinWidth(0).TextColor(th.TextSecondary).Font("monospace").FontSize(12).Selectable()
					})
			}
		}
		if snap.ShowImages && len(imgs) > 0 {
			tc.mu.Lock()
			msgRenderImageThumbs(c, th, imgs, imgCache)
			tc.mu.Unlock()
		}
	}
}

func tvRenderLinesBody(c *ui.Context, text string, maxLines int, fromTail, expanded bool, emptyLabel string, th *gui.GuiTheme) {
	shown, hidden, _ := tvPreviewLines(text, maxLines, fromTail, expanded)
	if shown == "" {
		if emptyLabel != "" {
			ui.Text(c, emptyLabel).TextColor(th.Dim).FontSize(12)
		}
		return
	}
	if hidden > 0 && fromTail {
		ui.Textf(c, "... (%d earlier lines, click Expand to show all)", hidden).TextColor(th.Muted).FontSize(12)
	}
	box := ui.Column(c).
		FillWidth().
		MinWidth(0).
		ClipX().
		Background(th.Inset).
		Border(1, th.Border).
		Radius(7).
		Padding(8, 10)
	if expanded {
		box = ui.Scroll(c).
			FillWidth().
			MinWidth(0).
			ClipX().
			MaxHeight(340).
			Background(th.Inset).
			Border(1, th.Border).
			Radius(7).
			Padding(8, 10)
	}
	box.Children(func() {
		ui.Text(c, shown).
			FillWidth().
			MinWidth(0).
			TextColor(th.ToolOutput).
			Font("monospace").
			FontSize(12).
			Selectable()
	})
	if hidden > 0 && !fromTail {
		ui.Textf(c, "... (%d more lines, click Expand to show all)", hidden).TextColor(th.Muted).FontSize(12)
	}
}
