// Ported from packages/telemetry/test/store.test.ts (pi v1.5.2).

package telemetry_test

import (
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/telemetry"
	"github.com/keejkrej/pi-go/telemetry/telemetrytest"
)

func tstoreNewFixture() (telemetrytest.StoreFixture, error) {
	ctx := telemetry.NewInMemoryTelemetryContext()
	return telemetrytest.StoreFixture{
		Context:      ctx,
		Store:        ctx,
		Flush:        func() error { return nil },
		AsyncDispose: func() error { return nil },
	}, nil
}

func TestStore_InMemoryConformance(t *testing.T) {
	for _, tc := range telemetrytest.CreateTelemetryStoreConformance(tstoreNewFixture) {
		t.Run(tc.Name, func(t *testing.T) {
			if err := tc.Run(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStore_RecordsWallClockBoundariesAndRelativeEventTimes(t *testing.T) {
	clockVal := 100.0
	restore := telemetry.MemoryOverrideClock(
		func() float64 { return 1000 },
		func() float64 { return clockVal },
	)
	defer restore()

	adapter := telemetry.NewInMemoryTelemetryContext()
	id := ""
	if _, err := adapter.StartSpan(&telemetry.SpanOptions{Name: "timed"}, func(span telemetry.TelemetrySpan) (any, error) {
		carrier := adapter.Inject(span)
		scope, _ := carrier.Get("pi-memory-scope")
		spanId, _ := carrier.Get("pi-memory-span")
		id = scope + ":" + spanId
		clockVal = 110
		span.AddEvent("event", thlpAttrs("count", 1))
		clockVal = 125
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	gotTrace, err := adapter.GetTrace(id)
	if err != nil {
		t.Fatal(err)
	}
	wantTrace := &telemetry.StoredTrace{
		TraceId: id,
		Spans: []telemetry.StoredSpan{
			{
				Id:         id,
				TraceId:    id,
				Name:       "timed",
				StartedMs:  1000,
				EndedMs:    1025,
				Attributes: thlpAttrs(),
				Events:     []telemetry.StoredSpanEvent{{Name: "event", TimeMs: 10, Attributes: thlpAttrs("count", 1)}},
				Links:      []telemetry.StoredSpanLink{},
				Status:     &telemetry.SpanStatusOk{Status: telemetry.SpanStatusKindOk},
			},
		},
	}
	if diff := cmp.Diff(wantTrace, gotTrace, thlpCmpOpts()...); diff != "" {
		t.Fatalf("trace mismatch (-want +got):\n%s", diff)
	}

	wantEvents := []telemetry.RecordedTelemetryEvent{
		{Name: "event", Attributes: thlpAttrs("count", 1)},
	}
	if diff := cmp.Diff(wantEvents, adapter.GetSpans()[0].Events, thlpCmpOpts()...); diff != "" {
		t.Fatalf("recorded events mismatch (-want +got):\n%s", diff)
	}
}

func TestStore_StampsStartsWithTelemetryNowToTheMicrosecondInOrder(t *testing.T) {
	clockVal := 100.0
	restore := telemetry.MemoryOverrideClock(
		func() float64 { return 1000 },
		func() float64 { return clockVal },
	)
	defer restore()

	adapter := telemetry.NewInMemoryTelemetryContext()
	if _, err := adapter.StartSpan(&telemetry.SpanOptions{Name: "first"}, func(telemetry.TelemetrySpan) (any, error) {
		clockVal = 100.25
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.StartSpan(&telemetry.SpanOptions{Name: "second"}, func(telemetry.TelemetrySpan) (any, error) {
		clockVal = 100.5
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	res, err := adapter.FindTraces(nil)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		name      string
		startedMs float64
		endedMs   float64
	}
	var got []row
	for _, tr := range res.Traces {
		for _, sp := range tr.Spans {
			got = append(got, row{name: sp.Name, startedMs: sp.StartedMs, endedMs: sp.EndedMs})
		}
	}
	want := []row{
		{name: "second", startedMs: 1000.25, endedMs: 1000.5},
		{name: "first", startedMs: 1000, endedMs: 1000.25},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(row{})); diff != "" {
		t.Fatalf("span timing order mismatch (-want +got):\n%s", diff)
	}
}

func TestStore_MapsRawMetricMeasurementsToCountOneRows(t *testing.T) {
	restore := telemetry.MemoryOverrideClock(func() float64 { return 20_001 }, nil)
	defer restore()

	adapter := telemetry.NewInMemoryTelemetryContext()
	adapter.Counter("calls", nil).Add(2, nil)
	adapter.Counter("calls", nil).Add(3, nil)

	rows, err := adapter.FindMetrics(telemetry.MetricQuery{Name: "calls"})
	if err != nil {
		t.Fatal(err)
	}
	type metricRow struct {
		intervalMs int64
		count      int
		sum        float64
	}
	gotRows := make([]metricRow, len(rows))
	for i, r := range rows {
		gotRows[i] = metricRow{intervalMs: r.IntervalMs, count: r.Count, sum: r.Sum}
	}
	wantRows := []metricRow{
		{intervalMs: 20_000, count: 1, sum: 2},
		{intervalMs: 20_000, count: 1, sum: 3},
	}
	if diff := cmp.Diff(wantRows, gotRows, cmp.AllowUnexported(metricRow{})); diff != "" {
		t.Fatalf("metric rows mismatch (-want +got):\n%s", diff)
	}

	wantRaw := []telemetry.RecordedTelemetryMeasurement{
		{Kind: telemetry.TelemetryMetricKindCounter, Name: "calls", Value: 2, Attributes: thlpAttrs()},
		{Kind: telemetry.TelemetryMetricKindCounter, Name: "calls", Value: 3, Attributes: thlpAttrs()},
	}
	// Null context on unlinked measurements.
	gotRaw := adapter.GetMetrics()
	for i := range wantRaw {
		if !gotRaw[i].Context.IsNull() {
			t.Fatalf("expected null context on measurement %d, got %v", i, gotRaw[i].Context)
		}
		if gotRaw[i].Kind != wantRaw[i].Kind || gotRaw[i].Name != wantRaw[i].Name || gotRaw[i].Value != wantRaw[i].Value {
			t.Fatalf("unexpected measurement[%d]: %#v", i, gotRaw[i])
		}
		if diff := cmp.Diff(wantRaw[i].Attributes, gotRaw[i].Attributes, thlpCmpOpts()...); diff != "" {
			t.Fatalf("measurement[%d] attributes mismatch (-want +got):\n%s", i, diff)
		}
	}
}

func TestStore_DetachesLinkAttributesAndErrorDetails(t *testing.T) {
	adapter := telemetry.NewInMemoryTelemetryContext()
	var target telemetry.TelemetrySpan
	if _, err := adapter.StartSpan(&telemetry.SpanOptions{Name: "target"}, func(span telemetry.TelemetrySpan) (any, error) {
		target = span
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.StartSpan(&telemetry.SpanOptions{
		Name:  "source",
		Links: []telemetry.SpanLink{{Context: target, Attributes: thlpAttrs("tags", []string{"original"})}},
	}, func(span telemetry.TelemetrySpan) (any, error) {
		span.SetStatus(&telemetry.SpanStatusError{
			Status: telemetry.SpanStatusKindError,
			Error:  &telemetry.SpanError{Name: "Expected", Message: "original"},
		})
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	res, err := adapter.FindTraces(&telemetry.TraceQuery{Name: thlpStrPtr("source")})
	if err != nil {
		t.Fatal(err)
	}
	tr := res.Traces[0]
	span := tr.Spans[0]
	if tags, ok := span.Links[0].Attributes.Get("tags"); ok {
		if s, ok := tags.([]string); ok && len(s) > 0 {
			s[0] = "changed"
		}
	}
	span.Links[0].Attributes.Set("tags", []string{"changed"})
	if errStatus, ok := span.Status.(*telemetry.SpanStatusError); ok && errStatus.Error != nil {
		errStatus.Error.Message = "changed"
	}

	freshTrace, err := adapter.GetTrace(tr.TraceId)
	if err != nil {
		t.Fatal(err)
	}
	fresh := freshTrace.Spans[0]
	gotTags, _ := fresh.Links[0].Attributes.Get("tags")
	if diff := cmp.Diff([]string{"original"}, gotTags); diff != "" {
		t.Fatalf("link tags mutated (-want +got):\n%s", diff)
	}
	wantStatus := &telemetry.SpanStatusError{
		Status: telemetry.SpanStatusKindError,
		Error:  &telemetry.SpanError{Name: "Expected", Message: "original"},
	}
	if diff := cmp.Diff(wantStatus, fresh.Status); diff != "" {
		t.Fatalf("status mutated (-want +got):\n%s", diff)
	}
}

func TestStore_TelemetryNowCountsSubMillisecondStepsWithinSameMillisecond(t *testing.T) {
	clockVal := 1000.25
	restore := telemetry.MemoryOverrideClock(
		func() float64 { return 50_000 },
		func() float64 { return clockVal },
	)
	defer restore()

	first := telemetry.TelemetryNow()
	clockVal = 1000.2504
	if diff := math.Abs(telemetry.TelemetryNow() - first); diff > 1e-6 {
		t.Fatalf("expected ~0ms delta, got %v", diff)
	}
	clockVal = 1000.7512
	if diff := math.Abs((telemetry.TelemetryNow() - first) - 0.501); diff > 1e-6 {
		t.Fatalf("expected ~0.501ms delta, got %v", telemetry.TelemetryNow()-first)
	}
	now := telemetry.TelemetryNow()
	if now*1000 != math.Trunc(now*1000) {
		t.Fatalf("expected TelemetryNow()*1000 to be an integer, got %v", now*1000)
	}
}

func TestStore_TelemetryNowFollowsWallClockStepPast10Ms(t *testing.T) {
	wallVal := 80_000.0
	clockVal := 1000.0
	restore := telemetry.MemoryOverrideClock(
		func() float64 { return wallVal },
		func() float64 { return clockVal },
	)
	defer restore()

	if got := telemetry.TelemetryNow(); got != 80_000 {
		t.Fatalf("got %v, want 80000", got)
	}
	clockVal = 1003.5
	wallVal = 80_005
	if got := telemetry.TelemetryNow(); got != 80_003.5 {
		t.Fatalf("got %v, want 80003.5", got)
	}
	clockVal = 1004
	wallVal = 3_650_004
	if got := telemetry.TelemetryNow(); got != 3_650_004 {
		t.Fatalf("got %v, want 3650004", got)
	}
	clockVal = 1004.125
	if got := telemetry.TelemetryNow(); got != 3_650_004.125 {
		t.Fatalf("got %v, want 3650004.125", got)
	}
}
