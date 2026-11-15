// Ported from packages/telemetry/src/index.ts (pi v1.0.0).

package telemetry

import (
	"fmt"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// AttributeValue is a string, float64, bool, []string, []float64, or []bool.
type AttributeValue = any

// SpanAttributes is an open attribute bag in insertion order.
// Nil means the TS property was absent. An undefined TS value is an omitted key.
type SpanAttributes = *omap.Map[string, AttributeValue]

// TelemetryCarrier is opaque, JSON-serializable adapter-owned propagation data.
type TelemetryCarrier = *omap.Map[string, string]

// TelemetryPropagation is an optional transport capability implemented by the application's telemetry adapter.
type TelemetryPropagation interface {
	Inject(context TelemetryContext) TelemetryCarrier
	// Extract returns a parent context from carrier. Absent or unknown carriers use the root; recognized identifiers are opaque strings.
	Extract(carrier TelemetryCarrier) TelemetryContext
}

// SpanLink associates a span with another TelemetryContext without changing its parent.
type SpanLink struct {
	Context    TelemetryContext `json:"-"`
	Attributes SpanAttributes   `json:"attributes,omitzero"`
}

// SpanOptions is the name and optional start attributes and links of a span.
type SpanOptions struct {
	Name       string         `json:"name"`
	Attributes SpanAttributes `json:"attributes,omitzero"`
	Links      []SpanLink     `json:"links,omitzero"`
}

// SpanStatusKind is the SpanStatus discriminator.
type SpanStatusKind string

const (
	SpanStatusKindOk    SpanStatusKind = "ok"
	SpanStatusKindError SpanStatusKind = "error"
)

// SpanStatus is {status:"ok"} or {status:"error", error?}.
type SpanStatus interface{ isSpanStatus() }

// SpanStatusOk is a successful span status.
type SpanStatusOk struct {
	Status SpanStatusKind `json:"status"`
}

func (*SpanStatusOk) isSpanStatus() {}

// SpanError is the optional error payload on an error status.
type SpanError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// SpanStatusError is an error span status. Error is omitted when absent.
type SpanStatusError struct {
	Status SpanStatusKind `json:"status"`
	Error  *SpanError     `json:"error,omitzero"`
}

func (*SpanStatusError) isSpanStatus() {}

// UnknownSpanStatus is an unrecognized status object, kept verbatim.
type UnknownSpanStatus struct{ Raw *jsonx.Object }

func (*UnknownSpanStatus) isSpanStatus() {}

func (u *UnknownSpanStatus) MarshalJSON() ([]byte, error) {
	return u.Raw.MarshalJSON()
}

// UnmarshalSpanStatus decodes a SpanStatus JSON object.
func UnmarshalSpanStatus(data []byte) (SpanStatus, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeSpanStatus(v)
}

// DecodeSpanStatus decodes a SpanStatus from a jsonx value.
func DecodeSpanStatus(v any) (SpanStatus, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		enc, err := jsonx.Encode(v)
		if err != nil {
			return nil, err
		}
		obj, ok = enc.(*jsonx.Object)
	}
	if !ok || obj == nil {
		return nil, fmt.Errorf("telemetry: expected SpanStatus object, got %T", v)
	}
	statusVal, _ := obj.Get("status")
	statusStr, _ := statusVal.(string)
	switch SpanStatusKind(statusStr) {
	case SpanStatusKindOk:
		return &SpanStatusOk{Status: SpanStatusKindOk}, nil
	case SpanStatusKindError:
		var spanErr *SpanError
		if errVal, hasErr := obj.Get("error"); hasErr && errVal != nil {
			var decoded SpanError
			if err := jsonx.Decode(errVal, &decoded); err != nil {
				return nil, err
			}
			spanErr = &decoded
		}
		return &SpanStatusError{Status: SpanStatusKindError, Error: spanErr}, nil
	default:
		return &UnknownSpanStatus{Raw: obj}, nil
	}
}

// StoredSpanEvent is a settled event snapshot on a StoredSpan.
// TimeMs is relative to span start at microsecond resolution.
type StoredSpanEvent struct {
	Name       string         `json:"name"`
	TimeMs     float64        `json:"timeMs"`
	Attributes SpanAttributes `json:"attributes"`
}

// StoredSpanLink is a settled link snapshot on a StoredSpan.
type StoredSpanLink struct {
	TraceId    string         `json:"traceId"`
	SpanId     string         `json:"spanId"`
	Attributes SpanAttributes `json:"attributes,omitzero"`
}

// StoredSpan is a settled span snapshot, independent of the recording adapter.
// Times are milliseconds at microsecond resolution; event times are relative to span start.
type StoredSpan struct {
	Id         string            `json:"id"`
	TraceId    string            `json:"traceId"`
	ParentId   *string           `json:"parentId,omitzero"`
	Name       string            `json:"name"`
	StartedMs  float64           `json:"startedMs"`
	EndedMs    float64           `json:"endedMs"`
	Attributes SpanAttributes    `json:"attributes"`
	Events     []StoredSpanEvent `json:"events"`
	Links      []StoredSpanLink  `json:"links"`
	Status     SpanStatus        `json:"status"`
}

// UnmarshalJSON decodes a StoredSpan JSON object, preserving polymorphic SpanStatus.
func (s *StoredSpan) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	type indexStoredSpanAlias struct {
		Id         string            `json:"id"`
		TraceId    string            `json:"traceId"`
		ParentId   *string           `json:"parentId,omitzero"`
		Name       string            `json:"name"`
		StartedMs  float64           `json:"startedMs"`
		EndedMs    float64           `json:"endedMs"`
		Attributes SpanAttributes    `json:"attributes"`
		Events     []StoredSpanEvent `json:"events"`
		Links      []StoredSpanLink  `json:"links"`
		Status     any               `json:"status"`
	}
	var raw indexStoredSpanAlias
	if err := jsonx.Decode(v, &raw); err != nil {
		return err
	}
	var status SpanStatus
	if raw.Status != nil {
		status, err = DecodeSpanStatus(raw.Status)
		if err != nil {
			return err
		}
	}
	*s = StoredSpan{
		Id:         raw.Id,
		TraceId:    raw.TraceId,
		ParentId:   raw.ParentId,
		Name:       raw.Name,
		StartedMs:  raw.StartedMs,
		EndedMs:    raw.EndedMs,
		Attributes: raw.Attributes,
		Events:     raw.Events,
		Links:      raw.Links,
		Status:     status,
	}
	return nil
}

// StoredTrace groups settled spans sharing one trace ID.
type StoredTrace struct {
	TraceId string       `json:"traceId"`
	Spans   []StoredSpan `json:"spans"`
}

// TraceQuery filters settled traces in TelemetryStore.FindTraces.
type TraceQuery struct {
	// Name filters by the earliest stored span's name, with span ID breaking timestamp ties.
	Name *string `json:"name,omitzero"`
	// Attributes requires all equalities to match one span; that span may be anywhere in the trace.
	Attributes *omap.Map[string, AttributeValue] `json:"attributes,omitzero"`
	Errors     bool                              `json:"errors,omitzero"`
	// StartMin and StartMax are inclusive bounds on the earliest stored span's start time.
	StartMin *float64 `json:"startMin,omitzero"`
	StartMax *float64 `json:"startMax,omitzero"`
	// Limit is a positive page size; defaults to 20.
	Limit *int `json:"limit,omitzero"`
	// Cursor is an opaque continuation from the previous page; retain the same filters.
	Cursor *string `json:"cursor,omitzero"`
}

// MetricQuery filters aggregated or raw metric rows in TelemetryStore.FindMetrics.
type MetricQuery struct {
	Name string `json:"name"`
	// StartMin and StartMax are inclusive bounds on interval start time.
	StartMin   *int64                            `json:"startMin,omitzero"`
	StartMax   *int64                            `json:"startMax,omitzero"`
	Attributes *omap.Map[string, AttributeValue] `json:"attributes,omitzero"`
}

// StoredMetric is one metric series row for an interval.
type StoredMetric struct {
	IntervalMs int64               `json:"intervalMs"`
	Name       string              `json:"name"`
	Kind       TelemetryMetricKind `json:"kind"`
	Unit       *string             `json:"unit,omitzero"`
	Attributes SpanAttributes      `json:"attributes"`
	Count      int                 `json:"count"`
	Sum        float64             `json:"sum"`
	Min        float64             `json:"min"`
	Max        float64             `json:"max"`
}

// FindTracesResult is one page of settled traces returned by TelemetryStore.FindTraces.
type FindTracesResult struct {
	Traces []StoredTrace `json:"traces"`
	Cursor *string       `json:"cursor,omitzero"`
}

// TelemetryStore reads settled traces without depending on a storage backend.
type TelemetryStore interface {
	FindTraces(query *TraceQuery) (FindTracesResult, error)
	GetTrace(traceId string) (*StoredTrace, error)
	FindMetrics(query MetricQuery) ([]StoredMetric, error)
}

// TelemetryContext starts callback-managed child spans.
// Adapter capabilities remain available on child spans and extracted parents.
type TelemetryContext interface {
	Metrics() TelemetryMetrics
	Propagation() TelemetryPropagation
	StartSpan(options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error)
}

// TelemetrySpan records attributes, events, links, and status, and starts child spans.
type TelemetrySpan interface {
	TelemetryContext
	AddLink(link SpanLink)
	AddEvent(name string, attributes SpanAttributes)
	SetAttributes(attributes SpanAttributes)
	SetStatus(status SpanStatus)
}

// TelemetryAttributeType is the schema type of an attribute value.
type TelemetryAttributeType string

const (
	TelemetryAttributeTypeString       TelemetryAttributeType = "string"
	TelemetryAttributeTypeNumber       TelemetryAttributeType = "number"
	TelemetryAttributeTypeBoolean      TelemetryAttributeType = "boolean"
	TelemetryAttributeTypeStringArray  TelemetryAttributeType = "string[]"
	TelemetryAttributeTypeNumberArray  TelemetryAttributeType = "number[]"
	TelemetryAttributeTypeBooleanArray TelemetryAttributeType = "boolean[]"
)

// TelemetryCardinality is the expected cardinality of an attribute.
type TelemetryCardinality string

const (
	TelemetryCardinalityLow  TelemetryCardinality = "low"
	TelemetryCardinalityHigh TelemetryCardinality = "high"
)

// TelemetryAttributeMetadata is shared documentation metadata for an attribute.
type TelemetryAttributeMetadata struct {
	Description string                `json:"description"`
	Sensitive   *bool                 `json:"sensitive,omitzero"`
	Cardinality *TelemetryCardinality `json:"cardinality,omitzero"`
}

// TelemetryAttributeDefinition is an end-attribute definition.
// Values is []string, []float64, or []bool for scalar types.
// ElementValues is that slice for array types.
// Examples is a slice of the value type, or a slice of those slices for array types.
// JSON key order follows the object literals: type, values, elementValues, description, then metadata.
type TelemetryAttributeDefinition struct {
	Type          TelemetryAttributeType `json:"type"`
	Values        any                    `json:"values,omitzero"`
	ElementValues any                    `json:"elementValues,omitzero"`
	Description   string                 `json:"description"`
	Sensitive     *bool                  `json:"sensitive,omitzero"`
	Cardinality   *TelemetryCardinality  `json:"cardinality,omitzero"`
	Examples      any                    `json:"examples,omitzero"`
}

// TelemetryStartAttributeDefinition is a start or event attribute definition.
// Required is always written. Other fields match TelemetryAttributeDefinition.
type TelemetryStartAttributeDefinition struct {
	Type          TelemetryAttributeType `json:"type"`
	Required      bool                   `json:"required"`
	Values        any                    `json:"values,omitzero"`
	ElementValues any                    `json:"elementValues,omitzero"`
	Description   string                 `json:"description"`
	Sensitive     *bool                  `json:"sensitive,omitzero"`
	Cardinality   *TelemetryCardinality  `json:"cardinality,omitzero"`
	Examples      any                    `json:"examples,omitzero"`
}

// TelemetryEventAttributeDefinition is an event attribute definition.
type TelemetryEventAttributeDefinition = TelemetryStartAttributeDefinition

// TelemetryEventDefinition is an event description and its attribute definitions.
type TelemetryEventDefinition struct {
	Description string                                               `json:"description"`
	Attributes  *omap.Map[string, TelemetryEventAttributeDefinition] `json:"attributes"`
}

// TelemetryParentKind is the TelemetryParentDefinition discriminator.
type TelemetryParentKind string

const (
	TelemetryParentKindAny            TelemetryParentKind = "any"
	TelemetryParentKindRootOrExternal TelemetryParentKind = "root_or_external"
	TelemetryParentKindSpans          TelemetryParentKind = "spans"
)

// TelemetryParentDefinition is the parent rule for a span.
type TelemetryParentDefinition interface{ isTelemetryParentDefinition() }

// TelemetryParentAny allows a root span or any caller span.
type TelemetryParentAny struct {
	Kind TelemetryParentKind `json:"kind"`
}

func (*TelemetryParentAny) isTelemetryParentDefinition() {}

// TelemetryParentRootOrExternal allows a root or a caller-owned span outside the schema.
type TelemetryParentRootOrExternal struct {
	Kind TelemetryParentKind `json:"kind"`
}

func (*TelemetryParentRootOrExternal) isTelemetryParentDefinition() {}

// TelemetryParentSpans allows only the listed schema span names.
type TelemetryParentSpans struct {
	Kind  TelemetryParentKind `json:"kind"`
	Spans []string            `json:"spans"`
}

func (*TelemetryParentSpans) isTelemetryParentDefinition() {}

// UnknownTelemetryParentDefinition is an unrecognized parent rule, kept verbatim.
type UnknownTelemetryParentDefinition struct{ Raw *jsonx.Object }

func (*UnknownTelemetryParentDefinition) isTelemetryParentDefinition() {}

func (u *UnknownTelemetryParentDefinition) MarshalJSON() ([]byte, error) {
	return u.Raw.MarshalJSON()
}

// UnmarshalTelemetryParentDefinition decodes a parent-rule JSON object.
func UnmarshalTelemetryParentDefinition(data []byte) (TelemetryParentDefinition, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeTelemetryParentDefinition(v)
}

// DecodeTelemetryParentDefinition decodes a parent rule from a jsonx value.
func DecodeTelemetryParentDefinition(v any) (TelemetryParentDefinition, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		enc, err := jsonx.Encode(v)
		if err != nil {
			return nil, err
		}
		obj, ok = enc.(*jsonx.Object)
	}
	if !ok || obj == nil {
		return nil, fmt.Errorf("telemetry: expected TelemetryParentDefinition object, got %T", v)
	}
	kindVal, _ := obj.Get("kind")
	kindStr, _ := kindVal.(string)
	switch TelemetryParentKind(kindStr) {
	case TelemetryParentKindAny:
		return &TelemetryParentAny{Kind: TelemetryParentKindAny}, nil
	case TelemetryParentKindRootOrExternal:
		return &TelemetryParentRootOrExternal{Kind: TelemetryParentKindRootOrExternal}, nil
	case TelemetryParentKindSpans:
		var p TelemetryParentSpans
		if err := jsonx.Decode(obj, &p); err != nil {
			return nil, err
		}
		p.Kind = TelemetryParentKindSpans
		return &p, nil
	default:
		return &UnknownTelemetryParentDefinition{Raw: obj}, nil
	}
}

// TelemetryStatusRule is the status metadata on a span definition.
// Default is the literal "ok".
type TelemetryStatusRule struct {
	Default   string `json:"default"`
	ErrorWhen string `json:"errorWhen"`
}

// TelemetrySpanDefinition is one span in a telemetry schema.
// StartAttributes and EndAttributes must be non-nil so they marshal as objects.
type TelemetrySpanDefinition struct {
	Description     string                                               `json:"description"`
	Parents         TelemetryParentDefinition                            `json:"parents"`
	StartAttributes *omap.Map[string, TelemetryStartAttributeDefinition] `json:"startAttributes"`
	EndAttributes   *omap.Map[string, TelemetryAttributeDefinition]      `json:"endAttributes"`
	Events          *omap.Map[string, TelemetryEventDefinition]          `json:"events,omitzero"`
	Status          TelemetryStatusRule                                  `json:"status"`
}

// UnmarshalJSON decodes a TelemetrySpanDefinition JSON object, preserving polymorphic Parents.
func (d *TelemetrySpanDefinition) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	type indexSpanDefAlias struct {
		Description     string                                               `json:"description"`
		Parents         any                                                  `json:"parents"`
		StartAttributes *omap.Map[string, TelemetryStartAttributeDefinition] `json:"startAttributes"`
		EndAttributes   *omap.Map[string, TelemetryAttributeDefinition]      `json:"endAttributes"`
		Events          *omap.Map[string, TelemetryEventDefinition]          `json:"events,omitzero"`
		Status          TelemetryStatusRule                                  `json:"status"`
	}
	var raw indexSpanDefAlias
	if err := jsonx.Decode(v, &raw); err != nil {
		return err
	}
	var parents TelemetryParentDefinition
	if raw.Parents != nil {
		parents, err = DecodeTelemetryParentDefinition(raw.Parents)
		if err != nil {
			return err
		}
	}
	*d = TelemetrySpanDefinition{
		Description:     raw.Description,
		Parents:         parents,
		StartAttributes: raw.StartAttributes,
		EndAttributes:   raw.EndAttributes,
		Events:          raw.Events,
		Status:          raw.Status,
	}
	return nil
}

// TelemetrySchemaDefinition is a serializable telemetry schema.
// Spans must be non-nil so it marshals as an object.
type TelemetrySchemaDefinition struct {
	Version float64                                      `json:"version"`
	Spans   *omap.Map[string, TelemetrySpanDefinition]   `json:"spans"`
	Metrics *omap.Map[string, TelemetryMetricDefinition] `json:"metrics,omitzero"`
}

// DefineTelemetrySchema is a typed identity helper for serializable telemetry schema data.
func DefineTelemetrySchema(schema *TelemetrySchemaDefinition) *TelemetrySchemaDefinition {
	return schema
}

// TS schema inference has no Go equivalent. These aliases keep the export names.
type (
	InferRequiredAndOptionalAttributes = SpanAttributes
	InferStartAttributes               = SpanAttributes
	InferOptionalAttributes            = SpanAttributes
	InferEventAttributes               = SpanAttributes
	ExactTelemetryAttributes           = SpanAttributes
	TelemetrySchemaSpanName            = string
	TelemetrySchemaSpanStartAttributes = SpanAttributes
	TelemetrySchemaSpanEndAttributes   = SpanAttributes
	TelemetrySchemaSpanEventName       = string
	TelemetrySchemaSpanEventAttributes = SpanAttributes
	SchemaTelemetrySpan                = TelemetrySpan
)

// TelemetrySchemaSpanUnion is one span's inferred name, attributes, and events.
type TelemetrySchemaSpanUnion struct {
	Name            string                            `json:"name"`
	StartAttributes SpanAttributes                    `json:"startAttributes"`
	EndAttributes   SpanAttributes                    `json:"endAttributes"`
	Events          *omap.Map[string, SpanAttributes] `json:"events"`
}

// TypedSpanStarter is a per-span overload set bound to one explicit parent context and one or more schemas.
// Go has one function for the whole overload set. The callback's child starter is bound to that callback's span.
type TypedSpanStarter func(name string, attributes SpanAttributes, callback func(span TelemetrySpan, startChildSpan TypedSpanStarter) (any, error)) (any, error)

func indexBindTypedSpanStarter(telemetryContext TelemetryContext) TypedSpanStarter {
	return func(name string, attributes SpanAttributes, callback func(span TelemetrySpan, startChildSpan TypedSpanStarter) (any, error)) (any, error) {
		return telemetryContext.StartSpan(&SpanOptions{Name: name, Attributes: attributes}, func(span TelemetrySpan) (any, error) {
			return callback(span, indexBindTypedSpanStarter(span))
		})
	}
}

// CreateTypedSpanStarter binds an explicit parent context to the combined span vocabulary of one or more schemas.
// Schema values are used only for type inference; no runtime schema validation is performed.
func CreateTypedSpanStarter(telemetryContext TelemetryContext, _ []*TelemetrySchemaDefinition) TypedSpanStarter {
	return indexBindTypedSpanStarter(telemetryContext)
}
