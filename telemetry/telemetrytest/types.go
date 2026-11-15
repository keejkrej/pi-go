// Ported from packages/telemetry/src/testing/types.ts (pi v1.5.2).

package telemetrytest

import "github.com/keejkrej/pi-go/telemetry"

// TelemetryAdapterFixture is a fresh adapter instance and normalized snapshot reader owned by one conformance case.
// Optional Propagation/Metrics/GetMetrics fields may be nil when an adapter does not support those capabilities.
// AsyncDispose is Symbol.asyncDispose. Nil AsyncDispose means the fixture has nothing to release.
type TelemetryAdapterFixture struct {
	Context      telemetry.TelemetryContext
	Propagation  telemetry.TelemetryPropagation
	Metrics      telemetry.TelemetryMetrics
	GetSpans     func() ([]telemetry.RecordedTelemetrySpan, error)
	GetMetrics   func() ([]telemetry.RecordedTelemetryMeasurement, error)
	AsyncDispose func() error
}

// TelemetryAdapterFixtureFactory creates an isolated adapter fixture for one conformance case.
type TelemetryAdapterFixtureFactory func() (TelemetryAdapterFixture, error)

// TelemetryAdapterConformanceCase is a runner-independent conformance case that can be registered with any test framework.
type TelemetryAdapterConformanceCase struct {
	Group string
	Name  string
	Run   func() error
}
