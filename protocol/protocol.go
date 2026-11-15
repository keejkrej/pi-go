// Ported from packages/protocol/src/protocol.ts (pi v1.0.0).

package protocol

import (
	"errors"
	"fmt"
	"math"

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
	if sid, ok := value.(ServerId); ok {
		value = string(sid)
	}
	return typebox.Check(protServerIdSchema, value)
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
	if u == nil {
		return []byte("null"), nil
	}
	return protMarshalUnknown(u.Raw)
}

func (u *UnknownRpcTarget) UnmarshalJSON(data []byte) error {
	return protUnmarshalUnknown(data, &u.Raw)
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
	if e == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(protClientMessageToValue(e))
}

func (e *RequestEnvelope) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	obj, ok := val.(*jsonx.Object)
	if !ok || obj == nil {
		return errors.New("request envelope: expected JSON object")
	}
	e.Type, _ = obj.GetString("type")
	e.Id, _ = obj.GetString("id")
	if targetAny, hasTarget := obj.Get("target"); hasTarget && targetAny != nil {
		target, err := DecodeRpcTarget(targetAny)
		if err != nil {
			return err
		}
		e.Target = target
	} else {
		e.Target = nil
	}
	e.Call, _ = obj.Get("call")
	return nil
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
	if e == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(protClientMessageToValue(e))
}

func (e *CancelEnvelope) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	obj, ok := val.(*jsonx.Object)
	if !ok || obj == nil {
		return errors.New("cancel envelope: expected JSON object")
	}
	e.Type, _ = obj.GetString("type")
	e.Id, _ = obj.GetString("id")
	if targetAny, hasTarget := obj.Get("target"); hasTarget && targetAny != nil {
		target, err := DecodeRpcTarget(targetAny)
		if err != nil {
			return err
		}
		e.Target = target
	} else {
		e.Target = nil
	}
	return nil
}

// UnknownClientMessage keeps a client message whose type is not hello, request, or cancel.
type UnknownClientMessage struct {
	Raw *jsonx.Object
}

func (*UnknownClientMessage) isClientMessage() {}

func (u *UnknownClientMessage) MarshalJSON() ([]byte, error) {
	if u == nil {
		return []byte("null"), nil
	}
	return protMarshalUnknown(u.Raw)
}

func (u *UnknownClientMessage) UnmarshalJSON(data []byte) error {
	return protUnmarshalUnknown(data, &u.Raw)
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
	if u == nil {
		return []byte("null"), nil
	}
	return protMarshalUnknown(u.Raw)
}

func (u *UnknownResponseEnvelope) UnmarshalJSON(data []byte) error {
	return protUnmarshalUnknown(data, &u.Raw)
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
	if u == nil {
		return []byte("null"), nil
	}
	return protMarshalUnknown(u.Raw)
}

func (u *UnknownServerMessage) UnmarshalJSON(data []byte) error {
	return protUnmarshalUnknown(data, &u.Raw)
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
	val, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeClientMessage(val)
}

// DecodeClientMessage decodes one client message from a jsonx value.
// An unrecognized shape becomes *UnknownClientMessage.
func DecodeClientMessage(v any) (ClientMessage, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("client message: expected object, got %T", v)
	}
	typ, hasType := obj.GetString("type")
	if hasType {
		switch typ {
		case "hello":
			if obj.Len() == 2 {
				if ver, ok := protDecodeNonNegSafeInt(obj, "version"); ok {
					return &ClientHello{
						Type:    "hello",
						Version: ver,
					}, nil
				}
			}
		case "request":
			if obj.Len() == 4 && obj.Has("target") && obj.Has("call") {
				if id, ok := obj.GetString("id"); ok {
					targetAny, _ := obj.Get("target")
					if target, err := DecodeRpcTarget(targetAny); err == nil {
						callVal, _ := obj.Get("call")
						return &RequestEnvelope{
							Type:   "request",
							Id:     id,
							Target: target,
							Call:   callVal,
						}, nil
					}
				}
			}
		case "cancel":
			if obj.Len() == 3 && obj.Has("target") {
				if id, ok := obj.GetString("id"); ok {
					targetAny, _ := obj.Get("target")
					if target, err := DecodeRpcTarget(targetAny); err == nil {
						return &CancelEnvelope{
							Type:   "cancel",
							Id:     id,
							Target: target,
						}, nil
					}
				}
			}
		}
	}
	return &UnknownClientMessage{Raw: obj}, nil
}

// UnmarshalServerMessage decodes one server message.
// An unrecognized shape becomes *UnknownServerMessage.
func UnmarshalServerMessage(data []byte) (ServerMessage, error) {
	val, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeServerMessage(val)
}

// DecodeServerMessage decodes one server message from a jsonx value.
// An unrecognized shape becomes *UnknownServerMessage.
func DecodeServerMessage(v any) (ServerMessage, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("server message: expected object, got %T", v)
	}
	typ, hasType := obj.GetString("type")
	if hasType {
		switch typ {
		case "hello":
			if obj.Len() == 3 {
				if ver, ok := protDecodeNonNegSafeInt(obj, "version"); ok {
					if sid, ok := obj.GetString("serverId"); ok {
						return &ServerHello{
							Type:     "hello",
							Version:  ver,
							ServerId: ServerId(sid),
						}, nil
					}
				}
			}
		case "hello_error":
			if obj.Len() == 2 {
				if errAny, ok := obj.Get("error"); ok {
					if pe, ok := protDecodeProtocolError(errAny); ok {
						return &ServerHelloError{
							Type:  "hello_error",
							Error: pe,
						}, nil
					}
				}
			}
		case "response":
			resp, err := DecodeResponseEnvelope(obj)
			if err == nil {
				return resp.(ServerMessage), nil
			}
		case "service_update":
			if obj.Len() == 3 && obj.Has("update") {
				if subId, ok := obj.GetString("subscriptionId"); ok {
					updateVal, _ := obj.Get("update")
					return &ServiceEventEnvelope{
						Type:           "service_update",
						SubscriptionId: subId,
						Update:         updateVal,
					}, nil
				}
			}
		case "attachment":
			if obj.Len() == 2 && obj.Has("attachment") {
				attAny, _ := obj.Get("attachment")
				if attAny == nil {
					return &AttachmentEnvelope{
						Type:       "attachment",
						Attachment: jsonx.Null[SessionTarget](),
					}, nil
				}
				if attObj, ok := attAny.(*jsonx.Object); ok && attObj != nil {
					if st, ok := protDecodeSessionTarget(attObj); ok {
						return &AttachmentEnvelope{
							Type:       "attachment",
							Attachment: jsonx.Some(st),
						}, nil
					}
				}
			}
		}
	}
	return &UnknownServerMessage{Raw: obj}, nil
}

// UnmarshalRpcTarget decodes one RPC target.
// An unrecognized shape becomes *UnknownRpcTarget.
func UnmarshalRpcTarget(data []byte) (RpcTarget, error) {
	val, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeRpcTarget(val)
}

// DecodeRpcTarget decodes one RPC target from a jsonx value.
// An unrecognized shape becomes *UnknownRpcTarget.
func DecodeRpcTarget(v any) (RpcTarget, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("rpc target: expected object, got %T", v)
	}
	if st, ok := protDecodeSessionTarget(obj); ok {
		return &st, nil
	}
	if obj.Len() == 1 {
		if sid, ok := obj.GetString("serverId"); ok {
			return &ServerTarget{ServerId: ServerId(sid)}, nil
		}
	}
	return &UnknownRpcTarget{Raw: obj}, nil
}

// UnmarshalResponseEnvelope decodes one response envelope.
// An unrecognized shape becomes *UnknownResponseEnvelope.
func UnmarshalResponseEnvelope(data []byte) (ResponseEnvelope, error) {
	val, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeResponseEnvelope(val)
}

// DecodeResponseEnvelope decodes one response envelope from a jsonx value.
// An unrecognized shape becomes *UnknownResponseEnvelope.
func DecodeResponseEnvelope(v any) (ResponseEnvelope, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("response envelope: expected object, got %T", v)
	}
	typ, hasType := obj.GetString("type")
	id, hasId := obj.GetString("id")
	okVal, hasOk := obj.GetBool("ok")
	if hasType && typ == "response" && hasId && hasOk {
		if okVal {
			hasRes := obj.Has("result")
			if obj.Len() == 3 || (obj.Len() == 4 && hasRes) {
				var resultOpt jsonx.Opt[chord.JsonValue]
				if hasRes {
					resVal, _ := obj.Get("result")
					if resVal == nil {
						resultOpt = jsonx.Null[chord.JsonValue]()
					} else {
						resultOpt = jsonx.Some(resVal)
					}
				}
				return &ResponseSuccess{
					Type:   "response",
					Id:     id,
					Ok:     true,
					Result: resultOpt,
				}, nil
			}
		} else if obj.Len() == 4 {
			if errAny, hasErr := obj.Get("error"); hasErr {
				if pe, ok := protDecodeProtocolError(errAny); ok {
					return &ResponseFailure{
						Type:  "response",
						Id:    id,
						Ok:    false,
						Error: pe,
					}, nil
				}
			}
		}
	}
	return &UnknownResponseEnvelope{Raw: obj}, nil
}

func protMarshalUnknown(raw *jsonx.Object) ([]byte, error) {
	if raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(raw)
}

func protUnmarshalUnknown(data []byte, dst **jsonx.Object) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if val == nil {
		*dst = nil
		return nil
	}
	obj, ok := val.(*jsonx.Object)
	if !ok || obj == nil {
		return errors.New("expected JSON object")
	}
	*dst = obj
	return nil
}

func protDecodeNonNegSafeInt(obj *jsonx.Object, key string) (int, bool) {
	v, ok := obj.GetNumber(key)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v || v < 0 || v > 9007199254740991 {
		return 0, false
	}
	return int(v), true
}

func protDecodeSessionTarget(obj *jsonx.Object) (SessionTarget, bool) {
	if obj.Len() != 3 {
		return SessionTarget{}, false
	}
	sid, okSid := obj.GetString("serverId")
	sess, okSess := obj.GetString("sessionId")
	att, okAtt := obj.GetString("attachmentId")
	if !okSid || !okSess || !okAtt {
		return SessionTarget{}, false
	}
	return SessionTarget{
		ServerId:     ServerId(sid),
		SessionId:    sess,
		AttachmentId: att,
	}, true
}

func protDecodeProtocolError(v any) (ProtocolError, bool) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil || obj.Len() != 2 {
		return ProtocolError{}, false
	}
	code, okCode := obj.GetString("code")
	msg, okMsg := obj.GetString("message")
	if !okCode || !okMsg {
		return ProtocolError{}, false
	}
	return ProtocolError{Code: code, Message: msg}, true
}

func protRpcTargetToValue(target RpcTarget) any {
	switch t := target.(type) {
	case *ServerTarget:
		if t == nil {
			return nil
		}
		return jsonx.ObjectOf("serverId", string(t.ServerId))
	case *SessionTarget:
		if t == nil {
			return nil
		}
		return jsonx.ObjectOf(
			"serverId", string(t.ServerId),
			"sessionId", t.SessionId,
			"attachmentId", t.AttachmentId,
		)
	case *UnknownRpcTarget:
		if t == nil || t.Raw == nil {
			return nil
		}
		return t.Raw
	default:
		return nil
	}
}

func protClientMessageToValue(msg ClientMessage) any {
	switch m := msg.(type) {
	case *ClientHello:
		if m == nil {
			return nil
		}
		return jsonx.ObjectOf("type", m.Type, "version", float64(m.Version))
	case *RequestEnvelope:
		if m == nil {
			return nil
		}
		return jsonx.ObjectOf(
			"type", m.Type,
			"id", m.Id,
			"target", protRpcTargetToValue(m.Target),
			"call", m.Call,
		)
	case *CancelEnvelope:
		if m == nil {
			return nil
		}
		return jsonx.ObjectOf(
			"type", m.Type,
			"id", m.Id,
			"target", protRpcTargetToValue(m.Target),
		)
	case *UnknownClientMessage:
		if m == nil || m.Raw == nil {
			return nil
		}
		return m.Raw
	default:
		return nil
	}
}

func protServerMessageToValue(msg ServerMessage) any {
	switch m := msg.(type) {
	case *ServerHello:
		if m == nil {
			return nil
		}
		return jsonx.ObjectOf(
			"type", m.Type,
			"version", float64(m.Version),
			"serverId", string(m.ServerId),
		)
	case *ServerHelloError:
		if m == nil {
			return nil
		}
		return jsonx.ObjectOf(
			"type", m.Type,
			"error", jsonx.ObjectOf("code", m.Error.Code, "message", m.Error.Message),
		)
	case *ResponseSuccess:
		if m == nil {
			return nil
		}
		obj := jsonx.ObjectOf(
			"type", m.Type,
			"id", m.Id,
			"ok", m.Ok,
		)
		if !m.Result.IsZero() {
			if m.Result.IsNull() {
				obj.Set("result", nil)
			} else {
				val, _ := m.Result.Get()
				obj.Set("result", val)
			}
		}
		return obj
	case *ResponseFailure:
		if m == nil {
			return nil
		}
		return jsonx.ObjectOf(
			"type", m.Type,
			"id", m.Id,
			"ok", m.Ok,
			"error", jsonx.ObjectOf("code", m.Error.Code, "message", m.Error.Message),
		)
	case *UnknownResponseEnvelope:
		if m == nil || m.Raw == nil {
			return nil
		}
		return m.Raw
	case *ServiceEventEnvelope:
		if m == nil {
			return nil
		}
		return jsonx.ObjectOf(
			"type", m.Type,
			"subscriptionId", m.SubscriptionId,
			"update", m.Update,
		)
	case *AttachmentEnvelope:
		if m == nil {
			return nil
		}
		var att any
		if st, ok := m.Attachment.Get(); ok {
			att = jsonx.ObjectOf(
				"serverId", string(st.ServerId),
				"sessionId", st.SessionId,
				"attachmentId", st.AttachmentId,
			)
		}
		return jsonx.ObjectOf(
			"type", m.Type,
			"attachment", att,
		)
	case *UnknownServerMessage:
		if m == nil || m.Raw == nil {
			return nil
		}
		return m.Raw
	default:
		return nil
	}
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
