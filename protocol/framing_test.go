// Ported from packages/protocol/test/framing.test.ts (pi v1.0.0).

package protocol

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/js"
)

func tframConcat(chunks ...[]byte) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

func tframIntPtr(v int) *int {
	return &v
}

func TestBinaryFraming(t *testing.T) {
	t.Run("prefixes payloads with a four-byte big-endian length", func(t *testing.T) {
		f1, err := EncodeFrame([]byte{0xaa, 0xbb, 0xcc})
		if err != nil {
			t.Fatalf("EncodeFrame failed: %v", err)
		}
		if !bytes.Equal(f1, []byte{0x00, 0x00, 0x00, 0x03, 0xaa, 0xbb, 0xcc}) {
			t.Fatalf("unexpected frame: %x", f1)
		}
		f2, err := EncodeFrame([]byte{})
		if err != nil {
			t.Fatalf("EncodeFrame empty failed: %v", err)
		}
		if !bytes.Equal(f2, []byte{0, 0, 0, 0}) {
			t.Fatalf("unexpected empty frame: %x", f2)
		}
	})

	t.Run("decodes fragmented, coalesced, and empty frames in order", func(t *testing.T) {
		f1, _ := EncodeFrame([]byte{1, 2, 3})
		f2, _ := EncodeFrame([]byte{})
		f3, _ := EncodeFrame([]byte{4})
		wire := tframConcat(f1, f2, f3)

		decoder, err := NewFrameDecoder(nil)
		if err != nil {
			t.Fatalf("NewFrameDecoder failed: %v", err)
		}
		var frames [][]byte
		for _, b := range wire {
			got, err := decoder.Push([]byte{b})
			if err != nil {
				t.Fatalf("Push failed: %v", err)
			}
			frames = append(frames, got...)
		}
		if err := decoder.End(); err != nil {
			t.Fatalf("End failed: %v", err)
		}
		want := [][]byte{{1, 2, 3}, {}, {4}}
		if len(frames) != len(want) {
			t.Fatalf("got %d frames, want %d", len(frames), len(want))
		}
		for i := range want {
			if !bytes.Equal(frames[i], want[i]) {
				t.Fatalf("frame %d: got %x, want %x", i, frames[i], want[i])
			}
		}

		coalesced, _ := NewFrameDecoder(nil)
		cframes, err := coalesced.Push(wire)
		if err != nil {
			t.Fatalf("coalesced Push failed: %v", err)
		}
		if err := coalesced.End(); err != nil {
			t.Fatalf("coalesced End failed: %v", err)
		}
		if len(cframes) != len(want) {
			t.Fatalf("got %d coalesced frames, want %d", len(cframes), len(want))
		}
	})

	t.Run("assembles payloads spanning multiple internal blocks", func(t *testing.T) {
		payload := make([]byte, 70_000)
		for i := range payload {
			payload[i] = byte(i % 251)
		}
		wire, err := EncodeFrame(payload)
		if err != nil {
			t.Fatalf("EncodeFrame failed: %v", err)
		}
		decoder, _ := NewFrameDecoder(nil)
		a, err := decoder.Push(wire[:101])
		if err != nil {
			t.Fatalf("Push 1 failed: %v", err)
		}
		b, err := decoder.Push(wire[101:65_541])
		if err != nil {
			t.Fatalf("Push 2 failed: %v", err)
		}
		c, err := decoder.Push(wire[65_541:])
		if err != nil {
			t.Fatalf("Push 3 failed: %v", err)
		}
		if err := decoder.End(); err != nil {
			t.Fatalf("End failed: %v", err)
		}
		frames := append(append(a, b...), c...)
		if len(frames) != 1 || !bytes.Equal(frames[0], payload) {
			t.Fatalf("multi-block assembly mismatch")
		}
	})

	t.Run("handles every split point across a frame", func(t *testing.T) {
		wire, _ := EncodeFrame([]byte{10, 20, 30, 40})
		for split := 0; split <= len(wire); split++ {
			decoder, _ := NewFrameDecoder(nil)
			a, err := decoder.Push(wire[:split])
			if err != nil {
				t.Fatalf("split %d part 1 failed: %v", split, err)
			}
			b, err := decoder.Push(wire[split:])
			if err != nil {
				t.Fatalf("split %d part 2 failed: %v", split, err)
			}
			if err := decoder.End(); err != nil {
				t.Fatalf("split %d End failed: %v", split, err)
			}
			frames := append(a, b...)
			if len(frames) != 1 || !bytes.Equal(frames[0], []byte{10, 20, 30, 40}) {
				t.Fatalf("split %d mismatch: %#v", split, frames)
			}
		}
	})

	t.Run("copies payload bytes instead of retaining or aliasing input chunks", func(t *testing.T) {
		chunk, _ := EncodeFrame([]byte{1, 2, 3})
		decoder, _ := NewFrameDecoder(nil)
		frames, err := decoder.Push(chunk)
		if err != nil {
			t.Fatalf("Push failed: %v", err)
		}
		for i := range chunk {
			chunk[i] = 9
		}
		if len(frames) != 1 || !bytes.Equal(frames[0], []byte{1, 2, 3}) {
			t.Fatalf("expected copied bytes unaffected by caller mutation, got %#v", frames)
		}
	})

	t.Run("accepts empty chunks and a clean empty stream", func(t *testing.T) {
		decoder, _ := NewFrameDecoder(nil)
		frames, err := decoder.Push([]byte{})
		if err != nil || len(frames) != 0 {
			t.Fatalf("expected no frames, got (%#v, %v)", frames, err)
		}
		if err := decoder.End(); err != nil {
			t.Fatalf("End failed: %v", err)
		}
	})

	t.Run("rejects a truncated stream at end", func(t *testing.T) {
		cases := [][]byte{
			{0, 0, 0},
			{0, 0, 0, 2, 1},
		}
		for _, wire := range cases {
			decoder, _ := NewFrameDecoder(nil)
			frames, err := decoder.Push(wire)
			if err != nil || len(frames) != 0 {
				t.Fatalf("unexpected Push result: (%#v, %v)", frames, err)
			}
			err = decoder.End()
			var ferr *FrameError
			if !errors.As(err, &ferr) {
				t.Fatalf("expected FrameError at End, got %v", err)
			}
		}
	})

	t.Run("rejects an oversized declared length as soon as its header is complete", func(t *testing.T) {
		decoder, _ := NewFrameDecoder(&FrameDecoderOptions{MaxFrameLength: tframIntPtr(3)})
		if _, err := decoder.Push([]byte{0, 0, 0, 4}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "limit") {
			t.Fatalf("expected limit error, got %v", err)
		}
		if _, err := decoder.Push([]byte{1}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "failed") {
			t.Fatalf("expected failed error, got %v", err)
		}
	})

	t.Run("accepts a frame exactly at the configured maximum", func(t *testing.T) {
		decoder, _ := NewFrameDecoder(&FrameDecoderOptions{MaxFrameLength: tframIntPtr(3)})
		wire, _ := EncodeFrame([]byte{1, 2, 3})
		frames, err := decoder.Push(wire)
		if err != nil || len(frames) != 1 || !bytes.Equal(frames[0], []byte{1, 2, 3}) {
			t.Fatalf("unexpected frames: (%#v, %v)", frames, err)
		}
		if err := decoder.End(); err != nil {
			t.Fatalf("End failed: %v", err)
		}
	})

	t.Run("cannot be pushed after end", func(t *testing.T) {
		decoder, _ := NewFrameDecoder(nil)
		if err := decoder.End(); err != nil {
			t.Fatalf("End failed: %v", err)
		}
		if _, err := decoder.Push([]byte{}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "ended") {
			t.Fatalf("expected ended error on Push, got %v", err)
		}
		if err := decoder.End(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "ended") {
			t.Fatalf("expected ended error on second End, got %v", err)
		}
	})

	t.Run("rejects invalid maximum frame length", func(t *testing.T) {
		for _, bad := range []int{-1, DefaultMaxFrameLength * 1_000} {
			_, err := NewFrameDecoder(&FrameDecoderOptions{MaxFrameLength: tframIntPtr(bad)})
			var jsErr *js.JSError
			if !errors.As(err, &jsErr) || jsErr.Name() != "RangeError" {
				t.Fatalf("expected RangeError for %d, got %v", bad, err)
			}
		}
	})
}
