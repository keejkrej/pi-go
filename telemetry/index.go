// Ported from packages/telemetry/src/index.ts (pi v1.0.0).

package telemetry

import (
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// AttributeValue is a string, float64, bool, []string, []float64, or []bool.
type AttributeValue = any

// SpanAttributes is an open attribute bag in insertion order.
// Nil means the TS property was absent. An undefined TS value is an omitted key.
type SpanAttributes = *omap.Map[string, AttributeValue]

// SpanOptions is the name and optional start attributes of a span.
type SpanOptions struct {
	Name       string         `json:"name"`
	Attributes SpanAttributes `json:"attributes,omitzero"`
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
	panic("unported: UnmarshalSpanStatus")
}

// DecodeSpanStatus decodes a SpanStatus from a jsonx value.
func DecodeSpanStatus(v any) (SpanStatus, error) {
	panic("unported: DecodeSpanStatus")
}

// TelemetryContext starts callback-managed child spans.
type TelemetryContext interface {
	StartSpan(options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error)
}

// TelemetrySpan records attributes, events, and status, and starts child spans.
type TelemetrySpan interface {
	TelemetryContext
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
	panic("unported: UnmarshalTelemetryParentDefinition")
}

// DecodeTelemetryParentDefinition decodes a parent rule from a jsonx value.
func DecodeTelemetryParentDefinition(v any) (TelemetryParentDefinition, error) {
	panic("unported: DecodeTelemetryParentDefinition")
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

// TelemetrySchemaDefinition is a serializable telemetry schema.
// Spans must be non-nil so it marshals as an object.
type TelemetrySchemaDefinition struct {
	Version float64                                    `json:"version"`
	Spans   *omap.Map[string, TelemetrySpanDefinition] `json:"spans"`
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
	panic("unported: indexBindTypedSpanStarter")
}

// CreateTypedSpanStarter binds an explicit parent context to the combined span vocabulary of one or more schemas.
// Schema values are used only for type inference; no runtime schema validation is performed.
func CreateTypedSpanStarter(telemetryContext TelemetryContext, schemas []*TelemetrySchemaDefinition) TypedSpanStarter {
	panic("unported: CreateTypedSpanStarter")
}
