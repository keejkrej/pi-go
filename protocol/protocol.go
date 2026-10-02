// Ported from packages/protocol/src/protocol.ts (pi v1.0.0).

package protocol

import (
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// ProtocolVersion is the only server hello version this protocol speaks.
const ProtocolVersion = 8

// protServerIdPattern is the canonical lowercase UUID v4 pattern.
const protServerIdPattern = "^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"

// ServerId is a canonical lowercase UUID v4.
type ServerId string

// IsServerId reports whether value matches the server id schema.
func IsServerId(value any) bool {
	panic("unported: IsServerId")
}

// ProtocolErrorCode is an application-defined error code. It is not a closed set.
type ProtocolErrorCode = string

// ProtocolError is a protocol failure. Field order is code, message.
type ProtocolError struct {
	Code    ProtocolErrorCode `json:"code"`
	Message string            `json:"message"`
}

// ClientMessage is a sealed union: *ClientHello, *RequestEnvelope, *CancelEnvelope,
// *UnknownClientMessage.
type ClientMessage interface{ isClientMessage() }

// ClientHello is the first frame sent by a client.
// Field order is type, version. Type is always "hello".
type ClientHello struct {
	Type string `json:"type"`
	// Version is the non-negative integer the client proposes.
	Version int `json:"version"`
}

func (*ClientHello) isClientMessage() {}

// ServerTarget is a server-wide call, fenced to one logical server.
// Field order is serverId.
type ServerTarget struct {
	ServerId ServerId `json:"serverId"`
}

func (*ServerTarget) isRpcTarget() {}

// SessionTarget is a session call, fenced to one logical server, durable session, and live attachment.
// Field order is serverId, sessionId, attachmentId.
type SessionTarget struct {
	ServerId     ServerId `json:"serverId"`
	SessionId    string   `json:"sessionId"`
	AttachmentId string   `json:"attachmentId"`
}

func (*SessionTarget) isRpcTarget() {}

// RpcTarget is a sealed union: *ServerTarget, *SessionTarget, *UnknownRpcTarget.
type RpcTarget interface{ isRpcTarget() }

// UnknownRpcTarget keeps a target that is neither a server target nor a session target.
type UnknownRpcTarget struct {
	Raw *jsonx.Object
}

func (*UnknownRpcTarget) isRpcTarget() {}

func (u *UnknownRpcTarget) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownRpcTarget.MarshalJSON")
}

func (u *UnknownRpcTarget) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownRpcTarget.UnmarshalJSON")
}

// RequestEnvelope is a routed call. Field order is type, id, target, call.
// Type is always "request". Call is an opaque JSON value.
type RequestEnvelope struct {
	Type   string          `json:"type"`
	Id     string          `json:"id"`
	Target RpcTarget       `json:"target"`
	Call   chord.JsonValue `json:"call"`
}

func (*RequestEnvelope) isClientMessage() {}

func (e *RequestEnvelope) MarshalJSON() ([]byte, error) {
	panic("unported: RequestEnvelope.MarshalJSON")
}

func (e *RequestEnvelope) UnmarshalJSON(data []byte) error {
	panic("unported: RequestEnvelope.UnmarshalJSON")
}

// CancelEnvelope cancels one routed call. Field order is type, id, target.
// Type is always "cancel".
type CancelEnvelope struct {
	Type   string    `json:"type"`
	Id     string    `json:"id"`
	Target RpcTarget `json:"target"`
}

func (*CancelEnvelope) isClientMessage() {}

func (e *CancelEnvelope) MarshalJSON() ([]byte, error) {
	panic("unported: CancelEnvelope.MarshalJSON")
}

func (e *CancelEnvelope) UnmarshalJSON(data []byte) error {
	panic("unported: CancelEnvelope.UnmarshalJSON")
}

// UnknownClientMessage keeps a client message whose type is not hello, request, or cancel.
type UnknownClientMessage struct {
	Raw *jsonx.Object
}

func (*UnknownClientMessage) isClientMessage() {}

func (u *UnknownClientMessage) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownClientMessage.MarshalJSON")
}

func (u *UnknownClientMessage) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownClientMessage.UnmarshalJSON")
}

// ServerMessage is a sealed union: *ServerHello, *ServerHelloError, *ResponseSuccess,
// *ResponseFailure, *UnknownResponseEnvelope, *ServiceEventEnvelope, *AttachmentEnvelope,
// *UnknownServerMessage.
type ServerMessage interface{ isServerMessage() }

// ServerHello is the server's handshake. Field order is type, version, serverId.
// Type is always "hello". Version is ProtocolVersion.
type ServerHello struct {
	Type     string   `json:"type"`
	Version  int      `json:"version"`
	ServerId ServerId `json:"serverId"`
}

func (*ServerHello) isServerMessage() {}

// ServerHelloError rejects a handshake. Field order is type, error.
// Type is always "hello_error".
type ServerHelloError struct {
	Type  string        `json:"type"`
	Error ProtocolError `json:"error"`
}

func (*ServerHelloError) isServerMessage() {}

// ResponseEnvelope is a sealed union: *ResponseSuccess, *ResponseFailure,
// *UnknownResponseEnvelope.
type ResponseEnvelope interface{ isResponseEnvelope() }

// ResponseSuccess is a successful response.
// Field order is type, id, ok, result. Type is always "response". Ok is always true.
// Result is absent when the key is omitted, null for JSON null, or a JSON value.
type ResponseSuccess struct {
	Type   string                     `json:"type"`
	Id     string                     `json:"id"`
	Ok     bool                       `json:"ok"`
	Result jsonx.Opt[chord.JsonValue] `json:"result,omitzero"`
}

func (*ResponseSuccess) isResponseEnvelope() {}

func (*ResponseSuccess) isServerMessage() {}

// ResponseFailure is a failed response.
// Field order is type, id, ok, error. Type is always "response". Ok is always false.
type ResponseFailure struct {
	Type  string        `json:"type"`
	Id    string        `json:"id"`
	Ok    bool          `json:"ok"`
	Error ProtocolError `json:"error"`
}

func (*ResponseFailure) isResponseEnvelope() {}

func (*ResponseFailure) isServerMessage() {}

// UnknownResponseEnvelope keeps a response whose ok field is not a boolean.
type UnknownResponseEnvelope struct {
	Raw *jsonx.Object
}

func (*UnknownResponseEnvelope) isResponseEnvelope() {}

func (*UnknownResponseEnvelope) isServerMessage() {}

func (u *UnknownResponseEnvelope) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownResponseEnvelope.MarshalJSON")
}

func (u *UnknownResponseEnvelope) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownResponseEnvelope.UnmarshalJSON")
}

// ServiceEventEnvelope is one service subscription update.
// Field order is type, subscriptionId, update. Type is always "service_update".
// Update is an opaque JSON value.
type ServiceEventEnvelope struct {
	Type           string          `json:"type"`
	SubscriptionId string          `json:"subscriptionId"`
	Update         chord.JsonValue `json:"update"`
}

func (*ServiceEventEnvelope) isServerMessage() {}

// AttachmentEnvelope is an out-of-band update to this presentation's selected Session route.
// Field order is type, attachment. Type is always "attachment".
// Attachment is a session target, or null when the presentation is detached.
// Absent and null both marshal as JSON null; null is the wire value for detach.
type AttachmentEnvelope struct {
	Type       string                   `json:"type"`
	Attachment jsonx.Opt[SessionTarget] `json:"attachment"`
}

func (*AttachmentEnvelope) isServerMessage() {}

// UnknownServerMessage keeps a server message whose type is not recognized.
type UnknownServerMessage struct {
	Raw *jsonx.Object
}

func (*UnknownServerMessage) isServerMessage() {}

func (u *UnknownServerMessage) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownServerMessage.MarshalJSON")
}

func (u *UnknownServerMessage) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownServerMessage.UnmarshalJSON")
}

func protStrictObject(kv ...any) *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	for i := 0; i < len(kv); i += 2 {
		props.Set(kv[i].(string), kv[i+1].(*typebox.Schema))
	}
	return typebox.Object(props, jsonx.ObjectOf("additionalProperties", false))
}

var protIdSchema = typebox.String(jsonx.ObjectOf("minLength", float64(1)))

// protOpaqueJsonValueSchema is Type.Unsafe(Type.Unknown()).
// The enumerable schema is Unknown. Protocol validation uses Check, not Convert.
var protOpaqueJsonValueSchema = typebox.Unknown()

var protServerIdSchema = typebox.String(jsonx.ObjectOf("pattern", protServerIdPattern))

var protProtocolErrorSchema = protStrictObject(
	"code", protIdSchema,
	"message", typebox.String(),
)

var protClientHelloSchema = protStrictObject(
	"type", typebox.Literal("hello"),
	"version", typebox.Integer(jsonx.ObjectOf("minimum", float64(0))),
)

var protServerTargetSchema = protStrictObject(
	"serverId", protServerIdSchema,
)

var protSessionTargetSchema = protStrictObject(
	"serverId", protServerIdSchema,
	"sessionId", protIdSchema,
	"attachmentId", protIdSchema,
)

var protRpcTargetSchema = typebox.Union([]*typebox.Schema{
	protServerTargetSchema,
	protSessionTargetSchema,
})

var protRequestEnvelopeSchema = protStrictObject(
	"type", typebox.Literal("request"),
	"id", protIdSchema,
	"target", protRpcTargetSchema,
	"call", protOpaqueJsonValueSchema,
)

var protCancelEnvelopeSchema = protStrictObject(
	"type", typebox.Literal("cancel"),
	"id", protIdSchema,
	"target", protRpcTargetSchema,
)

// ClientMessageSchema is the TypeBox schema for one client message.
var ClientMessageSchema = typebox.Union([]*typebox.Schema{
	protClientHelloSchema,
	protRequestEnvelopeSchema,
	protCancelEnvelopeSchema,
})

var protServerHelloSchema = protStrictObject(
	"type", typebox.Literal("hello"),
	"version", typebox.Literal(ProtocolVersion),
	"serverId", protServerIdSchema,
)

var protServerHelloErrorSchema = protStrictObject(
	"type", typebox.Literal("hello_error"),
	"error", protProtocolErrorSchema,
)

var protResponseEnvelopeSchema = typebox.Union([]*typebox.Schema{
	protStrictObject(
		"type", typebox.Literal("response"),
		"id", protIdSchema,
		"ok", typebox.Literal(true),
		"result", typebox.Optional(protOpaqueJsonValueSchema),
	),
	protStrictObject(
		"type", typebox.Literal("response"),
		"id", protIdSchema,
		"ok", typebox.Literal(false),
		"error", protProtocolErrorSchema,
	),
})

var protServiceEventEnvelopeSchema = protStrictObject(
	"type", typebox.Literal("service_update"),
	"subscriptionId", protIdSchema,
	"update", protOpaqueJsonValueSchema,
)

var protAttachmentEnvelopeSchema = protStrictObject(
	"type", typebox.Literal("attachment"),
	"attachment", typebox.Union([]*typebox.Schema{
		protSessionTargetSchema,
		typebox.Null(),
	}),
)

// ServerMessageSchema is the TypeBox schema for one server message.
var ServerMessageSchema = typebox.Union([]*typebox.Schema{
	protServerHelloSchema,
	protServerHelloErrorSchema,
	protResponseEnvelopeSchema,
	protServiceEventEnvelopeSchema,
	protAttachmentEnvelopeSchema,
})

// UnmarshalClientMessage decodes one client message.
// An unrecognized shape becomes *UnknownClientMessage.
func UnmarshalClientMessage(data []byte) (ClientMessage, error) {
	panic("unported: UnmarshalClientMessage")
}

// DecodeClientMessage decodes one client message from a jsonx value.
// An unrecognized shape becomes *UnknownClientMessage.
func DecodeClientMessage(v any) (ClientMessage, error) {
	panic("unported: DecodeClientMessage")
}

// UnmarshalServerMessage decodes one server message.
// An unrecognized shape becomes *UnknownServerMessage.
func UnmarshalServerMessage(data []byte) (ServerMessage, error) {
	panic("unported: UnmarshalServerMessage")
}

// DecodeServerMessage decodes one server message from a jsonx value.
// An unrecognized shape becomes *UnknownServerMessage.
func DecodeServerMessage(v any) (ServerMessage, error) {
	panic("unported: DecodeServerMessage")
}

// UnmarshalRpcTarget decodes one RPC target.
// An unrecognized shape becomes *UnknownRpcTarget.
func UnmarshalRpcTarget(data []byte) (RpcTarget, error) {
	panic("unported: UnmarshalRpcTarget")
}

// DecodeRpcTarget decodes one RPC target from a jsonx value.
// An unrecognized shape becomes *UnknownRpcTarget.
func DecodeRpcTarget(v any) (RpcTarget, error) {
	panic("unported: DecodeRpcTarget")
}

// UnmarshalResponseEnvelope decodes one response envelope.
// An unrecognized shape becomes *UnknownResponseEnvelope.
func UnmarshalResponseEnvelope(data []byte) (ResponseEnvelope, error) {
	panic("unported: UnmarshalResponseEnvelope")
}

// DecodeResponseEnvelope decodes one response envelope from a jsonx value.
// An unrecognized shape becomes *UnknownResponseEnvelope.
func DecodeResponseEnvelope(v any) (ResponseEnvelope, error) {
	panic("unported: DecodeResponseEnvelope")
}

var (
	_ ClientMessage    = (*ClientHello)(nil)
	_ ClientMessage    = (*RequestEnvelope)(nil)
	_ ClientMessage    = (*CancelEnvelope)(nil)
	_ ClientMessage    = (*UnknownClientMessage)(nil)
	_ RpcTarget        = (*ServerTarget)(nil)
	_ RpcTarget        = (*SessionTarget)(nil)
	_ RpcTarget        = (*UnknownRpcTarget)(nil)
	_ ResponseEnvelope = (*ResponseSuccess)(nil)
	_ ResponseEnvelope = (*ResponseFailure)(nil)
	_ ResponseEnvelope = (*UnknownResponseEnvelope)(nil)
	_ ServerMessage    = (*ServerHello)(nil)
	_ ServerMessage    = (*ServerHelloError)(nil)
	_ ServerMessage    = (*ResponseSuccess)(nil)
	_ ServerMessage    = (*ResponseFailure)(nil)
	_ ServerMessage    = (*UnknownResponseEnvelope)(nil)
	_ ServerMessage    = (*ServiceEventEnvelope)(nil)
	_ ServerMessage    = (*AttachmentEnvelope)(nil)
	_ ServerMessage    = (*UnknownServerMessage)(nil)
)
