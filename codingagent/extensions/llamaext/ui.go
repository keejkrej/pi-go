// Ported from packages/coding-agent/src/extensions/llama/ui.ts (pi v1.5.2).

package llamaext

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

const (
	uiMaxVisible      = 10
	uiSearchDebounce  = 500 * time.Millisecond
	uiDefaultSpecDesc = "Download model specification directly"
)

// LlamaManagerActionType identifies the action selected from the llama.cpp model manager view.
type LlamaManagerActionType string

const (
	LlamaActionLoad     LlamaManagerActionType = "load"
	LlamaActionUnload   LlamaManagerActionType = "unload"
	LlamaActionDownload LlamaManagerActionType = "download"
	LlamaActionCancel   LlamaManagerActionType = "cancel"
)

// LlamaManagerAction represents a user choice in ShowLlamaUi.
type LlamaManagerAction struct {
	Type  LlamaManagerActionType
	Model *LlamaModelInfo
}

// LlamaUi abstracts interactive dialogs and custom components for /llama so tests can mock UI interactions.
type LlamaUi interface {
	Select(ctx context.Context, title string, options []string) (string, bool, error)
	Confirm(ctx context.Context, title, message string) (bool, error)
	Input(ctx context.Context, title, placeholder string) (string, bool, error)
	Notify(message, level string)
	ShowManager(ctx context.Context, models []LlamaModelInfo) (LlamaManagerAction, error)
	SearchHuggingFace(ctx context.Context, hf *HuggingFaceClient) (string, bool, error)
	RunWithProgress(ctx context.Context, title string, confirmCancel bool, fn func(ctx context.Context, update func(LlamaProgress)) error) error
}

type uiExtensionLlamaUi struct {
	ui core.ExtensionUIContext
}

// NewExtensionLlamaUi adapts a core.ExtensionUIContext to LlamaUi.
func NewExtensionLlamaUi(ui core.ExtensionUIContext) LlamaUi {
	return &uiExtensionLlamaUi{ui: ui}
}

func (u *uiExtensionLlamaUi) Select(ctx context.Context, title string, options []string) (string, bool, error) {
	chosen, err := u.ui.Select(ctx, title, options, &core.ExtensionUIDialogOptions{Ctx: ctx})
	if err != nil {
		return "", false, err
	}
	if chosen == nil {
		return "", false, nil
	}
	return *chosen, true, nil
}

func (u *uiExtensionLlamaUi) Confirm(ctx context.Context, title, message string) (bool, error) {
	return u.ui.Confirm(ctx, title, message, &core.ExtensionUIDialogOptions{Ctx: ctx})
}

func (u *uiExtensionLlamaUi) Input(ctx context.Context, title, placeholder string) (string, bool, error) {
	var ph *string
	if placeholder != "" {
		ph = &placeholder
	}
	val, err := u.ui.Input(ctx, title, ph, &core.ExtensionUIDialogOptions{Ctx: ctx})
	if err != nil {
		return "", false, err
	}
	if val == nil {
		return "", false, nil
	}
	return *val, true, nil
}

func (u *uiExtensionLlamaUi) Notify(message, level string) {
	u.ui.Notify(message, level)
}

func (u *uiExtensionLlamaUi) ShowManager(ctx context.Context, models []LlamaModelInfo) (LlamaManagerAction, error) {
	return ShowLlamaUi(ctx, u.ui, models)
}

func (u *uiExtensionLlamaUi) SearchHuggingFace(ctx context.Context, hf *HuggingFaceClient) (string, bool, error) {
	res, err := u.ui.Custom(ctx, func(t tui.TUI, th *theme.Theme, kb *tui.KeybindingsManager, done func(any)) (tui.Component, error) {
		comp := NewHuggingFaceSearchComponent(t, th, hf, func(spec string) {
			done(spec)
		}, func() {
			done(nil)
		})
		return comp, nil
	})
	if err != nil {
		return "", false, err
	}
	if res == nil {
		return "", false, nil
	}
	if s, ok := res.(string); ok && s != "" {
		return s, true, nil
	}
	return "", false, nil
}

func (u *uiExtensionLlamaUi) RunWithProgress(
	ctx context.Context,
	title string,
	confirmCancel bool,
	fn func(ctx context.Context, update func(LlamaProgress)) error,
) error {
	return RunWithProgress(ctx, u.ui, title, confirmCancel, fn)
}

// uiLooksLikeDirectRepo checks whether query has owner/repo[:quant] form.
func uiLooksLikeDirectRepo(query string) bool {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\r\n") {
		return false
	}
	base := trimmed
	if colon := strings.IndexByte(base, ':'); colon >= 0 {
		if colon == len(base)-1 {
			return false
		}
		base = base[:colon]
	}
	parts := strings.Split(base, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

// HuggingFaceSearchComponent lets users search GGUF models on Hugging Face or enter an owner/repo[:quant] spec directly.
type HuggingFaceSearchComponent struct {
	*tui.Container

	mu            sync.Mutex
	tuiHost       tui.TUI
	th            *theme.Theme
	hf            *HuggingFaceClient
	input         *tui.Input
	listContainer *tui.Container
	selectList    *tui.SelectList
	statusText    *tui.Text
	onSelect      func(spec string)
	onCancel      func()
	cache         map[string][]HuggingFaceModel
	results       []HuggingFaceModel
	lastQuery     string
	debounceTimer *time.Timer
	searchCancel  context.CancelFunc
	focused       bool
	disposed      bool
}

var (
	_ tui.Component = (*HuggingFaceSearchComponent)(nil)
	_ tui.Focusable = (*HuggingFaceSearchComponent)(nil)
)

// NewHuggingFaceSearchComponent builds the interactive Hugging Face model search widget.
func NewHuggingFaceSearchComponent(
	t tui.TUI,
	th *theme.Theme,
	hf *HuggingFaceClient,
	onSelect func(spec string),
	onCancel func(),
) *HuggingFaceSearchComponent {
	if th == nil {
		th = theme.CurrentTheme()
	}
	placeholder := "Search GGUF models or enter owner/repo[:quant]"
	c := &HuggingFaceSearchComponent{
		Container:     tui.NewContainer(),
		tuiHost:       t,
		th:            th,
		hf:            hf,
		listContainer: tui.NewContainer(),
		statusText:    tui.NewText("", 1, 0, nil),
		onSelect:      onSelect,
		onCancel:      onCancel,
		cache:         make(map[string][]HuggingFaceModel),
	}
	c.input = tui.NewInput(&tui.InputOptions{
		Placeholder:      &placeholder,
		PlaceholderStyle: func(s string) string { return th.Fg(theme.FgMuted, s) },
	})
	c.input.OnSubmit = func(val string) {
		c.handleSubmit(val)
	}
	c.input.OnEscape = func() {
		if c.onCancel != nil {
			c.onCancel()
		}
	}

	c.AddChild(components.NewDynamicBorder())
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(th.Fg(theme.FgAccent, th.Bold("Download llama.cpp model")), 1, 0, nil))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(c.input)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(c.statusText)
	c.AddChild(c.listContainer)
	c.AddChild(tui.NewSpacer(1))
	hints := theme.RawKeyHint(th, "↑↓", "navigate") + "  " +
		theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, "select") + "  " +
		theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "cancel")
	c.AddChild(tui.NewText(hints, 1, 0, nil))
	c.AddChild(components.NewDynamicBorder())

	c.rebuildList()
	c.scheduleSearch("")
	return c
}

// CapturesViewportInput reports that this search widget intercepts pageUp/pageDown scrolling.
func (c *HuggingFaceSearchComponent) CapturesViewportInput() bool { return true }

// Focused reports whether the search input is focused.
func (c *HuggingFaceSearchComponent) Focused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focused
}

// SetFocused forwards focus state to the embedded search input.
func (c *HuggingFaceSearchComponent) SetFocused(focused bool) {
	c.mu.Lock()
	c.focused = focused
	c.mu.Unlock()
	c.input.SetFocused(focused)
}

// Dispose cancels pending debounce timers and in-flight HTTP searches.
func (c *HuggingFaceSearchComponent) Dispose() {
	c.mu.Lock()
	c.disposed = true
	if c.debounceTimer != nil {
		c.debounceTimer.Stop()
		c.debounceTimer = nil
	}
	if c.searchCancel != nil {
		c.searchCancel()
		c.searchCancel = nil
	}
	c.mu.Unlock()
}

func (c *HuggingFaceSearchComponent) handleSubmit(val string) {
	c.mu.Lock()
	list := c.selectList
	onSelect := c.onSelect
	c.mu.Unlock()

	if list != nil {
		if item := list.GetSelectedItem(); item != nil {
			if onSelect != nil {
				onSelect(item.Value)
			}
			return
		}
	}
	trimmed := strings.TrimSpace(val)
	if uiLooksLikeDirectRepo(trimmed) && onSelect != nil {
		onSelect(trimmed)
	}
}

func (c *HuggingFaceSearchComponent) rebuildList() {
	c.mu.Lock()
	query := strings.TrimSpace(c.input.GetValue())
	results := append([]HuggingFaceModel(nil), c.results...)
	onSelect := c.onSelect
	onCancel := c.onCancel
	c.mu.Unlock()

	filtered := tui.FuzzyFilter(results, query, func(m HuggingFaceModel) string {
		return m.Id
	})
	items := make([]tui.SelectItem, 0, len(filtered)+1)
	if uiLooksLikeDirectRepo(query) {
		alreadyPresent := false
		for _, m := range filtered {
			if m.Id == query {
				alreadyPresent = true
				break
			}
		}
		if !alreadyPresent {
			desc := uiDefaultSpecDesc
			items = append(items, tui.SelectItem{
				Value:       query,
				Label:       query,
				Description: &desc,
			})
		}
	}
	for _, m := range filtered {
		desc := fmt.Sprintf("%d downloads", m.Downloads)
		items = append(items, tui.SelectItem{
			Value:       m.Id,
			Label:       m.Id,
			Description: &desc,
		})
	}

	list := tui.NewSelectList(items, uiMaxVisible, theme.GetSelectListTheme(), nil)
	list.OnSelect = func(item tui.SelectItem) {
		if onSelect != nil {
			onSelect(item.Value)
		}
	}
	list.OnCancel = func() {
		if onCancel != nil {
			onCancel()
		}
	}

	c.mu.Lock()
	c.selectList = list
	c.mu.Unlock()

	c.listContainer.Clear()
	c.listContainer.AddChild(list)
}

func (c *HuggingFaceSearchComponent) setStatus(msg string) {
	if msg == "" {
		c.statusText.SetText("")
	} else {
		c.statusText.SetText(c.th.Fg(theme.FgMuted, msg))
	}
}

func (c *HuggingFaceSearchComponent) requestRender() {
	if c.tuiHost != nil {
		c.tuiHost.RequestRender(nil)
	}
}

func (c *HuggingFaceSearchComponent) scheduleSearch(query string) {
	trimmed := strings.TrimSpace(query)

	c.mu.Lock()
	if c.disposed || c.hf == nil {
		c.mu.Unlock()
		return
	}
	if cached, ok := c.cache[trimmed]; ok {
		c.results = cached
		c.mu.Unlock()
		c.setStatus("")
		c.rebuildList()
		c.requestRender()
		return
	}
	if c.debounceTimer != nil {
		c.debounceTimer.Stop()
		c.debounceTimer = nil
	}
	if c.searchCancel != nil {
		c.searchCancel()
		c.searchCancel = nil
	}
	delay := uiSearchDebounce
	if trimmed == "" {
		delay = 0
	}
	c.setStatus("Searching Hugging Face...")
	c.debounceTimer = time.AfterFunc(delay, func() {
		c.runSearch(trimmed)
	})
	c.mu.Unlock()
}

func (c *HuggingFaceSearchComponent) runSearch(query string) {
	c.mu.Lock()
	if c.disposed || c.hf == nil {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.searchCancel = cancel
	hf := c.hf
	host := c.tuiHost
	c.mu.Unlock()

	models, err := hf.Search(ctx, query)
	if ctx.Err() != nil {
		return
	}

	apply := func() {
		c.mu.Lock()
		if c.disposed {
			c.mu.Unlock()
			return
		}
		if err != nil {
			c.mu.Unlock()
			c.setStatus(c.th.Fg(theme.FgError, err.Error()))
			c.requestRender()
			return
		}
		c.cache[query] = models
		c.results = models
		c.mu.Unlock()
		c.setStatus("")
		c.rebuildList()
		c.requestRender()
	}

	if host != nil {
		host.Post(apply)
	} else {
		apply()
	}
}

// HandleInput routes navigation keys to SelectList and text input to Input.
func (c *HuggingFaceSearchComponent) HandleInput(data string) {
	kb := tui.GetKeybindings()
	c.mu.Lock()
	list := c.selectList
	prevQuery := c.lastQuery
	c.mu.Unlock()

	if list != nil && (kb.Matches(data, tui.KeybindingTuiSelectUp) ||
		kb.Matches(data, tui.KeybindingTuiSelectDown) ||
		kb.Matches(data, tui.KeybindingTuiSelectPageUp) ||
		kb.Matches(data, tui.KeybindingTuiSelectPageDown)) {
		list.HandleInput(data)
		return
	}

	c.input.HandleInput(data)
	nextQuery := c.input.GetValue()
	if nextQuery != prevQuery {
		c.mu.Lock()
		c.lastQuery = nextQuery
		c.mu.Unlock()
		c.rebuildList()
		c.scheduleSearch(nextQuery)
	}
}

type uiLlamaViewEntry struct {
	action LlamaManagerAction
	item   tui.SelectItem
}

// LlamaViewComponent displays server models (loaded/sleeping/unloaded) and the Download action.
type LlamaViewComponent struct {
	*tui.Container
	selectList *tui.SelectList
	entries    []uiLlamaViewEntry
	onAction   func(LlamaManagerAction)
}

var _ tui.Component = (*LlamaViewComponent)(nil)

func uiFormatModelDescription(m LlamaModelInfo) string {
	status := m.Status.Value
	switch status {
	case "loaded":
		if m.Meta != nil && m.Meta.NCtx != nil && *m.Meta.NCtx > 0 {
			return fmt.Sprintf("loaded (ctx %d) — select to unload", *m.Meta.NCtx)
		}
		return "loaded — select to unload"
	case "sleeping":
		return "sleeping — select to unload or wake"
	case "loading":
		return "loading"
	default:
		if m.Source != nil && *m.Source != "" {
			return fmt.Sprintf("%s (%s) — select to load", status, *m.Source)
		}
		return status + " — select to load"
	}
}

// NewLlamaViewComponent constructs the interactive model catalog view.
func NewLlamaViewComponent(th *theme.Theme, models []LlamaModelInfo, onAction func(LlamaManagerAction)) *LlamaViewComponent {
	if th == nil {
		th = theme.CurrentTheme()
	}
	v := &LlamaViewComponent{
		Container: tui.NewContainer(),
		onAction:  onAction,
	}

	items := make([]tui.SelectItem, 0, len(models)+1)
	v.entries = make([]uiLlamaViewEntry, 0, len(models)+1)

	for i := range models {
		m := models[i]
		desc := uiFormatModelDescription(m)
		actType := LlamaActionLoad
		if m.Status.Value == "loaded" || m.Status.Value == "sleeping" {
			actType = LlamaActionUnload
		}
		mCopy := m
		item := tui.SelectItem{
			Value:       m.Id,
			Label:       m.Id,
			Description: &desc,
		}
		items = append(items, item)
		v.entries = append(v.entries, uiLlamaViewEntry{
			action: LlamaManagerAction{Type: actType, Model: &mCopy},
			item:   item,
		})
	}

	dlDesc := "Search Hugging Face for GGUF models"
	dlItem := tui.SelectItem{
		Value:       "__download__",
		Label:       "+ Download model from Hugging Face",
		Description: &dlDesc,
	}
	items = append(items, dlItem)
	v.entries = append(v.entries, uiLlamaViewEntry{
		action: LlamaManagerAction{Type: LlamaActionDownload},
		item:   dlItem,
	})

	v.selectList = tui.NewSelectList(items, uiMaxVisible, theme.GetSelectListTheme(), nil)
	v.selectList.OnSelect = func(selected tui.SelectItem) {
		for _, e := range v.entries {
			if e.item.Value == selected.Value {
				if v.onAction != nil {
					v.onAction(e.action)
				}
				return
			}
		}
	}
	v.selectList.OnCancel = func() {
		if v.onAction != nil {
			v.onAction(LlamaManagerAction{Type: LlamaActionCancel})
		}
	}

	v.AddChild(components.NewDynamicBorder())
	v.AddChild(tui.NewSpacer(1))
	v.AddChild(tui.NewText(th.Fg(theme.FgAccent, th.Bold("llama.cpp Models")), 1, 0, nil))
	v.AddChild(tui.NewSpacer(1))
	v.AddChild(v.selectList)
	v.AddChild(tui.NewSpacer(1))
	hints := theme.RawKeyHint(th, "↑↓", "navigate") + "  " +
		theme.AppKeyHint(th, tui.KeybindingTuiSelectConfirm, "select") + "  " +
		theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "close")
	v.AddChild(tui.NewText(hints, 1, 0, nil))
	v.AddChild(components.NewDynamicBorder())
	return v
}

// CapturesViewportInput reports that LlamaViewComponent intercepts page navigation keys.
func (v *LlamaViewComponent) CapturesViewportInput() bool { return true }

// HandleInput forwards keyboard events to the internal SelectList.
func (v *LlamaViewComponent) HandleInput(data string) {
	if v.selectList != nil {
		v.selectList.HandleInput(data)
	}
}

// ShowLlamaUi renders the llama.cpp manager selector and returns the chosen action.
func ShowLlamaUi(ctx context.Context, extUi core.ExtensionUIContext, models []LlamaModelInfo) (LlamaManagerAction, error) {
	if extUi == nil {
		return LlamaManagerAction{Type: LlamaActionCancel}, nil
	}
	res, err := extUi.Custom(ctx, func(t tui.TUI, th *theme.Theme, kb *tui.KeybindingsManager, done func(any)) (tui.Component, error) {
		view := NewLlamaViewComponent(th, models, func(act LlamaManagerAction) {
			done(act)
		})
		return view, nil
	})
	if err != nil {
		return LlamaManagerAction{Type: LlamaActionCancel}, err
	}
	if act, ok := res.(LlamaManagerAction); ok {
		return act, nil
	}
	return LlamaManagerAction{Type: LlamaActionCancel}, nil
}

type uiProgressView struct {
	*tui.Container
	th              *theme.Theme
	host            tui.TUI
	titleText       *tui.Text
	msgText         *tui.Text
	spinner         *tui.Loader
	onCancelRequest func()
	confirmPending  bool
	hintText        *tui.Text
}

func uiNewProgressView(host tui.TUI, th *theme.Theme, title string, onCancelRequest func()) *uiProgressView {
	if th == nil {
		th = theme.CurrentTheme()
	}
	v := &uiProgressView{
		Container:       tui.NewContainer(),
		th:              th,
		host:            host,
		titleText:       tui.NewText(th.Fg(theme.FgAccent, th.Bold(title)), 1, 0, nil),
		msgText:         tui.NewText("", 1, 0, nil),
		hintText:        tui.NewText(theme.AppKeyHint(th, tui.KeybindingTuiSelectCancel, "cancel"), 1, 0, nil),
		onCancelRequest: onCancelRequest,
	}
	spinnerColor := func(s string) string { return th.Fg(theme.FgAccent, s) }
	msgColor := func(s string) string { return th.Fg(theme.FgMuted, s) }
	v.spinner = tui.NewLoader(host, spinnerColor, msgColor, title, nil)
	if host != nil {
		v.spinner.Start()
	}

	v.AddChild(components.NewDynamicBorder())
	v.AddChild(v.spinner)
	v.AddChild(v.msgText)
	v.AddChild(tui.NewSpacer(1))
	v.AddChild(v.hintText)
	v.AddChild(components.NewDynamicBorder())
	return v
}

func (v *uiProgressView) SetProgress(p LlamaProgress) {
	label := p.Message
	if p.Ratio != nil {
		label = fmt.Sprintf("%s (%.0f%%)", label, *p.Ratio*100)
	}
	if p.Detail != nil && *p.Detail != "" {
		label += " — " + *p.Detail
	}
	v.msgText.SetText(v.th.Fg(theme.FgMuted, label))
	if v.host != nil {
		v.host.RequestRender(nil)
	}
}

func (v *uiProgressView) HandleInput(data string) {
	if tui.GetKeybindings().Matches(data, tui.KeybindingTuiSelectCancel) && v.onCancelRequest != nil {
		v.onCancelRequest()
	}
}

func (v *uiProgressView) Dispose() {
	if v.spinner != nil {
		v.spinner.Stop()
	}
}

// RunWithProgress displays a custom progress view while executing fn, supporting Esc-triggered cancellation.
func RunWithProgress(
	ctx context.Context,
	extUi core.ExtensionUIContext,
	title string,
	confirmCancel bool,
	fn func(ctx context.Context, update func(LlamaProgress)) error,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	workCtx, cancelWork := context.WithCancel(ctx)
	defer cancelWork()

	if extUi == nil {
		return fn(workCtx, func(LlamaProgress) {})
	}

	var confirmMu sync.Mutex
	confirmOpen := false

	res, err := extUi.Custom(ctx, func(t tui.TUI, th *theme.Theme, kb *tui.KeybindingsManager, done func(any)) (tui.Component, error) {
		var view *uiProgressView
		requestCancel := func() {
			if !confirmCancel {
				cancelWork()
				done(context.Canceled)
				return
			}
			confirmMu.Lock()
			if confirmOpen {
				confirmMu.Unlock()
				return
			}
			confirmOpen = true
			confirmMu.Unlock()

			go func() {
				ok, _ := extUi.Confirm(workCtx, "Cancel operation?", "Press Enter to cancel or Esc to keep waiting.", &core.ExtensionUIDialogOptions{Ctx: workCtx})
				confirmMu.Lock()
				confirmOpen = false
				confirmMu.Unlock()
				if ok {
					cancelWork()
					done(context.Canceled)
				}
			}()
		}

		view = uiNewProgressView(t, th, title, requestCancel)
		go func() {
			err := fn(workCtx, func(p LlamaProgress) {
				if t != nil {
					t.Post(func() { view.SetProgress(p) })
				}
			})
			done(err)
		}()
		return view, nil
	})
	if err != nil {
		return err
	}
	if workErr, ok := res.(error); ok && workErr != nil {
		return workErr
	}
	return nil
}

// uiIsUrl checks whether s starts with http:// or https:// and parses cleanly (used by repo helpers if needed).
func uiIsUrl(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// ErrCancelled is returned when a llama.cpp interactive prompt or operation is cancelled by the user.
var ErrCancelled = errors.New("operation cancelled")
