// Ported from packages/telemetry/src/testing/conformance.ts (pi v1.0.0).

package telemetrytest

import "github.com/keejkrej/pi-go/telemetry"

type confConformanceTest func(fixture TelemetryAdapterFixture) error

func confCreateCase(factory TelemetryAdapterFixtureFactory, group string, name string, test confConformanceTest) TelemetryAdapterConformanceCase {
	panic("unported: confCreateCase")
}

func confFindSpan(spans []telemetry.RecordedTelemetrySpan, name string) (telemetry.RecordedTelemetrySpan, error) {
	panic("unported: confFindSpan")
}

func confRejectsWithSameValue(run func() (any, error), expected any) error {
	panic("unported: confRejectsWithSameValue")
}

func confUnreadable(value any) any {
	panic("unported: confUnreadable")
}

// CreateTelemetryAdapterConformance creates runner-independent cases for the callback telemetry adapter contract.
func CreateTelemetryAdapterConformance(factory TelemetryAdapterFixtureFactory) []TelemetryAdapterConformanceCase {
	panic("unported: CreateTelemetryAdapterConformance")
}
