// Ported from packages/telemetry/test/store-conformance.ts (pi v1.5.2).

package telemetrytest

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"

	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/telemetry"
)

// StoreFixture pairs an active TelemetryContext with its TelemetryStore for store conformance checks.
// Flush pushes any pending async writes before queries run; nil Flush is a no-op.
// SetNow optionally hooks custom store clocks alongside telemetry.MemoryOverrideClock.
type StoreFixture struct {
	Context      telemetry.TelemetryContext
	Store        telemetry.TelemetryStore
	Flush        func() error
	AsyncDispose func() error
	SetNow       func(dateNow func() float64) func()
}

// StoreFixtureFactory creates an isolated StoreFixture for one conformance case.
type StoreFixtureFactory func() (StoreFixture, error)

// TelemetryStoreConformanceCase is a runner-independent conformance case for TelemetryStore implementations.
type TelemetryStoreConformanceCase struct {
	Name string
	Run  func() error
}

var sconfIdPattern = regexp.MustCompile(`^[0-9a-f-]{36}:[1-9][0-9]*$`)

func sconfIntPtr(v int) *int {
	return &v
}

func sconfFloat64Ptr(v float64) *float64 {
	return &v
}

func sconfInt64Ptr(v int64) *int64 {
	return &v
}

func sconfFlush(f StoreFixture) error {
	if f.Flush != nil {
		return f.Flush()
	}
	return nil
}

func sconfDispose(f StoreFixture) error {
	if f.AsyncDispose != nil {
		return f.AsyncDispose()
	}
	return nil
}

func sconfCreateFixture(factory StoreFixtureFactory, dateNow, perfNow func() float64) (StoreFixture, func(), error) {
	restoreMem := telemetry.MemoryOverrideClock(dateNow, perfNow)
	f, err := factory()
	if err != nil {
		restoreMem()
		return StoreFixture{}, nil, err
	}
	var restoreCustom func()
	if f.SetNow != nil {
		restoreCustom = f.SetNow(dateNow)
	}
	cleanup := func() {
		if restoreCustom != nil {
			restoreCustom()
		}
		restoreMem()
	}
	return f, cleanup, nil
}

func sconfCreateCase(name string, run func() error) TelemetryStoreConformanceCase {
	return TelemetryStoreConformanceCase{
		Name: name,
		Run:  run,
	}
}

func sconfFindStoredSpan(spans []telemetry.StoredSpan, name string) (telemetry.StoredSpan, error) {
	for _, span := range spans {
		if span.Name == name {
			return span, nil
		}
	}
	return telemetry.StoredSpan{}, fmt.Errorf("expected stored span %q", name)
}

func sconfFindStoredSpanById(spans []telemetry.StoredSpan, id string) (telemetry.StoredSpan, error) {
	for _, span := range spans {
		if span.Id == id {
			return span, nil
		}
	}
	return telemetry.StoredSpan{}, fmt.Errorf("expected stored span id %q", id)
}

func sconfEqualStoredLinks(a, b []telemetry.StoredSpanLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].TraceId != b[i].TraceId || a[i].SpanId != b[i].SpanId || !confEqualAttributes(a[i].Attributes, b[i].Attributes) {
			return false
		}
	}
	return true
}

func sconfEqualStoredEvents(a, b []telemetry.StoredSpanEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].TimeMs != b[i].TimeMs || !confEqualAttributes(a[i].Attributes, b[i].Attributes) {
			return false
		}
	}
	return true
}

func sconfEqualStoredSpan(a, b telemetry.StoredSpan) bool {
	if a.Id != b.Id || a.TraceId != b.TraceId || a.Name != b.Name || a.StartedMs != b.StartedMs || a.EndedMs != b.EndedMs {
		return false
	}
	if (a.ParentId == nil) != (b.ParentId == nil) || (a.ParentId != nil && *a.ParentId != *b.ParentId) {
		return false
	}
	return confEqualAttributes(a.Attributes, b.Attributes) &&
		sconfEqualStoredEvents(a.Events, b.Events) &&
		sconfEqualStoredLinks(a.Links, b.Links) &&
		confEqualStatus(a.Status, b.Status)
}

func sconfEqualStoredTraces(a, b []telemetry.StoredTrace) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].TraceId != b[i].TraceId || len(a[i].Spans) != len(b[i].Spans) {
			return false
		}
		for j := range a[i].Spans {
			if !sconfEqualStoredSpan(a[i].Spans[j], b[i].Spans[j]) {
				return false
			}
		}
	}
	return true
}

// CreateTelemetryStoreConformance creates runner-independent shared read-store checks.
// Callers must run these cases sequentially (do not call t.Parallel) because clock overrides are process-wide.
func CreateTelemetryStoreConformance(factory StoreFixtureFactory) []TelemetryStoreConformanceCase {
	return []TelemetryStoreConformanceCase{
		sconfCreateCase("returns only settled traces, complete span snapshots, and detached results", func() (retErr error) {
			f, cleanupClock, err := sconfCreateFixture(factory, func() float64 { return 20_000 }, nil)
			if err != nil {
				return err
			}
			defer cleanupClock()
			defer func() {
				if dErr := sconfDispose(f); retErr == nil {
					retErr = dErr
				}
			}()

			started := make(chan struct{})
			gate := make(chan struct{})
			activeDone := make(chan error, 1)
			go func() {
				_, runErr := f.Context.StartSpan(&telemetry.SpanOptions{
					Name:       "root",
					Attributes: confAttrs("tags", []string{"original"}),
				}, func(root telemetry.TelemetrySpan) (any, error) {
					root.AddEvent("event", confAttrs("tags", []string{"original"}))
					close(started)
					<-gate
					_, childErr := root.StartSpan(&telemetry.SpanOptions{Name: "child"}, func(telemetry.TelemetrySpan) (any, error) {
						return nil, nil
					})
					return nil, childErr
				})
				activeDone <- runErr
			}()

			<-started
			if err := sconfFlush(f); err != nil {
				close(gate)
				<-activeDone
				return err
			}
			beforeSettle, err := f.Store.FindTraces(nil)
			if err != nil {
				close(gate)
				<-activeDone
				return err
			}
			if len(beforeSettle.Traces) != 0 {
				close(gate)
				<-activeDone
				return fmt.Errorf("expected no settled traces while active, got %d", len(beforeSettle.Traces))
			}
			missing, err := f.Store.GetTrace("missing")
			if err != nil {
				close(gate)
				<-activeDone
				return err
			}
			if missing != nil {
				close(gate)
				<-activeDone
				return fmt.Errorf("expected nil for missing trace, got %#v", missing)
			}

			close(gate)
			if err := <-activeDone; err != nil {
				return err
			}
			if err := sconfFlush(f); err != nil {
				return err
			}

			res, err := f.Store.FindTraces(nil)
			if err != nil {
				return err
			}
			if len(res.Traces) != 1 {
				return fmt.Errorf("expected 1 trace, got %d", len(res.Traces))
			}
			trace := res.Traces[0]
			root, err := sconfFindStoredSpan(trace.Spans, "root")
			if err != nil {
				return err
			}
			child, err := sconfFindStoredSpan(trace.Spans, "child")
			if err != nil {
				return err
			}
			if !sconfIdPattern.MatchString(root.Id) {
				return fmt.Errorf("unexpected root.Id format: %q", root.Id)
			}
			if root.TraceId != root.Id {
				return fmt.Errorf("expected root.TraceId (%q) == root.Id (%q)", root.TraceId, root.Id)
			}
			if root.ParentId != nil {
				return fmt.Errorf("expected nil root.ParentId, got %q", *root.ParentId)
			}
			if child.ParentId == nil || *child.ParentId != root.Id {
				return fmt.Errorf("expected child.ParentId %q, got %v", root.Id, child.ParentId)
			}
			if child.TraceId != root.TraceId {
				return fmt.Errorf("expected child.TraceId %q, got %q", root.TraceId, child.TraceId)
			}
			if root.StartedMs != 20_000 {
				return fmt.Errorf("expected root.StartedMs 20000, got %v", root.StartedMs)
			}
			if root.EndedMs < root.StartedMs {
				return fmt.Errorf("expected root.EndedMs (%v) >= root.StartedMs (%v)", root.EndedMs, root.StartedMs)
			}
			if len(root.Events) != 1 || root.Events[0].TimeMs < 0 {
				return fmt.Errorf("unexpected root.Events: %#v", root.Events)
			}
			if tags, ok := root.Attributes.Get("tags"); ok {
				if s, ok := tags.([]string); ok && len(s) > 0 {
					s[0] = "changed"
				}
			}
			root.Attributes.Set("tags", []string{"changed"})
			if tags, ok := root.Events[0].Attributes.Get("tags"); ok {
				if s, ok := tags.([]string); ok && len(s) > 0 {
					s[0] = "changed"
				}
			}
			root.Events[0].Attributes.Set("tags", []string{"changed"})

			fresh, err := f.Store.GetTrace(trace.TraceId)
			if err != nil {
				return err
			}
			if fresh == nil {
				return errors.New("expected trace from GetTrace")
			}
			freshRoot, err := sconfFindStoredSpanById(fresh.Spans, root.Id)
			if err != nil {
				return err
			}
			gotTags, _ := freshRoot.Attributes.Get("tags")
			if !reflect.DeepEqual(gotTags, []string{"original"}) {
				return fmt.Errorf("root tags mutated: %#v", gotTags)
			}
			gotEvTags, _ := freshRoot.Events[0].Attributes.Get("tags")
			if !reflect.DeepEqual(gotEvTags, []string{"original"}) {
				return fmt.Errorf("event tags mutated: %#v", gotEvTags)
			}
			return nil
		}),

		sconfCreateCase("filters by first-span name and start, any error, and all attributes on one span", func() (retErr error) {
			now := 10_000.0
			perf := 0.0
			f, cleanupClock, err := sconfCreateFixture(factory, func() float64 { return now }, func() float64 { return perf })
			if err != nil {
				return err
			}
			defer cleanupClock()
			defer func() {
				if dErr := sconfDispose(f); retErr == nil {
					retErr = dErr
				}
			}()

			if _, err := f.Context.StartSpan(&telemetry.SpanOptions{Name: "old"}, func(root telemetry.TelemetrySpan) (any, error) {
				now = 30_000
				if _, err := root.StartSpan(&telemetry.SpanOptions{Name: "recent-child", Attributes: confAttrs("left", 1)}, func(telemetry.TelemetrySpan) (any, error) {
					return nil, nil
				}); err != nil {
					return nil, err
				}
				perf = 1
				_, err := root.StartSpan(&telemetry.SpanOptions{Name: "split", Attributes: confAttrs("right", 2)}, func(telemetry.TelemetrySpan) (any, error) {
					return nil, nil
				})
				return nil, err
			}); err != nil {
				return err
			}

			now = 20_000
			if _, err := f.Context.StartSpan(&telemetry.SpanOptions{Name: "wanted"}, func(root telemetry.TelemetrySpan) (any, error) {
				now = 20_001
				perf = 2
				_, err := root.StartSpan(&telemetry.SpanOptions{
					Name:       "failure",
					Attributes: confAttrs("left", 1, "right", 2, "valid", true, `odd."key`, "value"),
				}, func(child telemetry.TelemetrySpan) (any, error) {
					child.SetStatus(&telemetry.SpanStatusError{Status: telemetry.SpanStatusKindError})
					return nil, nil
				})
				return nil, err
			}); err != nil {
				return err
			}

			now = 40_000
			if _, err := f.Context.StartSpan(&telemetry.SpanOptions{
				Name:       "new",
				Attributes: confAttrs("valid", 1),
			}, func(telemetry.TelemetrySpan) (any, error) {
				return nil, nil
			}); err != nil {
				return err
			}

			if err := sconfFlush(f); err != nil {
				return err
			}

			names := func(q *telemetry.TraceQuery) ([]string, error) {
				res, err := f.Store.FindTraces(q)
				if err != nil {
					return nil, err
				}
				out := make([]string, len(res.Traces))
				for i, tr := range res.Traces {
					if len(tr.Spans) > 0 {
						out[i] = tr.Spans[0].Name
					}
				}
				return out, nil
			}

			checks := []struct {
				query *telemetry.TraceQuery
				want  []string
			}{
				{
					query: &telemetry.TraceQuery{
						Name:       confStrPtr("wanted"),
						Attributes: confAttrs("left", 1, "right", 2),
						Errors:     true,
						StartMin:   sconfFloat64Ptr(20_000),
						StartMax:   sconfFloat64Ptr(20_000),
					},
					want: []string{"wanted"},
				},
				{query: &telemetry.TraceQuery{Name: confStrPtr("failure")}, want: []string{}},
				{query: &telemetry.TraceQuery{Attributes: confAttrs("left", 1, "right", 2)}, want: []string{"wanted"}},
				{query: &telemetry.TraceQuery{Attributes: confAttrs("valid", true, `odd."key`, "value")}, want: []string{"wanted"}},
				{query: &telemetry.TraceQuery{Attributes: confAttrs("valid", 1)}, want: []string{"new"}},
				{query: &telemetry.TraceQuery{Errors: true}, want: []string{"wanted"}},
				{query: &telemetry.TraceQuery{StartMin: sconfFloat64Ptr(20_000)}, want: []string{"new", "wanted"}},
				{query: &telemetry.TraceQuery{StartMax: sconfFloat64Ptr(10_000)}, want: []string{"old"}},
			}
			for _, tc := range checks {
				got, err := names(tc.query)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(got, tc.want) {
					return fmt.Errorf("query %#v: got %#v, want %#v", tc.query, got, tc.want)
				}
			}
			return nil
		}),

		sconfCreateCase("pages complete traces without duplicates when trace starts tie", func() (retErr error) {
			now := 20_000.0
			perf := 100.0
			f, cleanupClock, err := sconfCreateFixture(factory, func() float64 { return now }, func() float64 { return perf })
			if err != nil {
				return err
			}
			defer cleanupClock()
			defer func() {
				if dErr := sconfDispose(f); retErr == nil {
					retErr = dErr
				}
			}()

			for index := 0; index < 25; index++ {
				now = 20_000
				perf = 100
				if _, err := f.Context.StartSpan(&telemetry.SpanOptions{
					Name:       "root",
					Attributes: confAttrs("index", index),
				}, func(root telemetry.TelemetrySpan) (any, error) {
					now = 20_001
					perf = 101
					_, err := root.StartSpan(&telemetry.SpanOptions{Name: "child"}, func(telemetry.TelemetrySpan) (any, error) {
						return nil, nil
					})
					return nil, err
				}); err != nil {
					return err
				}
			}
			if err := sconfFlush(f); err != nil {
				return err
			}

			first, err := f.Store.FindTraces(nil)
			if err != nil {
				return err
			}
			if len(first.Traces) != 20 {
				return fmt.Errorf("expected 20 traces in first page, got %d", len(first.Traces))
			}
			if first.Cursor == nil {
				return errors.New("expected non-nil cursor after first page")
			}
			rest, err := f.Store.FindTraces(&telemetry.TraceQuery{Cursor: first.Cursor})
			if err != nil {
				return err
			}
			if len(rest.Traces) != 5 {
				return fmt.Errorf("expected 5 traces in second page, got %d", len(rest.Traces))
			}
			if rest.Cursor != nil {
				return fmt.Errorf("expected nil cursor after final page, got %q", *rest.Cursor)
			}
			allRes, err := f.Store.FindTraces(&telemetry.TraceQuery{Limit: sconfIntPtr(100)})
			if err != nil {
				return err
			}
			combined := append(append([]telemetry.StoredTrace{}, first.Traces...), rest.Traces...)
			if !sconfEqualStoredTraces(combined, allRes.Traces) {
				return errors.New("paginated results do not match full query")
			}
			for _, tr := range allRes.Traces {
				if len(tr.Spans) != 2 {
					return fmt.Errorf("expected 2 spans in trace %s, got %d", tr.TraceId, len(tr.Spans))
				}
			}
			var ids []string
			var cursor *string
			for {
				page, err := f.Store.FindTraces(&telemetry.TraceQuery{
					Name:   confStrPtr("root"),
					Limit:  sconfIntPtr(3),
					Cursor: cursor,
				})
				if err != nil {
					return err
				}
				for _, tr := range page.Traces {
					ids = append(ids, tr.TraceId)
				}
				cursor = page.Cursor
				if cursor == nil {
					break
				}
			}
			wantIds := make([]string, len(allRes.Traces))
			for i, tr := range allRes.Traces {
				wantIds[i] = tr.TraceId
			}
			if !reflect.DeepEqual(ids, wantIds) {
				return fmt.Errorf("limit=3 cursor ids %v != all ids %v", ids, wantIds)
			}
			return nil
		}),

		sconfCreateCase("inherits remote trace IDs and preserves remote parents and cross-trace links", func() (retErr error) {
			fSender, cleanupClock, err := sconfCreateFixture(factory, func() float64 { return 20_000 }, nil)
			if err != nil {
				return err
			}
			defer cleanupClock()
			defer func() {
				if dErr := sconfDispose(fSender); retErr == nil {
					retErr = dErr
				}
			}()
			fReceiver, err := factory()
			if err != nil {
				return err
			}
			defer func() {
				if dErr := sconfDispose(fReceiver); retErr == nil {
					retErr = dErr
				}
			}()

			var carrier telemetry.TelemetryCarrier
			if _, err := fSender.Context.StartSpan(&telemetry.SpanOptions{Name: "sender"}, func(span telemetry.TelemetrySpan) (any, error) {
				carrier = fSender.Context.Propagation().Inject(span)
				return nil, nil
			}); err != nil {
				return err
			}
			if carrier == nil {
				return errors.New("expected non-nil sender carrier")
			}
			if err := sconfFlush(fSender); err != nil {
				return err
			}
			sentRes, err := fSender.Store.FindTraces(nil)
			if err != nil {
				return err
			}
			sent := sentRes.Traces[0]
			wire, err := confRoundtripCarrier(carrier)
			if err != nil {
				return err
			}
			remote := fReceiver.Context.Propagation().Extract(wire)
			if _, err := remote.StartSpan(&telemetry.SpanOptions{Name: "receiver"}, func(telemetry.TelemetrySpan) (any, error) {
				return nil, nil
			}); err != nil {
				return err
			}
			if _, err := fReceiver.Context.StartSpan(&telemetry.SpanOptions{
				Name:  "linked",
				Links: []telemetry.SpanLink{{Context: remote, Attributes: confAttrs("reason", "background")}},
			}, func(telemetry.TelemetrySpan) (any, error) {
				return nil, nil
			}); err != nil {
				return err
			}
			if err := sconfFlush(fReceiver); err != nil {
				return err
			}

			continued, err := fReceiver.Store.GetTrace(sent.TraceId)
			if err != nil {
				return err
			}
			if continued == nil || len(continued.Spans) != 1 {
				return fmt.Errorf("expected 1 continued span, got %#v", continued)
			}
			cSpan := continued.Spans[0]
			if cSpan.TraceId != sent.TraceId || cSpan.Name != "receiver" || cSpan.ParentId == nil || *cSpan.ParentId != sent.Spans[0].Id {
				return fmt.Errorf("unexpected continued span: %#v", cSpan)
			}
			if cSpan.Id == sent.Spans[0].Id {
				return fmt.Errorf("expected distinct receiver span id %q", cSpan.Id)
			}
			linkedRes, err := fReceiver.Store.FindTraces(&telemetry.TraceQuery{Name: confStrPtr("linked")})
			if err != nil {
				return err
			}
			if len(linkedRes.Traces) != 1 {
				return fmt.Errorf("expected 1 linked trace, got %d", len(linkedRes.Traces))
			}
			linked := linkedRes.Traces[0]
			if linked.TraceId == sent.TraceId {
				return fmt.Errorf("expected distinct traceId for linked trace %q", linked.TraceId)
			}
			wantLinks := []telemetry.StoredSpanLink{
				{TraceId: sent.TraceId, SpanId: sent.Spans[0].Id, Attributes: confAttrs("reason", "background")},
			}
			if !sconfEqualStoredLinks(linked.Spans[0].Links, wantLinks) {
				return fmt.Errorf("unexpected stored links: %#v", linked.Spans[0].Links)
			}

			for _, key := range carrier.Keys() {
				incomplete := omap.NewMap[string, string]()
				for k, v := range carrier.All() {
					if k != key {
						incomplete.Set(k, v)
					}
				}
				if _, err := fReceiver.Context.Propagation().Extract(incomplete).StartSpan(&telemetry.SpanOptions{Name: "fallback"}, func(telemetry.TelemetrySpan) (any, error) {
					return nil, nil
				}); err != nil {
					return err
				}
			}
			if err := sconfFlush(fReceiver); err != nil {
				return err
			}
			fallbacksRes, err := fReceiver.Store.FindTraces(&telemetry.TraceQuery{Name: confStrPtr("fallback")})
			if err != nil {
				return err
			}
			if len(fallbacksRes.Traces) != carrier.Len() {
				return fmt.Errorf("expected %d fallback traces, got %d", carrier.Len(), len(fallbacksRes.Traces))
			}
			for _, tr := range fallbacksRes.Traces {
				if len(tr.Spans) == 0 || tr.Spans[0].ParentId != nil || tr.TraceId != tr.Spans[0].Id {
					return fmt.Errorf("expected root fallback trace, got %#v", tr)
				}
			}
			return nil
		}),

		sconfCreateCase("accepts external string identities without imposing an ID format", func() (retErr error) {
			f, err := factory()
			if err != nil {
				return err
			}
			defer func() {
				if dErr := sconfDispose(f); retErr == nil {
					retErr = dErr
				}
			}()

			var carrier telemetry.TelemetryCarrier
			if _, err := f.Context.StartSpan(&telemetry.SpanOptions{Name: "source"}, func(span telemetry.TelemetrySpan) (any, error) {
				carrier = f.Context.Propagation().Inject(span)
				return nil, nil
			}); err != nil {
				return err
			}
			external := omap.NewMap[string, string]()
			for _, key := range carrier.Keys() {
				external.Set(key, "external-"+key)
			}
			parent := f.Context.Propagation().Extract(external)
			if !confEqualCarrier(f.Context.Propagation().Inject(parent), external) {
				return fmt.Errorf("expected Inject(parent) == external, got %v", f.Context.Propagation().Inject(parent))
			}
			if _, err := parent.StartSpan(&telemetry.SpanOptions{Name: "continued"}, func(telemetry.TelemetrySpan) (any, error) {
				return nil, nil
			}); err != nil {
				return err
			}
			if err := sconfFlush(f); err != nil {
				return err
			}
			res, err := f.Store.FindTraces(&telemetry.TraceQuery{Name: confStrPtr("continued")})
			if err != nil {
				return err
			}
			if len(res.Traces) != 1 {
				return fmt.Errorf("expected 1 continued trace, got %d", len(res.Traces))
			}
			tr := res.Traces[0]
			if !strings.HasPrefix(tr.TraceId, "external-") {
				return fmt.Errorf("expected external- traceId prefix, got %q", tr.TraceId)
			}
			if len(tr.Spans) == 0 || tr.Spans[0].ParentId == nil || !strings.HasPrefix(*tr.Spans[0].ParentId, "external-") {
				return fmt.Errorf("expected external- parentId prefix, got %#v", tr.Spans)
			}
			return nil
		}),

		sconfCreateCase("queries metric names, inclusive interval bounds, typed attribute equality, and detached values", func() (retErr error) {
			now := 20_001.0
			f, cleanupClock, err := sconfCreateFixture(factory, func() float64 { return now }, nil)
			if err != nil {
				return err
			}
			defer cleanupClock()
			defer func() {
				if dErr := sconfDispose(f); retErr == nil {
					retErr = dErr
				}
			}()

			counter := f.Context.Metrics().Counter("hits", &telemetry.InstrumentOptions{Unit: confStrPtr("By")})
			counter.Add(2, confAttrs("method", "read", "valid", true))
			counter.Add(3, confAttrs("valid", true, "method", "read"))
			counter.Add(11, confAttrs("method", "write", "valid", 1))
			f.Context.Metrics().Counter("other", nil).Add(100, confAttrs("method", "read", "valid", true))
			now = 30_001
			counter.Add(7, confAttrs("method", "read", "valid", true))
			now = 40_001
			if err := sconfFlush(f); err != nil {
				return err
			}

			query := telemetry.MetricQuery{
				Name:       "hits",
				StartMin:   sconfInt64Ptr(20_000),
				StartMax:   sconfInt64Ptr(20_000),
				Attributes: confAttrs("method", "read", "valid", true),
			}
			rows, err := f.Store.FindMetrics(query)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return errors.New("expected non-empty metric rows")
			}
			count := 0
			sum := 0.0
			minVal := math.Inf(1)
			maxVal := math.Inf(-1)
			for _, row := range rows {
				count += row.Count
				sum += row.Sum
				if row.Min < minVal {
					minVal = row.Min
				}
				if row.Max > maxVal {
					maxVal = row.Max
				}
				if row.Kind != telemetry.TelemetryMetricKindCounter || row.Unit == nil || *row.Unit != "By" || row.IntervalMs != 20_000 {
					return fmt.Errorf("unexpected metric row metadata: %#v", row)
				}
			}
			if count != 2 || sum != 5 || minVal != 2 || maxVal != 3 {
				return fmt.Errorf("unexpected aggregation: count=%d sum=%v min=%v max=%v", count, sum, minVal, maxVal)
			}

			rows[0].Attributes.Set("method", "changed")
			freshRows, err := f.Store.FindMetrics(query)
			if err != nil {
				return err
			}
			gotMethod, _ := freshRows[0].Attributes.Get("method")
			if gotMethod != "read" {
				return fmt.Errorf("expected detached attribute method=read, got %#v", gotMethod)
			}

			missingRows, err := f.Store.FindMetrics(telemetry.MetricQuery{Name: "missing"})
			if err != nil {
				return err
			}
			if len(missingRows) != 0 {
				return fmt.Errorf("expected empty rows for missing metric, got %d", len(missingRows))
			}

			intValidRows, err := f.Store.FindMetrics(telemetry.MetricQuery{
				Name:       "hits",
				Attributes: confAttrs("valid", 1),
			})
			if err != nil {
				return err
			}
			intValidSum := 0.0
			for _, row := range intValidRows {
				intValidSum += row.Sum
			}
			if intValidSum != 11 {
				return fmt.Errorf("expected sum 11 for valid=1, got %v", intValidSum)
			}

			startMinRows, err := f.Store.FindMetrics(telemetry.MetricQuery{
				Name:     "hits",
				StartMin: sconfInt64Ptr(30_000),
			})
			if err != nil {
				return err
			}
			startMinSum := 0.0
			for _, row := range startMinRows {
				startMinSum += row.Sum
			}
			if startMinSum != 7 {
				return fmt.Errorf("expected sum 7 for startMin=30000, got %v", startMinSum)
			}
			return nil
		}),
	}
}
