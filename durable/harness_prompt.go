// Ported from packages/durable/src/harness/prompt.ts (pi v1.0.0).

package durable

import (
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/omap"
)

// ReplaySections replays system messages in order to recover the currently shown prompt sections.
func ReplaySections(messages []ai.Message) *omap.Map[string, string] {
	shown := omap.NewMap[string, string]()
	for _, msg := range messages {
		sys, ok := msg.(*ai.SystemMessage)
		if !ok || sys == nil || sys.Sections == nil {
			continue
		}
		for k, v := range sys.Sections.All() {
			if v == nil {
				shown.Delete(k)
			} else {
				shown.Set(k, *v)
			}
		}
	}
	return shown
}

// RenderSections renders the agent's prompt sections in order. Sections whose Render fails keep
// their shown text (if any) and report the error, unless ctx is already aborted.
func RenderSections(
	sections []*PromptSection,
	input PromptInput,
	shown *omap.Map[string, string],
	report func(error),
	ctx chord.Context,
) (*omap.Map[string, string], error) {
	desired := omap.NewMap[string, string]()
	for _, sec := range sections {
		if sec == nil {
			continue
		}
		text, ok, err := sec.Render(input, ctx)
		if err != nil {
			if hutIsContextAborted(ctx) {
				return nil, err
			}
			if report != nil {
				report(err)
			}
			if kept, had := shown.Get(sec.Key); had {
				desired.Set(sec.Key, kept)
			}
			continue
		}
		if !ok {
			continue
		}
		tagged := sec.Tag == nil || *sec.Tag
		if tagged {
			desired.Set(sec.Key, fmt.Sprintf("<%s>\n%s\n</%s>", sec.Key, text, sec.Key))
		} else {
			desired.Set(sec.Key, text)
		}
	}
	return desired, nil
}

type hpToolChanges struct {
	Removed []ai.ToolReference
	Added   []*ai.Tool
}

// PlanSystemEntries plans the positional pi.system entry drafts that align view's replayed sections
// and offered tools with desired and tools.
func PlanSystemEntries(
	view *ContextView,
	desired *omap.Map[string, string],
	tools []*ToolRegistration,
	timestamp int64,
) []EntryDraft {
	desiredTools := make([]*ai.Tool, len(tools))
	for i, t := range tools {
		desiredTools[i] = t.AiTool()
	}

	if view != nil && view.Head != nil {
		hasLaterSystem := false
		for _, entry := range view.Entries {
			if SystemEntry.Is(&entry) && entry.Id > view.Head.Id {
				hasLaterSystem = true
				break
			}
		}
		if !hasLaterSystem {
			var edits []ContextEdit
			for _, entry := range view.Entries {
				if SystemEntry.Is(&entry) {
					edits = append(edits, ContextEdit{
						Target: entry.Id,
						Action: ContextEditActionOmit,
					})
				}
			}
			baselineMap := omap.NewMap[string, *string]()
			for k, v := range desired.All() {
				val := v
				baselineMap.Set(k, &val)
			}
			decls := make([]*ai.Tool, len(desiredTools))
			for i, t := range desiredTools {
				decls[i] = ai.ToToolDeclaration(t)
			}
			draft := hpSystemEntry(baselineMap, &hpToolChanges{Added: decls}, timestamp)
			if len(edits) > 0 {
				draft.Edits = edits
			}
			return []EntryDraft{draft}
		}
	}

	var viewMessages []ai.Message
	if view != nil {
		viewMessages = view.Messages
	}
	sections := hpPlanSections(ReplaySections(viewMessages), desired)
	changes := hpPlanTools(ai.GetCurrentTools(viewMessages), desiredTools)
	if len(changes.Removed) == 0 && len(changes.Added) == 0 {
		out := make([]EntryDraft, len(sections))
		for i, patch := range sections {
			out[i] = hpSystemEntry(patch, nil, timestamp)
		}
		return out
	}
	if len(sections) == 0 {
		return []EntryDraft{hpSystemEntry(nil, &changes, timestamp)}
	}
	out := make([]EntryDraft, len(sections))
	for i, patch := range sections {
		var toolPatch *hpToolChanges
		if i == len(sections)-1 {
			toolPatch = &changes
		}
		out[i] = hpSystemEntry(patch, toolPatch, timestamp)
	}
	return out
}

func hpPlanTools(offered []*ai.Tool, desired []*ai.Tool) hpToolChanges {
	wanted := make(map[string]*ai.Tool, len(desired))
	for _, t := range desired {
		wanted[t.Name] = t
	}
	var kept []*ai.Tool
	keptNames := make(map[string]struct{})
	for _, t := range offered {
		if next, ok := wanted[t.Name]; ok && ai.DeclarationsEqual(t, next) {
			kept = append(kept, t)
			keptNames[t.Name] = struct{}{}
		}
	}
	var added []*ai.Tool
	for _, t := range desired {
		if _, ok := keptNames[t.Name]; !ok {
			added = append(added, t)
		}
	}
	replayed := make([]*ai.Tool, 0, len(kept)+len(added))
	replayed = append(replayed, kept...)
	replayed = append(replayed, added...)

	orderMismatch := len(replayed) != len(desired)
	if !orderMismatch {
		for i, t := range replayed {
			if t.Name != desired[i].Name {
				orderMismatch = true
				break
			}
		}
	}
	if orderMismatch {
		removedRefs := make([]ai.ToolReference, len(offered))
		for i, t := range offered {
			removedRefs[i] = ai.ToolReference{Name: t.Name}
		}
		addedDecls := make([]*ai.Tool, len(desired))
		for i, t := range desired {
			addedDecls[i] = ai.ToToolDeclaration(t)
		}
		return hpToolChanges{Removed: removedRefs, Added: addedDecls}
	}

	var removedRefs []ai.ToolReference
	for _, t := range offered {
		if _, ok := keptNames[t.Name]; !ok {
			removedRefs = append(removedRefs, ai.ToolReference{Name: t.Name})
		}
	}
	addedDecls := make([]*ai.Tool, len(added))
	for i, t := range added {
		addedDecls[i] = ai.ToToolDeclaration(t)
	}
	return hpToolChanges{Removed: removedRefs, Added: addedDecls}
}

func hpPlanSections(shown *omap.Map[string, string], desired *omap.Map[string, string]) []*omap.Map[string, *string] {
	patchedOrder := make([]string, 0, desired.Len())
	for k := range shown.All() {
		if desired.Has(k) {
			patchedOrder = append(patchedOrder, k)
		}
	}
	for k := range desired.All() {
		if !shown.Has(k) {
			patchedOrder = append(patchedOrder, k)
		}
	}
	desiredOrder := desired.Keys()
	orderDiffers := len(patchedOrder) != len(desiredOrder)
	if !orderDiffers {
		for i, k := range patchedOrder {
			if k != desiredOrder[i] {
				orderDiffers = true
				break
			}
		}
	}
	if orderDiffers {
		removeAll := omap.NewMap[string, *string]()
		for k := range shown.All() {
			removeAll.Set(k, nil)
		}
		addAll := omap.NewMap[string, *string]()
		for k, v := range desired.All() {
			val := v
			addAll.Set(k, &val)
		}
		return []*omap.Map[string, *string]{removeAll, addAll}
	}

	patch := omap.NewMap[string, *string]()
	for k, prev := range shown.All() {
		next, exists := desired.Get(k)
		if !exists {
			patch.Set(k, nil)
		} else if next != prev {
			val := next
			patch.Set(k, &val)
		}
	}
	for k, v := range desired.All() {
		if !shown.Has(k) {
			val := v
			patch.Set(k, &val)
		}
	}
	if patch.Len() == 0 {
		return nil
	}
	return []*omap.Map[string, *string]{patch}
}

func hpSystemEntry(sections *omap.Map[string, *string], tools *hpToolChanges, timestamp int64) EntryDraft {
	empty := ""
	msg := &ai.SystemMessage{
		Role:      "system",
		Content:   ai.SystemMessageContent{Text: &empty},
		Timestamp: timestamp,
	}
	if sections != nil {
		msg.Sections = sections
	}
	if tools != nil {
		if len(tools.Removed) > 0 {
			msg.ToolsRemoved = tools.Removed
		}
		if len(tools.Added) > 0 {
			msg.ToolsAdded = tools.Added
		}
	}
	return EntryDraft{Model: []ai.Message{msg}}
}
