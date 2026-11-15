// Ported from packages/durable/test/harness-prompt.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type thprDummyReader struct{}

func (thprDummyReader) Snapshot(_ chord.Context, _ durable.AnyDocToken, _ ...any) (durable.JsonObject, error) {
	return nil, nil
}

func (thprDummyReader) SnapshotAsOf(_ chord.Context, _ durable.AnyDocToken, _ durable.ConversationId, _ ...any) (durable.JsonObject, error) {
	return nil, nil
}

type thprPlanned struct {
	sections map[string]*string
	keys     []string
	omit     []durable.EntryId
}

func thprOrderedPairs(m *omap.Map[string, string]) [][2]string {
	out := make([][2]string, 0, m.Len())
	for k, v := range m.All() {
		out = append(out, [2]string{k, v})
	}
	return out
}

func thprMapFromPairs(pairs [][2]string) *omap.Map[string, string] {
	m := omap.NewMap[string, string]()
	for _, p := range pairs {
		m.Set(p[0], p[1])
	}
	return m
}

func thprApply(t *testing.T, conv durable.Conversation, desiredPairs [][2]string) []thprPlanned {
	t.Helper()
	ctx := testkit.TestContext
	view, err := conv.Context(ctx)
	if err != nil {
		t.Fatalf("Context failed: %v", err)
	}
	desired := thprMapFromPairs(desiredPairs)
	drafts := durable.PlanSystemEntries(view, desired, nil, 7)
	if _, err := conv.Commit(ctx, func(tx durable.Tx) (any, error) {
		for _, draft := range drafts {
			if _, err := tx.AppendTypedEntry(durable.SystemEntry, conv.Id(), draft); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("commit drafts failed: %v", err)
	}
	after, err := conv.Context(ctx)
	if err != nil {
		t.Fatalf("after Context failed: %v", err)
	}
	replayed := thprOrderedPairs(durable.ReplaySections(after.Messages))
	if !reflect.DeepEqual(replayed, desiredPairs) {
		t.Fatalf("replayed = %v, want %v", replayed, desiredPairs)
	}
	out := make([]thprPlanned, len(drafts))
	for i, draft := range drafts {
		msg := draft.Model[0].(*ai.SystemMessage)
		if msg.Role != "system" || msg.Content.Text == nil || *msg.Content.Text != "" || msg.Timestamp != 7 {
			t.Fatalf("system msg = %+v", msg)
		}
		secs := make(map[string]*string)
		var keys []string
		if msg.Sections != nil {
			for k, v := range msg.Sections.All() {
				keys = append(keys, k)
				secs[k] = v
			}
		}
		var omit []durable.EntryId
		for _, ed := range draft.Edits {
			if ed.Action != durable.ContextEditActionOmit {
				t.Fatalf("unexpected edit action: %s", ed.Action)
			}
			omit = append(omit, ed.Target)
		}
		out[i] = thprPlanned{sections: secs, keys: keys, omit: omit}
	}
	return out
}

func thprExpectPlanSections(t *testing.T, plans []thprPlanned, wants []struct {
	pairs [][2]*string
	omit  []durable.EntryId
}) {
	t.Helper()
	if len(plans) != len(wants) {
		t.Fatalf("len(plans) = %d, want %d (%+v)", len(plans), len(wants), plans)
	}
	for i, p := range plans {
		w := wants[i]
		gotPairs := make([][2]*string, len(p.keys))
		for j, k := range p.keys {
			keyCopy := k
			gotPairs[j] = [2]*string{&keyCopy, p.sections[k]}
		}
		if len(gotPairs) != len(w.pairs) {
			t.Fatalf("plan[%d] keys = %v, want len %d", i, p.keys, len(w.pairs))
		}
		for j := range gotPairs {
			if *gotPairs[j][0] != *w.pairs[j][0] {
				t.Fatalf("plan[%d][%d] key = %s, want %s", i, j, *gotPairs[j][0], *w.pairs[j][0])
			}
			gv, wv := gotPairs[j][1], w.pairs[j][1]
			if (gv == nil) != (wv == nil) || (gv != nil && *gv != *wv) {
				t.Fatalf("plan[%d][%d] val = %v, want %v", i, j, gv, wv)
			}
		}
		if !reflect.DeepEqual(p.omit, w.omit) {
			t.Fatalf("plan[%d].omit = %v, want %v", i, p.omit, w.omit)
		}
	}
}

func thprS(v string) *string { return &v }
func thprK(v string) *string { return &v }

func thprRoot(t *testing.T) durable.Conversation {
	t.Helper()
	harness, _, err := testkit.OpenHarness(storage.NewMemoryStorage(), nil)
	if err != nil {
		t.Fatalf("OpenHarness failed: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close(testkit.TestContext) })
	root, err := harness.Root(testkit.TestContext)
	if err != nil {
		t.Fatalf("Root failed: %v", err)
	}
	return root
}

func thprLastSystemId(t *testing.T, conv durable.Conversation) durable.EntryId {
	t.Helper()
	page, err := conv.Entries(durable.ConversationEntryQuery{}, 100, nil, testkit.TestContext)
	if err != nil {
		t.Fatalf("Entries failed: %v", err)
	}
	for _, e := range page.Items {
		if e.Kind == "pi.system" {
			return e.Id
		}
	}
	t.Fatalf("no pi.system entry found")
	return 0
}

func thprMarker(t *testing.T, conv durable.Conversation, head durable.EntryHead) durable.EntryId {
	t.Helper()
	res, err := conv.Commit(testkit.TestContext, func(tx durable.Tx) (any, error) {
		e, err := tx.AppendEntry(conv.Id(), durable.EntryDraft{
			Kind:  "summary",
			Head:  head,
			Model: ai.MessageList{testkit.User("summary")},
		})
		if err != nil {
			return nil, err
		}
		return e.Id, nil
	})
	if err != nil {
		t.Fatalf("marker failed: %v", err)
	}
	return res.(durable.EntryId)
}

func TestHarnessPromptPreparation(t *testing.T) {
	ctx := testkit.TestContext
	input := durable.PromptInput{
		ConversationId: durable.IdFromNumber[durable.ConversationId](1),
		Agent:          &durable.Agent{ThinkingLevel: ai.ModelThinkingLevelOff},
		Shown:          map[string]string{},
		Read:           thprDummyReader{},
	}

	t.Run("renders sections in order with tags, omissions, wrappers, and failures", func(t *testing.T) {
		reg := durable.CreateRegistry()
		_, _ = testkit.AddSection(reg, "preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "You are helpful.", true, nil
		}, testkit.AddSectionOptions{Tag: func() *bool { b := false; return &b }()})
		_, _ = testkit.AddSection(reg, "cwd", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "/repo", true, nil
		})
		_, _ = testkit.AddSection(reg, "skipped", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "", false, nil
		})
		_, _ = testkit.AddSection(reg, "failing", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "", false, errors.New("render failed")
		})
		_, _ = testkit.AddSection(reg, "new-failing", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "", false, errors.New("also failed")
		})
		_ = reg.Install(durable.DefineExtension(&durable.Extension{
			Name: "git",
			Wraps: []durable.Wrap{
				durable.WrapSection("cwd", func(inner *durable.PromptSection) *durable.PromptSection {
					return durable.Section(inner.Key, func(v durable.PromptInput, c chord.Context) (string, bool, error) {
						txt, ok, err := inner.Render(v, c)
						if err != nil || !ok {
							return txt, ok, err
						}
						return txt + " (git)", true, nil
					})
				}),
			},
		}))
		var reports []error
		shown := thprMapFromPairs([][2]string{
			{"failing", "<failing>\nold\n</failing>"},
			{"cwd", "stale"},
		})
		agent := durable.ResolveAgent(nil, reg.Snapshot(), durable.ResolveSettings(nil), func(err error) {
			t.Fatalf("unexpected agent resolve report: %v", err)
		})
		desired, err := durable.RenderSections(agent.Sections, input, shown, func(err error) {
			reports = append(reports, err)
		}, ctx)
		if err != nil {
			t.Fatalf("RenderSections failed: %v", err)
		}
		want := [][2]string{
			{"preamble", "You are helpful."},
			{"cwd", "<cwd>\n/repo (git)\n</cwd>"},
			{"failing", "<failing>\nold\n</failing>"},
		}
		if got := thprOrderedPairs(desired); !reflect.DeepEqual(got, want) {
			t.Fatalf("desired = %v, want %v", got, want)
		}
		var msgs []string
		for _, r := range reports {
			msgs = append(msgs, r.Error())
		}
		if !reflect.DeepEqual(msgs, []string{"render failed", "also failed"}) {
			t.Fatalf("reports = %v", msgs)
		}
	})

	t.Run("propagates section errors after cancellation", func(t *testing.T) {
		rawCtx, cancel := context.WithCancelCause(context.Background())
		cancel(errors.New("cancelled"))
		cancelled := chord.WithAbortSignal(rawCtx, ctx)
		failing := durable.Section("a", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "", false, errors.New("cancelled")
		})
		_, err := durable.RenderSections([]*durable.PromptSection{failing}, input, omap.NewMap[string, string](), func(_ error) {}, cancelled)
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected cancelled error, got %v", err)
		}
	})

	t.Run("replays sections in place, deletes on null, and appends re-additions", func(t *testing.T) {
		m1 := omap.NewMap[string, *string]()
		m1.Set("a", thprS("1"))
		m1.Set("b", thprS("2"))
		m1.Set("c", thprS("3"))
		m2 := omap.NewMap[string, *string]()
		m2.Set("b", thprS("20"))
		m2.Set("a", nil)
		m3 := omap.NewMap[string, *string]()
		m3.Set("a", thprS("10"))
		shown := durable.ReplaySections([]ai.Message{
			testkit.System(m1),
			testkit.User("x"),
			testkit.System(m2),
			testkit.System(m3),
		})
		want := [][2]string{{"b", "20"}, {"c", "3"}, {"a", "10"}}
		if got := thprOrderedPairs(shown); !reflect.DeepEqual(got, want) {
			t.Fatalf("shown = %v, want %v", got, want)
		}
	})

	t.Run("emits minimal value patches, removals, and additions", func(t *testing.T) {
		conv := thprRoot(t)
		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{{"a", "1"}, {"b", "2"}, {"c", "3"}}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{{pairs: [][2]*string{{thprK("a"), thprS("1")}, {thprK("b"), thprS("2")}, {thprK("c"), thprS("3")}}}})

		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{{"a", "1"}, {"b", "20"}, {"c", "3"}, {"d", "4"}}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{{pairs: [][2]*string{{thprK("b"), thprS("20")}, {thprK("d"), thprS("4")}}}})

		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{{"a", "1"}, {"c", "3"}, {"d", "4"}}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{{pairs: [][2]*string{{thprK("b"), nil}}}})

		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{{"a", "1"}, {"c", "3"}, {"d", "4"}}), nil)

		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{{pairs: [][2]*string{{thprK("a"), nil}, {thprK("c"), nil}, {thprK("d"), nil}}}})
	})

	t.Run("rewrites order-only changes and re-additions as two entries", func(t *testing.T) {
		conv := thprRoot(t)
		thprApply(t, conv, [][2]string{{"a", "1"}, {"b", "2"}})
		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{{"b", "2"}, {"a", "1"}}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{
			{pairs: [][2]*string{{thprK("a"), nil}, {thprK("b"), nil}}},
			{pairs: [][2]*string{{thprK("b"), thprS("2")}, {thprK("a"), thprS("1")}}},
		})

		readded := thprRoot(t)
		thprApply(t, readded, [][2]string{{"a", "1"}, {"b", "2"}, {"c", "3"}})
		thprExpectPlanSections(t, thprApply(t, readded, [][2]string{{"a", "1"}, {"c", "3"}}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{{pairs: [][2]*string{{thprK("b"), nil}}}})
		// Patching would append `b` after `c`.
		thprExpectPlanSections(t, thprApply(t, readded, [][2]string{{"a", "1"}, {"b", "2"}, {"c", "3"}}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{
			{pairs: [][2]*string{{thprK("a"), nil}, {thprK("c"), nil}}},
			{pairs: [][2]*string{{thprK("a"), thprS("1")}, {thprK("b"), thprS("2")}, {thprK("c"), thprS("3")}}},
		})
	})

	t.Run("rebaselines after a head marker, omitting retained deltas on both sides of it", func(t *testing.T) {
		conv := thprRoot(t)
		thprApply(t, conv, [][2]string{{"a", "1"}, {"b", "2"}})
		_, _ = conv.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(conv.Id(), durable.EntryDraft{
				Kind:  "pi.user",
				Model: ai.MessageList{testkit.User("hi")},
			})
		})
		thprApply(t, conv, [][2]string{{"a", "1"}, {"b", "20"}})
		delta := thprLastSystemId(t, conv)
		// The head keeps the delta but cuts its baseline: replay alone would show only `b`.
		thprMarker(t, conv, durable.EntryHeadAt(delta))
		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{{"a", "1"}, {"b", "20"}}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{{
			pairs: [][2]*string{{thprK("a"), thprS("1")}, {thprK("b"), thprS("20")}},
			omit:  []durable.EntryId{delta},
		}})
		baseline := thprLastSystemId(t, conv)
		// A system entry follows the marker now, so later changes are ordinary patches.
		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{{"a", "1"}, {"b", "21"}}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{{pairs: [][2]*string{{thprK("b"), thprS("21")}}}})
		after := thprLastSystemId(t, conv)
		// A second marker keeps deltas from both sides of the first one.
		thprMarker(t, conv, durable.EntryHeadAt(delta))
		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{{"a", "1"}, {"b", "21"}}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{{
			pairs: [][2]*string{{thprK("a"), thprS("1")}, {thprK("b"), thprS("21")}},
			omit:  []durable.EntryId{delta, baseline, after},
		}})
	})

	t.Run("writes a complete post-head baseline even when replay already matches", func(t *testing.T) {
		conv := thprRoot(t)
		thprApply(t, conv, [][2]string{{"a", "1"}})
		baseline := thprLastSystemId(t, conv)
		thprMarker(t, conv, durable.EntryHeadAt(baseline))
		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{{"a", "1"}}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{{
			pairs: [][2]*string{{thprK("a"), thprS("1")}},
			omit:  []durable.EntryId{baseline},
		}})
		thprMarker(t, conv, durable.EntryHeadSelf())
		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{}), []struct {
			pairs [][2]*string
			omit  []durable.EntryId
		}{{}})
		thprExpectPlanSections(t, thprApply(t, conv, [][2]string{}), nil)
	})
}

func TestHarnessToolLoadoutPreparation(t *testing.T) {
	ctx := testkit.TestContext
	decl := func(name string, description ...string) *durable.ToolRegistration {
		desc := name
		if len(description) > 0 {
			desc = description[0]
		}
		return &durable.ToolRegistration{
			Name:        name,
			Description: desc,
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
		}
	}

	type toolStep struct {
		removed  []string
		added    []string
		sections [][2]*string
	}
	applyTools := func(t *testing.T, conv durable.Conversation, tools []*durable.ToolRegistration, sections [][2]string) []toolStep {
		t.Helper()
		view, err := conv.Context(ctx)
		if err != nil {
			t.Fatalf("Context failed: %v", err)
		}
		drafts := durable.PlanSystemEntries(view, thprMapFromPairs(sections), tools, 7)
		if _, err := conv.Commit(ctx, func(tx durable.Tx) (any, error) {
			for _, d := range drafts {
				if _, err := tx.AppendTypedEntry(durable.SystemEntry, conv.Id(), d); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}); err != nil {
			t.Fatalf("commit drafts failed: %v", err)
		}
		after, err := conv.Context(ctx)
		if err != nil {
			t.Fatalf("after Context failed: %v", err)
		}
		offered := ai.GetCurrentTools(after.Messages)
		wantOffered := make([]*ai.Tool, len(tools))
		for i, tl := range tools {
			wantOffered[i] = ai.ToToolDeclaration(tl.AiTool())
		}
		if len(offered) != len(wantOffered) {
			t.Fatalf("offered len = %d, want %d", len(offered), len(wantOffered))
		}
		for i := range offered {
			if !ai.DeclarationsEqual(offered[i], wantOffered[i]) {
				t.Fatalf("offered[%d] = %+v, want %+v", i, offered[i], wantOffered[i])
			}
		}
		out := make([]toolStep, len(drafts))
		for i, d := range drafts {
			msg := d.Model[0].(*ai.SystemMessage)
			var rem []string
			for _, r := range msg.ToolsRemoved {
				rem = append(rem, r.Name)
			}
			var add []string
			for _, a := range msg.ToolsAdded {
				add = append(add, a.Name)
			}
			var secPairs [][2]*string
			if msg.Sections != nil {
				for k, v := range msg.Sections.All() {
					keyCopy := k
					secPairs = append(secPairs, [2]*string{&keyCopy, v})
				}
			}
			out[i] = toolStep{removed: rem, added: add, sections: secPairs}
		}
		return out
	}

	t.Run("adds, removes, replaces changed declarations, and rewrites the order when needed", func(t *testing.T) {
		conv := thprRoot(t)
		a, b, c := decl("a"), decl("b"), decl("c")
		if got := applyTools(t, conv, []*durable.ToolRegistration{a, b}, nil); !reflect.DeepEqual(got, []toolStep{{added: []string{"a", "b"}}}) {
			t.Fatalf("step 1 = %+v", got)
		}
		if got := applyTools(t, conv, []*durable.ToolRegistration{a, b}, nil); len(got) != 0 {
			t.Fatalf("step 2 = %+v", got)
		}
		if got := applyTools(t, conv, []*durable.ToolRegistration{a, b, c}, nil); !reflect.DeepEqual(got, []toolStep{{added: []string{"c"}}}) {
			t.Fatalf("step 3 = %+v", got)
		}
		if got := applyTools(t, conv, []*durable.ToolRegistration{a, c}, nil); !reflect.DeepEqual(got, []toolStep{{removed: []string{"b"}}}) {
			t.Fatalf("step 4 = %+v", got)
		}
		// A changed declaration at the end is removed and re-added in place.
		c2 := decl("c", "changed")
		if got := applyTools(t, conv, []*durable.ToolRegistration{a, c2}, nil); !reflect.DeepEqual(got, []toolStep{{removed: []string{"c"}, added: []string{"c"}}}) {
			t.Fatalf("step 5 = %+v", got)
		}
		// A changed declaration in the middle would move to the end, so the whole order is rewritten.
		a2 := decl("a", "changed")
		if got := applyTools(t, conv, []*durable.ToolRegistration{a2, c2}, nil); !reflect.DeepEqual(got, []toolStep{{removed: []string{"a", "c"}, added: []string{"a", "c"}}}) {
			t.Fatalf("step 6 = %+v", got)
		}
		// Order-only change.
		if got := applyTools(t, conv, []*durable.ToolRegistration{c2, a2}, nil); !reflect.DeepEqual(got, []toolStep{{removed: []string{"a", "c"}, added: []string{"c", "a"}}}) {
			t.Fatalf("step 7 = %+v", got)
		}
		if got := applyTools(t, conv, nil, nil); !reflect.DeepEqual(got, []toolStep{{removed: []string{"c", "a"}}}) {
			t.Fatalf("step 8 = %+v", got)
		}
	})

	t.Run("puts tool changes on the last section entry and re-declares every tool after a head cut", func(t *testing.T) {
		conv := thprRoot(t)
		a, b := decl("a"), decl("b")
		got1 := applyTools(t, conv, []*durable.ToolRegistration{a}, [][2]string{{"x", "1"}, {"y", "2"}})
		want1 := []toolStep{{added: []string{"a"}, sections: [][2]*string{{thprK("x"), thprS("1")}, {thprK("y"), thprS("2")}}}}
		if !reflect.DeepEqual(got1, want1) {
			t.Fatalf("got1 = %+v", got1)
		}
		// Section order changes need two entries; the tool change rides on the second.
		got2 := applyTools(t, conv, []*durable.ToolRegistration{a, b}, [][2]string{{"y", "2"}, {"x", "1"}})
		want2 := []toolStep{
			{sections: [][2]*string{{thprK("x"), nil}, {thprK("y"), nil}}},
			{added: []string{"b"}, sections: [][2]*string{{thprK("y"), thprS("2")}, {thprK("x"), thprS("1")}}},
		}
		if !reflect.DeepEqual(got2, want2) {
			t.Fatalf("got2 = %+v", got2)
		}
		thprMarker(t, conv, durable.EntryHeadSelf())
		got3 := applyTools(t, conv, []*durable.ToolRegistration{a, b}, [][2]string{{"y", "2"}, {"x", "1"}})
		want3 := []toolStep{{added: []string{"a", "b"}, sections: [][2]*string{{thprK("y"), thprS("2")}, {thprK("x"), thprS("1")}}}}
		if !reflect.DeepEqual(got3, want3) {
			t.Fatalf("got3 = %+v", got3)
		}
	})
}
