// Ported from packages/telemetry/test/conformance.test.ts (pi v1.5.2).

package telemetry_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/telemetry"
	"github.com/keejkrej/pi-go/telemetry/telemetrytest"
)

func tconfNewFixture() (telemetrytest.TelemetryAdapterFixture, error) {
	ctx := telemetry.NewInMemoryTelemetryContext()
	return telemetrytest.TelemetryAdapterFixture{
		Context:     ctx,
		Propagation: ctx,
		Metrics:     ctx,
		GetMetrics: func() ([]telemetry.RecordedTelemetryMeasurement, error) {
			return ctx.GetMetrics(), nil
		},
		GetSpans: func() ([]telemetry.RecordedTelemetrySpan, error) {
			return ctx.GetSpans(), nil
		},
		AsyncDispose: func() error { return nil },
	}, nil
}

func TestConformance_InMemoryTelemetryContextConformance(t *testing.T) {
	cases := telemetrytest.CreateTelemetryAdapterConformance(tconfNewFixture)
	var groups []string
	for _, tc := range cases {
		if !slices.Contains(groups, tc.Group) {
			groups = append(groups, tc.Group)
		}
	}
	for _, group := range groups {
		t.Run(group, func(t *testing.T) {
			for _, tc := range cases {
				if tc.Group != group {
					continue
				}
				t.Run(tc.Name, func(t *testing.T) {
					if err := tc.Run(); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestConformance_ReturnsDetachedSnapshotsWithoutExposingMutableRecordingState(t *testing.T) {
	ctx := telemetry.NewInMemoryTelemetryContext()
	var openSettled bool
	var openEndSequence *int

	if _, err := ctx.StartSpan(&telemetry.SpanOptions{
		Name:       "snapshot",
		Attributes: thlpAttrs("tags", []string{"initial"}),
	}, func(span telemetry.TelemetrySpan) (any, error) {
		span.AddEvent("event", thlpAttrs("value", 1))
		open := ctx.GetSpans()[0]
		openSettled = open.Settled
		openEndSequence = open.EndSequence
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	if openSettled {
		t.Fatalf("expected open.Settled to be false, got true")
	}
	if openEndSequence != nil {
		t.Fatalf("expected open.EndSequence to be nil, got %v", *openEndSequence)
	}

	first := ctx.GetSpans()[0]
	if !first.Settled {
		t.Fatalf("expected settled to be true")
	}
	if first.EndSequence == nil || *first.EndSequence != 1 {
		t.Fatalf("expected endSequence 1, got %v", first.EndSequence)
	}
	if tags, ok := first.Attributes.Get("tags"); ok {
		if slice, ok := tags.([]string); ok && len(slice) > 0 {
			slice[0] = "mutated"
		}
	}
	first.Attributes.Set("tags", []string{"mutated"})
	first.Events[0].Attributes.Set("value", 2)

	second := ctx.GetSpans()[0]
	if diff := cmp.Diff(thlpAttrs("tags", []string{"initial"}), second.Attributes, thlpCmpOpts()...); diff != "" {
		t.Fatalf("attributes mismatch (-want +got):\n%s", diff)
	}
	wantEvents := []telemetry.RecordedTelemetryEvent{
		{Name: "event", Attributes: thlpAttrs("value", 1)},
	}
	if diff := cmp.Diff(wantEvents, second.Events, thlpCmpOpts()...); diff != "" {
		t.Fatalf("events mismatch (-want +got):\n%s", diff)
	}
}
