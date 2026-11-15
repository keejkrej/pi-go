package gui

import (
	"context"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

// SelectItem describes one selectable entry in a SelectList.
type SelectItem struct {
	Value       string
	Label       string
	Description string
	Badge       string
	Disabled    bool
}

// SelectList renders a searchable, keyboard- and pointer-navigable list of items.
type SelectList struct {
	mu               sync.Mutex
	items            []SelectItem
	filtered         []SelectItem
	filter           string
	lastFilter       string
	searchable       bool
	placeholder      string
	selected         int
	lastSelected     int
	maxHeight        float32
	confirmOnClick   bool
	autoFocus        bool
	theme            *GuiTheme
	listState        ui.ListState
	onSelect         func(SelectItem)
	onHighlight      func(SelectItem)
	onCancel         func()
	pendingSelection bool
}

// NewSelectList creates a SelectList initialized with items.
func NewSelectList(items []SelectItem) *SelectList {
	sl := &SelectList{
		maxHeight:      320,
		confirmOnClick: true,
		placeholder:    "Filter...",
		lastSelected:   -1,
	}
	sl.listState.Selected = &sl.selected
	sl.listState.Key = func(row int) any {
		if row >= 0 && row < len(sl.filtered) {
			return sl.filtered[row].Value
		}
		return row
	}
	sl.listState.Label = func(row int) string {
		if row >= 0 && row < len(sl.filtered) {
			return sl.filtered[row].Label
		}
		return ""
	}
	sl.SetItems(items)
	return sl
}

// SetItems replaces the underlying options and reapplies any active filter.
func (s *SelectList) SetItems(items []SelectItem) {
	s.mu.Lock()
	s.items = append([]SelectItem(nil), items...)
	s.applyFilterLocked()
	s.mu.Unlock()
}

// Items returns a copy of all unfiltered items.
func (s *SelectList) Items() []SelectItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SelectItem, len(s.items))
	copy(out, s.items)
	return out
}

// FilteredItems returns a copy of the currently matching items.
func (s *SelectList) FilteredItems() []SelectItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SelectItem, len(s.filtered))
	copy(out, s.filtered)
	return out
}

// SetSearchable toggles the filter input field above the list.
func (s *SelectList) SetSearchable(searchable bool) {
	s.mu.Lock()
	s.searchable = searchable
	s.mu.Unlock()
}

// SetPlaceholder sets the search box placeholder text.
func (s *SelectList) SetPlaceholder(placeholder string) {
	s.mu.Lock()
	s.placeholder = placeholder
	s.mu.Unlock()
}

// SetConfirmOnClick controls whether clicking a row immediately confirms (default true).
func (s *SelectList) SetConfirmOnClick(confirm bool) {
	s.mu.Lock()
	s.confirmOnClick = confirm
	s.mu.Unlock()
}

// SetAutoFocus controls whether the filter box or list requests focus when shown.
func (s *SelectList) SetAutoFocus(auto bool) {
	s.mu.Lock()
	s.autoFocus = auto
	s.mu.Unlock()
}

// SetMaxHeight sets the maximum DIP height of the list viewport.
func (s *SelectList) SetMaxHeight(h float32) {
	s.mu.Lock()
	s.maxHeight = h
	s.mu.Unlock()
}

// SetTheme sets an optional GuiTheme override.
func (s *SelectList) SetTheme(theme *GuiTheme) {
	s.mu.Lock()
	s.theme = theme
	s.mu.Unlock()
}

// SetOnSelect registers a handler called when the user confirms an item.
func (s *SelectList) SetOnSelect(fn func(SelectItem)) {
	s.mu.Lock()
	s.onSelect = fn
	s.mu.Unlock()
}

// SetOnHighlight registers a handler called when the highlighted item changes.
func (s *SelectList) SetOnHighlight(fn func(SelectItem)) {
	s.mu.Lock()
	s.onHighlight = fn
	s.mu.Unlock()
}

// SetOnCancel registers a handler called when Escape cancels selection.
func (s *SelectList) SetOnCancel(fn func()) {
	s.mu.Lock()
	s.onCancel = fn
	s.mu.Unlock()
}

// SetFilter filters items matching query (using FuzzyFilter on label/description/value).
func (s *SelectList) SetFilter(query string) {
	s.mu.Lock()
	s.filter = query
	s.lastFilter = query
	s.applyFilterLocked()
	s.mu.Unlock()
}

// Filter returns the active search query string.
func (s *SelectList) Filter() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filter
}

func (s *SelectList) applyFilterLocked() {
	q := strings.TrimSpace(s.filter)
	if q == "" {
		s.filtered = append([]SelectItem(nil), s.items...)
	} else {
		s.filtered = FuzzyFilter(s.items, q, func(it SelectItem) string {
			return it.Label + " " + it.Value + " " + it.Description
		})
	}
	if len(s.filtered) == 0 {
		s.selected = -1
		s.listState.Choose(-1)
		return
	}
	s.selected = 0
	for i, it := range s.filtered {
		if !it.Disabled {
			s.selected = i
			break
		}
	}
	s.listState.Choose(s.selected)
	s.listState.ScrollIntoView(s.selected)
}

// SelectedIndex returns the active filtered row index (-1 when none).
func (s *SelectList) SelectedIndex() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selected
}

// SetSelectedIndex moves the selection to idx (clamped into filtered items).
func (s *SelectList) SetSelectedIndex(idx int) {
	s.mu.Lock()
	if len(s.filtered) == 0 {
		s.selected = -1
		s.mu.Unlock()
		return
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s.filtered) {
		idx = len(s.filtered) - 1
	}
	s.selected = idx
	it := s.filtered[idx]
	onHL := s.onHighlight
	s.listState.Choose(idx)
	s.listState.ScrollIntoView(idx)
	s.mu.Unlock()

	if onHL != nil {
		onHL(it)
	}
}

// SelectedItem returns the currently highlighted item if valid.
func (s *SelectList) SelectedItem() (SelectItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selected < 0 || s.selected >= len(s.filtered) {
		return SelectItem{}, false
	}
	return s.filtered[s.selected], true
}

// MoveUp moves selection upward, wrapping to the last enabled item.
func (s *SelectList) MoveUp() {
	s.moveStep(-1)
}

// MoveDown moves selection downward, wrapping to the first enabled item.
func (s *SelectList) MoveDown() {
	s.moveStep(1)
}

func (s *SelectList) moveStep(delta int) {
	s.mu.Lock()
	n := len(s.filtered)
	if n == 0 {
		s.mu.Unlock()
		return
	}
	cur := s.selected
	if cur < 0 || cur >= n {
		cur = 0
	} else {
		cur = (cur + delta + n) % n
	}
	// Skip disabled items up to n steps.
	for step := 0; step < n && s.filtered[cur].Disabled; step++ {
		cur = (cur + delta + n) % n
	}
	s.selected = cur
	it := s.filtered[cur]
	onHL := s.onHighlight
	s.listState.Choose(cur)
	s.listState.ScrollIntoView(cur)
	s.mu.Unlock()

	if onHL != nil {
		onHL(it)
	}
}

// Confirm invokes OnSelect for the currently selected non-disabled item.
func (s *SelectList) Confirm() {
	s.mu.Lock()
	if s.selected < 0 || s.selected >= len(s.filtered) {
		s.mu.Unlock()
		return
	}
	it := s.filtered[s.selected]
	if it.Disabled {
		s.mu.Unlock()
		return
	}
	fn := s.onSelect
	s.mu.Unlock()
	if fn != nil {
		fn(it)
	}
}

// Cancel invokes OnCancel.
func (s *SelectList) Cancel() {
	s.mu.Lock()
	fn := s.onCancel
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// View builds the immediate-mode SelectList widget.
func (s *SelectList) View(c *ui.Context) ui.Element {
	s.mu.Lock()
	theme := ResolveGuiTheme(c, s.theme)
	searchable := s.searchable
	placeholder := s.placeholder
	maxH := s.maxHeight
	autoFocus := s.autoFocus
	if s.filter != s.lastFilter {
		s.lastFilter = s.filter
		s.applyFilterLocked()
	}
	items := append([]SelectItem(nil), s.filtered...)
	onHighlight := s.onHighlight
	onCancel := s.onCancel
	s.mu.Unlock()

	var submitPending bool
	var cancelPending bool

	root := ui.Column(c).FillWidth().Gap(8)
	root.Children(func() {
		if searchable {
			in := ui.TextInput(c, &s.filter).
				FillWidth().
				Placeholder(placeholder).
				Label(placeholder)
			if autoFocus {
				in.AutoFocus()
			}
			in.OnChange(func() {
				s.mu.Lock()
				s.lastFilter = s.filter
				s.applyFilterLocked()
				s.mu.Unlock()
			})
			in.OnTextKey(func(k ui.TextKey) bool {
				switch k.Key {
				case ui.KeyUp:
					if k.Mods == 0 {
						s.MoveUp()
						return true
					}
				case ui.KeyDown:
					if k.Mods == 0 {
						s.MoveDown()
						return true
					}
				case ui.KeyEnter:
					if k.Mods == 0 {
						s.Confirm()
						return true
					}
				case ui.KeyEscape:
					if k.Mods == 0 {
						s.Cancel()
						return true
					}
				}
				return false
			})
		}

		if len(items) == 0 {
			ui.Box(c).FillWidth().Padding(14, 12).Children(func() {
				ui.Text(c, "No matching items").TextColor(theme.Muted)
			})
			return
		}

		listBox := ui.Column(c).
			FillWidth().
			Background(theme.Surface).
			Border(1, theme.Border).
			Radius(theme.Radius).
			Padding(4)
		if maxH > 0 {
			listBox.MaxHeight(maxH)
		}

		listBox.Children(func() {
			s.listState.Key = func(row int) any {
				if row >= 0 && row < len(items) {
					return items[row].Value
				}
				return row
			}
			s.listState.Label = func(row int) string {
				if row >= 0 && row < len(items) {
					return items[row].Label
				}
				return ""
			}
			listEl := ui.List(c, &s.listState, len(items), func(i int) {
				it := items[i]
				isSel := i == s.selected
				row := ui.Row(c).
					FillWidth().
					Padding(7, 10).
					Radius(theme.Radius - 2).
					AlignItems(ui.Center).
					Justify(ui.SpaceBetween).
					Gap(10)
				if !it.Disabled {
					row.Cursor(ui.CursorPointer)
				} else {
					row.Opacity(0.5)
				}
				if isSel {
					row.Background(theme.SelectedBg)
				} else if !it.Disabled && row.Hovered() {
					row.Background(theme.SurfaceHover)
				}
				rowIdx := i
				rowItem := it
				row.OnClick(func() {
					if rowItem.Disabled {
						return
					}
					s.mu.Lock()
					prev := s.selected
					s.selected = rowIdx
					s.listState.Choose(rowIdx)
					s.listState.ScrollIntoView(rowIdx)
					hlCB := s.onHighlight
					selCB := s.onSelect
					conf := s.confirmOnClick
					s.mu.Unlock()
					if rowIdx != prev && hlCB != nil {
						hlCB(rowItem)
					}
					if conf && selCB != nil {
						selCB(rowItem)
					}
				})
				row.Children(func() {
					ui.Column(c).Shrink(1).Grow(1).Gap(2).Children(func() {
						fg := theme.Text
						if isSel {
							fg = theme.SelectedText
						}
						ui.Text(c, it.Label).TextColor(fg).FontWeight(500).SingleLine()
						if it.Description != "" {
							ui.Text(c, it.Description).TextColor(theme.Muted).FontSize(12).SingleLine()
						}
					})
					if it.Badge != "" {
						ui.Badge(c, it.Badge)
					}
				})
			}).FillWidth()
			if maxH > 0 {
				listEl.MaxHeight(maxH - 8)
			}
			if autoFocus && !searchable {
				listEl.AutoFocus()
			}
			if listEl.Changed() && onHighlight != nil && s.selected >= 0 && s.selected < len(items) {
				onHighlight(items[s.selected])
			}
			if listEl.Submitted() {
				submitPending = true
			}
			if !searchable && s.listState.Focused(c) {
				if c.Shortcut(0, ui.KeyEscape) {
					cancelPending = true
				}
			}
		})
	})

	if submitPending {
		s.Confirm()
	} else if cancelPending && onCancel != nil {
		onCancel()
	}

	return root
}

// SettingKind identifies how a SettingItem row renders and mutates values.
type SettingKind uint8

const (
	SettingToggle SettingKind = iota
	SettingEnum
	SettingAction
	SettingSubmenu
)

// SettingItem describes a configurable option row inside SettingsList.
type SettingItem struct {
	ID           string
	Label        string
	Description  string
	Kind         SettingKind
	CurrentValue string
	Values       []string
	ActionLabel  string
	OnAction     func()
}

// SettingsList renders interactive configuration rows (switches, enum cyclers,
// submenus, and action triggers) with optional live search filtering.
type SettingsList struct {
	mu            sync.Mutex
	items         []SettingItem
	filtered      []SettingItem
	search        string
	lastSearch    string
	enableSearch  bool
	selected      int
	maxHeight     float32
	theme         *GuiTheme
	onChange      func(id, newValue string)
	onOpenSubmenu func(item SettingItem)
	onCancel      func()
}

// NewSettingsList creates a SettingsList initialized with items.
func NewSettingsList(items []SettingItem) *SettingsList {
	sl := &SettingsList{
		maxHeight: 420,
	}
	sl.SetItems(items)
	return sl
}

// SetItems replaces all settings entries.
func (s *SettingsList) SetItems(items []SettingItem) {
	s.mu.Lock()
	s.items = make([]SettingItem, len(items))
	for i, it := range items {
		it.Values = append([]string(nil), it.Values...)
		s.items[i] = it
	}
	s.applySearchLocked()
	s.mu.Unlock()
}

// Items returns a copy of all settings items.
func (s *SettingsList) Items() []SettingItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SettingItem, len(s.items))
	copy(out, s.items)
	return out
}

// FilteredItems returns a copy of filtered items.
func (s *SettingsList) FilteredItems() []SettingItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SettingItem, len(s.filtered))
	copy(out, s.filtered)
	return out
}

// SetEnableSearch shows or hides the search input above the settings rows.
func (s *SettingsList) SetEnableSearch(enable bool) {
	s.mu.Lock()
	s.enableSearch = enable
	s.mu.Unlock()
}

// SetSearch filters settings rows matching query.
func (s *SettingsList) SetSearch(query string) {
	s.mu.Lock()
	s.search = query
	s.lastSearch = query
	s.applySearchLocked()
	s.mu.Unlock()
}

// UpdateValue updates the stored CurrentValue for item id without firing OnChange.
func (s *SettingsList) UpdateValue(id, value string) {
	s.mu.Lock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].CurrentValue = value
			break
		}
	}
	s.applySearchLocked()
	s.mu.Unlock()
}

// SetOnChange registers a handler notified whenever a toggle/enum value changes.
func (s *SettingsList) SetOnChange(fn func(id, newValue string)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// SetOnOpenSubmenu registers a handler notified when a SettingSubmenu item activates.
func (s *SettingsList) SetOnOpenSubmenu(fn func(SettingItem)) {
	s.mu.Lock()
	s.onOpenSubmenu = fn
	s.mu.Unlock()
}

// SetOnCancel registers a handler notified when Escape cancels the settings panel.
func (s *SettingsList) SetOnCancel(fn func()) {
	s.mu.Lock()
	s.onCancel = fn
	s.mu.Unlock()
}

// SetTheme sets an optional GuiTheme override.
func (s *SettingsList) SetTheme(theme *GuiTheme) {
	s.mu.Lock()
	s.theme = theme
	s.mu.Unlock()
}

func (s *SettingsList) applySearchLocked() {
	q := strings.TrimSpace(s.search)
	if q == "" {
		s.filtered = append([]SettingItem(nil), s.items...)
	} else {
		s.filtered = FuzzyFilter(s.items, q, func(it SettingItem) string {
			return it.Label + " " + it.ID + " " + it.Description
		})
	}
	if len(s.filtered) == 0 {
		s.selected = -1
	} else if s.selected < 0 || s.selected >= len(s.filtered) {
		s.selected = 0
	}
}

func nextEnumValue(item SettingItem, delta int) string {
	if item.Kind == SettingToggle {
		if strings.EqualFold(item.CurrentValue, "true") || strings.EqualFold(item.CurrentValue, "on") {
			return "false"
		}
		return "true"
	}
	if len(item.Values) == 0 {
		return item.CurrentValue
	}
	idx := 0
	found := false
	for i, v := range item.Values {
		if v == item.CurrentValue {
			idx = i
			found = true
			break
		}
	}
	if !found {
		return item.Values[0]
	}
	next := (idx + delta + len(item.Values)) % len(item.Values)
	return item.Values[next]
}

// CycleItem steps setting row idx by delta (+1 or -1) and returns the new value.
func (s *SettingsList) CycleItem(id string, delta int) string {
	s.mu.Lock()
	var targetIdx = -1
	for i := range s.items {
		if s.items[i].ID == id {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		s.mu.Unlock()
		return ""
	}
	nextVal := nextEnumValue(s.items[targetIdx], delta)
	changed := nextVal != s.items[targetIdx].CurrentValue
	s.items[targetIdx].CurrentValue = nextVal
	s.applySearchLocked()
	cb := s.onChange
	s.mu.Unlock()

	if changed && cb != nil {
		cb(id, nextVal)
	}
	return nextVal
}

// View builds the immediate-mode SettingsList widget.
func (s *SettingsList) View(c *ui.Context) ui.Element {
	s.mu.Lock()
	theme := ResolveGuiTheme(c, s.theme)
	enableSearch := s.enableSearch
	if s.search != s.lastSearch {
		s.lastSearch = s.search
		s.applySearchLocked()
	}
	rows := append([]SettingItem(nil), s.filtered...)
	sel := s.selected
	onChange := s.onChange
	onSubmenu := s.onOpenSubmenu
	onCancel := s.onCancel
	maxH := s.maxHeight
	s.mu.Unlock()

	type pendingEvent struct {
		changedID  string
		newValue   string
		actionFn   func()
		submenuIt  *SettingItem
		cancelFlag bool
		selectIdx  int
	}
	ev := pendingEvent{selectIdx: -1}

	root := ui.Column(c).FillWidth().Gap(8)
	root.Children(func() {
		if enableSearch {
			in := ui.TextInput(c, &s.search).
				FillWidth().
				Placeholder("Search settings...").
				Label("Search settings")
			in.OnChange(func() {
				s.mu.Lock()
				s.lastSearch = s.search
				s.applySearchLocked()
				s.mu.Unlock()
			})
			in.OnTextKey(func(k ui.TextKey) bool {
				if k.Key == ui.KeyEscape && k.Mods == 0 {
					s.mu.Lock()
					cb := s.onCancel
					s.mu.Unlock()
					if cb != nil {
						cb()
					}
					return true
				}
				return false
			})
		}

		panel := ui.Scroll(c).
			FillWidth().
			Background(theme.Surface).
			Border(1, theme.Border).
			Radius(theme.Radius).
			Padding(6)
		if maxH > 0 {
			panel.MaxHeight(maxH)
		}

		panel.Children(func() {
			col := ui.Column(c).FillWidth().Gap(4)
			col.Children(func() {
				if len(rows) == 0 {
					ui.Box(c).FillWidth().Padding(12).Children(func() {
						ui.Text(c, "No matching settings").TextColor(theme.Muted)
					})
					return
				}

				for i, item := range rows {
					rowIdx := i
					it := item
					isSel := rowIdx == sel

					row := ui.Row(c).
						Key(it.ID).
						FillWidth().
						Padding(8, 12).
						Radius(theme.Radius - 2).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween).
						Gap(12)
					if isSel {
						row.Background(theme.SelectedBg.Alpha(0.45))
					} else if row.Hovered() {
						row.Background(theme.SurfaceHover)
					}

					row.Children(func() {
						ui.Column(c).Shrink(1).Grow(1).Gap(2).Children(func() {
							ui.Text(c, it.Label).TextColor(theme.Text).FontWeight(500)
							if it.Description != "" {
								ui.Text(c, it.Description).TextColor(theme.Muted).FontSize(12)
							}
						})

						switch it.Kind {
						case SettingToggle:
							on := strings.EqualFold(it.CurrentValue, "true") || strings.EqualFold(it.CurrentValue, "on")
							sw := ui.Switch(c, &on).Label("Toggle " + it.Label)
							if sw.Changed() {
								next := "false"
								if on {
									next = "true"
								}
								ev.changedID = it.ID
								ev.newValue = next
								ev.selectIdx = rowIdx
							}
						case SettingEnum:
							valText := it.CurrentValue
							if valText == "" && len(it.Values) > 0 {
								valText = it.Values[0]
							}
							btn := ui.Button(c, valText).Label(it.Label + ": " + valText).Cursor(ui.CursorPointer)
							if btn.Clicked() {
								ev.changedID = it.ID
								ev.newValue = nextEnumValue(it, 1)
								ev.selectIdx = rowIdx
							}
						case SettingAction:
							lbl := it.ActionLabel
							if lbl == "" {
								lbl = "Run"
							}
							btn := ui.Button(c, lbl).Label(lbl + ": " + it.Label).Cursor(ui.CursorPointer)
							if btn.Clicked() {
								ev.actionFn = it.OnAction
								ev.selectIdx = rowIdx
							}
						case SettingSubmenu:
							lbl := it.CurrentValue
							if lbl == "" {
								lbl = "Configure ›"
							} else {
								lbl += " ›"
							}
							btn := ui.Button(c, lbl).Label("Open " + it.Label).Cursor(ui.CursorPointer)
							if btn.Clicked() {
								cp := it
								ev.submenuIt = &cp
								ev.selectIdx = rowIdx
							}
						}
					})
				}
			})
		})
	})

	if ev.selectIdx >= 0 {
		s.mu.Lock()
		s.selected = ev.selectIdx
		s.mu.Unlock()
	}
	if ev.changedID != "" {
		s.UpdateValue(ev.changedID, ev.newValue)
		if onChange != nil {
			onChange(ev.changedID, ev.newValue)
		}
	}
	if ev.actionFn != nil {
		ev.actionFn()
	}
	if ev.submenuIt != nil && onSubmenu != nil {
		onSubmenu(*ev.submenuIt)
	}
	if ev.cancelFlag && onCancel != nil {
		onCancel()
	}
	return root
}

// SearchBar wraps ui.FindBar with query, match count, current match tracking,
// and state transition callbacks.
type SearchBar struct {
	mu            sync.Mutex
	open          bool
	query         string
	matches       int
	current       int
	lastOpen      bool
	lastQuery     string
	lastCurrent   int
	onQueryChange func(query string)
	onMatchChange func(index int)
	onClose       func()
}

// NewSearchBar creates a closed SearchBar ready for FindBar integration.
func NewSearchBar() *SearchBar {
	return &SearchBar{}
}

// Open opens the search bar.
func (sb *SearchBar) Open() {
	sb.mu.Lock()
	sb.open = true
	sb.mu.Unlock()
}

// Close closes the search bar and fires OnClose if it was open.
func (sb *SearchBar) Close() {
	sb.mu.Lock()
	wasOpen := sb.open
	sb.open = false
	sb.lastOpen = false
	cb := sb.onClose
	sb.mu.Unlock()
	if wasOpen && cb != nil {
		cb()
	}
}

// Toggle opens or closes the search bar.
func (sb *SearchBar) Toggle() {
	sb.mu.Lock()
	open := !sb.open
	sb.open = open
	cb := sb.onClose
	if !open {
		sb.lastOpen = false
	}
	sb.mu.Unlock()
	if !open && cb != nil {
		cb()
	}
}

// IsOpen reports whether the search bar is currently visible.
func (sb *SearchBar) IsOpen() bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.open
}

// Query returns the current search query string.
func (sb *SearchBar) Query() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.query
}

// SetQuery sets the search query programmatically and fires OnQueryChange if changed.
func (sb *SearchBar) SetQuery(q string) {
	sb.mu.Lock()
	changed := sb.query != q
	sb.query = q
	sb.lastQuery = q
	cb := sb.onQueryChange
	sb.mu.Unlock()
	if changed && cb != nil {
		cb(q)
	}
}

// SetMatches updates the total number of search matches.
func (sb *SearchBar) SetMatches(count int) {
	if count < 0 {
		count = 0
	}
	sb.mu.Lock()
	sb.matches = count
	if count == 0 {
		sb.current = 0
	} else if sb.current >= count {
		sb.current = count - 1
	}
	sb.mu.Unlock()
}

// Matches returns the total match count.
func (sb *SearchBar) Matches() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.matches
}

// SetCurrent sets the active match index [0, Matches()).
func (sb *SearchBar) SetCurrent(index int) {
	sb.mu.Lock()
	if sb.matches > 0 {
		index = ((index % sb.matches) + sb.matches) % sb.matches
	} else {
		index = 0
	}
	changed := sb.current != index
	sb.current = index
	sb.lastCurrent = index
	cb := sb.onMatchChange
	sb.mu.Unlock()
	if changed && cb != nil {
		cb(index)
	}
}

// Current returns the active match index.
func (sb *SearchBar) Current() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.current
}

// Next advances to the next match wrapping around.
func (sb *SearchBar) Next() {
	sb.mu.Lock()
	m := sb.matches
	cur := sb.current
	sb.mu.Unlock()
	if m > 0 {
		sb.SetCurrent((cur + 1) % m)
	}
}

// Prev moves to the previous match wrapping around.
func (sb *SearchBar) Prev() {
	sb.mu.Lock()
	m := sb.matches
	cur := sb.current
	sb.mu.Unlock()
	if m > 0 {
		sb.SetCurrent((cur - 1 + m) % m)
	}
}

// SetOnQueryChange sets the query change callback.
func (sb *SearchBar) SetOnQueryChange(fn func(string)) {
	sb.mu.Lock()
	sb.onQueryChange = fn
	sb.mu.Unlock()
}

// SetOnMatchChange sets the active match change callback.
func (sb *SearchBar) SetOnMatchChange(fn func(int)) {
	sb.mu.Lock()
	sb.onMatchChange = fn
	sb.mu.Unlock()
}

// SetOnClose sets the bar close callback.
func (sb *SearchBar) SetOnClose(fn func()) {
	sb.mu.Lock()
	sb.onClose = fn
	sb.mu.Unlock()
}

// View builds ui.FindBar and dispatches any query/match/close transitions.
func (sb *SearchBar) View(c *ui.Context) ui.Element {
	sb.mu.Lock()
	open := sb.open
	query := sb.query
	matches := sb.matches
	current := sb.current
	onQuery := sb.onQueryChange
	onMatch := sb.onMatchChange
	onClose := sb.onClose
	prevOpen := sb.lastOpen
	prevQuery := sb.lastQuery
	prevCurrent := sb.lastCurrent
	sb.mu.Unlock()

	bar := ui.FindBar(c, &open, &query, matches, &current)

	sb.mu.Lock()
	sb.open = open
	sb.query = query
	sb.current = current
	sb.lastOpen = open
	sb.lastQuery = query
	sb.lastCurrent = current
	sb.mu.Unlock()

	if query != prevQuery && onQuery != nil {
		onQuery(query)
	}
	if (current != prevCurrent || bar.Changed()) && onMatch != nil && matches > 0 {
		onMatch(current)
	}
	if prevOpen && !open && onClose != nil {
		onClose()
	}
	return bar
}

// Loader displays an animated ui.Spinner alongside a status message.
type Loader struct {
	mu      sync.Mutex
	message string
	running bool
	theme   *GuiTheme
}

// NewLoader creates a running Loader displaying message.
func NewLoader(message string) *Loader {
	return &Loader{
		message: message,
		running: true,
	}
}

// SetMessage updates the status label next to the spinner.
func (l *Loader) SetMessage(msg string) {
	l.mu.Lock()
	l.message = msg
	l.mu.Unlock()
}

// Message returns the current status message.
func (l *Loader) Message() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.message
}

// Start marks the loader active.
func (l *Loader) Start() {
	l.mu.Lock()
	l.running = true
	l.mu.Unlock()
}

// Stop stops showing the spinner.
func (l *Loader) Stop() {
	l.mu.Lock()
	l.running = false
	l.mu.Unlock()
}

// Running reports whether the loader is currently running.
func (l *Loader) Running() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

// SetTheme sets an optional GuiTheme override.
func (l *Loader) SetTheme(theme *GuiTheme) {
	l.mu.Lock()
	l.theme = theme
	l.mu.Unlock()
}

// View builds the immediate-mode Loader row.
func (l *Loader) View(c *ui.Context) ui.Element {
	l.mu.Lock()
	msg := l.message
	running := l.running
	theme := ResolveGuiTheme(c, l.theme)
	l.mu.Unlock()

	if !running {
		return ui.Box(c).Hidden(true)
	}
	row := ui.Row(c).Gap(8).AlignItems(ui.Center)
	row.Children(func() {
		ui.Spinner(c).TextColor(theme.Accent).Label(msg)
		if msg != "" {
			ui.Text(c, msg).TextColor(theme.Muted)
		}
	})
	return row
}

// CancellableLoader extends Loader with Escape key and Cancel button cancellation
// backed by a context.Context.
type CancellableLoader struct {
	*Loader
	ctxMu      sync.Mutex
	ctx        context.Context
	cancelFn   context.CancelFunc
	cancelled  bool
	showCancel bool
	onCancel   func()
}

// NewCancellableLoader creates a running CancellableLoader with an associated
// cancellable context derived from context.Background().
func NewCancellableLoader(message string) *CancellableLoader {
	ctx, cancel := context.WithCancel(context.Background())
	return &CancellableLoader{
		Loader:     NewLoader(message),
		ctx:        ctx,
		cancelFn:   cancel,
		showCancel: true,
	}
}

// Context returns the loader's cancellation context.
func (cl *CancellableLoader) Context() context.Context {
	cl.ctxMu.Lock()
	defer cl.ctxMu.Unlock()
	return cl.ctx
}

// Cancelled reports whether Cancel has been triggered.
func (cl *CancellableLoader) Cancelled() bool {
	cl.ctxMu.Lock()
	defer cl.ctxMu.Unlock()
	return cl.cancelled
}

// SetOnCancel sets the callback executed once on cancellation.
func (cl *CancellableLoader) SetOnCancel(fn func()) {
	cl.ctxMu.Lock()
	cl.onCancel = fn
	cl.ctxMu.Unlock()
}

// SetShowCancelButton toggles showing an inline Cancel button beside the spinner.
func (cl *CancellableLoader) SetShowCancelButton(show bool) {
	cl.ctxMu.Lock()
	cl.showCancel = show
	cl.ctxMu.Unlock()
}

// Cancel marks the loader cancelled, cancels its context, stops the spinner,
// and triggers OnCancel once.
func (cl *CancellableLoader) Cancel() {
	cl.ctxMu.Lock()
	if cl.cancelled {
		cl.ctxMu.Unlock()
		return
	}
	cl.cancelled = true
	cancel := cl.cancelFn
	cb := cl.onCancel
	cl.ctxMu.Unlock()

	cl.Stop()
	if cancel != nil {
		cancel()
	}
	if cb != nil {
		cb()
	}
}

// Dispose cleans up the underlying context without invoking OnCancel.
func (cl *CancellableLoader) Dispose() {
	cl.ctxMu.Lock()
	cancel := cl.cancelFn
	cl.ctxMu.Unlock()
	cl.Stop()
	if cancel != nil {
		cancel()
	}
}

// View builds the CancellableLoader row with Escape shortcut and Cancel button.
func (cl *CancellableLoader) View(c *ui.Context) ui.Element {
	cl.Loader.mu.Lock()
	msg := cl.Loader.message
	running := cl.Loader.running
	theme := ResolveGuiTheme(c, cl.Loader.theme)
	cl.Loader.mu.Unlock()

	cl.ctxMu.Lock()
	showCancel := cl.showCancel
	cl.ctxMu.Unlock()

	if !running {
		return ui.Box(c).Hidden(true)
	}

	shouldCancel := false
	row := ui.Row(c).Gap(10).AlignItems(ui.Center)
	row.Children(func() {
		ui.Spinner(c).TextColor(theme.Accent).Label(msg)
		if msg != "" {
			ui.Text(c, msg).TextColor(theme.Muted)
		}
		if showCancel {
			btn := ui.Button(c, "Cancel").Cursor(ui.CursorPointer)
			if btn.Clicked() {
				shouldCancel = true
			}
		}
	})
	if c.Shortcut(0, ui.KeyEscape) {
		shouldCancel = true
	}
	if shouldCancel {
		cl.Cancel()
	}
	return row
}

// CardVariant classifies the semantic appearance of a CollapsibleCard.
type CardVariant uint8

const (
	CardNeutral CardVariant = iota
	CardThinking
	CardToolPending
	CardToolSuccess
	CardToolError
	CardBash
	CardBashExcluded
)

// CollapsibleCard wraps ui.CollapsibleBase with Pi semantic styling for
// thinking blocks, tool executions, and bash command panels.
type CollapsibleCard struct {
	mu       sync.Mutex
	Title    string
	Subtitle string
	Badge    string
	Variant  CardVariant
	Open     bool
	Theme    *GuiTheme
	OnToggle func(open bool)
}

// NewCollapsibleCard creates a CollapsibleCard with title, variant, and initial open state.
func NewCollapsibleCard(title string, variant CardVariant, open bool) *CollapsibleCard {
	return &CollapsibleCard{
		Title:   title,
		Variant: variant,
		Open:    open,
	}
}

// SetOpen updates the expanded state.
func (cc *CollapsibleCard) SetOpen(open bool) {
	cc.mu.Lock()
	cc.Open = open
	cc.mu.Unlock()
}

// IsOpen reports whether the card is currently expanded.
func (cc *CollapsibleCard) IsOpen() bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.Open
}

// View builds the immediate-mode CollapsibleCard element wrapping content.
func (cc *CollapsibleCard) View(c *ui.Context, content func(c *ui.Context)) ui.Element {
	cc.mu.Lock()
	title := cc.Title
	subtitle := cc.Subtitle
	badge := cc.Badge
	variant := cc.Variant
	open := cc.Open
	theme := ResolveGuiTheme(c, cc.Theme)
	onToggle := cc.OnToggle
	cc.mu.Unlock()

	bg := theme.Surface
	borderColor := theme.BorderMuted
	titleColor := theme.ToolTitle
	switch variant {
	case CardThinking:
		bg = theme.Surface
		borderColor = theme.BorderMuted
		titleColor = theme.ThinkingText
	case CardToolPending:
		bg = theme.ToolPendingBg
		borderColor = theme.BorderAccent
	case CardToolSuccess:
		bg = theme.ToolSuccessBg
		borderColor = theme.Success.Alpha(0.45)
	case CardToolError:
		bg = theme.ToolErrorBg
		borderColor = theme.Error.Alpha(0.55)
	case CardBash:
		bg = theme.Surface
		borderColor = theme.BashMode
	case CardBashExcluded:
		bg = theme.Surface
		borderColor = theme.Dim
	}

	card := ui.Column(c).
		FillWidth().
		Background(bg).
		Border(1, borderColor).
		Radius(theme.Radius).
		Padding(8, 12).
		Gap(6)

	var toggled bool
	card.Children(func() {
		cp := ui.CollapsibleBase(c, &open)
		cp.Trigger.FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Cursor(ui.CursorPointer)
		cp.Trigger.Children(func() {
			ui.Row(c).Shrink(1).Grow(1).AlignItems(ui.Center).Gap(8).Children(func() {
				chevron := "▸"
				if open || cp.Progress() > 0.5 {
					chevron = "▾"
				}
				ui.Text(c, chevron).TextColor(theme.Muted)
				ui.Text(c, title).TextColor(titleColor).FontWeight(600).SingleLine()
				if subtitle != "" {
					ui.Text(c, subtitle).TextColor(theme.Muted).FontSize(12).SingleLine()
				}
			})
			if badge != "" {
				ui.Badge(c, badge)
			}
		})
		toggled = cp.Trigger.Changed()

		cp.Panel(func() {
			if content != nil {
				ui.Column(c).FillWidth().Padding(4, 0, 2, 16).Children(func() {
					content(c)
				})
			}
		})
	})

	cc.mu.Lock()
	cc.Open = open
	cc.mu.Unlock()
	if toggled && onToggle != nil {
		onToggle(open)
	}
	return card
}
