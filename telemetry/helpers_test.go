package telemetry_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/telemetry"
)

type thlpKV[K comparable, V any] struct {
	Key   K
	Value V
}

type thlpMapRepr[K comparable, V any] struct {
	IsNil   bool
	Entries []thlpKV[K, V]
}

type thlpOptRepr[T any] struct {
	Present bool
	Null    bool
	Value   T
}

type thlpKindError struct {
	Kind string
}

func (e *thlpKindError) Error() string {
	return e.Kind
}

func thlpTransformMap[K comparable, V any](m *omap.Map[K, V]) thlpMapRepr[K, V] {
	if m == nil {
		return thlpMapRepr[K, V]{IsNil: true}
	}
	entries := make([]thlpKV[K, V], 0, m.Len())
	for k, v := range m.All() {
		entries = append(entries, thlpKV[K, V]{Key: k, Value: v})
	}
	return thlpMapRepr[K, V]{Entries: entries}
}

func thlpTransformOpt[T any](o jsonx.Opt[T]) thlpOptRepr[T] {
	if o.IsZero() {
		return thlpOptRepr[T]{}
	}
	if o.IsNull() {
		return thlpOptRepr[T]{Present: true, Null: true}
	}
	val, _ := o.Get()
	return thlpOptRepr[T]{Present: true, Value: val}
}

func thlpCmpOpts() cmp.Options {
	return cmp.Options{
		cmp.Transformer("omapAttr", thlpTransformMap[string, telemetry.AttributeValue]),
		cmp.Transformer("omapCarrier", thlpTransformMap[string, string]),
		cmp.Transformer("omapSpanDef", thlpTransformMap[string, telemetry.TelemetrySpanDefinition]),
		cmp.Transformer("omapStartAttrDef", thlpTransformMap[string, telemetry.TelemetryStartAttributeDefinition]),
		cmp.Transformer("omapAttrDef", thlpTransformMap[string, telemetry.TelemetryAttributeDefinition]),
		cmp.Transformer("omapEventDef", thlpTransformMap[string, telemetry.TelemetryEventDefinition]),
		cmp.Transformer("omapMetricDef", thlpTransformMap[string, telemetry.TelemetryMetricDefinition]),
		cmp.Transformer("optCarrier", thlpTransformOpt[telemetry.TelemetryCarrier]),
		cmp.Transformer("optInt", thlpTransformOpt[int]),
	}
}

func thlpStrPtr(s string) *string {
	return &s
}

func thlpAttrs(pairs ...any) telemetry.SpanAttributes {
	m := omap.NewMap[string, telemetry.AttributeValue]()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func thlpCarrier(pairs ...string) telemetry.TelemetryCarrier {
	m := omap.NewMap[string, string]()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

func thlpMap[V any](pairs ...any) *omap.Map[string, V] {
	m := omap.NewMap[string, V]()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1].(V))
	}
	return m
}

func thlpFindSpan(t *testing.T, spans []telemetry.RecordedTelemetrySpan, name string) telemetry.RecordedTelemetrySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("expected recorded span named %q", name)
	return telemetry.RecordedTelemetrySpan{}
}
