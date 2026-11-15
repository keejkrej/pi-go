// Ported from packages/coding-agent/src/modes/interactive/components/user-message-selector.ts (pi v1.5.2).

package components

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/tui"
)

const (
	usrmsgMaxVisible        = 10
	usrmsgEmptyAutoCancelMs = 100 * time.Millisecond
)

// UserMessageItem describes one selectable user message entry in session history.
type UserMessageItem struct {
	Id        string  // Entry ID in the session.
	Text      string  // Message text content.
	Timestamp *string // Optional timestamp if available.
}

// UserMessageList renders selectable historical user messages for session branching.
type UserMessageList struct {
	messages      []UserMessageItem
	selectedIndex int
	maxVisible    int
	OnSelect      func(entryId string)
	OnCancel      func()
}

var _ tui.Component = (*UserMessageList)(nil)

func usrmsgNewList(messages []UserMessageItem, initialSelectedId string) *UserMessageList {
	copied := make([]UserMessageItem, len(messages))
	copy(copied, messages)

	initialIndex := -1
	if initialSelectedId != "" {
		for i, msg := range copied {
			if msg.Id == initialSelectedId {
				initialIndex = i
				break
			}
		}
	}
	selected := max(0, len(copied)-1)
	if initialIndex >= 0 {
		selected = initialIndex
	}

	return &UserMessageList{
		messages:      copied,
		selectedIndex: selected,
		maxVisible:    usrmsgMaxVisible,
	}
}

// CapturesViewportInput reports that this list intercepts viewport scrolling keys.
func (l *UserMessageList) CapturesViewportInput() bool {
	return true
}

// Invalidate drops cached rendering state.
func (l *UserMessageList) Invalidate() {}

// Render renders visible user message entries with cursor and scroll indicator.
func (l *UserMessageList) Render(width int) []string {
	th := theme.CurrentTheme()
	if len(l.messages) == 0 {
		return []string{th.Fg(theme.FgMuted, "  No user messages found")}
	}

	startIndex := max(0, min(l.selectedIndex-l.maxVisible/2, len(l.messages)-l.maxVisible))
	endIndex := min(startIndex+l.maxVisible, len(l.messages))

	lines := make([]string, 0, (endIndex-startIndex)*3+1)
	maxMsgWidth := max(0, width-2)

	for i := startIndex; i < endIndex; i++ {
		message := l.messages[i]
		isSelected := i == l.selectedIndex

		normalized := js.Trim(strings.ReplaceAll(message.Text, "\n", " "))
		cursor := "  "
		if isSelected {
			cursor = th.Fg(theme.FgAccent, "› ")
		}
		truncated := tui.TruncateToWidth(normalized, maxMsgWidth, "...", false)
		styledMsg := truncated
		if isSelected {
			styledMsg = th.Bold(truncated)
		}
		lines = append(lines, cursor+styledMsg)

		metadata := fmt.Sprintf("  Message %d of %d", i+1, len(l.messages))
		lines = append(lines, th.Fg(theme.FgMuted, metadata), "")
	}

	if startIndex > 0 || endIndex < len(l.messages) {
		scrollInfo := fmt.Sprintf("  (%d/%d)", l.selectedIndex+1, len(l.messages))
		lines = append(lines, th.Fg(theme.FgMuted, scrollInfo))
	}

	return lines
}

// HandleInput processes selection navigation, page movement, confirm, and cancel keys.
func (l *UserMessageList) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	pageIndex := tui.GetSelectPageIndex(kb, keyData, l.selectedIndex, len(l.messages), l.maxVisible)

	switch {
	case kb.Matches(keyData, tui.KeybindingTuiSelectUp):
		if len(l.messages) == 0 {
			return
		}
		if l.selectedIndex == 0 {
			l.selectedIndex = len(l.messages) - 1
		} else {
			l.selectedIndex--
		}
	case kb.Matches(keyData, tui.KeybindingTuiSelectDown):
		if len(l.messages) == 0 {
			return
		}
		if l.selectedIndex == len(l.messages)-1 {
			l.selectedIndex = 0
		} else {
			l.selectedIndex++
		}
	case pageIndex != nil:
		if len(l.messages) == 0 {
			return
		}
		l.selectedIndex = *pageIndex
	case kb.Matches(keyData, tui.KeybindingTuiSelectConfirm):
		if l.selectedIndex >= 0 && l.selectedIndex < len(l.messages) && l.OnSelect != nil {
			l.OnSelect(l.messages[l.selectedIndex].Id)
		}
	case kb.Matches(keyData, tui.KeybindingTuiSelectCancel):
		if l.OnCancel != nil {
			l.OnCancel()
		}
	}
}

// UserMessageSelectorComponent renders the fork-from-message dialog.
type UserMessageSelectorComponent struct {
	*tui.Container
	messageList     *UserMessageList
	mu              sync.Mutex
	autoCancelTimer *time.Timer
	disposed        bool
}

// NewUserMessageSelectorComponent constructs a user message selector for branching.
func NewUserMessageSelectorComponent(
	messages []UserMessageItem,
	onSelect func(entryId string),
	onCancel func(),
	initialSelectedId ...string,
) *UserMessageSelectorComponent {
	var selectedId string
	if len(initialSelectedId) > 0 {
		selectedId = initialSelectedId[0]
	}

	th := theme.CurrentTheme()
	c := &UserMessageSelectorComponent{
		Container: tui.NewContainer(),
	}

	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(th.Bold("Fork from Message"), 1, 0, nil))
	c.AddChild(tui.NewText(th.Fg(theme.FgMuted, "Select a user message to copy the active path up to that point into a new session"), 1, 0, nil))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder())
	c.AddChild(tui.NewSpacer(1))

	c.messageList = usrmsgNewList(messages, selectedId)
	c.messageList.OnSelect = onSelect
	c.messageList.OnCancel = onCancel
	c.AddChild(c.messageList)

	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder())

	if len(messages) == 0 && onCancel != nil {
		c.autoCancelTimer = time.AfterFunc(usrmsgEmptyAutoCancelMs, func() {
			c.mu.Lock()
			disposed := c.disposed
			c.mu.Unlock()
			if !disposed {
				onCancel()
			}
		})
	}

	return c
}

// GetMessageList returns the inner selectable message list.
func (u *UserMessageSelectorComponent) GetMessageList() *UserMessageList {
	return u.messageList
}

// Dispose stops any pending auto-cancel timer when no messages are present.
func (u *UserMessageSelectorComponent) Dispose() {
	u.mu.Lock()
	u.disposed = true
	timer := u.autoCancelTimer
	u.autoCancelTimer = nil
	u.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}
