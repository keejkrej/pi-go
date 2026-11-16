// Ported from packages/protocol/test/protocol.test.ts (pi v1.0.0).

package protocol

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol/cbor"
)

func tprotIntPtr(v int) *int {
	return &v
}

var tprotClientHello = &ClientHello{Type: "hello", Version: ProtocolVersion}
var tprotServerHello = &ServerHello{
	Type:     "hello",
	Version:  ProtocolVersion,
	ServerId: "00000000-0000-4000-8000-000000000001",
}

func TestProtocolValidation(t *testing.T) {
	t.Run("negotiates protocol version 8", func(t *testing.T) {
		if ProtocolVersion != 8 {
			t.Fatalf("got ProtocolVersion=%d, want 8", ProtocolVersion)
		}
		if !IsSupportedProtocolVersion(8) {
			t.Fatalf("expected 8 to be supported")
		}
		if IsSupportedProtocolVersion(7) {
			t.Fatalf("expected 7 not to be supported")
		}
		if IsSupportedProtocolVersion(8.5) {
			t.Fatalf("expected 8.5 not to be supported")
		}
		if IsSupportedProtocolVersion(math.NaN()) || IsSupportedProtocolVersion(math.Inf(1)) {
			t.Fatalf("expected non-finite version not to be supported")
		}
	})

	t.Run("accepts integer client hello version for negotiation", func(t *testing.T) {
		for _, version := range []int{0, ProtocolVersion, ProtocolVersion + 1} {
			msg := &ClientHello{Type: "hello", Version: version}
			parsed, err := ParseClientMessage(msg)
			if err != nil || !reflect.DeepEqual(parsed, msg) {
				t.Fatalf("version %d: got (%#v, %v)", version, parsed, err)
			}
		}
	})

	t.Run("rejects invalid client hello", func(t *testing.T) {
		invalids := []any{
			jsonx.ObjectOf("type", "hello", "version", "8"),
			jsonx.ObjectOf("type", "hello", "version", 8.5),
			jsonx.ObjectOf("type", "hello", "version", float64(ProtocolVersion), "extra", true),
			&ClientHello{Type: "hello", Version: -1},
			(*ClientHello)(nil),
		}
		for i, candidate := range invalids {
			_, err := ParseClientMessage(candidate)
			var pve *ProtocolValidationError
			if !errors.As(err, &pve) {
				t.Fatalf("case %d: expected ProtocolValidationError, got %v", i, err)
			}
		}
	})

	t.Run("rejects non-canonical UUIDv4 server ID", func(t *testing.T) {
		badIds := []string{
			"",
			"server-1",
			"00000000-0000-7000-8000-000000000001",
			"00000000-0000-4000-7000-000000000001",
			"00000000-0000-4000-8000-00000000000A",
		}
		for _, sid := range badIds {
			if IsServerId(sid) || IsServerId(ServerId(sid)) {
				t.Fatalf("expected IsServerId(%q) false", sid)
			}
			msg := jsonx.ObjectOf(
				"type", "request",
				"id", "request-1",
				"target", jsonx.ObjectOf("serverId", sid),
				"call", jsonx.ObjectOf("serviceId", "pi.models", "member", "list", "args", []any{}),
			)
			_, err := ParseClientMessage(msg)
			var pve *ProtocolValidationError
			if !errors.As(err, &pve) {
				t.Fatalf("serverId %q: expected ProtocolValidationError, got %v", sid, err)
			}
		}
		if !IsServerId(ServerId("00000000-0000-4000-8000-000000000001")) {
			t.Fatalf("expected canonical ServerId to pass IsServerId")
		}
	})

	t.Run("keeps routed request and event payloads opaque", func(t *testing.T) {
		request := &RequestEnvelope{
			Type: "request",
			Id:   "request-1",
			Target: &SessionTarget{
				ServerId:     "00000000-0000-4000-8000-000000000001",
				SessionId:    "session-1",
				AttachmentId: "attachment-1",
			},
			Call: jsonx.ObjectOf(
				"serviceId", "application.custom",
				"instance", jsonx.ObjectOf("key", "instance-1", "generation", float64(2)),
				"member", "invoke",
				"args", []any{jsonx.ObjectOf("arbitrary", true), []any{"opaque"}},
			),
		}
		parsedReq, err := ParseClientMessage(request)
		if err != nil || !reflect.DeepEqual(parsedReq, request) {
			t.Fatalf("got (%#v, %v), want %#v", parsedReq, err, request)
		}

		opaqueEvent := &ServiceEventEnvelope{
			Type:           "service_update",
			SubscriptionId: "subscription-1",
			Update:         jsonx.ObjectOf("applicationDefined", true),
		}
		parsedEvt, err := ParseServerMessage(opaqueEvent)
		if err != nil || !reflect.DeepEqual(parsedEvt, opaqueEvent) {
			t.Fatalf("got (%#v, %v), want %#v", parsedEvt, err, opaqueEvent)
		}
	})

	t.Run("rejects non-strict-JSON opaque payloads without normalizing bytes or NaN", func(t *testing.T) {
		for _, badCall := range []any{[]byte{1}, math.NaN(), math.Inf(1)} {
			req := &RequestEnvelope{
				Type:   "request",
				Id:     "request-1",
				Target: &ServerTarget{ServerId: "00000000-0000-4000-8000-000000000001"},
				Call:   badCall,
			}
			_, err := ParseClientMessage(req)
			var pve *ProtocolValidationError
			if !errors.As(err, &pve) {
				t.Fatalf("expected ProtocolValidationError for non-JSON call %#v, got %v", badCall, err)
			}
		}
	})

	t.Run("rejects malformed request boundaries", func(t *testing.T) {
		badReqs := []any{
			jsonx.ObjectOf("type", "request", "id", "request-1", "target", jsonx.ObjectOf("serverId", "00000000-0000-4000-8000-000000000001", "sessionId", "s1"), "call", jsonx.NewObject()),
			jsonx.ObjectOf("type", "request", "id", "", "target", jsonx.ObjectOf("serverId", "00000000-0000-4000-8000-000000000001"), "call", jsonx.NewObject()),
			jsonx.ObjectOf("type", "cancel", "id", "request-1", "target", jsonx.ObjectOf("serverId", "00000000-0000-4000-8000-000000000001"), "extra", true),
		}
		for i, bad := range badReqs {
			_, err := ParseClientMessage(bad)
			var pve *ProtocolValidationError
			if !errors.As(err, &pve) {
				t.Fatalf("badReq %d: expected ProtocolValidationError, got %v", i, err)
			}
		}
	})

	t.Run("accepts successful void, null, and value responses and attachment envelopes", func(t *testing.T) {
		voidRespObj := jsonx.ObjectOf("type", "response", "id", "request-1", "ok", true)
		parsed, err := ParseServerMessage(voidRespObj)
		if err != nil {
			t.Fatalf("ParseServerMessage void failed: %v", err)
		}
		rs, ok := parsed.(*ResponseSuccess)
		if !ok || !rs.Result.IsZero() {
			t.Fatalf("expected ResponseSuccess with absent Result, got %#v", parsed)
		}

		nullRespObj := jsonx.ObjectOf("type", "response", "id", "request-1", "ok", true, "result", nil)
		parsedNull, err := ParseServerMessage(nullRespObj)
		if err != nil {
			t.Fatalf("ParseServerMessage null failed: %v", err)
		}
		rsNull, ok := parsedNull.(*ResponseSuccess)
		if !ok || !rsNull.Result.IsNull() {
			t.Fatalf("expected ResponseSuccess with null Result, got %#v", parsedNull)
		}

		detachWire, err := EncodeServerMessage(&AttachmentEnvelope{Type: "attachment", Attachment: jsonx.Null[SessionTarget]()}, nil)
		if err != nil {
			t.Fatalf("EncodeServerMessage detach failed: %v", err)
		}
		dec, _ := NewServerMessageDecoder(nil)
		msgs, err := dec.Push(detachWire)
		if err != nil || len(msgs) != 1 {
			t.Fatalf("decode detach failed: (%#v, %v)", msgs, err)
		}
		if att, ok := msgs[0].(*AttachmentEnvelope); !ok || !att.Attachment.IsNull() {
			t.Fatalf("expected null AttachmentEnvelope, got %#v", msgs[0])
		}
	})

	t.Run("rejects malformed server boundaries", func(t *testing.T) {
		badMsgs := []any{
			jsonx.ObjectOf("type", "hello", "version", float64(ProtocolVersion), "serverId", "server-1"),
			jsonx.ObjectOf("type", "response", "id", "request-1", "ok", true, "result", []any{}, "extra", true),
			jsonx.ObjectOf("type", "response", "id", "request-1", "ok", false, "error", jsonx.ObjectOf("code", "", "message", "bad")),
			jsonx.ObjectOf("type", "hello", "version", float64(ProtocolVersion), "serverId", "00000000-0000-4000-8000-000000000001", "snapshot", jsonx.NewObject()),
			jsonx.ObjectOf("type", "unknown", "event", jsonx.NewObject()),
			`{"type":"hello","version":8}`,
		}
		for i, bad := range badMsgs {
			_, err := ParseServerMessage(bad)
			var pve *ProtocolValidationError
			if !errors.As(err, &pve) {
				t.Fatalf("bad server %d: expected ProtocolValidationError, got %v", i, err)
			}
		}
		if _, err := ParseClientMessage(`{"type":"hello","version":8}`); err == nil {
			t.Fatalf("expected JSON string client message to fail")
		}
	})

	t.Run("accepts opaque error codes", func(t *testing.T) {
		for _, code := range []string{"wrong_server", "cancelled", "service_not_found", "application_error"} {
			msg := &ResponseFailure{
				Type:  "response",
				Id:    "request-1",
				Ok:    false,
				Error: ProtocolError{Code: code, Message: "safe"},
			}
			parsed, err := ParseServerMessage(msg)
			if err != nil || !reflect.DeepEqual(parsed, msg) {
				t.Fatalf("code %s: got (%#v, %v)", code, parsed, err)
			}
		}
	})

	t.Run("decodes unknown variants cleanly without losing raw fields", func(t *testing.T) {
		unkClient, err := UnmarshalClientMessage([]byte(`{"type":"future","x":1}`))
		if err != nil {
			t.Fatalf("UnmarshalClientMessage failed: %v", err)
		}
		if u, ok := unkClient.(*UnknownClientMessage); !ok || u.Raw == nil {
			t.Fatalf("expected *UnknownClientMessage, got %#v", unkClient)
		}
		unkTarget, err := UnmarshalRpcTarget([]byte(`{"custom":"route"}`))
		if err != nil {
			t.Fatalf("UnmarshalRpcTarget failed: %v", err)
		}
		if u, ok := unkTarget.(*UnknownRpcTarget); !ok || u.Raw == nil {
			t.Fatalf("expected *UnknownRpcTarget, got %#v", unkTarget)
		}
		unkResp, err := UnmarshalResponseEnvelope([]byte(`{"type":"response","id":"r1","ok":"maybe"}`))
		if err != nil {
			t.Fatalf("UnmarshalResponseEnvelope failed: %v", err)
		}
		if u, ok := unkResp.(*UnknownResponseEnvelope); !ok || u.Raw == nil {
			t.Fatalf("expected *UnknownResponseEnvelope, got %#v", unkResp)
		}
		unkSrv, err := UnmarshalServerMessage([]byte(`{"type":"future_srv","v":2}`))
		if err != nil {
			t.Fatalf("UnmarshalServerMessage failed: %v", err)
		}
		if u, ok := unkSrv.(*UnknownServerMessage); !ok || u.Raw == nil {
			t.Fatalf("expected *UnknownServerMessage, got %#v", unkSrv)
		}
	})
}

func TestValidatedFramedProtocolAPIs(t *testing.T) {
	t.Run("encodes complete client and server frames", func(t *testing.T) {
		cwire, err := EncodeClientMessage(tprotClientHello, nil)
		if err != nil {
			t.Fatalf("EncodeClientMessage failed: %v", err)
		}
		fd, _ := NewFrameDecoder(nil)
		cframes, err := fd.Push(cwire)
		if err != nil || len(cframes) != 1 {
			t.Fatalf("frame decode failed: (%#v, %v)", cframes, err)
		}
		cval, err := cbor.DecodeCbor(cframes[0], nil)
		if err != nil {
			t.Fatalf("DecodeCbor failed: %v", err)
		}
		cparsed, err := ParseClientMessage(cval)
		if err != nil || !reflect.DeepEqual(cparsed, tprotClientHello) {
			t.Fatalf("client round-trip mismatch: (%#v, %v)", cparsed, err)
		}

		swire, err := EncodeServerMessage(tprotServerHello, nil)
		if err != nil {
			t.Fatalf("EncodeServerMessage failed: %v", err)
		}
		fd2, _ := NewFrameDecoder(nil)
		sframes, err := fd2.Push(swire)
		if err != nil || len(sframes) != 1 {
			t.Fatalf("server frame decode failed: (%#v, %v)", sframes, err)
		}
		sval, err := cbor.DecodeCbor(sframes[0], nil)
		if err != nil {
			t.Fatalf("DecodeCbor failed: %v", err)
		}
		sparsed, err := ParseServerMessage(sval)
		if err != nil || !reflect.DeepEqual(sparsed, tprotServerHello) {
			t.Fatalf("server round-trip mismatch: (%#v, %v)", sparsed, err)
		}
	})

	t.Run("preserves -0.0 inside opaque Call across CBOR frame round-trip", func(t *testing.T) {
		negZero := math.Copysign(0, -1)
		req := &RequestEnvelope{
			Type:   "request",
			Id:     "req-neg0",
			Target: &ServerTarget{ServerId: "00000000-0000-4000-8000-000000000001"},
			Call:   negZero,
		}
		wire, err := EncodeClientMessage(req, nil)
		if err != nil {
			t.Fatalf("EncodeClientMessage failed: %v", err)
		}
		dec, _ := NewClientMessageDecoder(nil)
		msgs, err := dec.Push(wire)
		if err != nil || len(msgs) != 1 {
			t.Fatalf("decode failed: (%#v, %v)", msgs, err)
		}
		got := msgs[0].(*RequestEnvelope)
		f, ok := got.Call.(float64)
		if !ok || f != 0 || !math.Signbit(f) {
			t.Fatalf("expected -0.0 preserved in Call, got %#v", got.Call)
		}
	})

	t.Run("enforces outbound frame limits", func(t *testing.T) {
		if _, err := EncodeClientMessage(tprotClientHello, &FrameDecoderOptions{MaxFrameLength: tprotIntPtr(8)}); err == nil {
			t.Fatalf("expected error for small maxFrameLength on client")
		} else {
			var pve *ProtocolValidationError
			if !errors.As(err, &pve) {
				t.Fatalf("expected ProtocolValidationError, got %v", err)
			}
		}
		if _, err := EncodeServerMessage(tprotServerHello, &FrameDecoderOptions{MaxFrameLength: tprotIntPtr(8)}); err == nil {
			t.Fatalf("expected error for small maxFrameLength on server")
		}
	})

	t.Run("incrementally decodes fragmented and coalesced client messages", func(t *testing.T) {
		request := &RequestEnvelope{
			Type:   "request",
			Id:     "request-1",
			Target: &ServerTarget{ServerId: "00000000-0000-4000-8000-000000000001"},
			Call:   jsonx.ObjectOf("serviceId", "pi.session-directory", "member", "list", "args", []any{}),
		}
		first, _ := EncodeClientMessage(tprotClientHello, nil)
		second, _ := EncodeClientMessage(request, nil)
		wire := append(append([]byte{}, first...), second...)

		for split := 0; split <= len(wire); split++ {
			dec, _ := NewClientMessageDecoder(nil)
			a, err := dec.Push(wire[:split])
			if err != nil {
				t.Fatalf("split %d part 1 failed: %v", split, err)
			}
			b, err := dec.Push(wire[split:])
			if err != nil {
				t.Fatalf("split %d part 2 failed: %v", split, err)
			}
			if err := dec.End(); err != nil {
				t.Fatalf("split %d End failed: %v", split, err)
			}
			msgs := append(a, b...)
			if len(msgs) != 2 || !reflect.DeepEqual(msgs[0], tprotClientHello) || !reflect.DeepEqual(msgs[1], request) {
				t.Fatalf("split %d mismatch: %#v", split, msgs)
			}
		}
	})

	t.Run("incrementally decodes fragmented and coalesced server messages", func(t *testing.T) {
		resp := &ResponseSuccess{
			Type:   "response",
			Id:     "request-1",
			Ok:     true,
			Result: jsonx.Some[any]([]any{}),
		}
		first, _ := EncodeServerMessage(tprotServerHello, nil)
		second, _ := EncodeServerMessage(resp, nil)
		wire := append(append([]byte{}, first...), second...)

		split := len(first) + len(second)/2
		dec, _ := NewServerMessageDecoder(nil)
		m1, err := dec.Push(wire[:split])
		if err != nil || len(m1) != 1 || !reflect.DeepEqual(m1[0], tprotServerHello) {
			t.Fatalf("part 1 mismatch: (%#v, %v)", m1, err)
		}
		m2, err := dec.Push(wire[split:])
		if err != nil || len(m2) != 1 || !reflect.DeepEqual(m2[0], resp) {
			t.Fatalf("part 2 mismatch: (%#v, %v)", m2, err)
		}
		if err := dec.End(); err != nil {
			t.Fatalf("End failed: %v", err)
		}
	})

	t.Run("rejects invalid framed input and transitions to failed", func(t *testing.T) {
		fEmpty, _ := EncodeFrame([]byte{})
		fBadCbor, _ := EncodeFrame([]byte{0xff})
		badObjCbor, _ := cbor.EncodeCbor(jsonx.ObjectOf("type", "hello", "version", float64(1), "extra", true), nil)
		fBadSchema, _ := EncodeFrame(badObjCbor)

		for _, wire := range [][]byte{fEmpty, fBadCbor, fBadSchema} {
			dec, _ := NewClientMessageDecoder(nil)
			_, err := dec.Push(wire)
			var pve *ProtocolValidationError
			if !errors.As(err, &pve) {
				t.Fatalf("expected ProtocolValidationError, got %v", err)
			}
			helloWire, _ := EncodeClientMessage(tprotClientHello, nil)
			if _, err2 := dec.Push(helloWire); err2 == nil || !strings.Contains(strings.ToLower(err2.Error()), "failed") {
				t.Fatalf("expected failed error after failure, got %v", err2)
			}
		}
	})

	t.Run("rejects truncated and oversized framing", func(t *testing.T) {
		trunc, _ := NewServerMessageDecoder(nil)
		if msgs, err := trunc.Push([]byte{0, 0, 0, 2, 1}); err != nil || len(msgs) != 0 {
			t.Fatalf("unexpected Push: (%#v, %v)", msgs, err)
		}
		err := trunc.End()
		var pve *ProtocolValidationError
		if !errors.As(err, &pve) {
			t.Fatalf("expected ProtocolValidationError on truncated End, got %v", err)
		}

		oversized, _ := NewClientMessageDecoder(&FrameDecoderOptions{MaxFrameLength: tprotIntPtr(3)})
		if _, err := oversized.Push([]byte{0, 0, 0, 4}); !errors.As(err, &pve) {
			t.Fatalf("expected ProtocolValidationError on oversized header, got %v", err)
		}
	})
}
