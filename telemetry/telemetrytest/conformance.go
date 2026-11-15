// Ported from packages/telemetry/src/testing/conformance.ts (pi v1.5.2).

package telemetrytest

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/telemetry"
)

type confConformanceTest func(fixture TelemetryAdapterFixture) error

type confResult struct {
	Value int
}

type confAsyncError struct {
	Kind string
}

func (e *confAsyncError) Error() string {
	return e.Kind
}

func confStrPtr(s string) *string {
	return &s
}

func confAttrs(pairs ...any) telemetry.SpanAttributes {
	m := omap.NewMap[string, telemetry.AttributeValue]()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func confCreateCase(factory TelemetryAdapterFixtureFactory, group string, name string, test confConformanceTest) TelemetryAdapterConformanceCase {
	return TelemetryAdapterConformanceCase{
		Group: group,
		Name:  name,
		Run: func() (retErr error) {
			fixture, err := factory()
			if err != nil {
				return err
			}
			if fixture.AsyncDispose != nil {
				defer func() {
					disposeErr := fixture.AsyncDispose()
					if retErr == nil {
						retErr = disposeErr
					}
				}()
			}
			return test(fixture)
		},
	}
}

func confFindSpan(spans []telemetry.RecordedTelemetrySpan, name string) (telemetry.RecordedTelemetrySpan, error) {
	for _, span := range spans {
		if span.Name == name {
			return span, nil
		}
	}
	return telemetry.RecordedTelemetrySpan{}, fmt.Errorf("expected recorded span %s", name)
}

func confRejectsWithSameValue(run func() (any, error), expected any) error {
	var (
		err      error
		panicked bool
		panicVal any
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				panicVal = r
			}
		}()
		_, err = run()
	}()
	if panicked {
		if panicVal != expected {
			return fmt.Errorf("expected panic value %#v, got %#v", expected, panicVal)
		}
		return nil
	}
	if err == nil {
		return errors.New("expected operation to fail")
	}
	if err != expected {
		return fmt.Errorf("expected error %#v, got %#v", expected, err)
	}
	return nil
}

func confEqualCarrier(a, b telemetry.TelemetryCarrier) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Len() != b.Len() {
		return false
	}
	for k, va := range a.All() {
		vb, ok := b.Get(k)
		if !ok || va != vb {
			return false
		}
	}
	return true
}

func confEqualOptCarrier(a, b jsonx.Opt[telemetry.TelemetryCarrier]) bool {
	if a.IsZero() || b.IsZero() {
		return a.IsZero() && b.IsZero()
	}
	if a.IsNull() || b.IsNull() {
		return a.IsNull() && b.IsNull()
	}
	va, _ := a.Get()
	vb, _ := b.Get()
	return confEqualCarrier(va, vb)
}

func confEqualAttributes(a, b telemetry.SpanAttributes) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Len() != b.Len() {
		return false
	}
	for k, va := range a.All() {
		vb, ok := b.Get(k)
		if !ok || !reflect.DeepEqual(va, vb) {
			return false
		}
	}
	return true
}

func confEqualStatus(a, b telemetry.SpanStatus) bool {
	return reflect.DeepEqual(a, b)
}

func confEqualLinks(a, b []telemetry.RecordedTelemetryLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !confEqualCarrier(a[i].Context, b[i].Context) || !confEqualAttributes(a[i].Attributes, b[i].Attributes) {
			return false
		}
	}
	return true
}

func confEqualEvents(a, b []telemetry.RecordedTelemetryEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || !confEqualAttributes(a[i].Attributes, b[i].Attributes) {
			return false
		}
	}
	return true
}

func confRoundtripCarrier(carrier telemetry.TelemetryCarrier) (telemetry.TelemetryCarrier, error) {
	encoded, err := jsonx.Marshal(carrier)
	if err != nil {
		return nil, err
	}
	var decoded telemetry.TelemetryCarrier
	if err := jsonx.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// CreateTelemetryAdapterConformance creates runner-independent cases for the callback telemetry adapter contract.
func CreateTelemetryAdapterConformance(factory TelemetryAdapterFixtureFactory) []TelemetryAdapterConformanceCase {
	return []TelemetryAdapterConformanceCase{
		confCreateCase(
			factory,
			"capabilities",
			"carries adapter capabilities through contexts and spans",
			func(fixture TelemetryAdapterFixture) error {
				metrics := fixture.Metrics
				if metrics == nil {
					metrics = fixture.Context.Metrics()
				}
				propagation := fixture.Propagation
				if propagation == nil {
					propagation = fixture.Context.Propagation()
				}
				assertCapabilities := func(ctx telemetry.TelemetryContext) error {
					if ctx.Metrics() != metrics {
						return fmt.Errorf("expected context.Metrics() %#v, got %#v", metrics, ctx.Metrics())
					}
					if ctx.Propagation() != propagation {
						return fmt.Errorf("expected context.Propagation() %#v, got %#v", propagation, ctx.Propagation())
					}
					return nil
				}
				if err := assertCapabilities(fixture.Context); err != nil {
					return err
				}
				var settled telemetry.TelemetrySpan
				_, err := fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "capabilities"}, func(span telemetry.TelemetrySpan) (any, error) {
					settled = span
					if err := assertCapabilities(span); err != nil {
						return nil, err
					}
					if _, err := span.StartSpan(&telemetry.SpanOptions{Name: "capabilities.child"}, func(child telemetry.TelemetrySpan) (any, error) {
						return nil, assertCapabilities(child)
					}); err != nil {
						return nil, err
					}
					if propagation != nil {
						parent := propagation.Extract(propagation.Inject(span))
						if err := assertCapabilities(parent); err != nil {
							return nil, err
						}
						if _, err := parent.StartSpan(&telemetry.SpanOptions{Name: "capabilities.extracted"}, func(extracted telemetry.TelemetrySpan) (any, error) {
							return nil, assertCapabilities(extracted)
						}); err != nil {
							return nil, err
						}
					}
					return nil, nil
				})
				if err != nil {
					return err
				}
				if settled == nil {
					return errors.New("expected settled span")
				}
				_, err = settled.StartSpan(&telemetry.SpanOptions{Name: "capabilities.inert"}, func(inert telemetry.TelemetrySpan) (any, error) {
					return nil, assertCapabilities(inert)
				})
				return err
			},
		),

		confCreateCase(
			factory,
			"metrics",
			"records ordered measurements and optional histogram exemplars",
			func(fixture TelemetryAdapterFixture) error {
				if fixture.Metrics == nil {
					return nil
				}
				if fixture.GetMetrics == nil {
					return errors.New("metrics fixtures require a normalized measurement reader")
				}
				fixture.Metrics.
					Counter("bytes", &telemetry.InstrumentOptions{Description: confStrPtr("Encoded bytes"), Unit: confStrPtr("By")}).
					Add(12, confAttrs("direction", "out"))
				active := fixture.Metrics.UpDownCounter("active", nil)
				active.Add(1, nil)
				active.Add(-1, nil)
				histogram := fixture.Metrics.Histogram("duration", &telemetry.HistogramOptions{
					InstrumentOptions: telemetry.InstrumentOptions{Unit: confStrPtr("ms")},
					Boundaries:        []float64{1, 10},
				})
				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "measured"}, func(span telemetry.TelemetrySpan) (any, error) {
					histogram.Record(5, confAttrs("method", "read"), span)
					return nil, nil
				}); err != nil {
					return err
				}
				measurements, err := fixture.GetMetrics()
				if err != nil {
					return err
				}
				type summary struct {
					kind  telemetry.TelemetryMetricKind
					name  string
					value float64
				}
				gotSummaries := make([]summary, len(measurements))
				for i, m := range measurements {
					gotSummaries[i] = summary{kind: m.Kind, name: m.Name, value: m.Value}
				}
				wantSummaries := []summary{
					{telemetry.TelemetryMetricKindCounter, "bytes", 12},
					{telemetry.TelemetryMetricKindUpDownCounter, "active", 1},
					{telemetry.TelemetryMetricKindUpDownCounter, "active", -1},
					{telemetry.TelemetryMetricKindHistogram, "duration", 5},
				}
				if !reflect.DeepEqual(gotSummaries, wantSummaries) {
					return fmt.Errorf("measurements mismatch: got %#v, want %#v", gotSummaries, wantSummaries)
				}
				if !confEqualAttributes(measurements[0].Attributes, confAttrs("direction", "out")) {
					return fmt.Errorf("unexpected measurement[0] attributes: %v", measurements[0].Attributes)
				}
				if measurements[0].Description == nil || *measurements[0].Description != "Encoded bytes" {
					return fmt.Errorf("unexpected measurement[0] description: %v", measurements[0].Description)
				}
				if measurements[0].Unit == nil || *measurements[0].Unit != "By" {
					return fmt.Errorf("unexpected measurement[0] unit: %v", measurements[0].Unit)
				}
				if !reflect.DeepEqual(measurements[3].Boundaries, []float64{1, 10}) {
					return fmt.Errorf("unexpected measurement[3] boundaries: %v", measurements[3].Boundaries)
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				measured, err := confFindSpan(spans, "measured")
				if err != nil {
					return err
				}
				if !confEqualOptCarrier(measurements[3].Context, jsonx.Some(measured.Context)) {
					return fmt.Errorf("unexpected measurement[3] context: %v, want %v", measurements[3].Context, measured.Context)
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"metrics",
			"copies instrument metadata and measurement attributes",
			func(fixture TelemetryAdapterFixture) error {
				if fixture.Metrics == nil {
					return nil
				}
				if fixture.GetMetrics == nil {
					return errors.New("metrics fixtures require a normalized measurement reader")
				}
				boundaries := []float64{1, 10}
				options := &telemetry.HistogramOptions{
					InstrumentOptions: telemetry.InstrumentOptions{Description: confStrPtr("Original")},
					Boundaries:        boundaries,
				}
				histogram := fixture.Metrics.Histogram("snapshot", options)
				boundaries = append(boundaries, 100)
				_ = boundaries
				*options.Description = "Changed"
				tags := []string{"original"}
				attributes := confAttrs("tags", tags)
				histogram.Record(-1, attributes, nil)
				tags = append(tags, "changed")
				attributes.Set("tags", tags)
				measurements, err := fixture.GetMetrics()
				if err != nil {
					return err
				}
				if len(measurements) != 1 {
					return fmt.Errorf("expected 1 measurement, got %d", len(measurements))
				}
				if !confEqualAttributes(measurements[0].Attributes, confAttrs("tags", []string{"original"})) {
					return fmt.Errorf("unexpected measurement attributes: %v", measurements[0].Attributes)
				}
				if !reflect.DeepEqual(measurements[0].Boundaries, []float64{1, 10}) {
					return fmt.Errorf("unexpected measurement boundaries: %v", measurements[0].Boundaries)
				}
				if measurements[0].Description == nil || *measurements[0].Description != "Original" {
					return fmt.Errorf("unexpected measurement description: %v", measurements[0].Description)
				}
				if !measurements[0].Context.IsNull() {
					return fmt.Errorf("expected null measurement context, got %v", measurements[0].Context)
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"links",
			"records initial and late links without changing parents",
			func(fixture TelemetryAdapterFixture) error {
				var target telemetry.TelemetrySpan
				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "target"}, func(span telemetry.TelemetrySpan) (any, error) {
					target = span
					return nil, nil
				}); err != nil {
					return err
				}
				if target == nil {
					return errors.New("expected target span")
				}
				reason := []string{"initial"}
				attributes := confAttrs("reason", reason)
				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{
					Name:  "linked",
					Links: []telemetry.SpanLink{{Context: target, Attributes: attributes}},
				}, func(span telemetry.TelemetrySpan) (any, error) {
					reason = append(reason, "mutated")
					attributes.Set("reason", reason)
					span.AddLink(telemetry.SpanLink{Context: target, Attributes: confAttrs("reason", "late")})
					span.AddLink(telemetry.SpanLink{Context: telemetry.NoopTelemetryContext})
					return nil, nil
				}); err != nil {
					return err
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				linked, err := confFindSpan(spans, "linked")
				if err != nil {
					return err
				}
				targetSpan, err := confFindSpan(spans, "target")
				if err != nil {
					return err
				}
				if !linked.ParentId.IsNull() {
					return fmt.Errorf("expected null parentId, got %v", linked.ParentId)
				}
				if !linked.ParentContext.IsNull() {
					return fmt.Errorf("expected null parentContext, got %v", linked.ParentContext)
				}
				wantLinks := []telemetry.RecordedTelemetryLink{
					{Context: targetSpan.Context, Attributes: confAttrs("reason", []string{"initial"})},
					{Context: targetSpan.Context, Attributes: confAttrs("reason", "late")},
				}
				if !confEqualLinks(linked.Links, wantLinks) {
					return fmt.Errorf("links mismatch: got %#v, want %#v", linked.Links, wantLinks)
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"links",
			"ignores links added after source settlement",
			func(fixture TelemetryAdapterFixture) error {
				var source telemetry.TelemetrySpan
				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "link-source"}, func(span telemetry.TelemetrySpan) (any, error) {
					source = span
					return nil, nil
				}); err != nil {
					return err
				}
				if source == nil {
					return errors.New("expected source span")
				}
				source.AddLink(telemetry.SpanLink{Context: source})
				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				linkSource, err := confFindSpan(spans, "link-source")
				if err != nil {
					return err
				}
				if len(linkSource.Links) != 0 {
					return fmt.Errorf("expected empty links, got %#v", linkSource.Links)
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"propagation",
			"roundtrips parent identity through JSON without retaining the carrier",
			func(fixture TelemetryAdapterFixture) error {
				if fixture.Propagation == nil {
					return nil
				}
				propagation := fixture.Propagation
				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "sender"}, func(span telemetry.TelemetrySpan) (any, error) {
					carrier := propagation.Inject(span)
					if carrier == nil {
						return nil, errors.New("expected injected carrier")
					}
					wire, err := confRoundtripCarrier(carrier)
					if err != nil {
						return nil, err
					}
					parent := propagation.Extract(wire)
					for _, key := range wire.Keys() {
						wire.Set(key, "mutated")
					}
					if _, err := parent.StartSpan(&telemetry.SpanOptions{Name: "receiver"}, func(telemetry.TelemetrySpan) (any, error) {
						return 42, nil
					}); err != nil {
						return nil, err
					}
					if !confEqualCarrier(propagation.Inject(span), carrier) {
						return nil, errors.New("expected inject(span) to remain unchanged after wire mutation")
					}
					return nil, nil
				}); err != nil {
					return err
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				receiver, err := confFindSpan(spans, "receiver")
				if err != nil {
					return err
				}
				sender, err := confFindSpan(spans, "sender")
				if err != nil {
					return err
				}
				if !confEqualOptCarrier(receiver.ParentContext, jsonx.Some(sender.Context)) {
					return fmt.Errorf("receiver parentContext %v != sender context %v", receiver.ParentContext, sender.Context)
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"propagation",
			"absent and unknown carriers produce recorded roots",
			func(fixture TelemetryAdapterFixture) error {
				if fixture.Propagation == nil {
					return nil
				}
				if got := fixture.Propagation.Inject(telemetry.NoopTelemetryContext); got != nil {
					return fmt.Errorf("expected nil carrier for noop context, got %v", got)
				}
				unknown := omap.NewMap[string, string]()
				unknown.Set("unknown", "format")
				carriers := []telemetry.TelemetryCarrier{
					nil,
					omap.NewMap[string, string](),
					unknown,
				}
				for index, carrier := range carriers {
					ctx := fixture.Propagation.Extract(carrier)
					res, err := ctx.StartSpan(&telemetry.SpanOptions{Name: fmt.Sprintf("fallback-%d", index)}, func(telemetry.TelemetrySpan) (any, error) {
						return 42, nil
					})
					if err != nil {
						return err
					}
					if res != 42 {
						return fmt.Errorf("expected 42, got %#v", res)
					}
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				for _, span := range spans {
					if !span.ParentContext.IsNull() {
						return fmt.Errorf("expected null parentContext for %s, got %v", span.Name, span.ParentContext)
					}
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"propagation",
			"propagates across fresh adapters and multiple hops",
			func(fixture TelemetryAdapterFixture) (retErr error) {
				if fixture.Propagation == nil {
					return nil
				}
				receiver, err := factory()
				if err != nil {
					return err
				}
				if receiver.AsyncDispose != nil {
					defer func() {
						if dErr := receiver.AsyncDispose(); retErr == nil {
							retErr = dErr
						}
					}()
				}
				worker, err := factory()
				if err != nil {
					return err
				}
				if worker.AsyncDispose != nil {
					defer func() {
						if dErr := worker.AsyncDispose(); retErr == nil {
							retErr = dErr
						}
					}()
				}
				if receiver.Propagation == nil || worker.Propagation == nil {
					return errors.New("expected propagation on each fixture")
				}
				var senderCarrier telemetry.TelemetryCarrier
				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "sender"}, func(span telemetry.TelemetrySpan) (any, error) {
					senderCarrier = fixture.Propagation.Inject(span)
					return nil, nil
				}); err != nil {
					return err
				}
				if senderCarrier == nil {
					return errors.New("expected senderCarrier")
				}
				senderWire, err := confRoundtripCarrier(senderCarrier)
				if err != nil {
					return err
				}
				var receiverCarrier telemetry.TelemetryCarrier
				if _, err := receiver.Propagation.Extract(senderWire).StartSpan(&telemetry.SpanOptions{Name: "receiver"}, func(span telemetry.TelemetrySpan) (any, error) {
					receiverCarrier = receiver.Propagation.Inject(span)
					return nil, nil
				}); err != nil {
					return err
				}
				if receiverCarrier == nil {
					return errors.New("expected receiverCarrier")
				}
				receiverWire, err := confRoundtripCarrier(receiverCarrier)
				if err != nil {
					return err
				}
				if _, err := worker.Propagation.Extract(receiverWire).StartSpan(&telemetry.SpanOptions{Name: "worker"}, func(telemetry.TelemetrySpan) (any, error) {
					return nil, nil
				}); err != nil {
					return err
				}
				receiverSpans, err := receiver.GetSpans()
				if err != nil {
					return err
				}
				receiverSpan, err := confFindSpan(receiverSpans, "receiver")
				if err != nil {
					return err
				}
				if !confEqualOptCarrier(receiverSpan.ParentContext, jsonx.Some(senderCarrier)) {
					return fmt.Errorf("receiver parentContext %v != senderCarrier %v", receiverSpan.ParentContext, senderCarrier)
				}
				workerSpans, err := worker.GetSpans()
				if err != nil {
					return err
				}
				workerSpan, err := confFindSpan(workerSpans, "worker")
				if err != nil {
					return err
				}
				if !confEqualOptCarrier(workerSpan.ParentContext, jsonx.Some(receiverCarrier)) {
					return fmt.Errorf("worker parentContext %v != receiverCarrier %v", workerSpan.ParentContext, receiverCarrier)
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"callback lifecycle",
			"admits once synchronously and preserves the result",
			func(fixture TelemetryAdapterFixture) error {
				admitted := false
				calls := 0
				expected := &confResult{Value: 42}
				result, err := fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "success"}, func(telemetry.TelemetrySpan) (any, error) {
					admitted = true
					calls++
					return expected, nil
				})
				if !admitted {
					return errors.New("expected callback to be admitted synchronously")
				}
				if calls != 1 {
					return fmt.Errorf("expected 1 call, got %d", calls)
				}
				if err != nil {
					return err
				}
				if result != expected {
					return fmt.Errorf("expected exact result %p, got %#v", expected, result)
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				span, err := confFindSpan(spans, "success")
				if err != nil {
					return err
				}
				if !confEqualStatus(span.Status, &telemetry.SpanStatusOk{Status: telemetry.SpanStatusKindOk}) {
					return fmt.Errorf("expected ok status, got %#v", span.Status)
				}
				if !span.Settled {
					return errors.New("expected settled span")
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"callback lifecycle",
			"preserves synchronous and asynchronous rejection values",
			func(fixture TelemetryAdapterFixture) error {
				syncError := errors.New("sync")
				if err := confRejectsWithSameValue(func() (any, error) {
					return fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "sync-error"}, func(telemetry.TelemetrySpan) (any, error) {
						return nil, syncError
					})
				}, syncError); err != nil {
					return err
				}

				asyncError := &confAsyncError{Kind: "async"}
				if err := confRejectsWithSameValue(func() (any, error) {
					return fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "async-error"}, func(telemetry.TelemetrySpan) (any, error) {
						return nil, asyncError
					})
				}, asyncError); err != nil {
					return err
				}

				emptyError := errors.New("")
				if err := confRejectsWithSameValue(func() (any, error) {
					return fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "undefined-error"}, func(telemetry.TelemetrySpan) (any, error) {
						return nil, emptyError
					})
				}, emptyError); err != nil {
					return err
				}

				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				for _, name := range []string{"sync-error", "async-error", "undefined-error"} {
					span, err := confFindSpan(spans, name)
					if err != nil {
						return err
					}
					if st, ok := span.Status.(*telemetry.SpanStatusError); !ok || st == nil || st.Status != telemetry.SpanStatusKindError {
						return fmt.Errorf("expected error status for %s, got %#v", name, span.Status)
					}
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"status",
			"uses last explicit status without automatic overwrite",
			func(fixture TelemetryAdapterFixture) error {
				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "last-status"}, func(span telemetry.TelemetrySpan) (any, error) {
					span.SetStatus(&telemetry.SpanStatusError{
						Status: telemetry.SpanStatusKindError,
						Error:  &telemetry.SpanError{Name: "Expected", Message: "first"},
					})
					span.SetStatus(&telemetry.SpanStatusOk{Status: telemetry.SpanStatusKindOk})
					return nil, nil
				}); err != nil {
					return err
				}

				thrown := errors.New("after explicit status")
				if err := confRejectsWithSameValue(func() (any, error) {
					return fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "explicit-before-throw"}, func(span telemetry.TelemetrySpan) (any, error) {
						span.SetStatus(&telemetry.SpanStatusOk{Status: telemetry.SpanStatusKindOk})
						return nil, thrown
					})
				}, thrown); err != nil {
					return err
				}

				rejected := errors.New("after async explicit status")
				if err := confRejectsWithSameValue(func() (any, error) {
					return fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "explicit-before-rejection"}, func(span telemetry.TelemetrySpan) (any, error) {
						span.SetStatus(&telemetry.SpanStatusError{
							Status: telemetry.SpanStatusKindError,
							Error:  &telemetry.SpanError{Name: "Expected", Message: "async failure"},
						})
						return nil, rejected
					})
				}, rejected); err != nil {
					return err
				}

				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "expected-failure"}, func(span telemetry.TelemetrySpan) (any, error) {
					span.SetStatus(&telemetry.SpanStatusError{
						Status: telemetry.SpanStatusKindError,
						Error:  &telemetry.SpanError{Name: "Expected", Message: "returned failure"},
					})
					return map[string]bool{"ok": false}, nil
				}); err != nil {
					return err
				}

				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				lastStatus, err := confFindSpan(spans, "last-status")
				if err != nil {
					return err
				}
				if !confEqualStatus(lastStatus.Status, &telemetry.SpanStatusOk{Status: telemetry.SpanStatusKindOk}) {
					return fmt.Errorf("last-status status mismatch: %#v", lastStatus.Status)
				}
				explicitBeforeThrow, err := confFindSpan(spans, "explicit-before-throw")
				if err != nil {
					return err
				}
				if !confEqualStatus(explicitBeforeThrow.Status, &telemetry.SpanStatusOk{Status: telemetry.SpanStatusKindOk}) {
					return fmt.Errorf("explicit-before-throw status mismatch: %#v", explicitBeforeThrow.Status)
				}
				explicitBeforeRejection, err := confFindSpan(spans, "explicit-before-rejection")
				if err != nil {
					return err
				}
				wantAsyncStatus := &telemetry.SpanStatusError{
					Status: telemetry.SpanStatusKindError,
					Error:  &telemetry.SpanError{Name: "Expected", Message: "async failure"},
				}
				if !confEqualStatus(explicitBeforeRejection.Status, wantAsyncStatus) {
					return fmt.Errorf("explicit-before-rejection status mismatch: %#v", explicitBeforeRejection.Status)
				}
				expectedFailure, err := confFindSpan(spans, "expected-failure")
				if err != nil {
					return err
				}
				wantReturnedStatus := &telemetry.SpanStatusError{
					Status: telemetry.SpanStatusKindError,
					Error:  &telemetry.SpanError{Name: "Expected", Message: "returned failure"},
				}
				if !confEqualStatus(expectedFailure.Status, wantReturnedStatus) {
					return fmt.Errorf("expected-failure status mismatch: %#v", expectedFailure.Status)
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"recording",
			"merges attributes and records ordered events",
			func(fixture TelemetryAdapterFixture) error {
				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{
					Name:       "recording",
					Attributes: confAttrs("start", "value", "overwrite", "start", "ignored", nil),
				}, func(span telemetry.TelemetrySpan) (any, error) {
					span.SetAttributes(confAttrs("count", 1, "overwrite", "middle"))
					span.SetAttributes(confAttrs("count", nil, "overwrite", "end"))
					span.AddEvent("first", confAttrs("index", 1, "ignored", nil))
					span.AddEvent("second", confAttrs("index", 2))
					return nil, nil
				}); err != nil {
					return err
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				span, err := confFindSpan(spans, "recording")
				if err != nil {
					return err
				}
				wantAttrs := confAttrs("start", "value", "overwrite", "end", "count", 1)
				if !confEqualAttributes(span.Attributes, wantAttrs) {
					return fmt.Errorf("attributes mismatch: got %v, want %v", span.Attributes, wantAttrs)
				}
				wantEvents := []telemetry.RecordedTelemetryEvent{
					{Name: "first", Attributes: confAttrs("index", 1)},
					{Name: "second", Attributes: confAttrs("index", 2)},
				}
				if !confEqualEvents(span.Events, wantEvents) {
					return fmt.Errorf("events mismatch: got %#v, want %#v", span.Events, wantEvents)
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"recording",
			"makes calls after settlement inert",
			func(fixture TelemetryAdapterFixture) error {
				var settledSpan telemetry.TelemetrySpan
				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{
					Name:       "settled",
					Attributes: confAttrs("value", "initial"),
				}, func(span telemetry.TelemetrySpan) (any, error) {
					settledSpan = span
					return nil, nil
				}); err != nil {
					return err
				}
				if settledSpan == nil {
					return errors.New("expected callback span")
				}
				settledSpan.SetAttributes(confAttrs("value", "late"))
				settledSpan.AddEvent("late", confAttrs("value", true))
				settledSpan.SetStatus(&telemetry.SpanStatusError{Status: telemetry.SpanStatusKindError})
				childAdmitted := false
				childResult, err := settledSpan.StartSpan(&telemetry.SpanOptions{Name: "late-child"}, func(telemetry.TelemetrySpan) (any, error) {
					childAdmitted = true
					return 7, nil
				})
				if err != nil {
					return err
				}
				if !childAdmitted {
					return errors.New("expected child span callback to be admitted")
				}
				if childResult != 7 {
					return fmt.Errorf("expected childResult 7, got %#v", childResult)
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				if len(spans) != 1 {
					return fmt.Errorf("expected 1 span, got %d", len(spans))
				}
				if !confEqualAttributes(spans[0].Attributes, confAttrs("value", "initial")) {
					return fmt.Errorf("attributes changed after settlement: %v", spans[0].Attributes)
				}
				if len(spans[0].Events) != 0 {
					return fmt.Errorf("expected empty events after settlement, got %#v", spans[0].Events)
				}
				if !confEqualStatus(spans[0].Status, &telemetry.SpanStatusOk{Status: telemetry.SpanStatusKindOk}) {
					return fmt.Errorf("status changed after settlement: %#v", spans[0].Status)
				}
				return nil
			},
		),

		confCreateCase(
			factory,
			"parentage",
			"records nested and concurrent child relationships",
			func(fixture TelemetryAdapterFixture) error {
				firstStarted := make(chan struct{})
				firstGate := make(chan struct{})
				firstDone := make(chan error, 1)

				if _, err := fixture.Context.StartSpan(&telemetry.SpanOptions{Name: "parent"}, func(parent telemetry.TelemetrySpan) (any, error) {
					go func() {
						_, childErr := parent.StartSpan(&telemetry.SpanOptions{Name: "first-child"}, func(telemetry.TelemetrySpan) (any, error) {
							close(firstStarted)
							<-firstGate
							return nil, nil
						})
						firstDone <- childErr
					}()
					<-firstStarted
					second, err := parent.StartSpan(&telemetry.SpanOptions{Name: "second-child"}, func(telemetry.TelemetrySpan) (any, error) {
						return "done", nil
					})
					if err != nil {
						close(firstGate)
						<-firstDone
						return nil, err
					}
					if second != "done" {
						close(firstGate)
						<-firstDone
						return nil, fmt.Errorf("expected second-child result done, got %#v", second)
					}
					close(firstGate)
					return nil, <-firstDone
				}); err != nil {
					return err
				}

				spans, err := fixture.GetSpans()
				if err != nil {
					return err
				}
				parent, err := confFindSpan(spans, "parent")
				if err != nil {
					return err
				}
				first, err := confFindSpan(spans, "first-child")
				if err != nil {
					return err
				}
				second, err := confFindSpan(spans, "second-child")
				if err != nil {
					return err
				}
				if !parent.ParentId.IsNull() {
					return fmt.Errorf("expected null parentId, got %v", parent.ParentId)
				}
				firstParentId, ok := first.ParentId.Get()
				if !ok || firstParentId != parent.Id {
					return fmt.Errorf("expected first-child parentId %d, got %v", parent.Id, first.ParentId)
				}
				secondParentId, ok := second.ParentId.Get()
				if !ok || secondParentId != parent.Id {
					return fmt.Errorf("expected second-child parentId %d, got %v", parent.Id, second.ParentId)
				}
				if second.EndSequence == nil || first.EndSequence == nil || parent.EndSequence == nil {
					return errors.New("expected non-nil endSequence on all settled spans")
				}
				if *second.EndSequence >= *first.EndSequence {
					return fmt.Errorf("expected second endSequence (%d) < first (%d)", *second.EndSequence, *first.EndSequence)
				}
				if *first.EndSequence >= *parent.EndSequence {
					return fmt.Errorf("expected first endSequence (%d) < parent (%d)", *first.EndSequence, *parent.EndSequence)
				}
				return nil
			},
		),
	}
}
