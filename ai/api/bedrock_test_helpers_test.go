// Test helpers and pure stdlib AWS EventStream binary frame encoder for ai/api Bedrock tests.

package api_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tbdStrPtr(s string) *string { return &s }

func tbdBoolPtr(b bool) *bool { return &b }

func tbdModel(id, name, baseUrl string, compat ai.ModelCompat) *ai.Model {
	if name == "" {
		name = id
	}
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     name,
			Api:      string(ai.KnownApiBedrockConverseStream),
			Provider: "amazon-bedrock",
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
			Cost: ai.ModelCost{
				ModelCostRates: ai.ModelCostRates{
					Input:      5.0,
					Output:     25.0,
					CacheRead:  0.5,
					CacheWrite: 6.25,
				},
			},
		},
		Reasoning:     true,
		ContextWindow: 200000,
		MaxTokens:     16384,
		Compat:        ai.ModelCompatValue{V: compat},
	}
}

func tbdContext() *ai.TranscriptContext {
	return ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tbdStrPtr("hello")}, Timestamp: 1},
		},
	})
}

func tbdModelWithHeaders(baseUrl string, headers *omap.Map[string, string]) *ai.Model {
	m := tbdModel("us.anthropic.claude-opus-4-8", "", baseUrl, nil)
	m.Headers = headers
	return m
}

func tbdDrainEvents(ctx context.Context, stream *ai.AssistantMessageEventStream) []ai.AssistantMessageEvent {
	var out []ai.AssistantMessageEvent
	for {
		ev, ok, err := stream.Next(ctx)
		if err != nil || !ok {
			break
		}
		out = append(out, ev)
	}
	return out
}

func tbdWriteEventStreamHeader(buf *bytes.Buffer, name, value string) {
	buf.WriteByte(byte(len(name)))
	buf.WriteString(name)
	buf.WriteByte(7) // AWS EventStream header value type 7 = UTF-8 string
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(value)))
	buf.Write(l[:])
	buf.WriteString(value)
}

func tbdEncodeFrame(msgType, typeHeaderName, typeHeaderVal, jsonPayload string) []byte {
	var hdr bytes.Buffer
	tbdWriteEventStreamHeader(&hdr, ":message-type", msgType)
	tbdWriteEventStreamHeader(&hdr, typeHeaderName, typeHeaderVal)
	tbdWriteEventStreamHeader(&hdr, ":content-type", "application/json")

	hdrBytes := hdr.Bytes()
	payloadBytes := []byte(jsonPayload)
	totalLen := uint32(12 + len(hdrBytes) + len(payloadBytes) + 4)

	var frame bytes.Buffer
	var pre [8]byte
	binary.BigEndian.PutUint32(pre[0:4], totalLen)
	binary.BigEndian.PutUint32(pre[4:8], uint32(len(hdrBytes)))
	frame.Write(pre[:])

	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(pre[:]))
	frame.Write(crc[:])
	frame.Write(hdrBytes)
	frame.Write(payloadBytes)
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(frame.Bytes()))
	frame.Write(crc[:])
	return frame.Bytes()
}

func tbdEncodeEventFrame(eventType, jsonPayload string) []byte {
	return tbdEncodeFrame("event", ":event-type", eventType, jsonPayload)
}

func tbdEncodeExceptionFrame(exceptionType, jsonPayload string) []byte {
	return tbdEncodeFrame("exception", ":exception-type", exceptionType, jsonPayload)
}
