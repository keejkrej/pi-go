// Ported from packages/telemetry/src/noop.ts (pi v1.0.0).

package telemetry

// noopSpan is the shared frozen inert span.
type noopSpan struct{}

func (*noopSpan) StartSpan(options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	panic("unported: StartSpan")
}

func (*noopSpan) AddEvent(name string, attributes SpanAttributes) {}

func (*noopSpan) SetAttributes(attributes SpanAttributes) {}

func (*noopSpan) SetStatus(status SpanStatus) {}

var noopTelemetrySpan TelemetrySpan = &noopSpan{}

// NoopTelemetryContext is the shared telemetry context used when an application does not provide one.
var NoopTelemetryContext TelemetryContext = noopTelemetrySpan

var _ TelemetrySpan = (*noopSpan)(nil)
