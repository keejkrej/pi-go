// Ported from packages/telemetry/src/metrics.ts (pi v1.5.2).

package telemetry

import "github.com/keejkrej/pi-go/internal/omap"

// InstrumentOptions configures optional metadata for a metric instrument.
type InstrumentOptions struct {
	Description *string `json:"description,omitzero"`
	Unit        *string `json:"unit,omitzero"`
}

// HistogramOptions configures optional metadata and bucket hints for a histogram.
type HistogramOptions struct {
	InstrumentOptions
	// Boundaries are bucket hints; callers supply finite, strictly increasing values.
	Boundaries []float64 `json:"boundaries,omitzero"`
}

// TelemetryCounter records monotonic non-negative measurements.
type TelemetryCounter interface {
	Add(value float64, attributes SpanAttributes)
}

// TelemetryUpDownCounter records signed additive measurements.
type TelemetryUpDownCounter interface {
	Add(value float64, attributes SpanAttributes)
}

// TelemetryHistogram records value distributions with optional exemplar span context.
type TelemetryHistogram interface {
	Record(value float64, attributes SpanAttributes, context TelemetryContext)
}

// TelemetryMetrics is an optional adapter capability with synchronous factories and recording methods.
// Typed inputs are trusted; callers supply finite measurements and nonnegative counter increments.
// Malformed input behavior is unspecified.
type TelemetryMetrics interface {
	Counter(name string, options *InstrumentOptions) TelemetryCounter
	UpDownCounter(name string, options *InstrumentOptions) TelemetryUpDownCounter
	Histogram(name string, options *HistogramOptions) TelemetryHistogram
}

// TelemetryMetricKind is the kind of a declared metric instrument.
type TelemetryMetricKind string

const (
	TelemetryMetricKindCounter       TelemetryMetricKind = "counter"
	TelemetryMetricKindUpDownCounter TelemetryMetricKind = "upDownCounter"
	TelemetryMetricKindHistogram     TelemetryMetricKind = "histogram"
)

// TelemetryMetricAttributeDefinition is an attribute definition in a metric schema.
// Cardinality should be omitted or "low"; high-cardinality attributes do not belong on metrics.
type TelemetryMetricAttributeDefinition = TelemetryStartAttributeDefinition

// TelemetryMetricDefinition is a serializable metric instrument definition in a telemetry schema.
// Attributes must be non-nil so it marshals as an object.
type TelemetryMetricDefinition struct {
	Kind        TelemetryMetricKind                                   `json:"kind"`
	Description string                                                `json:"description"`
	Unit        *string                                               `json:"unit,omitzero"`
	Boundaries  []float64                                             `json:"boundaries,omitzero"`
	Attributes  *omap.Map[string, TelemetryMetricAttributeDefinition] `json:"attributes"`
}

// SchemaTelemetryCounter is a schema-constrained counter or up-down counter.
// Go erases TS attribute constraints at compile time; this alias keeps the export name.
type SchemaTelemetryCounter = TelemetryCounter

// SchemaTelemetryHistogram is a schema-constrained histogram.
// Go erases TS attribute constraints at compile time; this alias keeps the export name.
type SchemaTelemetryHistogram = TelemetryHistogram

// TypedTelemetryMetrics provides schema-bound instrument accessors over a TelemetryMetrics adapter.
type TypedTelemetryMetrics interface {
	Counter(name string) SchemaTelemetryCounter
	UpDownCounter(name string) SchemaTelemetryCounter
	Histogram(name string) SchemaTelemetryHistogram
}

type metricsTypedAdapter struct {
	metrics     TelemetryMetrics
	definitions map[string]TelemetryMetricDefinition
}

func (a *metricsTypedAdapter) Counter(name string) SchemaTelemetryCounter {
	var opts *InstrumentOptions
	if def, ok := a.definitions[name]; ok {
		desc := def.Description
		opts = &InstrumentOptions{
			Description: &desc,
			Unit:        def.Unit,
		}
	}
	return a.metrics.Counter(name, opts)
}

func (a *metricsTypedAdapter) UpDownCounter(name string) SchemaTelemetryCounter {
	var opts *InstrumentOptions
	if def, ok := a.definitions[name]; ok {
		desc := def.Description
		opts = &InstrumentOptions{
			Description: &desc,
			Unit:        def.Unit,
		}
	}
	return a.metrics.UpDownCounter(name, opts)
}

func (a *metricsTypedAdapter) Histogram(name string) SchemaTelemetryHistogram {
	var opts *HistogramOptions
	if def, ok := a.definitions[name]; ok {
		desc := def.Description
		opts = &HistogramOptions{
			InstrumentOptions: InstrumentOptions{
				Description: &desc,
				Unit:        def.Unit,
			},
			Boundaries: def.Boundaries,
		}
	}
	return a.metrics.Histogram(name, opts)
}

// CreateTypedMetrics binds instrument names, kinds, metadata, and measurement attributes to one or more schemas.
func CreateTypedMetrics(metrics TelemetryMetrics, schemas []*TelemetrySchemaDefinition) TypedTelemetryMetrics {
	definitions := make(map[string]TelemetryMetricDefinition)
	for _, schema := range schemas {
		if schema == nil || schema.Metrics == nil {
			continue
		}
		for name, definition := range schema.Metrics.All() {
			definitions[name] = definition
		}
	}
	return &metricsTypedAdapter{
		metrics:     metrics,
		definitions: definitions,
	}
}
