// Ported from packages/durable/test/harness-view.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type thvwFrame struct {
	value durable.ConversationView
	ops   []chord.Op
}

var thvwMounted = []string{"pi.agent", "pi.inbox", "pi.live", "pi.usage"}

type thvwRecorder struct {
	initial durable.ConversationView
	mu      sync.Mutex
	frames  []thvwFrame
	stop    func() durable.WatchEnd
}

func (r *thvwRecorder) snapshot() []thvwFrame {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]thvwFrame, len(r.frames))
	copy(out, r.frames)
	return out
}

func thvwRecord(t *testing.T, conv durable.Conversation) *thvwRecorder {
	t.Helper()
	watch, err := conv.Watch(testkit.TestContext)
	if err != nil {
		t.Fatalf("conv.Watch failed: %v", err)
	}
	rec := &thvwRecorder{
		initial: watch.Value(),
		stop:    watch.Stop,
	}
	if err := watch.Start(func(value durable.ConversationView, ops []chord.Op, _ chord.Context) error {
		rec.mu.Lock()
		rec.frames = append(rec.frames, thvwFrame{value: value, ops: ops})
		rec.mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("watch.Start failed: %v", err)
	}
	return rec
}

func thvwFresh(t *testing.T, conv durable.Conversation) durable.ConversationView {
	t.Helper()
	state, err := conv.ViewState(testkit.TestContext)
	if err != nil {
		t.Fatalf("ViewState failed: %v", err)
	}
	val, _ := state.Value()
	state.Dispose()
	return val
}

func thvwCommitted(t *testing.T, harness durable.Harness, conv durable.Conversation, record durable.ConversationRecord) durable.ConversationView {
	t.Helper()
	ctx := testkit.TestContext
	docs := make(map[string]durable.JsonObject)
	for _, token := range []*durable.DocToken{durable.AgentDoc, durable.LiveDoc, durable.InboxDoc, durable.UsageDoc} {
		val, err := harness.Snapshot(ctx, token, conv.Id())
		if err != nil {
			t.Fatalf("Snapshot(%s) failed: %v", token.Definition.Kind, err)
		}
		if val != nil {
			docs[token.Definition.Kind] = val
		}
	}
	cview, err := conv.Context(ctx)
	if err != nil {
		t.Fatalf("conv.Context failed: %v", err)
	}
	return durable.ConversationView{
		Conversation: record,
		Entries:      cview.Entries,
		Docs:         docs,
	}
}

func thvwEncode(t *testing.T, v any) any {
	t.Helper()
	encoded, err := jsonx.Encode(v)
	if err != nil {
		t.Fatalf("jsonx.Encode(%T) failed: %v", v, err)
	}
	return encoded
}

func thvwReplay(t *testing.T, initial durable.ConversationView, frames []thvwFrame) durable.ConversationView {
	t.Helper()
	cur := thvwEncode(t, initial)
	var last durable.ConversationView
	for i, frame := range frames {
		ops := make([]chord.Op, len(frame.ops))
		for j, op := range frame.ops {
			if r, ok := op.(chord.OpReplace); ok {
				ops[j] = chord.OpReplace{Value: thvwEncode(t, r.Value)}
			} else {
				ops[j] = op
			}
		}
		next, err := chord.ApplyImmutable(cur, ops)
		if err != nil {
			t.Fatalf("ApplyImmutable frame[%d] failed: %v", i, err)
		}
		want := thvwEncode(t, frame.value)
		if !jsonx.DeepEqual(next, want) {
			t.Fatalf("frame[%d] replay mismatch:\n got: %v\nwant: %v", i, next, want)
		}
		cur = next
		last = frame.value
	}
	return last
}

func thvwAnswer(text string) providers.FauxResponseMessage {
	return providers.FauxResponseMessage{
		Message: providers.FauxAssistantMessage(providers.FauxAssistantText(text), nil),
	}
}

func thvwMountedTouches(harness durable.Harness, conv durable.Conversation) *atomic.Int32 {
	count := &atomic.Int32{}
	harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
		for _, ch := range pub.Changes {
			switch c := ch.(type) {
			case durable.StorageWriteEntry:
				if c.Value.ConversationId == conv.Id() {
					count.Add(1)
					return
				}
			case durable.DocumentCommitChangeUpdate:
				if c.ConversationId != nil && *c.ConversationId == conv.Id() && len(c.Ops) > 0 {
					for _, kind := range thvwMounted {
						if c.Record.Kind == kind {
							count.Add(1)
							return
						}
					}
				}
			}
		}
	})
	return count
}

func thvwWaitFrames(t *testing.T, rec *thvwRecorder, n int) []thvwFrame {
	t.Helper()
	if err := testkit.Eventually(func() (bool, error) {
		return len(rec.snapshot()) >= n, nil
	}); err != nil {
		t.Fatalf("timed out waiting for %d frames, got %d", n, len(rec.snapshot()))
	}
	return rec.snapshot()
}

func TestHarnessConversationView(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("hydrates the active entries and the built-in documents", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup failed: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{thvwAnswer("hello")})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: ai.UserMessageContent{Text: thprS("hi")},
		}, ctx)
		if err != nil {
			t.Fatalf("Submit failed: %v", err)
		}
		if _, err := sub.Wait(ctx); err != nil {
			t.Fatalf("sub.Wait failed: %v", err)
		}
		view := thvwFresh(t, root)
		if view.Conversation.Id != root.Id() || view.Conversation.Parent != nil || view.Conversation.Owner != nil {
			t.Fatalf("conversation = %+v", view.Conversation)
		}
		all, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries failed: %v", err)
		}
		if !jsonx.DeepEqual(thvwEncode(t, view.Entries), thvwEncode(t, all)) {
			t.Fatalf("entries mismatch")
		}
		var docKeys []string
		for k := range view.Docs {
			docKeys = append(docKeys, k)
		}
		sort.Strings(docKeys)
		if !reflect.DeepEqual(docKeys, thvwMounted) {
			t.Fatalf("docKeys = %v", docKeys)
		}
		if !jsonx.DeepEqual(view.Docs["pi.live"], jsonx.NewObject()) {
			t.Fatalf("pi.live = %v", view.Docs["pi.live"])
		}
		if !jsonx.DeepEqual(view.Docs["pi.inbox"], jsonx.ObjectOf("items", []any{})) {
			t.Fatalf("pi.inbox = %v", view.Docs["pi.inbox"])
		}
	})

	t.Run("publishes one frame per touching commit, whose operations rebuild every revision", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		release := testkit.NewDeferred[struct{}]()
		step := providers.FauxResponseFactory(func(reqCtx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			select {
			case <-release.Done():
				return thvwAnswer("a longer answer").Message, nil
			case <-reqCtx.Done():
				return nil, context.Cause(reqCtx)
			}
		})
		setup.Faux.SetResponses([]providers.FauxResponseStep{step})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		rec := thvwRecord(t, root)
		defer rec.stop()
		touching := thvwMountedTouches(harness, root)

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: ai.UserMessageContent{Text: thprS("hi")},
		}, ctx)
		if err != nil {
			t.Fatalf("Submit failed: %v", err)
		}
		if err := testkit.WaitFor(func() (bool, error) {
			for _, f := range rec.snapshot() {
				if live, ok := f.value.Docs["pi.live"]; ok && live != nil {
					if g, has := live.Get("generation"); has && g != nil {
						return true, nil
					}
				}
			}
			return false, nil
		}); err != nil {
			t.Fatalf("WaitFor generation failed: %v", err)
		}
		release.Resolve(struct{}{})
		if _, err := sub.Wait(ctx); err != nil {
			t.Fatalf("sub.Wait failed: %v", err)
		}
		if err := harness.WaitForIdle(ctx); err != nil {
			t.Fatalf("WaitForIdle failed: %v", err)
		}
		frames := thvwWaitFrames(t, rec, int(touching.Load()))
		if len(frames) != int(touching.Load()) {
			t.Fatalf("len(frames) = %d, touching = %d", len(frames), touching.Load())
		}
		replayed := thvwReplay(t, rec.initial, frames)
		committed := thvwCommitted(t, harness, root, rec.initial.Conversation)
		if !jsonx.DeepEqual(thvwEncode(t, replayed), thvwEncode(t, committed)) {
			t.Fatalf("replayed != committed")
		}
		// Check first frame inserted the user entry at entries[0].
		foundUserSplice := false
		for _, op := range frames[0].ops {
			if sp, ok := op.(chord.OpSplice); ok && len(sp.Path) == 1 && sp.Path[0] == chord.SegString("entries") && sp.Index == 0 && sp.Remove == 0 && len(sp.Items) == 1 {
				if obj, ok := sp.Items[0].(*jsonx.Object); ok {
					if k, _ := obj.Get("kind"); k == "pi.user" {
						foundUserSplice = true
					}
				}
			}
		}
		if !foundUserSplice {
			t.Fatalf("expected pi.user splice in frame[0]: %#v", frames[0].ops)
		}
		// Document operations keep their exact shape under the mount path.
		foundGenSet := false
		for _, f := range frames {
			for _, op := range f.ops {
				if st, ok := op.(chord.OpSet); ok && len(st.Path) == 3 &&
					st.Path[0] == chord.SegString("docs") && st.Path[1] == chord.SegString("pi.live") && st.Path[2] == chord.SegString("generation") &&
					jsonx.DeepEqual(st.Value, jsonx.ObjectOf("attempt", float64(1))) {
					foundGenSet = true
				}
			}
		}
		if !foundGenSet {
			t.Fatalf("expected docs.pi.live.generation set op across frames")
		}
	})

	t.Run("shares unchanged parts between revisions and skips commits that touch nothing mounted", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		other, _ := harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		rec := thvwRecord(t, root)
		defer rec.stop()
		_, _ = other.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(other.Id(), durable.EntryDraft{Kind: "note"})
		})
		_ = root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh)}, ctx)
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		})
		frames := thvwWaitFrames(t, rec, 2)
		if len(frames) != 2 {
			t.Fatalf("len(frames) = %d, want 2", len(frames))
		}
		wantOp := []chord.Op{chord.OpSet{
			Path:  chord.Path{chord.SegString("docs"), chord.SegString("pi.agent"), chord.SegString("thinkingLevel")},
			Value: "high",
		}}
		if !reflect.DeepEqual(frames[0].ops, wantOp) {
			t.Fatalf("frame[0].ops = %#v, want %#v", frames[0].ops, wantOp)
		}
		if len(frames[0].value.Entries) != len(rec.initial.Entries) {
			t.Fatalf("frame[0] changed entries")
		}
		if frames[0].value.Docs["pi.live"] != rec.initial.Docs["pi.live"] {
			t.Fatalf("pi.live pointer changed unnecessarily")
		}
		if reflect.ValueOf(frames[1].value.Docs).Pointer() != reflect.ValueOf(frames[0].value.Docs).Pointer() {
			t.Fatalf("docs map pointer changed unnecessarily")
		}
	})

	t.Run("cuts the entries at a head marker, keeping the entries from its head", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(ctx) }()
		note := func(kind string) durable.EntryRecord {
			res, _ := root.Commit(ctx, func(tx durable.Tx) (any, error) {
				return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: kind})
			})
			return *res.(*durable.EntryRecord)
		}
		note("a")
		b := note("b")
		note("c")
		rec := thvwRecord(t, root)
		defer rec.stop()
		res, _ := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "summary", Head: durable.EntryHeadAt(b.Id)})
		})
		summary := *res.(*durable.EntryRecord)
		note("d")
		_ = root.Reset(nil, ctx)
		frames := thvwWaitFrames(t, rec, 3)
		var kinds [][]string
		for _, f := range frames {
			var k []string
			for _, e := range f.value.Entries {
				k = append(k, e.Kind)
			}
			kinds = append(kinds, k)
		}
		wantKinds := [][]string{{"summary", "b", "c"}, {"summary", "b", "c", "d"}, {"pi.reset"}}
		if !reflect.DeepEqual(kinds, wantKinds) {
			t.Fatalf("kinds = %v, want %v", kinds, wantKinds)
		}
		if len(frames[0].ops) != 1 {
			t.Fatalf("expected 1 op in frame[0], got %#v", frames[0].ops)
		}
		sp, ok := frames[0].ops[0].(chord.OpSplice)
		if !ok || sp.Index != 0 || sp.Remove != 1 || len(sp.Items) != 1 || !jsonx.DeepEqual(sp.Items[0], thvwEncode(t, summary)) {
			t.Fatalf("frame[0].ops[0] = %#v", frames[0].ops[0])
		}
		replayed := thvwReplay(t, rec.initial, frames)
		committed := thvwCommitted(t, harness, root, rec.initial.Conversation)
		if !jsonx.DeepEqual(thvwEncode(t, replayed), thvwEncode(t, committed)) {
			t.Fatalf("replayed != committed")
		}
	})

	t.Run("keeps only mounted entries for a raw head write that targets before the active range", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(ctx) }()
		res, _ := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "old"})
		})
		old := *res.(*durable.EntryRecord)
		_ = root.Reset(nil, ctx)
		rec := thvwRecord(t, root)
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "summary", Head: durable.EntryHeadAt(old.Id)})
		})
		frames := thvwWaitFrames(t, rec, 1)
		var kinds []string
		for _, e := range frames[0].value.Entries {
			kinds = append(kinds, e.Kind)
		}
		if !reflect.DeepEqual(kinds, []string{"summary"}) {
			t.Fatalf("mounted kinds = %v", kinds)
		}
		rec.stop()
		fresh := thvwFresh(t, root)
		var freshKinds []string
		for _, e := range fresh.Entries {
			freshKinds = append(freshKinds, e.Kind)
		}
		if !reflect.DeepEqual(freshKinds, []string{"summary", "old"}) {
			t.Fatalf("fresh kinds = %v", freshKinds)
		}
	})

	t.Run("cuts a fork's view into its inherited entries", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(ctx) }()
		ra, _ := root.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "a"}) })
		rb, _ := root.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "b"}) })
		a := *ra.(*durable.EntryRecord)
		b := *rb.(*durable.EntryRecord)
		fork, _ := root.Fork(b.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		rec := thvwRecord(t, fork)
		defer rec.stop()
		_, _ = fork.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(fork.Id(), durable.EntryDraft{Kind: "summary", Head: durable.EntryHeadAt(b.Id)})
		})
		frames := thvwWaitFrames(t, rec, 1)
		if len(rec.initial.Entries) != 2 || rec.initial.Entries[0].Id != a.Id || rec.initial.Entries[1].Id != b.Id {
			t.Fatalf("initial.Entries = %v", rec.initial.Entries)
		}
		var fKinds []string
		for _, e := range frames[0].value.Entries {
			fKinds = append(fKinds, e.Kind)
		}
		if !reflect.DeepEqual(fKinds, []string{"summary", "b"}) {
			t.Fatalf("fork frame kinds = %v", fKinds)
		}
		committed := thvwCommitted(t, harness, fork, rec.initial.Conversation)
		if !jsonx.DeepEqual(thvwEncode(t, frames[0].value), thvwEncode(t, committed)) {
			t.Fatalf("fork frame != committed")
		}
	})

	t.Run("shows a fork's inherited entries and follows only the fork's own commits", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(ctx) }()
		rf, _ := root.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "first"}) })
		first := *rf.(*durable.EntryRecord)
		fork, _ := root.Fork(first.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		rec := thvwRecord(t, fork)
		defer rec.stop()
		if len(rec.initial.Entries) != 1 || rec.initial.Entries[0].Kind != "first" {
			t.Fatalf("initial entries = %v", rec.initial.Entries)
		}
		wantParent := &durable.ConversationParent{ConversationId: root.Id(), At: first.Id}
		if !reflect.DeepEqual(rec.initial.Conversation.Parent, wantParent) {
			t.Fatalf("parent = %+v", rec.initial.Conversation.Parent)
		}
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "parent"}) })
		_, _ = fork.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.AppendEntry(fork.Id(), durable.EntryDraft{Kind: "child"}) })
		frames := thvwWaitFrames(t, rec, 1)
		if len(frames) != 1 {
			t.Fatalf("len(frames) = %d", len(frames))
		}
		var kinds []string
		for _, e := range frames[0].value.Entries {
			kinds = append(kinds, e.Kind)
		}
		if !reflect.DeepEqual(kinds, []string{"first", "child"}) {
			t.Fatalf("kinds = %v", kinds)
		}
	})

	t.Run("unmounts a retired document and mounts its recreation whole", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(ctx) }()
		rec := thvwRecord(t, root)
		defer rec.stop()
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return nil, tx.RetireDoc(durable.LiveDoc, root.Id())
		})
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) {
			d, err := tx.Doc(durable.LiveDoc, root.Id())
			if err != nil {
				return nil, err
			}
			d.Value.Set("tools", []any{})
			return nil, nil
		})
		frames := thvwWaitFrames(t, rec, 2)
		want0 := []chord.Op{chord.OpDelete{Path: chord.Path{chord.SegString("docs"), chord.SegString("pi.live")}}}
		want1 := []chord.Op{chord.OpSet{Path: chord.Path{chord.SegString("docs"), chord.SegString("pi.live")}, Value: jsonx.ObjectOf("tools", []any{})}}
		if !reflect.DeepEqual(frames[0].ops, want0) || !reflect.DeepEqual(frames[1].ops, want1) {
			t.Fatalf("ops[0]=%#v ops[1]=%#v", frames[0].ops, frames[1].ops)
		}
		if _, exists := frames[0].value.Docs["pi.live"]; exists {
			t.Fatalf("expected pi.live absent after retire")
		}
	})

	t.Run("replaces undelivered frames with the newest view after 100 pending frames", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(ctx) }()
		watch, _ := root.Watch(ctx)
		defer watch.Stop()
		for i := 0; i < 101; i++ {
			_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) {
				return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
			})
		}
		var mu sync.Mutex
		var frames []thvwFrame
		_ = watch.Start(func(value durable.ConversationView, ops []chord.Op, _ chord.Context) error {
			mu.Lock()
			frames = append(frames, thvwFrame{value: value, ops: ops})
			mu.Unlock()
			return nil
		})
		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return len(frames) == 1, nil
		}); err != nil {
			t.Fatalf("Eventually failed: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(frames) != 1 || len(frames[0].ops) != 1 {
			t.Fatalf("frames = %+v", frames)
		}
		rep, ok := frames[0].ops[0].(chord.OpReplace)
		if !ok || !jsonx.DeepEqual(thvwEncode(t, rep.Value), thvwEncode(t, frames[0].value)) || len(frames[0].value.Entries) != 101 {
			t.Fatalf("overflow reset = %#v, len(entries)=%d", frames[0].ops, len(frames[0].value.Entries))
		}
	})

	t.Run("keeps states and watches of one conversation independent and remounts after the last one detaches", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(ctx) }()
		state, _ := root.ViewState(ctx)
		rec := thvwRecord(t, root)
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "one"})
		})
		_ = thvwWaitFrames(t, rec, 1)
		if err := testkit.Eventually(func() (bool, error) {
			v, _ := state.Value()
			return len(v.Entries) == 1 && v.Entries[0].Kind == "one", nil
		}); err != nil {
			t.Fatalf("state did not update: %v", err)
		}
		rec.stop()
		if len(rec.snapshot()) != 1 {
			t.Fatalf("len(frames) = %d", len(rec.snapshot()))
		}
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "two"})
		})
		if err := testkit.Eventually(func() (bool, error) {
			v, _ := state.Value()
			return len(v.Entries) == 2 && v.Entries[0].Kind == "one" && v.Entries[1].Kind == "two", nil
		}); err != nil {
			t.Fatalf("state did not get second entry: %v", err)
		}
		last, _ := state.Value()
		state.Dispose()
		rebuilt := thvwFresh(t, root)
		if !jsonx.DeepEqual(thvwEncode(t, rebuilt), thvwEncode(t, last)) {
			t.Fatalf("rebuilt != last")
		}
	})

	t.Run("ends states and watches at close and rejects later acquisition", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		watch, _ := root.Watch(ctx)
		state, _ := root.ViewState(ctx)
		_ = harness.Close(ctx)
		end := watch.Closed()
		if end.Reason != durable.WatchEndSessionClosed {
			t.Fatalf("watch.Closed = %+v", end)
		}
		if v, _ := state.Value(); len(v.Entries) != 0 {
			t.Fatalf("state.Value entries = %v", v.Entries)
		}
		if _, err := root.Watch(ctx); err == nil {
			t.Fatalf("expected error on Watch after close")
		}
		if _, err := root.ViewState(ctx); err == nil {
			t.Fatalf("expected error on ViewState after close")
		}
	})

	t.Run("rejects an acquisition cancelled or closed while it waits for the Session line", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		hold := func() (func(), <-chan error) {
			release := testkit.NewDeferred[struct{}]()
			done := make(chan error, 1)
			go func() {
				err := harness.Commit(ctx, func(_ durable.Tx) error {
					_, _ = release.Wait()
					return nil
				})
				done <- err
			}()
			testkit.Flush()
			return func() { release.Resolve(struct{}{}) }, done
		}
		rel, blk := hold()
		rawCtx, cancel := context.WithCancelCause(context.Background())
		cancelled := make(chan error, 1)
		go func() {
			_, err := root.Watch(chord.WithAbortSignal(rawCtx, ctx))
			cancelled <- err
		}()
		testkit.Flush()
		cancel(errors.New("cancelled"))
		rel()
		<-blk
		if err := <-cancelled; err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected cancelled error, got %v", err)
		}

		rel, blk = hold()
		closedWhileQueued := make(chan error, 1)
		go func() {
			_, err := root.Watch(ctx)
			closedWhileQueued <- err
		}()
		testkit.Flush()
		closing := make(chan error, 1)
		go func() { closing <- harness.Close(ctx) }()
		testkit.Flush()
		rel()
		<-blk
		if err := <-closedWhileQueued; err == nil || !strings.Contains(err.Error(), "Harness is closed") {
			t.Fatalf("expected Harness is closed, got %v", err)
		}
		if err := <-closing; err != nil {
			t.Fatalf("closing failed: %v", err)
		}
	})

	t.Run("shares one mount between concurrent observers and isolates a failing listener", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(ctx) }()
		failing, _ := root.Watch(ctx)
		rec := thvwRecord(t, root)
		defer rec.stop()
		shared, _ := root.ViewState(ctx)
		sv, _ := shared.Value()
		if !jsonx.DeepEqual(thvwEncode(t, failing.Value()), thvwEncode(t, sv)) {
			t.Fatalf("shared initial mismatch")
		}
		shared.Dispose()
		_ = failing.Start(func(_ durable.ConversationView, _ []chord.Op, _ chord.Context) error {
			return errors.New("listener failed")
		})
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "one"}) })
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "two"}) })
		end := failing.Closed()
		if end.Reason != durable.WatchEndListenerError {
			t.Fatalf("failing.Closed = %+v", end)
		}
		thvwWaitFrames(t, rec, 2)
	})

	t.Run("publishes one frame for a commit that appends several entries and edits a document", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, _ := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		defer func() { _ = harness.Close(ctx) }()
		other, _ := durable.DefineDoc(durable.DocDefinition{
			Kind:    "app.other",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkInitial,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("n", float64(0)) },
		})
		rec := thvwRecord(t, root)
		defer rec.stop()
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) {
			if _, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "a"}); err != nil {
				return nil, err
			}
			d, err := tx.Doc(durable.LiveDoc, root.Id())
			if err != nil {
				return nil, err
			}
			d.Value.Set("tools", []any{})
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "b"})
		})
		// A document that is not mounted publishes nothing.
		_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) {
			d, err := tx.Doc(other, root.Id())
			if err != nil {
				return nil, err
			}
			d.Value.Set("n", float64(1))
			return nil, nil
		})
		frames := thvwWaitFrames(t, rec, 1)
		testkit.Flush()
		frames = rec.snapshot()
		if len(frames) != 1 {
			t.Fatalf("len(frames) = %d, want 1", len(frames))
		}
		var kinds []string
		for _, e := range frames[0].value.Entries {
			kinds = append(kinds, e.Kind)
		}
		if !reflect.DeepEqual(kinds, []string{"a", "b"}) {
			t.Fatalf("kinds = %v", kinds)
		}
		replayed := thvwReplay(t, rec.initial, frames)
		committed := thvwCommitted(t, harness, root, rec.initial.Conversation)
		if !jsonx.DeepEqual(thvwEncode(t, replayed), thvwEncode(t, committed)) {
			t.Fatalf("replayed != committed")
		}
	})
}
