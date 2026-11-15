package telemetrytest_test

import (
	"testing"

	"github.com/keejkrej/pi-go/telemetry"
	"github.com/keejkrej/pi-go/telemetry/telemetrytest"
)

func TestTelemetrytest_SkipsOptionalMetricsAndPropagationAndDisposesFixtures(t *testing.T) {
	disposed := 0
	cases := telemetrytest.CreateTelemetryAdapterConformance(func() (telemetrytest.TelemetryAdapterFixture, error) {
		ctx := telemetry.NewInMemoryTelemetryContext()
		return telemetrytest.TelemetryAdapterFixture{
			Context: ctx,
			GetSpans: func() ([]telemetry.RecordedTelemetrySpan, error) {
				return ctx.GetSpans(), nil
			},
			AsyncDispose: func() error {
				disposed++
				return nil
			},
		}, nil
	})
	for _, tc := range cases {
		t.Run(tc.Group+"/"+tc.Name, func(t *testing.T) {
			if err := tc.Run(); err != nil {
				t.Fatal(err)
			}
		})
	}
	// 14 cases + 2 additional fixtures created inside the multi-hop propagation check? Wait: when Propagation==nil multi-hop skips!
	// Wait: without Propagation, propagation cases return early, so only 1 fixture per case is disposed.
	if disposed != len(cases) {
		t.Fatalf("expected %d disposes, got %d", len(cases), disposed)
	}
}
