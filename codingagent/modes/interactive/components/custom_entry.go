// Ported from packages/coding-agent/src/modes/interactive/components/custom-entry.ts (pi v1.5.2).

package components

import (
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// CustomEntryComponent renders a custom session entry when a renderer produces content for it.
type CustomEntryComponent struct {
	*tui.Container
	entry      *core.CustomEntry
	renderer   core.EntryRenderer
	outputPad  int
	expanded   bool
	hasContent bool
}

// NewCustomEntryComponent creates a CustomEntryComponent for entry using renderer (nil-safe).
func NewCustomEntryComponent(entry *core.CustomEntry, renderer core.EntryRenderer, outputPad ...int) *CustomEntryComponent {
	pad := 1
	if len(outputPad) > 0 {
		pad = max(0, outputPad[0])
	}
	c := &CustomEntryComponent{
		Container: tui.NewContainer(),
		entry:     entry,
		renderer:  renderer,
		outputPad: pad,
	}
	c.rebuild()
	return c
}

// HasContent reports whether the entry renderer produced a non-nil component.
func (c *CustomEntryComponent) HasContent() bool {
	return c.hasContent
}

// SetExpanded sets the expanded display flag and rebuilds content.
func (c *CustomEntryComponent) SetExpanded(expanded bool) {
	if c.expanded == expanded {
		return
	}
	c.expanded = expanded
	c.rebuild()
}

// SetOutputPad updates horizontal padding and rebuilds content.
func (c *CustomEntryComponent) SetOutputPad(padding int) {
	c.outputPad = max(0, padding)
	c.rebuild()
}

// Invalidate drops cached rendering state and rebuilds the component.
func (c *CustomEntryComponent) Invalidate() {
	c.Container.Invalidate()
	c.rebuild()
}

func (c *CustomEntryComponent) rebuild() {
	c.Clear()
	c.hasContent = false
	if c.renderer == nil || c.entry == nil {
		return
	}
	rendered := cenSafeRender(c.renderer, c.entry, core.EntryRenderOptions{
		KeybindingHints: core.DefaultKeybindingHints(),
		Expanded:        c.expanded,
	}, theme.CurrentTheme())
	if rendered == nil {
		return
	}
	c.hasContent = true
	if c.outputPad > 0 {
		box := tui.NewBox(c.outputPad, 0, nil)
		box.AddChild(rendered)
		c.AddChild(box)
		return
	}
	c.AddChild(rendered)
}

func cenSafeRender(
	renderer core.EntryRenderer,
	entry *core.CustomEntry,
	opts core.EntryRenderOptions,
	th *theme.Theme,
) (comp tui.Component) {
	defer func() {
		if recover() != nil {
			comp = nil
		}
	}()
	return renderer(entry, opts, th)
}
