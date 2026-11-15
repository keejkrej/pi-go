// Ported from packages/telemetry/test/metrics.test.ts (pi v1.5.2).

package telemetry_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/telemetry"
)

func tmetSchemas() (*telemetry.TelemetrySchemaDefinition, *telemetry.TelemetrySchemaDefinition) {
	low := telemetry.TelemetryCardinalityLow
	traffic := telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
		Version: 1,
		Spans:   thlpMap[telemetry.TelemetrySpanDefinition](),
		Metrics: thlpMap[telemetry.TelemetryMetricDefinition](
			"network.bytes", telemetry.TelemetryMetricDefinition{
				Kind:        telemetry.TelemetryMetricKindCounter,
				Description: "Encoded bytes",
				Unit:        thlpStrPtr("By"),
				Attributes: thlpMap[telemetry.TelemetryMetricAttributeDefinition](
					"direction", telemetry.TelemetryMetricAttributeDefinition{
						Type:        telemetry.TelemetryAttributeTypeString,
						Values:      []string{"in", "out"},
						Required:    true,
						Description: "Direction",
						Cardinality: &low,
					},
				),
			},
			"network.active", telemetry.TelemetryMetricDefinition{
				Kind:        telemetry.TelemetryMetricKindUpDownCounter,
				Description: "Open links",
				Attributes:  thlpMap[telemetry.TelemetryMetricAttributeDefinition](),
			},
		),
	})
	latency := telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
		Version: 1,
		Spans:   thlpMap[telemetry.TelemetrySpanDefinition](),
		Metrics: thlpMap[telemetry.TelemetryMetricDefinition](
			"call.duration", telemetry.TelemetryMetricDefinition{
				Kind:        telemetry.TelemetryMetricKindHistogram,
				Description: "Call duration",
				Unit:        thlpStrPtr("ms"),
				Boundaries:  []float64{1, 10, 100},
				Attributes: thlpMap[telemetry.TelemetryMetricAttributeDefinition](
					"method", telemetry.TelemetryMetricAttributeDefinition{
						Type:        telemetry.TelemetryAttributeTypeString,
						Required:    false,
						Description: "Method",
					},
				),
			},
		),
	})
	return traffic, latency
}

func TestMetrics_BindsSchemaNamesKindsMetadataAndExactAttributes(t *testing.T) {
	traffic, latency := tmetSchemas()
	adapter := telemetry.NewInMemoryTelemetryContext()
	metrics := telemetry.CreateTypedMetrics(adapter, []*telemetry.TelemetrySchemaDefinition{traffic, latency})

	metrics.Counter("network.bytes").Add(128, thlpAttrs("direction", "out"))
	metrics.UpDownCounter("network.active").Add(1, nil)
	metrics.UpDownCounter("network.active").Add(-1, nil)

	if _, err := adapter.StartSpan(&telemetry.SpanOptions{Name: "request"}, func(span telemetry.TelemetrySpan) (any, error) {
		metrics.Histogram("call.duration").Record(12, thlpAttrs("method", "read"), span)
		metrics.Histogram("call.duration").Record(13, nil, nil)
		metrics.Histogram("call.duration").Record(14, nil, span)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	measurements := adapter.GetMetrics()
	spans := adapter.GetSpans()
	want0 := telemetry.RecordedTelemetryMeasurement{
		Kind:        telemetry.TelemetryMetricKindCounter,
		Name:        "network.bytes",
		Description: thlpStrPtr("Encoded bytes"),
		Unit:        thlpStrPtr("By"),
		Value:       128,
		Attributes:  thlpAttrs("direction", "out"),
		Context:     jsonx.Null[telemetry.TelemetryCarrier](),
	}
	if diff := cmp.Diff(want0, measurements[0], thlpCmpOpts()...); diff != "" {
		t.Fatalf("measurement[0] mismatch (-want +got):\n%s", diff)
	}

	want3 := telemetry.RecordedTelemetryMeasurement{
		Kind:        telemetry.TelemetryMetricKindHistogram,
		Name:        "call.duration",
		Description: thlpStrPtr("Call duration"),
		Unit:        thlpStrPtr("ms"),
		Boundaries:  []float64{1, 10, 100},
		Value:       12,
		Attributes:  thlpAttrs("method", "read"),
		Context:     jsonx.Some(spans[0].Context),
	}
	if diff := cmp.Diff(want3, measurements[3], thlpCmpOpts()...); diff != "" {
		t.Fatalf("measurement[3] mismatch (-want +got):\n%s", diff)
	}
	if !measurements[4].Context.IsNull() {
		t.Fatalf("expected null measurement[4].Context, got %v", measurements[4].Context)
	}
	if diff := cmp.Diff(jsonx.Some(spans[0].Context), measurements[5].Context, thlpCmpOpts()...); diff != "" {
		t.Fatalf("measurement[5].Context mismatch (-want +got):\n%s", diff)
	}

	encoded, err := jsonx.Stringify(traffic)
	if err != nil {
		t.Fatalf("expected JSON.stringify(traffic) to succeed: %v", err)
	}
	if encoded == "" {
		t.Fatalf("expected non-empty traffic schema JSON")
	}
}

func TestMetrics_RequiresHistogramAttributesWhenSchemaDeclaresThem(t *testing.T) {
	schema := telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
		Version: 1,
		Spans:   thlpMap[telemetry.TelemetrySpanDefinition](),
		Metrics: thlpMap[telemetry.TelemetryMetricDefinition](
			"required", telemetry.TelemetryMetricDefinition{
				Kind:        telemetry.TelemetryMetricKindHistogram,
				Description: "Required tag",
				Attributes: thlpMap[telemetry.TelemetryMetricAttributeDefinition](
					"cache", telemetry.TelemetryMetricAttributeDefinition{
						Type:        telemetry.TelemetryAttributeTypeBoolean,
						Required:    true,
						Description: "Cache hit",
					},
				),
			},
		),
	})
	adapter := telemetry.NewInMemoryTelemetryContext()
	metrics := telemetry.CreateTypedMetrics(adapter, []*telemetry.TelemetrySchemaDefinition{schema})
	metrics.Histogram("required").Record(1, thlpAttrs("cache", true), nil)
	got := adapter.GetMetrics()
	if len(got) != 1 || got[0].Name != "required" {
		t.Fatalf("expected 1 measurement named required, got %#v", got)
	}
}

func TestMetrics_ReturnsDetachedMeasurementSnapshots(t *testing.T) {
	adapter := telemetry.NewInMemoryTelemetryContext()
	histogram := adapter.Histogram("duration", &telemetry.HistogramOptions{Boundaries: []float64{1, 2}})
	if _, err := adapter.StartSpan(&telemetry.SpanOptions{Name: "request"}, func(span telemetry.TelemetrySpan) (any, error) {
		histogram.Record(1, thlpAttrs("tags", []string{"original"}), span)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	measurement := adapter.GetMetrics()[0]
	ctx, ok := measurement.Context.Get()
	if !ok || ctx == nil {
		t.Fatalf("expected non-null measurement context")
	}
	measurement.Boundaries[0] = 3
	if tags, ok := measurement.Attributes.Get("tags"); ok {
		if s, ok := tags.([]string); ok && len(s) > 0 {
			s[0] = "mutated"
		}
	}
	measurement.Attributes.Set("tags", []string{"mutated"})
	ctx.Set("pi-memory-span", "999")

	fresh := adapter.GetMetrics()[0]
	if diff := cmp.Diff([]float64{1, 2}, fresh.Boundaries); diff != "" {
		t.Fatalf("boundaries mutated (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(thlpAttrs("tags", []string{"original"}), fresh.Attributes, thlpCmpOpts()...); diff != "" {
		t.Fatalf("attributes mutated (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(jsonx.Some(adapter.GetSpans()[0].Context), fresh.Context, thlpCmpOpts()...); diff != "" {
		t.Fatalf("context mutated (-want +got):\n%s", diff)
	}
}

func TestMetrics_SharesNoopInstruments(t *testing.T) {
	options := &telemetry.InstrumentOptions{Unit: thlpStrPtr("By")}
	attributes := thlpAttrs("direction", "out")
	counter := telemetry.NoopTelemetryMetrics.Counter("bytes", options)
	active := telemetry.NoopTelemetryMetrics.UpDownCounter("active", options)
	histogram := telemetry.NoopTelemetryMetrics.Histogram("duration", &telemetry.HistogramOptions{InstrumentOptions: *options})

	if counter != telemetry.NoopTelemetryMetrics.Counter("other", nil) {
		t.Fatalf("expected NoopTelemetryMetrics.Counter to share singleton")
	}
	if active != telemetry.NoopTelemetryMetrics.UpDownCounter("other", nil) {
		t.Fatalf("expected NoopTelemetryMetrics.UpDownCounter to share singleton")
	}
	if histogram != telemetry.NoopTelemetryMetrics.Histogram("other", nil) {
		t.Fatalf("expected NoopTelemetryMetrics.Histogram to share singleton")
	}
	counter.Add(1, attributes)
	active.Add(-1, attributes)
	histogram.Record(1, attributes, telemetry.NoopTelemetryContext)
}
