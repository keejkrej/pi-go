// Ported from packages/telemetry/src/testing/types.ts (pi v1.0.0).

package telemetrytest

import "github.com/keejkrej/pi-go/telemetry"

// TelemetryAdapterFixture is a fresh adapter instance and normalized snapshot reader owned by one conformance case.
// AsyncDispose is Symbol.asyncDispose. Nil AsyncDispose means the fixture has nothing to release.
type TelemetryAdapterFixture struct {
	Context      telemetry.TelemetryContext
	GetSpans     func() ([]telemetry.RecordedTelemetrySpan, error)
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
