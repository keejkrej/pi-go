package ai_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func assertJSONObjectEqual(t *testing.T, got *jsonx.Object, wantJSON string) {
	t.Helper()
	wantVal, err := jsonx.Parse([]byte(wantJSON))
	if err != nil {
		t.Fatalf("jsonx.Parse(%q): %v", wantJSON, err)
	}
	wantObj, ok := wantVal.(*jsonx.Object)
	if !ok {
		t.Fatalf("wantJSON %q is %T, not *jsonx.Object", wantJSON, wantVal)
	}
	if !jsonx.DeepEqual(got, wantObj) {
		t.Fatalf("got %s, want %s", jsonx.MustStringify(got), jsonx.MustStringify(wantObj))
	}
}

func TestParseStreamingJson(t *testing.T) {
	t.Run("keeps invalid \\u escapes as literal text without dropping fields", func(t *testing.T) {
		assertJSONObjectEqual(t, ai.ParseStreamingJson(`{"text":"\url{x}"}`), `{"text":"\\url{x}"}`)
		assertJSONObjectEqual(t,
			ai.ParseStreamingJson(`{"path":"a.tex","content":"\usepackage{x}","mode":"w"}`),
			`{"path":"a.tex","content":"\\usepackage{x}","mode":"w"}`,
		)
	})

	t.Run("decodes valid \\u escapes", func(t *testing.T) {
		assertJSONObjectEqual(t, ai.ParseStreamingJson(`{"text":"caf\u00e9 \url"}`), `{"text":"café \\url"}`)
	})

	t.Run("omits a \\u escape that is cut off mid-stream", func(t *testing.T) {
		assertJSONObjectEqual(t, ai.ParseStreamingJson(`{"text":"ab\u00`), `{"text":"ab"}`)
	})
}

func TestParseToolCallArguments(t *testing.T) {
	t.Run("parses complete JSON and repairs invalid escapes", func(t *testing.T) {
		args, errPtr := ai.ParseToolCallArguments(`{"kind":"seek","note":"\url"}`)
		if errPtr != nil {
			t.Fatalf("unexpected error: %q", *errPtr)
		}
		assertJSONObjectEqual(t, args, `{"kind":"seek","note":"\\url"}`)
	})

	t.Run("uses empty arguments for empty text", func(t *testing.T) {
		args, errPtr := ai.ParseToolCallArguments("")
		if errPtr != nil {
			t.Fatalf("unexpected error: %q", *errPtr)
		}
		assertJSONObjectEqual(t, args, `{}`)
	})

	t.Run("does not keep the valid prefix of malformed JSON", func(t *testing.T) {
		raw := `{"action":"apply","actions":[{"kind":"seek","time":.5}]}`
		args, errPtr := ai.ParseToolCallArguments(raw)
		assertJSONObjectEqual(t, args, `{}`)
		if errPtr == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(*errPtr, "\n\nReceived arguments:\n") {
			t.Fatalf("missing Received arguments marker: %q", *errPtr)
		}
		if !strings.HasSuffix(*errPtr, raw) {
			t.Fatalf("error %q does not end with %q", *errPtr, raw)
		}
	})

	t.Run("does not complete cut-off JSON", func(t *testing.T) {
		raw := `{"kind":"seek","clipId":"clip-fl`
		args, errPtr := ai.ParseToolCallArguments(raw)
		assertJSONObjectEqual(t, args, `{}`)
		if errPtr == nil {
			t.Fatal("expected error")
		}
		if !strings.HasSuffix(*errPtr, raw) {
			t.Fatalf("error %q does not end with %q", *errPtr, raw)
		}
	})

	t.Run("rejects JSON that is not an object", func(t *testing.T) {
		args, errPtr := ai.ParseToolCallArguments(`[1,2]`)
		assertJSONObjectEqual(t, args, `{}`)
		want := "Tool arguments must be a JSON object\n\nReceived arguments:\n[1,2]"
		if errPtr == nil || *errPtr != want {
			t.Fatalf("errPtr = %v, want %q", errPtr, want)
		}
	})
}
