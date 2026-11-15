package gui

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
)

const msgBashPreviewLines = 20

// MessageCard is implemented by every transcript card component rendered in the GUI chat view.
type MessageCard interface {
	View(c *ui.Context) ui.Element
}

// FormatTokens formats token counts compactly (e.g. 950, 1.4k, 1.2M).
func FormatTokens(n int) string {
	switch {
	case n < 0:
		return fmt.Sprintf("%d", n)
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1000000:
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}

// FormatCost formats a dollar amount for footer, assistant badge, and cost summaries.
func FormatCost(cost float64) string {
	if cost <= 0 {
		return "$0.00"
	}
	if cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	if cost < 1.0 {
		return fmt.Sprintf("$%.3f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}

// FormatBytes formats a byte count into B, KB, MB, or GB units.
func FormatBytes(n int64) string {
	if n < 0 {
		return "0 B"
	}
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}

// FormatDuration formats an elapsed duration concisely (e.g. 42ms, 1.8s, 2m 5s).
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := int(d / time.Minute)
	secs := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm %ds", mins, secs)
}

type msgDecodedImage struct {
	key string
	src ui.ImageSource
	ok  bool
}

func msgRenderImageThumbs(c *ui.Context, th *gui.GuiTheme, images []ai.ImageContent, cache map[string]msgDecodedImage) {
	if len(images) == 0 {
		return
	}
	ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
		for i, img := range images {
			mime := img.MimeType
			if mime == "" {
				mime = "image"
			}
			key := fmt.Sprintf("%s:%d:%s", mime, len(img.Data), msgPrefix(img.Data, 24))
			entry, found := cache[key]
			if !found {
				entry = msgDecodedImage{key: key}
				if raw, err := base64.StdEncoding.DecodeString(img.Data); err == nil && len(raw) > 0 {
					if src, decErr := ui.DecodeImage(raw); decErr == nil && src != nil {
						entry.src = src
						entry.ok = true
					}
				}
				cache[key] = entry
			}
			if entry.ok && entry.src != nil {
				ui.Column(c).
					Background(th.SurfaceElevated).
					Border(1, th.BorderMuted).
					Radius(th.Radius).
					Padding(4).
					Gap(4).
					Children(func() {
						ui.Image(c, entry.src).Size(120, 80).Fit(ui.Contain)
						ui.Textf(c, "%s", mime).TextColor(th.Muted).FontSize(11)
					})
			} else {
				ui.Box(c).
					Background(th.SurfaceElevated).
					Border(1, th.BorderMuted).
					Radius(th.Radius).
					Padding(4, 8).
					Children(func() {
						ui.Textf(c, "[image %d: %s]", i+1, mime).TextColor(th.Muted).FontSize(12)
					})
			}
		}
	})
}

func msgPrefix(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func msgFormatTimestamp(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.UnixMilli(ts).Format("15:04:05")
}

func msgExtractTextAndImages(content ai.UserMessageContent) (string, []ai.ImageContent) {
	if content.Text != nil {
		return *content.Text, nil
	}
	var sb strings.Builder
	var imgs []ai.ImageContent
	for _, block := range content.Blocks {
		switch b := block.(type) {
		case *ai.TextContent:
			if b != nil && b.Text != "" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(b.Text)
			}
		case *ai.ImageContent:
			if b != nil {
				imgs = append(imgs, *b)
			}
		}
	}
	return sb.String(), imgs
}

// ============================================================================
// UserMessageCard
// ============================================================================

// UserMessageOption configures a UserMessageCard.
type UserMessageOption func(*UserMessageCard)

// WithUserImages attaches image blocks to the UserMessageCard.
func WithUserImages(images []ai.ImageContent) UserMessageOption {
	return func(u *UserMessageCard) {
		u.images = append([]ai.ImageContent(nil), images...)
	}
}

// WithUserTimestamp sets the epoch-millisecond timestamp rendered in the card header.
func WithUserTimestamp(ts int64) UserMessageOption {
	return func(u *UserMessageCard) { u.timestamp = ts }
}

// WithUserMessageId records an optional message/entry identifier on the card.
func WithUserMessageId(id string) UserMessageOption {
	return func(u *UserMessageCard) { u.messageId = id }
}

// WithUserTheme applies a custom *gui.GuiTheme override.
func WithUserTheme(th *gui.GuiTheme) UserMessageOption {
	return func(u *UserMessageCard) { u.theme = th }
}

// WithUserOnLink sets a handler called when links inside user markdown are clicked.
func WithUserOnLink(fn func(string)) UserMessageOption {
	return func(u *UserMessageCard) { u.onLink = fn }
}

// WithUserOnCopy sets a handler invoked when the user clicks the copy button.
func WithUserOnCopy(fn func(string)) UserMessageOption {
	return func(u *UserMessageCard) { u.onCopy = fn }
}

// UserMessageCard renders a user prompt turn inside the native GUI transcript.
type UserMessageCard struct {
	mu        sync.Mutex
	text      string
	images    []ai.ImageContent
	timestamp int64
	messageId string
	theme     *gui.GuiTheme
	onLink    func(string)
	onCopy    func(string)
	mdView    *gui.MarkdownView
	imgCache  map[string]msgDecodedImage
}

// NewUserMessageCard creates a UserMessageCard for text with optional configuration.
func NewUserMessageCard(text string, opts ...UserMessageOption) *UserMessageCard {
	u := &UserMessageCard{
		text:     text,
		imgCache: make(map[string]msgDecodedImage),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(u)
		}
	}
	u.mdView = gui.NewMarkdownView(u.text, gui.MarkdownConfig{
		Done:   true,
		Theme:  u.theme,
		OnLink: u.onLink,
	})
	return u
}

// SetText updates the user message markdown text.
func (u *UserMessageCard) SetText(text string) {
	u.mu.Lock()
	u.text = text
	u.mdView.SetText(text)
	u.mu.Unlock()
}

// Text returns the current user message text.
func (u *UserMessageCard) Text() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.text
}

// SetImages replaces attached images.
func (u *UserMessageCard) SetImages(images []ai.ImageContent) {
	u.mu.Lock()
	u.images = append([]ai.ImageContent(nil), images...)
	u.mu.Unlock()
}

// SetTimestamp updates the message timestamp in epoch milliseconds.
func (u *UserMessageCard) SetTimestamp(ts int64) {
	u.mu.Lock()
	u.timestamp = ts
	u.mu.Unlock()
}

// SetMessageId updates the optional entry identifier.
func (u *UserMessageCard) SetMessageId(id string) {
	u.mu.Lock()
	u.messageId = id
	u.mu.Unlock()
}

// SetTheme sets an optional GuiTheme override.
func (u *UserMessageCard) SetTheme(th *gui.GuiTheme) {
	u.mu.Lock()
	u.theme = th
	u.mdView.SetTheme(th)
	u.mu.Unlock()
}

// SetOnLink sets the Markdown link click callback.
func (u *UserMessageCard) SetOnLink(fn func(string)) {
	u.mu.Lock()
	u.onLink = fn
	u.mdView.SetOnLink(fn)
	u.mu.Unlock()
}

// SetOnCopy sets the copy-message callback.
func (u *UserMessageCard) SetOnCopy(fn func(string)) {
	u.mu.Lock()
	u.onCopy = fn
	u.mu.Unlock()
}

// View builds the MyGo UI element for the user message card.
func (u *UserMessageCard) View(c *ui.Context) ui.Element {
	u.mu.Lock()
	text := u.text
	images := append([]ai.ImageContent(nil), u.images...)
	ts := msgFormatTimestamp(u.timestamp)
	th := gui.ResolveGuiTheme(c, u.theme)
	onCopy := u.onCopy
	md := u.mdView
	imgCache := u.imgCache
	u.mu.Unlock()

	var copyClicked bool
	card := ui.Column(c).
		FillWidth().
		Background(th.UserMessageBg).
		Border(1, th.BorderMuted).
		Radius(th.Radius).
		Padding(10, 14).
		Gap(6)

	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "You").TextColor(th.Accent).FontWeight(600).FontSize(12)
				if ts != "" {
					ui.Text(c, ts).TextColor(th.Muted).FontSize(11)
				}
			})
			if text != "" {
				btn := ui.Button(c, "Copy").Cursor(ui.CursorPointer)
				if btn.Clicked() {
					copyClicked = true
				}
			}
		})

		if text != "" && md != nil {
			md.View(c)
		}
		if len(images) > 0 {
			u.mu.Lock()
			msgRenderImageThumbs(c, th, images, imgCache)
			u.mu.Unlock()
		}
	})

	if copyClicked {
		if onCopy != nil {
			onCopy(text)
		} else {
			_ = utils.CopyToClipboard(text)
		}
	}
	return card
}

// ============================================================================
// AssistantMessageCard
// ============================================================================

// AssistantMessageOption configures an AssistantMessageCard.
type AssistantMessageOption func(*AssistantMessageCard)

// WithAssistantStreaming configures whether the assistant card starts in streaming mode.
func WithAssistantStreaming(streaming bool) AssistantMessageOption {
	return func(a *AssistantMessageCard) { a.streaming = streaming }
}

// WithAssistantHideThinking hides thinking blocks behind an optional notice label.
func WithAssistantHideThinking(hide bool) AssistantMessageOption {
	return func(a *AssistantMessageCard) { a.hideThinking = hide }
}

// WithAssistantThinkingExpanded expands thinking disclosure cards by default.
func WithAssistantThinkingExpanded(expanded bool) AssistantMessageOption {
	return func(a *AssistantMessageCard) { a.thinkingExpanded = expanded }
}

// WithAssistantHiddenThinkingLabel overrides the placeholder shown when thinking blocks are hidden.
func WithAssistantHiddenThinkingLabel(label string) AssistantMessageOption {
	return func(a *AssistantMessageCard) { a.hiddenThinkingLabel = label }
}

// WithAssistantShowUsage enables or disables the footer token/cost badge row (default true).
func WithAssistantShowUsage(show bool) AssistantMessageOption {
	return func(a *AssistantMessageCard) { a.showUsage = show }
}

// WithAssistantShowToolCallChips controls whether inline toolCall chips render inside the card.
func WithAssistantShowToolCallChips(show bool) AssistantMessageOption {
	return func(a *AssistantMessageCard) { a.showToolCallChips = show }
}

// WithAssistantTheme applies a custom *gui.GuiTheme override.
func WithAssistantTheme(th *gui.GuiTheme) AssistantMessageOption {
	return func(a *AssistantMessageCard) { a.theme = th }
}

// WithAssistantOnLink registers a link click handler for rendered markdown.
func WithAssistantOnLink(fn func(string)) AssistantMessageOption {
	return func(a *AssistantMessageCard) { a.onLink = fn }
}

// WithAssistantOnCopyCode registers a code-block copy handler.
func WithAssistantOnCopyCode(fn func(language, code string)) AssistantMessageOption {
	return func(a *AssistantMessageCard) { a.onCopyCode = fn }
}

// WithAssistantOnCopyMessage registers a copy-full-message handler.
func WithAssistantOnCopyMessage(fn func(string)) AssistantMessageOption {
	return func(a *AssistantMessageCard) { a.onCopyMessage = fn }
}

type msgAssistantSnapshot struct {
	content       []ai.Content
	provider      string
	model         string
	thinkingLevel string
	usage         ai.Usage
	stopReason    ai.StopReason
	errorMessage  string
	timestamp     int64
	durationMs    int64
	hasDuration   bool
}

type msgRetryBanner struct {
	active      bool
	attempt     int
	maxAttempts int
	delay       time.Duration
	errMsg      string
}

// AssistantMessageCard renders a streaming or completed assistant turn, supporting
// incremental markdown rendering, collapsible thinking blocks, retry status, error
// banners, and usage/cost/stop-reason badges.
type AssistantMessageCard struct {
	mu                  sync.Mutex
	rawMsg              *ai.AssistantMessage
	snap                msgAssistantSnapshot
	streaming           bool
	hideThinking        bool
	thinkingExpanded    bool
	hiddenThinkingLabel string
	showUsage           bool
	showToolCallChips   bool
	retry               msgRetryBanner
	theme               *gui.GuiTheme
	onLink              func(string)
	onCopyCode          func(language, code string)
	onCopyMessage       func(string)
	mdPool              []*gui.MarkdownView
	thinkingOpen        map[int]bool
}

// NewAssistantMessageCard constructs an AssistantMessageCard for msg.
func NewAssistantMessageCard(msg *ai.AssistantMessage, opts ...AssistantMessageOption) *AssistantMessageCard {
	a := &AssistantMessageCard{
		showUsage:    true,
		thinkingOpen: make(map[int]bool),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	if msg != nil {
		a.Update(msg)
	}
	return a
}

func msgSnapshotAssistant(msg *ai.AssistantMessage) msgAssistantSnapshot {
	if msg == nil {
		return msgAssistantSnapshot{}
	}
	contentCopy := make([]ai.Content, 0, len(msg.Content))
	for _, blk := range msg.Content {
		switch b := blk.(type) {
		case *ai.TextContent:
			if b != nil {
				cp := *b
				contentCopy = append(contentCopy, &cp)
			}
		case *ai.ThinkingContent:
			if b != nil {
				cp := *b
				contentCopy = append(contentCopy, &cp)
			}
		case *ai.ImageContent:
			if b != nil {
				cp := *b
				contentCopy = append(contentCopy, &cp)
			}
		case *ai.ToolCall:
			if b != nil {
				cp := *b
				contentCopy = append(contentCopy, &cp)
			}
		}
	}
	model := msg.Model
	if msg.ResponseModel != nil && *msg.ResponseModel != "" {
		model = *msg.ResponseModel
	}
	var think string
	if msg.ThinkingLevel != nil {
		think = string(*msg.ThinkingLevel)
	}
	var errText string
	if msg.ErrorMessage != nil {
		errText = *msg.ErrorMessage
	}
	var dur int64
	var hasDur bool
	if msg.DurationMs != nil {
		dur = *msg.DurationMs
		hasDur = true
	}
	return msgAssistantSnapshot{
		content:       contentCopy,
		provider:      string(msg.Provider),
		model:         model,
		thinkingLevel: think,
		usage:         msg.Usage,
		stopReason:    msg.StopReason,
		errorMessage:  errText,
		timestamp:     msg.Timestamp,
		durationMs:    dur,
		hasDuration:   hasDur,
	}
}

// Update refreshes the card state from a streaming or completed *ai.AssistantMessage.
func (a *AssistantMessageCard) Update(msg *ai.AssistantMessage) {
	if msg == nil {
		return
	}
	snap := msgSnapshotAssistant(msg)
	a.mu.Lock()
	a.rawMsg = msg
	a.snap = snap
	a.syncMarkdownPoolLocked()
	a.mu.Unlock()
}

func (a *AssistantMessageCard) syncMarkdownPoolLocked() {
	isDone := !a.streaming && a.snap.stopReason != "" && a.snap.stopReason != ai.StopReasonPending
	textIdx := 0
	for i, blk := range a.snap.content {
		tc, ok := blk.(*ai.TextContent)
		if !ok || tc == nil {
			continue
		}
		trimmed := strings.TrimSpace(tc.Text)
		if trimmed == "" {
			continue
		}
		for len(a.mdPool) <= textIdx {
			mv := gui.NewMarkdownView("", gui.MarkdownConfig{
				Done:       isDone,
				Theme:      a.theme,
				OnLink:     a.onLink,
				OnCopyCode: a.onCopyCode,
			})
			a.mdPool = append(a.mdPool, mv)
		}
		mv := a.mdPool[textIdx]
		mv.SetTheme(a.theme)
		mv.SetOnLink(a.onLink)
		mv.SetOnCopyCode(a.onCopyCode)
		mv.SetText(trimmed)
		// Only the final content block should continue stream animation while active.
		blockDone := isDone || i < len(a.snap.content)-1
		mv.SetDone(blockDone)
		textIdx++
	}
}

// Message returns the most recently supplied *ai.AssistantMessage pointer.
func (a *AssistantMessageCard) Message() *ai.AssistantMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rawMsg
}

// SetStreaming updates whether the assistant message is actively streaming.
func (a *AssistantMessageCard) SetStreaming(streaming bool) {
	a.mu.Lock()
	a.streaming = streaming
	a.syncMarkdownPoolLocked()
	a.mu.Unlock()
}

// SetHideThinkingBlock controls whether thinking blocks are hidden.
func (a *AssistantMessageCard) SetHideThinkingBlock(hide bool) {
	a.mu.Lock()
	a.hideThinking = hide
	a.mu.Unlock()
}

// SetThinkingExpanded expands or collapses thinking disclosures on this card.
func (a *AssistantMessageCard) SetThinkingExpanded(expanded bool) {
	a.mu.Lock()
	a.thinkingExpanded = expanded
	for k := range a.thinkingOpen {
		a.thinkingOpen[k] = expanded
	}
	a.mu.Unlock()
}

// IsThinkingExpanded reports the card's default thinking expansion state.
func (a *AssistantMessageCard) IsThinkingExpanded() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.thinkingExpanded
}

// SetHiddenThinkingLabel sets the placeholder label when hideThinkingBlock is enabled.
func (a *AssistantMessageCard) SetHiddenThinkingLabel(label string) {
	a.mu.Lock()
	a.hiddenThinkingLabel = label
	a.mu.Unlock()
}

// SetShowUsage controls whether usage/cost/stop-reason badges render.
func (a *AssistantMessageCard) SetShowUsage(show bool) {
	a.mu.Lock()
	a.showUsage = show
	a.mu.Unlock()
}

// SetShowToolCallChips controls whether inline tool call summaries render inside the card.
func (a *AssistantMessageCard) SetShowToolCallChips(show bool) {
	a.mu.Lock()
	a.showToolCallChips = show
	a.mu.Unlock()
}

// SetRetryStatus shows an inline auto-retry banner on the card.
func (a *AssistantMessageCard) SetRetryStatus(attempt, maxAttempts int, delay time.Duration, errMsg string) {
	a.mu.Lock()
	a.retry = msgRetryBanner{
		active:      true,
		attempt:     attempt,
		maxAttempts: maxAttempts,
		delay:       delay,
		errMsg:      errMsg,
	}
	a.mu.Unlock()
}

// ClearRetryStatus clears any active auto-retry banner.
func (a *AssistantMessageCard) ClearRetryStatus() {
	a.mu.Lock()
	a.retry = msgRetryBanner{}
	a.mu.Unlock()
}

// SetTheme overrides the theme used to render this card.
func (a *AssistantMessageCard) SetTheme(th *gui.GuiTheme) {
	a.mu.Lock()
	a.theme = th
	for _, mv := range a.mdPool {
		mv.SetTheme(th)
	}
	a.mu.Unlock()
}

// SetOnLink sets the Markdown link callback.
func (a *AssistantMessageCard) SetOnLink(fn func(string)) {
	a.mu.Lock()
	a.onLink = fn
	for _, mv := range a.mdPool {
		mv.SetOnLink(fn)
	}
	a.mu.Unlock()
}

// SetOnCopyCode sets the Markdown code copy callback.
func (a *AssistantMessageCard) SetOnCopyCode(fn func(language, code string)) {
	a.mu.Lock()
	a.onCopyCode = fn
	for _, mv := range a.mdPool {
		mv.SetOnCopyCode(fn)
	}
	a.mu.Unlock()
}

// SetOnCopyMessage sets the copy-full-text callback.
func (a *AssistantMessageCard) SetOnCopyMessage(fn func(string)) {
	a.mu.Lock()
	a.onCopyMessage = fn
	a.mu.Unlock()
}

func (a *AssistantMessageCard) fullCopyTextLocked() string {
	var parts []string
	for _, blk := range a.snap.content {
		if tc, ok := blk.(*ai.TextContent); ok && tc != nil && strings.TrimSpace(tc.Text) != "" {
			parts = append(parts, strings.TrimSpace(tc.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

// View builds the MyGo element tree for the assistant message card.
func (a *AssistantMessageCard) View(c *ui.Context) ui.Element {
	a.mu.Lock()
	snap := a.snap
	streaming := a.streaming
	hideThinking := a.hideThinking
	defaultThinkOpen := a.thinkingExpanded
	hiddenThinkLabel := a.hiddenThinkingLabel
	showUsage := a.showUsage
	showToolChips := a.showToolCallChips
	retry := a.retry
	th := gui.ResolveGuiTheme(c, a.theme)
	onCopyMsg := a.onCopyMessage
	copyPayload := a.fullCopyTextLocked()
	mdPool := append([]*gui.MarkdownView(nil), a.mdPool...)
	thinkOpenCopy := make(map[int]bool, len(a.thinkingOpen))
	for k, v := range a.thinkingOpen {
		thinkOpenCopy[k] = v
	}
	a.mu.Unlock()

	var copyClicked bool
	var toggledThinking []struct {
		idx  int
		open bool
	}

	root := ui.Column(c).
		FillWidth().
		Padding(6, 4).
		Gap(8)

	root.Children(func() {
		// Compact header bar when model/time/streaming/copy controls exist
		if snap.model != "" || streaming || copyPayload != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					label := "Assistant"
					if snap.model != "" {
						if snap.provider != "" {
							label = snap.provider + "/" + snap.model
						} else {
							label = snap.model
						}
					}
					ui.Text(c, label).TextColor(th.Muted).FontWeight(600).FontSize(12)
					if snap.thinkingLevel != "" && snap.thinkingLevel != "off" {
						ui.Badge(c, "thinking:"+snap.thinkingLevel)
					}
					if ts := msgFormatTimestamp(snap.timestamp); ts != "" {
						ui.Text(c, ts).TextColor(th.Dim).FontSize(11)
					}
					if streaming {
						ui.Spinner(c).TextColor(th.Accent).Label("Streaming assistant response")
					}
				})
				if copyPayload != "" {
					btn := ui.Button(c, "Copy").Cursor(ui.CursorPointer)
					if btn.Clicked() {
						copyClicked = true
					}
				}
			})
		}

		// Retry banner if retrying
		if retry.active {
			ui.Row(c).
				FillWidth().
				Background(th.SurfaceElevated).
				Border(1, th.Warning).
				Radius(th.Radius).
				Padding(6, 10).
				AlignItems(ui.Center).
				Gap(8).
				Children(func() {
					ui.Spinner(c).TextColor(th.Warning).Label("Retrying")
					delayStr := FormatDuration(retry.delay)
					msgText := fmt.Sprintf("Retrying (%d/%d in %s)", retry.attempt, retry.maxAttempts, delayStr)
					if retry.errMsg != "" {
						msgText += ": " + retry.errMsg
					}
					ui.Text(c, msgText).TextColor(th.Warning).FontSize(12).Wrap()
				})
		}

		textBlockIdx := 0
		thinkBlockIdx := 0
		for _, blk := range snap.content {
			switch b := blk.(type) {
			case *ai.ThinkingContent:
				if b == nil {
					continue
				}
				isRedacted := b.Redacted != nil && *b.Redacted
				thinkText := strings.TrimSpace(b.Thinking)
				if !isRedacted && thinkText == "" {
					continue
				}
				idx := thinkBlockIdx
				thinkBlockIdx++

				if hideThinking {
					lbl := strings.TrimSpace(hiddenThinkLabel)
					if lbl == "" {
						lbl = "Thinking..."
					}
					ui.Text(c, lbl).TextColor(th.Dim).FontSize(12)
					continue
				}

				if isRedacted {
					ui.Box(c).
						FillWidth().
						Background(th.Surface).
						Border(1, th.BorderMuted).
						Radius(th.Radius).
						Padding(6, 10).
						Children(func() {
							ui.Text(c, "[thinking redacted]").TextColor(th.Dim).FontSize(12)
						})
					continue
				}

				open := defaultThinkOpen
				if v, ok := thinkOpenCopy[idx]; ok {
					open = v
				}
				subtitle := fmt.Sprintf("%d chars", len([]rune(thinkText)))
				thinkCard := ui.Column(c).
					Key(fmt.Sprintf("thinking-%d", idx)).
					FillWidth().
					Background(th.Surface).
					Border(1, th.BorderMuted).
					Radius(th.Radius).
					Padding(8, 12).
					Gap(6)
				thinkCard.Children(func() {
					cp := ui.CollapsibleBase(c, &open)
					cp.Trigger.FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Cursor(ui.CursorPointer)
					cp.Trigger.Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
							chev := "▸"
							if open {
								chev = "▾"
							}
							ui.Text(c, chev).TextColor(th.Muted)
							ui.Text(c, "Thinking").TextColor(th.ThinkingText).FontWeight(600)
							ui.Text(c, subtitle).TextColor(th.Muted).FontSize(12)
						})
					})
					if cp.Trigger.Changed() {
						toggledThinking = append(toggledThinking, struct {
							idx  int
							open bool
						}{idx: idx, open: open})
					}
					cp.Panel(func() {
						ui.Text(c, thinkText).TextColor(th.ThinkingText).Selectable().Wrap()
					})
				})

			case *ai.TextContent:
				if b == nil || strings.TrimSpace(b.Text) == "" {
					continue
				}
				if textBlockIdx < len(mdPool) && mdPool[textBlockIdx] != nil {
					mdPool[textBlockIdx].View(c)
				}
				textBlockIdx++

			case *ai.ToolCall:
				if b == nil || !showToolChips {
					continue
				}
				ui.Row(c).
					FillWidth().
					Background(th.SurfaceElevated).
					Border(1, th.BorderMuted).
					Radius(th.Radius).
					Padding(4, 8).
					AlignItems(ui.Center).
					Gap(8).
					Children(func() {
						ui.Badge(c, "tool")
						ui.Text(c, b.Name).TextColor(th.ToolTitle).Font("monospace").FontSize(12)
						if b.Id != "" {
							ui.Text(c, b.Id).TextColor(th.Dim).FontSize(11)
						}
					})
			}
		}

		// Aborted / error notice matching TS AssistantMessageComponent
		switch snap.stopReason {
		case ai.StopReasonAborted:
			errMsg := snap.errorMessage
			if errMsg != "" && errMsg != "Request was aborted" {
				ui.Text(c, "Aborted: "+errMsg).TextColor(th.Error).Wrap()
			} else {
				ui.Text(c, "Operation aborted").TextColor(th.Warning)
			}
		case ai.StopReasonError:
			errMsg := snap.errorMessage
			if errMsg == "" {
				errMsg = "Unknown error"
			}
			ui.Box(c).
				FillWidth().
				Background(th.ToolErrorBg).
				Border(1, th.Error).
				Radius(th.Radius).
				Padding(8, 12).
				Children(func() {
					ui.Text(c, "Error: "+errMsg).TextColor(th.Error).Selectable().Wrap()
				})
		}

		// Usage / cost / duration / stop reason badges
		if showUsage && !streaming && (snap.usage.Input > 0 || snap.usage.Output > 0 || snap.usage.Cost.Total > 0 || snap.stopReason != "") {
			ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
				if snap.usage.Input > 0 || snap.usage.Output > 0 {
					ui.Textf(c, "↑%s ↓%s", FormatTokens(snap.usage.Input), FormatTokens(snap.usage.Output)).
						TextColor(th.Muted).FontSize(11)
				}
				if snap.usage.CacheRead > 0 {
					ui.Textf(c, "R%s", FormatTokens(snap.usage.CacheRead)).TextColor(th.Dim).FontSize(11)
				}
				if snap.usage.CacheWrite > 0 {
					ui.Textf(c, "W%s", FormatTokens(snap.usage.CacheWrite)).TextColor(th.Dim).FontSize(11)
				}
				if snap.usage.Cost.Total > 0 {
					ui.Text(c, FormatCost(snap.usage.Cost.Total)).TextColor(th.Muted).FontSize(11)
				}
				if snap.hasDuration && snap.durationMs > 0 {
					ui.Text(c, FormatDuration(time.Duration(snap.durationMs)*time.Millisecond)).TextColor(th.Dim).FontSize(11)
				}
				if snap.stopReason != "" && snap.stopReason != ai.StopReasonStop && snap.stopReason != ai.StopReasonToolUse {
					ui.Badge(c, string(snap.stopReason))
				}
			})
		}
	})

	if len(toggledThinking) > 0 {
		a.mu.Lock()
		for _, item := range toggledThinking {
			a.thinkingOpen[item.idx] = item.open
		}
		a.mu.Unlock()
	}

	if copyClicked && copyPayload != "" {
		if onCopyMsg != nil {
			onCopyMsg(copyPayload)
		} else {
			_ = utils.CopyToClipboard(copyPayload)
		}
	}

	return root
}

// ============================================================================
// BashExecutionCard
// ============================================================================

// BashStatus represents the execution lifecycle state of a BashExecutionCard.
type BashStatus string

const (
	BashRunning   BashStatus = "running"
	BashComplete  BashStatus = "complete"
	BashCancelled BashStatus = "cancelled"
	BashError     BashStatus = "error"
)

// BashCardOption configures a BashExecutionCard.
type BashCardOption func(*BashExecutionCard)

// WithBashExpanded initializes the BashExecutionCard open/collapsed state.
func WithBashExpanded(expanded bool) BashCardOption {
	return func(b *BashExecutionCard) { b.expanded = expanded }
}

// WithBashTheme applies a GuiTheme override.
func WithBashTheme(th *gui.GuiTheme) BashCardOption {
	return func(b *BashExecutionCard) { b.theme = th }
}

// WithBashOnCancel registers a callback fired when the user clicks Cancel on a running command.
func WithBashOnCancel(fn func()) BashCardOption {
	return func(b *BashExecutionCard) { b.onCancel = fn }
}

// WithBashOnCopy registers a copy callback for bash command output.
func WithBashOnCopy(fn func(string)) BashCardOption {
	return func(b *BashExecutionCard) { b.onCopy = fn }
}

// BashExecutionCard renders a user `!` or `!!` command execution card with streaming
// output, ANSI stripping, tail preview with expand toggle, exit badge, and truncation hint.
type BashExecutionCard struct {
	mu                 sync.Mutex
	command            string
	excludeFromContext bool
	outputLines        []string
	status             BashStatus
	exitCode           *int
	truncated          bool
	fullOutputPath     string
	truncation         *core.TruncationResult
	expanded           bool
	theme              *gui.GuiTheme
	onCancel           func()
	onCopy             func(string)
}

// NewBashExecutionCard constructs a BashExecutionCard in BashRunning state.
func NewBashExecutionCard(command string, excludeFromContext bool, opts ...BashCardOption) *BashExecutionCard {
	b := &BashExecutionCard{
		command:            command,
		excludeFromContext: excludeFromContext,
		status:             BashRunning,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	return b
}

func msgNormalizeChunkLines(chunk string) []string {
	clean := utils.StripAnsi(chunk)
	clean = strings.ReplaceAll(clean, "\r\n", "\n")
	clean = strings.ReplaceAll(clean, "\r", "\n")
	return strings.Split(clean, "\n")
}

// AppendOutput appends a streaming stdout/stderr chunk, preserving line continuations.
func (b *BashExecutionCard) AppendOutput(chunk string) {
	if chunk == "" {
		return
	}
	newLines := msgNormalizeChunkLines(chunk)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.outputLines) > 0 && len(newLines) > 0 {
		b.outputLines[len(b.outputLines)-1] += newLines[0]
		b.outputLines = append(b.outputLines, newLines[1:]...)
	} else {
		b.outputLines = append(b.outputLines, newLines...)
	}
}

// SetOutput replaces all accumulated command output.
func (b *BashExecutionCard) SetOutput(output string) {
	b.mu.Lock()
	if output == "" {
		b.outputLines = nil
	} else {
		b.outputLines = msgNormalizeChunkLines(output)
	}
	b.mu.Unlock()
}

// SetComplete transitions the card to its terminal status and attaches optional truncation metadata.
func (b *BashExecutionCard) SetComplete(
	exitCode *int,
	cancelled bool,
	truncated bool,
	fullOutputPath *string,
	truncation *core.TruncationResult,
) {
	b.mu.Lock()
	if exitCode != nil {
		cp := *exitCode
		b.exitCode = &cp
	} else {
		b.exitCode = nil
	}
	b.truncated = truncated
	if fullOutputPath != nil {
		b.fullOutputPath = *fullOutputPath
	} else {
		b.fullOutputPath = ""
	}
	b.truncation = truncation
	switch {
	case cancelled:
		b.status = BashCancelled
	case exitCode != nil && *exitCode != 0:
		b.status = BashError
	default:
		b.status = BashComplete
	}
	b.mu.Unlock()
}

// SetFromMessage populates all fields from a persisted *core.BashExecutionMessage.
func (b *BashExecutionCard) SetFromMessage(msg *core.BashExecutionMessage) {
	if msg == nil {
		return
	}
	b.mu.Lock()
	b.command = msg.Command
	b.excludeFromContext = msg.ExcludeFromContext != nil && *msg.ExcludeFromContext
	if msg.Output == "" {
		b.outputLines = nil
	} else {
		b.outputLines = msgNormalizeChunkLines(msg.Output)
	}
	b.truncated = msg.Truncated
	if msg.FullOutputPath != nil {
		b.fullOutputPath = *msg.FullOutputPath
	} else {
		b.fullOutputPath = ""
	}
	if msg.ExitCode != nil {
		cp := *msg.ExitCode
		b.exitCode = &cp
	} else {
		b.exitCode = nil
	}
	switch {
	case msg.Cancelled:
		b.status = BashCancelled
	case msg.ExitCode != nil && *msg.ExitCode != 0:
		b.status = BashError
	default:
		b.status = BashComplete
	}
	b.mu.Unlock()
}

// SetExpanded switches between collapsed preview (20 lines) and full output view.
func (b *BashExecutionCard) SetExpanded(expanded bool) {
	b.mu.Lock()
	b.expanded = expanded
	b.mu.Unlock()
}

// IsExpanded reports whether full output is expanded.
func (b *BashExecutionCard) IsExpanded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.expanded
}

// SetTheme sets an optional GuiTheme override.
func (b *BashExecutionCard) SetTheme(th *gui.GuiTheme) {
	b.mu.Lock()
	b.theme = th
	b.mu.Unlock()
}

// SetOnCancel sets the cancel callback invoked for running commands.
func (b *BashExecutionCard) SetOnCancel(fn func()) {
	b.mu.Lock()
	b.onCancel = fn
	b.mu.Unlock()
}

// SetOnCopy sets the copy-output callback.
func (b *BashExecutionCard) SetOnCopy(fn func(string)) {
	b.mu.Lock()
	b.onCopy = fn
	b.mu.Unlock()
}

// Command returns the command string.
func (b *BashExecutionCard) Command() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.command
}

// Output returns the current normalized output string.
func (b *BashExecutionCard) Output() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.outputLines, "\n")
}

// Status returns the current BashStatus.
func (b *BashExecutionCard) Status() BashStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}

// ExitCode returns a copy of the process exit code if known.
func (b *BashExecutionCard) ExitCode() *int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exitCode == nil {
		return nil
	}
	cp := *b.exitCode
	return &cp
}

// ExcludeFromContext reports whether this bash execution used `!!`.
func (b *BashExecutionCard) ExcludeFromContext() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.excludeFromContext
}

// View builds the MyGo element tree for the bash execution card.
func (b *BashExecutionCard) View(c *ui.Context) ui.Element {
	b.mu.Lock()
	cmd := b.command
	excluded := b.excludeFromContext
	lines := append([]string(nil), b.outputLines...)
	status := b.status
	exitCode := b.exitCode
	truncated := b.truncated
	fullPath := b.fullOutputPath
	truncInfo := b.truncation
	expanded := b.expanded
	th := gui.ResolveGuiTheme(c, b.theme)
	onCancel := b.onCancel
	onCopy := b.onCopy
	b.mu.Unlock()

	borderCol := th.BashMode
	prefixColor := th.BashMode
	if excluded {
		borderCol = th.Dim
		prefixColor = th.Dim
	} else if status == BashError {
		borderCol = th.Error
	}

	var statusBadge string
	switch status {
	case BashRunning:
		statusBadge = "running"
	case BashCancelled:
		statusBadge = "cancelled"
	case BashError:
		if exitCode != nil {
			statusBadge = fmt.Sprintf("exit %d", *exitCode)
		} else {
			statusBadge = "error"
		}
	case BashComplete:
		if exitCode != nil {
			statusBadge = fmt.Sprintf("exit %d", *exitCode)
		} else {
			statusBadge = "exit 0"
		}
	}

	nonEmptyLines := make([]string, len(lines))
	copy(nonEmptyLines, lines)
	hiddenCount := 0
	displayLines := nonEmptyLines
	if !expanded && len(nonEmptyLines) > msgBashPreviewLines {
		hiddenCount = len(nonEmptyLines) - msgBashPreviewLines
		displayLines = nonEmptyLines[hiddenCount:]
	}
	displayText := strings.Join(displayLines, "\n")
	fullOutput := strings.Join(nonEmptyLines, "\n")

	var cancelClicked, copyClicked, toggleExpandClicked bool

	card := ui.Column(c).
		FillWidth().
		Background(th.Surface).
		Border(1, borderCol).
		Radius(th.Radius).
		Padding(10, 14).
		Gap(8)

	card.Children(func() {
		// Header row
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
			ui.Row(c).Shrink(1).Grow(1).AlignItems(ui.Center).Gap(8).Children(func() {
				if status == BashRunning {
					ui.Spinner(c).TextColor(prefixColor).Label("Running command")
				}
				prefix := "$ "
				if excluded {
					prefix = "!! $ "
				}
				ui.Text(c, prefix+cmd).TextColor(prefixColor).FontWeight(600).Font("monospace").Wrap()
			})
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Children(func() {
				if excluded {
					ui.Badge(c, "excluded")
				}
				if statusBadge != "" {
					ui.Badge(c, statusBadge)
				}
				if status == BashRunning && onCancel != nil {
					if ui.Button(c, "Cancel").Cursor(ui.CursorPointer).Clicked() {
						cancelClicked = true
					}
				}
				if len(nonEmptyLines) > msgBashPreviewLines {
					lbl := "Expand"
					if expanded {
						lbl = "Collapse"
					}
					if ui.Button(c, lbl).Cursor(ui.CursorPointer).Clicked() {
						toggleExpandClicked = true
					}
				}
				if fullOutput != "" {
					if ui.Button(c, "Copy").Cursor(ui.CursorPointer).Clicked() {
						copyClicked = true
					}
				}
			})
		})

		// Body output
		if displayText != "" {
			if hiddenCount > 0 {
				ui.Textf(c, "... (%d earlier lines hidden, click Expand to show)", hiddenCount).
					TextColor(th.Muted).FontSize(12)
			}
			outBox := ui.Column(c).
				FillWidth().
				Background(th.Background).
				Border(1, th.BorderMuted).
				Radius(th.Radius-2).
				Padding(8, 10)
			if expanded {
				outBox = ui.Scroll(c).
					FillWidth().
					MaxHeight(360).
					Background(th.Background).
					Border(1, th.BorderMuted).
					Radius(th.Radius-2).
					Padding(8, 10)
			}
			outBox.Children(func() {
				ui.Text(c, displayText).
					TextColor(th.ToolOutput).
					Font("monospace").
					FontSize(12).
					Selectable().
					Wrap()
			})
		} else if status != BashRunning {
			ui.Text(c, "(no output)").TextColor(th.Dim).FontSize(12)
		}

		// Truncation / status footer
		if status == BashCancelled {
			ui.Text(c, "(command cancelled)").TextColor(th.Warning).FontSize(12)
		}
		if truncated && fullPath != "" {
			var info string
			if truncInfo != nil {
				info = fmt.Sprintf("Output truncated (%d/%d lines, %s/%s). Full output: %s",
					truncInfo.OutputLines, truncInfo.TotalLines,
					FormatBytes(int64(truncInfo.OutputBytes)), FormatBytes(int64(truncInfo.TotalBytes)),
					fullPath)
			} else {
				info = "Output truncated. Full output: " + fullPath
			}
			ui.Text(c, info).TextColor(th.Muted).FontSize(12).Wrap()
		}
	})

	if toggleExpandClicked {
		b.mu.Lock()
		b.expanded = !b.expanded
		b.mu.Unlock()
	}
	if cancelClicked && onCancel != nil {
		onCancel()
	}
	if copyClicked && fullOutput != "" {
		if onCopy != nil {
			onCopy(fullOutput)
		} else {
			_ = utils.CopyToClipboard(fullOutput)
		}
	}

	return card
}

// ============================================================================
// Summary / Custom / Skill Cards
// ============================================================================

// SummaryCardOption configures CompactionSummaryCard, BranchSummaryCard,
// SkillInvocationCard, and CostSummaryCard.
type SummaryCardOption func(*msgSummaryConfig)

type msgSummaryConfig struct {
	expanded  bool
	theme     *gui.GuiTheme
	timestamp int64
	messageId string
	onLink    func(string)
	onToggle  func(bool)
}

// WithCardExpanded sets whether a summary/skill card opens expanded by default.
func WithCardExpanded(expanded bool) SummaryCardOption {
	return func(o *msgSummaryConfig) { o.expanded = expanded }
}

// WithCardTheme sets a *gui.GuiTheme override.
func WithCardTheme(th *gui.GuiTheme) SummaryCardOption {
	return func(o *msgSummaryConfig) { o.theme = th }
}

// WithCardTimestamp sets an optional epoch-ms timestamp on the card.
func WithCardTimestamp(ts int64) SummaryCardOption {
	return func(o *msgSummaryConfig) { o.timestamp = ts }
}

// WithCardMessageId records an optional entry ID on the card.
func WithCardMessageId(id string) SummaryCardOption {
	return func(o *msgSummaryConfig) { o.messageId = id }
}

// WithCardOnLink registers a link handler for markdown inside the card.
func WithCardOnLink(fn func(string)) SummaryCardOption {
	return func(o *msgSummaryConfig) { o.onLink = fn }
}

// WithCardOnToggle registers a callback when the user toggles open/collapsed state.
func WithCardOnToggle(fn func(bool)) SummaryCardOption {
	return func(o *msgSummaryConfig) { o.onToggle = fn }
}

type msgCollapsibleMarkdownCard struct {
	mu       sync.Mutex
	title    string
	subtitle string
	badge    string
	bodyMd   string
	expanded bool
	theme    *gui.GuiTheme
	onLink   func(string)
	onToggle func(bool)
	mdView   *gui.MarkdownView
}

func msgNewCollapsibleMarkdownCard(
	title, subtitle, badge, bodyMd string,
	opts ...SummaryCardOption,
) *msgCollapsibleMarkdownCard {
	var cfg msgSummaryConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	c := &msgCollapsibleMarkdownCard{
		title:    title,
		subtitle: subtitle,
		badge:    badge,
		bodyMd:   bodyMd,
		expanded: cfg.expanded,
		theme:    cfg.theme,
		onLink:   cfg.onLink,
		onToggle: cfg.onToggle,
	}
	c.mdView = gui.NewMarkdownView(bodyMd, gui.MarkdownConfig{
		Done:   true,
		Theme:  cfg.theme,
		OnLink: cfg.onLink,
	})
	return c
}

func (m *msgCollapsibleMarkdownCard) setExpanded(expanded bool) {
	m.mu.Lock()
	m.expanded = expanded
	m.mu.Unlock()
}

func (m *msgCollapsibleMarkdownCard) isExpanded() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.expanded
}

func (m *msgCollapsibleMarkdownCard) setTheme(th *gui.GuiTheme) {
	m.mu.Lock()
	m.theme = th
	if m.mdView != nil {
		m.mdView.SetTheme(th)
	}
	m.mu.Unlock()
}

func (m *msgCollapsibleMarkdownCard) view(c *ui.Context) ui.Element {
	m.mu.Lock()
	title := m.title
	subtitle := m.subtitle
	badge := m.badge
	open := m.expanded
	th := gui.ResolveGuiTheme(c, m.theme)
	onToggle := m.onToggle
	md := m.mdView
	m.mu.Unlock()

	card := ui.Column(c).
		FillWidth().
		Background(th.CustomMessageBg).
		Border(1, th.BorderMuted).
		Radius(th.Radius).
		Padding(10, 14).
		Gap(6)

	var toggled bool
	card.Children(func() {
		cp := ui.CollapsibleBase(c, &open)
		cp.Trigger.FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Cursor(ui.CursorPointer)
		cp.Trigger.Children(func() {
			ui.Row(c).Shrink(1).Grow(1).AlignItems(ui.Center).Gap(8).Children(func() {
				chev := "▸"
				if open {
					chev = "▾"
				}
				ui.Text(c, chev).TextColor(th.Muted)
				ui.Text(c, title).TextColor(th.CustomMessageLabel).FontWeight(700)
				if subtitle != "" {
					ui.Text(c, subtitle).TextColor(th.CustomMessageText).FontSize(12).SingleLine()
				}
			})
			if badge != "" {
				ui.Badge(c, badge)
			}
		})
		toggled = cp.Trigger.Changed()
		cp.Panel(func() {
			ui.Column(c).FillWidth().Padding(4, 0, 2, 14).Children(func() {
				if md != nil {
					md.View(c)
				}
			})
		})
	})

	if toggled {
		m.mu.Lock()
		m.expanded = open
		m.mu.Unlock()
		if onToggle != nil {
			onToggle(open)
		}
	}
	return card
}

// CompactionSummaryCard renders a collapsible `[compaction]` summary entry in the transcript.
type CompactionSummaryCard struct {
	inner   *msgCollapsibleMarkdownCard
	message *core.CompactionSummaryMessage
}

// NewCompactionSummaryCard creates a CompactionSummaryCard from msg.
func NewCompactionSummaryCard(msg *core.CompactionSummaryMessage, opts ...SummaryCardOption) *CompactionSummaryCard {
	tokens := 0
	summary := ""
	if msg != nil {
		tokens = msg.TokensBefore
		summary = msg.Summary
	}
	subtitle := fmt.Sprintf("Compacted from %s tokens", FormatTokens(tokens))
	return &CompactionSummaryCard{
		inner:   msgNewCollapsibleMarkdownCard("[compaction]", subtitle, fmt.Sprintf("%d tok", tokens), summary, opts...),
		message: msg,
	}
}

// SetExpanded expands or collapses the compaction summary body.
func (cs *CompactionSummaryCard) SetExpanded(expanded bool) { cs.inner.setExpanded(expanded) }

// IsExpanded reports whether the compaction summary is expanded.
func (cs *CompactionSummaryCard) IsExpanded() bool { return cs.inner.isExpanded() }

// SetTheme overrides the card's GuiTheme.
func (cs *CompactionSummaryCard) SetTheme(th *gui.GuiTheme) { cs.inner.setTheme(th) }

// View renders the CompactionSummaryCard element.
func (cs *CompactionSummaryCard) View(c *ui.Context) ui.Element { return cs.inner.view(c) }

// BranchSummaryCard renders a collapsible `[branch]` summary entry in the transcript.
type BranchSummaryCard struct {
	inner   *msgCollapsibleMarkdownCard
	message *core.BranchSummaryMessage
}

// NewBranchSummaryCard creates a BranchSummaryCard from msg.
func NewBranchSummaryCard(msg *core.BranchSummaryMessage, opts ...SummaryCardOption) *BranchSummaryCard {
	subtitle := "Branch summary"
	badge := ""
	summary := ""
	if msg != nil {
		summary = msg.Summary
		if msg.FromId != nil && *msg.FromId != "" {
			badge = "from:" + msgPrefix(*msg.FromId, 8)
		}
	}
	return &BranchSummaryCard{
		inner:   msgNewCollapsibleMarkdownCard("[branch]", subtitle, badge, summary, opts...),
		message: msg,
	}
}

// SetExpanded expands or collapses the branch summary body.
func (bs *BranchSummaryCard) SetExpanded(expanded bool) { bs.inner.setExpanded(expanded) }

// IsExpanded reports whether the branch summary is expanded.
func (bs *BranchSummaryCard) IsExpanded() bool { return bs.inner.isExpanded() }

// SetTheme overrides the card's GuiTheme.
func (bs *BranchSummaryCard) SetTheme(th *gui.GuiTheme) { bs.inner.setTheme(th) }

// View renders the BranchSummaryCard element.
func (bs *BranchSummaryCard) View(c *ui.Context) ui.Element { return bs.inner.view(c) }

// SkillInvocationCard renders a collapsible `[skill]` block injected into the transcript.
type SkillInvocationCard struct {
	inner *msgCollapsibleMarkdownCard
	block core.ParsedSkillBlock
}

// NewSkillInvocationCard creates a SkillInvocationCard for a parsed skill block.
func NewSkillInvocationCard(block core.ParsedSkillBlock, opts ...SummaryCardOption) *SkillInvocationCard {
	badge := ""
	if block.Location != "" {
		badge = block.Location
	}
	return &SkillInvocationCard{
		inner: msgNewCollapsibleMarkdownCard("[skill]", block.Name, badge, block.Content, opts...),
		block: block,
	}
}

// SetExpanded expands or collapses the skill body.
func (sk *SkillInvocationCard) SetExpanded(expanded bool) { sk.inner.setExpanded(expanded) }

// IsExpanded reports whether the skill body is expanded.
func (sk *SkillInvocationCard) IsExpanded() bool { return sk.inner.isExpanded() }

// SetTheme overrides the card's GuiTheme.
func (sk *SkillInvocationCard) SetTheme(th *gui.GuiTheme) { sk.inner.setTheme(th) }

// View renders the SkillInvocationCard element.
func (sk *SkillInvocationCard) View(c *ui.Context) ui.Element { return sk.inner.view(c) }

// CustomMessageRenderer allows extensions to provide custom native MyGo UI elements
// for a *core.CustomMessage. Returning nil falls back to standard card rendering.
type CustomMessageRenderer func(c *ui.Context, msg *core.CustomMessage, expanded bool, th *gui.GuiTheme) ui.Element

// CustomMessageOption configures a CustomMessageCard.
type CustomMessageOption func(*CustomMessageCard)

// WithCustomRenderer installs an extension GUI renderer callback.
func WithCustomRenderer(fn CustomMessageRenderer) CustomMessageOption {
	return func(cm *CustomMessageCard) { cm.customRenderer = fn }
}

// WithCustomExpanded sets initial expanded state.
func WithCustomExpanded(expanded bool) CustomMessageOption {
	return func(cm *CustomMessageCard) { cm.expanded = expanded }
}

// WithCustomTheme sets an optional GuiTheme override.
func WithCustomTheme(th *gui.GuiTheme) CustomMessageOption {
	return func(cm *CustomMessageCard) { cm.theme = th }
}

// WithCustomOnLink sets a link handler for markdown inside the custom card.
func WithCustomOnLink(fn func(string)) CustomMessageOption {
	return func(cm *CustomMessageCard) { cm.onLink = fn }
}

// CustomMessageCard renders an extension CustomMessage with optional custom renderer.
type CustomMessageCard struct {
	mu             sync.Mutex
	message        *core.CustomMessage
	customRenderer CustomMessageRenderer
	expanded       bool
	theme          *gui.GuiTheme
	onLink         func(string)
	mdView         *gui.MarkdownView
	text           string
	images         []ai.ImageContent
	imgCache       map[string]msgDecodedImage
}

// NewCustomMessageCard creates a CustomMessageCard from msg.
func NewCustomMessageCard(msg *core.CustomMessage, opts ...CustomMessageOption) *CustomMessageCard {
	cm := &CustomMessageCard{
		message:  msg,
		imgCache: make(map[string]msgDecodedImage),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cm)
		}
	}
	if msg != nil {
		cm.text, cm.images = msgExtractTextAndImages(msg.Content)
	}
	cm.mdView = gui.NewMarkdownView(cm.text, gui.MarkdownConfig{
		Done:   true,
		Theme:  cm.theme,
		OnLink: cm.onLink,
	})
	return cm
}

// SetExpanded updates the expanded flag passed to custom/default renderers.
func (cm *CustomMessageCard) SetExpanded(expanded bool) {
	cm.mu.Lock()
	cm.expanded = expanded
	cm.mu.Unlock()
}

// IsExpanded reports the card's expanded state.
func (cm *CustomMessageCard) IsExpanded() bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.expanded
}

// SetTheme updates the card's GuiTheme.
func (cm *CustomMessageCard) SetTheme(th *gui.GuiTheme) {
	cm.mu.Lock()
	cm.theme = th
	if cm.mdView != nil {
		cm.mdView.SetTheme(th)
	}
	cm.mu.Unlock()
}

// View renders the CustomMessageCard element.
func (cm *CustomMessageCard) View(c *ui.Context) ui.Element {
	cm.mu.Lock()
	msg := cm.message
	renderer := cm.customRenderer
	expanded := cm.expanded
	th := gui.ResolveGuiTheme(c, cm.theme)
	text := cm.text
	images := append([]ai.ImageContent(nil), cm.images...)
	md := cm.mdView
	imgCache := cm.imgCache
	cm.mu.Unlock()

	if renderer != nil {
		if el := renderer(c, msg, expanded, th); el != (ui.Element{}) {
			return el
		}
	}

	label := "[custom]"
	if msg != nil && msg.CustomType != "" {
		label = "[" + msg.CustomType + "]"
	}

	card := ui.Column(c).
		FillWidth().
		Background(th.CustomMessageBg).
		Border(1, th.BorderMuted).
		Radius(th.Radius).
		Padding(10, 14).
		Gap(6)

	var toggleClicked bool
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, label).TextColor(th.CustomMessageLabel).FontWeight(700)
				if msg != nil {
					if ts := msgFormatTimestamp(msg.Timestamp); ts != "" {
						ui.Text(c, ts).TextColor(th.Dim).FontSize(11)
					}
				}
			})
			if strings.Count(text, "\n") > 12 {
				lbl := "Expand"
				if expanded {
					lbl = "Collapse"
				}
				if ui.Button(c, lbl).Cursor(ui.CursorPointer).Clicked() {
					toggleClicked = true
				}
			}
		})

		if text != "" && md != nil {
			md.View(c)
		}
		if len(images) > 0 {
			cm.mu.Lock()
			msgRenderImageThumbs(c, th, images, imgCache)
			cm.mu.Unlock()
		}
	})

	if toggleClicked {
		cm.mu.Lock()
		cm.expanded = !cm.expanded
		cm.mu.Unlock()
	}

	return card
}

// ============================================================================
// Header / Announcement / CostSummary / Queue & Status Banners
// ============================================================================

// HeaderAnnouncementOptions configures HeaderAnnouncement startup chrome.
type HeaderAnnouncementOptions struct {
	Version           string
	ModelLabel        string
	Cwd               string
	Extensions        []string
	Skills            []string
	Prompts           []string
	Themes            []string
	AnnouncementTitle string
	AnnouncementUrl   string
	Warnings          []string
	Theme             *gui.GuiTheme
	OnOpenUrl         func(url string)
	OnDismissWarnings func()
}

// HeaderAnnouncement renders the startup banner, loaded resource counts, quick key
// hints, optional announcement link, and dismissible startup/deprecation warnings.
type HeaderAnnouncement struct {
	mu                sync.Mutex
	opts              HeaderAnnouncementOptions
	warningsDismissed bool
	announceDismissed bool
}

// NewHeaderAnnouncement creates a HeaderAnnouncement component.
func NewHeaderAnnouncement(opts HeaderAnnouncementOptions) *HeaderAnnouncement {
	cp := opts
	cp.Extensions = append([]string(nil), opts.Extensions...)
	cp.Skills = append([]string(nil), opts.Skills...)
	cp.Prompts = append([]string(nil), opts.Prompts...)
	cp.Themes = append([]string(nil), opts.Themes...)
	cp.Warnings = append([]string(nil), opts.Warnings...)
	return &HeaderAnnouncement{opts: cp}
}

// SetWarnings updates the startup/deprecation warnings shown in the header card.
func (h *HeaderAnnouncement) SetWarnings(warnings []string) {
	h.mu.Lock()
	h.opts.Warnings = append([]string(nil), warnings...)
	h.warningsDismissed = false
	h.mu.Unlock()
}

// DismissWarnings hides active warning alerts.
func (h *HeaderAnnouncement) DismissWarnings() {
	h.mu.Lock()
	h.warningsDismissed = true
	cb := h.opts.OnDismissWarnings
	h.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// SetTheme overrides the GuiTheme used to render the header card.
func (h *HeaderAnnouncement) SetTheme(th *gui.GuiTheme) {
	h.mu.Lock()
	h.opts.Theme = th
	h.mu.Unlock()
}

// View builds the MyGo UI element tree for the header announcement card.
func (h *HeaderAnnouncement) View(c *ui.Context) ui.Element {
	h.mu.Lock()
	opts := h.opts
	warnDismissed := h.warningsDismissed
	annDismissed := h.announceDismissed
	th := gui.ResolveGuiTheme(c, opts.Theme)
	h.mu.Unlock()

	var dismissWarnClicked, dismissAnnClicked, openUrlClicked bool

	root := ui.Column(c).
		FillWidth().
		Background(th.Surface).
		Border(1, th.BorderMuted).
		Radius(th.Radius).
		Padding(14, 16).
		Gap(8)

	root.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "pi").TextColor(th.Accent).FontWeight(700).FontSize(18)
				if opts.Version != "" {
					ui.Badge(c, "v"+strings.TrimPrefix(opts.Version, "v"))
				}
				if opts.ModelLabel != "" {
					ui.Text(c, opts.ModelLabel).TextColor(th.Muted).FontSize(12)
				}
			})
			if opts.Cwd != "" {
				ui.Text(c, utils.ShortenHomePath(opts.Cwd)).TextColor(th.Dim).Font("monospace").FontSize(12)
			}
		})

		// Quick keybinding hint strip
		ui.Row(c).FillWidth().Wrap().Gap(10).Children(func() {
			hints := []string{
				"Enter send",
				"Shift+Enter newline",
				"/ commands",
				"@ files",
				"! bash",
				"Ctrl+P models",
				"Ctrl+T tree",
				"Ctrl+O expand",
			}
			for _, hint := range hints {
				ui.Text(c, hint).TextColor(th.Dim).FontSize(11)
			}
		})

		// Loaded resource summary pills
		if len(opts.Extensions) > 0 || len(opts.Skills) > 0 || len(opts.Prompts) > 0 || len(opts.Themes) > 0 {
			ui.Row(c).FillWidth().Wrap().Gap(6).Children(func() {
				if len(opts.Extensions) > 0 {
					ui.Badge(c, fmt.Sprintf("%d extensions", len(opts.Extensions)))
				}
				if len(opts.Skills) > 0 {
					ui.Badge(c, fmt.Sprintf("%d skills", len(opts.Skills)))
				}
				if len(opts.Prompts) > 0 {
					ui.Badge(c, fmt.Sprintf("%d prompts", len(opts.Prompts)))
				}
				if len(opts.Themes) > 0 {
					ui.Badge(c, fmt.Sprintf("%d themes", len(opts.Themes)))
				}
			})
		}

		// Announcement banner
		if !annDismissed && (opts.AnnouncementTitle != "" || opts.AnnouncementUrl != "") {
			ui.Row(c).
				FillWidth().
				Background(th.SurfaceElevated).
				Border(1, th.BorderAccent).
				Radius(th.Radius-2).
				Padding(8, 12).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Gap(8).
				Children(func() {
					ui.Column(c).Shrink(1).Grow(1).Gap(2).Children(func() {
						if opts.AnnouncementTitle != "" {
							ui.Text(c, opts.AnnouncementTitle).TextColor(th.Accent).FontWeight(600).FontSize(13)
						}
						if opts.AnnouncementUrl != "" {
							ui.Text(c, opts.AnnouncementUrl).TextColor(th.MdLink).FontSize(12)
						}
					})
					ui.Row(c).Shrink(0).Gap(6).Children(func() {
						if opts.AnnouncementUrl != "" {
							if ui.Button(c, "Open").Cursor(ui.CursorPointer).Clicked() {
								openUrlClicked = true
							}
						}
						if ui.Button(c, "Dismiss").Cursor(ui.CursorPointer).Clicked() {
							dismissAnnClicked = true
						}
					})
				})
		}

		// Warnings
		if !warnDismissed && len(opts.Warnings) > 0 {
			ui.Column(c).
				FillWidth().
				Background(th.SurfaceElevated).
				Border(1, th.Warning).
				Radius(th.Radius-2).
				Padding(8, 12).
				Gap(4).
				Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
						ui.Text(c, "Startup Notices").TextColor(th.Warning).FontWeight(600).FontSize(12)
						if ui.Button(c, "Dismiss Warnings").Cursor(ui.CursorPointer).Clicked() {
							dismissWarnClicked = true
						}
					})
					for _, w := range opts.Warnings {
						ui.Text(c, "• "+w).TextColor(th.Warning).FontSize(12).Wrap()
					}
				})
		}
	})

	if openUrlClicked && opts.AnnouncementUrl != "" {
		if opts.OnOpenUrl != nil {
			opts.OnOpenUrl(opts.AnnouncementUrl)
		} else {
			utils.OpenBrowser(opts.AnnouncementUrl)
		}
	}
	if dismissAnnClicked {
		h.mu.Lock()
		h.announceDismissed = true
		h.mu.Unlock()
	}
	if dismissWarnClicked {
		h.DismissWarnings()
	}

	return root
}

// CostSummaryCard renders aggregated session token usage and per-model cost breakdown.
type CostSummaryCard struct {
	mu        sync.Mutex
	totals    core.UsageTotals
	breakdown []core.UsageCostBreakdownEntry
	theme     *gui.GuiTheme
}

// NewCostSummaryCard creates a CostSummaryCard from UsageTotals and UsageCostBreakdownEntry slices.
func NewCostSummaryCard(
	totals core.UsageTotals,
	breakdown []core.UsageCostBreakdownEntry,
	opts ...SummaryCardOption,
) *CostSummaryCard {
	var cfg msgSummaryConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return &CostSummaryCard{
		totals:    totals,
		breakdown: append([]core.UsageCostBreakdownEntry(nil), breakdown...),
		theme:     cfg.theme,
	}
}

// Update replaces the displayed totals and model breakdown entries.
func (cs *CostSummaryCard) Update(totals core.UsageTotals, breakdown []core.UsageCostBreakdownEntry) {
	cs.mu.Lock()
	cs.totals = totals
	cs.breakdown = append([]core.UsageCostBreakdownEntry(nil), breakdown...)
	cs.mu.Unlock()
}

// SetTheme overrides the card's GuiTheme.
func (cs *CostSummaryCard) SetTheme(th *gui.GuiTheme) {
	cs.mu.Lock()
	cs.theme = th
	cs.mu.Unlock()
}

// View builds the CostSummaryCard UI tree.
func (cs *CostSummaryCard) View(c *ui.Context) ui.Element {
	cs.mu.Lock()
	totals := cs.totals
	breakdown := append([]core.UsageCostBreakdownEntry(nil), cs.breakdown...)
	th := gui.ResolveGuiTheme(c, cs.theme)
	cs.mu.Unlock()

	card := ui.Column(c).
		FillWidth().
		Background(th.Surface).
		Border(1, th.BorderMuted).
		Radius(th.Radius).
		Padding(12, 14).
		Gap(8)

	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, "Session Cost & Usage").TextColor(th.Text).FontWeight(700)
			ui.Badge(c, FormatCost(totals.Cost))
		})
		ui.Row(c).FillWidth().Wrap().Gap(10).Children(func() {
			ui.Textf(c, "Input %s", FormatTokens(totals.Input)).TextColor(th.Muted).FontSize(12)
			ui.Textf(c, "Output %s", FormatTokens(totals.Output)).TextColor(th.Muted).FontSize(12)
			ui.Textf(c, "Cache Read %s", FormatTokens(totals.CacheRead)).TextColor(th.Dim).FontSize(12)
			ui.Textf(c, "Cache Write %s", FormatTokens(totals.CacheWrite)).TextColor(th.Dim).FontSize(12)
		})
		for _, entry := range breakdown {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
				ui.Text(c, entry.Key).TextColor(th.Text).Font("monospace").FontSize(12)
				ui.Textf(c, "%s tokens · %s", FormatTokens(entry.Tokens), FormatCost(entry.Cost)).
					TextColor(th.Muted).FontSize(12)
			})
		}
	})
	return card
}

// QueueBanner displays queued steering and follow-up messages above the editor.
type QueueBanner struct {
	mu       sync.Mutex
	steering []string
	followUp []string
	theme    *gui.GuiTheme
	onClear  func()
}

// NewQueueBanner creates an empty QueueBanner.
func NewQueueBanner() *QueueBanner {
	return &QueueBanner{}
}

// SetQueues updates queued steering and follow-up message previews.
func (q *QueueBanner) SetQueues(steering, followUp []string) {
	q.mu.Lock()
	q.steering = append([]string(nil), steering...)
	q.followUp = append([]string(nil), followUp...)
	q.mu.Unlock()
}

// Counts returns the current (steeringCount, followUpCount).
func (q *QueueBanner) Counts() (int, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.steering), len(q.followUp)
}

// SetOnClear registers a callback invoked when the user clicks Clear Queue.
func (q *QueueBanner) SetOnClear(fn func()) {
	q.mu.Lock()
	q.onClear = fn
	q.mu.Unlock()
}

// SetTheme overrides the banner's GuiTheme.
func (q *QueueBanner) SetTheme(th *gui.GuiTheme) {
	q.mu.Lock()
	q.theme = th
	q.mu.Unlock()
}

// View renders the QueueBanner when non-empty, or a hidden placeholder when empty.
func (q *QueueBanner) View(c *ui.Context) ui.Element {
	q.mu.Lock()
	steering := append([]string(nil), q.steering...)
	followUp := append([]string(nil), q.followUp...)
	th := gui.ResolveGuiTheme(c, q.theme)
	onClear := q.onClear
	q.mu.Unlock()

	if len(steering) == 0 && len(followUp) == 0 {
		return ui.Box(c).Hidden(true)
	}

	var clearClicked bool
	box := ui.Column(c).
		FillWidth().
		Background(th.SurfaceElevated).
		Border(1, th.BorderAccent).
		Radius(th.Radius).
		Padding(8, 12).
		Gap(4)

	box.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				if len(steering) > 0 {
					ui.Badge(c, fmt.Sprintf("Steering (%d)", len(steering)))
				}
				if len(followUp) > 0 {
					ui.Badge(c, fmt.Sprintf("Follow-up (%d)", len(followUp)))
				}
			})
			if onClear != nil {
				if ui.Button(c, "Clear Queue").Cursor(ui.CursorPointer).Clicked() {
					clearClicked = true
				}
			}
		})
		for _, text := range steering {
			ui.Text(c, "→ [steer] "+msgSingleLinePreview(text, 96)).TextColor(th.Muted).FontSize(12)
		}
		for _, text := range followUp {
			ui.Text(c, "↳ [follow-up] "+msgSingleLinePreview(text, 96)).TextColor(th.Muted).FontSize(12)
		}
	})

	if clearClicked && onClear != nil {
		onClear()
	}
	return box
}

func msgSingleLinePreview(s string, maxRunes int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " "))
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}

// StatusBanner renders active working, compaction, auto-retry, or notice state.
type StatusBanner struct {
	mu           sync.Mutex
	working      bool
	workingMsg   string
	compacting   bool
	retrying     bool
	retryAttempt int
	retryMax     int
	retryDelay   time.Duration
	retryErr     string
	noticeText   string
	noticeError  bool
	theme        *gui.GuiTheme
	onAbort      func()
}

// NewStatusBanner creates a StatusBanner.
func NewStatusBanner() *StatusBanner {
	return &StatusBanner{}
}

// SetWorking toggles the active working spinner and message.
func (s *StatusBanner) SetWorking(active bool, message string) {
	s.mu.Lock()
	s.working = active
	s.workingMsg = message
	s.mu.Unlock()
}

// SetRetrying sets or updates retry progress state.
func (s *StatusBanner) SetRetrying(attempt, maxAttempts int, delay time.Duration, errMsg string) {
	s.mu.Lock()
	s.retrying = attempt > 0 || maxAttempts > 0
	s.retryAttempt = attempt
	s.retryMax = maxAttempts
	s.retryDelay = delay
	s.retryErr = errMsg
	s.mu.Unlock()
}

// SetCompacting toggles active compaction status.
func (s *StatusBanner) SetCompacting(active bool) {
	s.mu.Lock()
	s.compacting = active
	s.mu.Unlock()
}

// SetCustomNotice displays or clears (when empty) an informational or error status banner.
func (s *StatusBanner) SetCustomNotice(text string, isError bool) {
	s.mu.Lock()
	s.noticeText = text
	s.noticeError = isError
	s.mu.Unlock()
}

// SetOnAbort sets a callback fired when the user clicks Stop/Abort while working.
func (s *StatusBanner) SetOnAbort(fn func()) {
	s.mu.Lock()
	s.onAbort = fn
	s.mu.Unlock()
}

// SetTheme overrides the banner's GuiTheme.
func (s *StatusBanner) SetTheme(th *gui.GuiTheme) {
	s.mu.Lock()
	s.theme = th
	s.mu.Unlock()
}

// IsActive reports whether any status banner is currently visible.
func (s *StatusBanner) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.working || s.compacting || s.retrying || s.noticeText != ""
}

// View builds the StatusBanner UI element tree.
func (s *StatusBanner) View(c *ui.Context) ui.Element {
	s.mu.Lock()
	working := s.working
	workingMsg := s.workingMsg
	compacting := s.compacting
	retrying := s.retrying
	retryAttempt := s.retryAttempt
	retryMax := s.retryMax
	retryDelay := s.retryDelay
	retryErr := s.retryErr
	notice := s.noticeText
	noticeErr := s.noticeError
	th := gui.ResolveGuiTheme(c, s.theme)
	onAbort := s.onAbort
	s.mu.Unlock()

	if !working && !compacting && !retrying && notice == "" {
		return ui.Box(c).Hidden(true)
	}

	var abortClicked bool
	row := ui.Row(c).
		FillWidth().
		Background(th.SurfaceElevated).
		Border(1, th.BorderMuted).
		Radius(th.Radius).
		Padding(6, 12).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Gap(10)

	if noticeErr {
		row.Border(1, th.Error)
	} else if retrying {
		row.Border(1, th.Warning)
	} else if working || compacting {
		row.Border(1, th.BorderAccent)
	}

	row.Children(func() {
		ui.Row(c).Shrink(1).Grow(1).AlignItems(ui.Center).Gap(8).Children(func() {
			switch {
			case retrying:
				ui.Spinner(c).TextColor(th.Warning).Label("Retrying")
				msgText := fmt.Sprintf("Retrying (%d/%d in %s)", retryAttempt, retryMax, FormatDuration(retryDelay))
				if retryErr != "" {
					msgText += ": " + retryErr
				}
				ui.Text(c, msgText).TextColor(th.Warning).FontSize(12).Wrap()
			case compacting:
				ui.Spinner(c).TextColor(th.Accent).Label("Compacting")
				ui.Text(c, "Compacting context...").TextColor(th.Accent).FontSize(12)
			case working:
				lbl := workingMsg
				if lbl == "" {
					lbl = "Working..."
				}
				ui.Spinner(c).TextColor(th.Accent).Label(lbl)
				ui.Text(c, lbl).TextColor(th.Muted).FontSize(12)
			case notice != "":
				col := th.Muted
				if noticeErr {
					col = th.Error
				}
				ui.Text(c, notice).TextColor(col).FontSize(12).Wrap()
			}
		})

		if (working || compacting || retrying) && onAbort != nil {
			if ui.Button(c, "Stop").Cursor(ui.CursorPointer).Clicked() {
				abortClicked = true
			}
		}
	})

	if abortClicked && onAbort != nil {
		onAbort()
	}
	return row
}
