// Ported from packages/telemetry/src/noop.ts (pi v1.0.0).

package telemetry

type noopCounterImpl struct{}

func (*noopCounterImpl) Add(value float64, attributes SpanAttributes) {}

type noopUpDownCounterImpl struct{}

func (*noopUpDownCounterImpl) Add(value float64, attributes SpanAttributes) {}

type noopHistogramImpl struct{}

func (*noopHistogramImpl) Record(value float64, attributes SpanAttributes, context TelemetryContext) {
}

var (
	noopCounter       TelemetryCounter       = &noopCounterImpl{}
	noopUpDownCounter TelemetryUpDownCounter = &noopUpDownCounterImpl{}
	noopHistogram     TelemetryHistogram     = &noopHistogramImpl{}
)

type noopMetricsImpl struct{}

func (*noopMetricsImpl) Counter(name string, options *InstrumentOptions) TelemetryCounter {
	return noopCounter
}

func (*noopMetricsImpl) UpDownCounter(name string, options *InstrumentOptions) TelemetryUpDownCounter {
	return noopUpDownCounter
}

func (*noopMetricsImpl) Histogram(name string, options *HistogramOptions) TelemetryHistogram {
	return noopHistogram
}

// NoopTelemetryMetrics provides shared instruments that never inspect or retain metadata, measurements, or contexts.
var NoopTelemetryMetrics TelemetryMetrics = &noopMetricsImpl{}

type noopPropagationImpl struct{}

func (*noopPropagationImpl) Inject(context TelemetryContext) TelemetryCarrier {
	return nil
}

func (*noopPropagationImpl) Extract(carrier TelemetryCarrier) TelemetryContext {
	return NoopTelemetryContext
}

// NoopTelemetryPropagation is a shared propagation capability that neither inspects nor retains its inputs.
var NoopTelemetryPropagation TelemetryPropagation = &noopPropagationImpl{}

// noopSpan is the shared frozen inert span.
type noopSpan struct{}

func (*noopSpan) Metrics() TelemetryMetrics {
	return NoopTelemetryMetrics
}

func (*noopSpan) Propagation() TelemetryPropagation {
	return NoopTelemetryPropagation
}

func (*noopSpan) StartSpan(options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	return callback(noopTelemetrySpan)
}

func (*noopSpan) AddLink(link SpanLink) {}

func (*noopSpan) AddEvent(name string, attributes SpanAttributes) {}

func (*noopSpan) SetAttributes(attributes SpanAttributes) {}

func (*noopSpan) SetStatus(status SpanStatus) {}

var noopTelemetrySpan TelemetrySpan = &noopSpan{}

// NoopTelemetryContext is the shared telemetry context used when an application does not provide one.
var NoopTelemetryContext TelemetrySpan = noopTelemetrySpan

var (
	_ TelemetryCounter       = (*noopCounterImpl)(nil)
	_ TelemetryUpDownCounter = (*noopUpDownCounterImpl)(nil)
	_ TelemetryHistogram     = (*noopHistogramImpl)(nil)
	_ TelemetryMetrics       = (*noopMetricsImpl)(nil)
	_ TelemetryPropagation   = (*noopPropagationImpl)(nil)
	_ TelemetrySpan          = (*noopSpan)(nil)
)
