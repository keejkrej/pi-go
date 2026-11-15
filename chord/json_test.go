// Ported from packages/chord/test/json.test.ts (pi v1.0.0).

package chord

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestIsJsonValue(t *testing.T) {
	t.Run("checks strict JSON without normalizing it", func(t *testing.T) {
		nested := jsonx.ObjectOf("nested", []any{float64(1), true, nil})
		if !IsJsonValue(nested) {
			t.Fatalf("expected nested object to be valid strict JSON")
		}
		if IsJsonValue([]byte{1}) {
			t.Fatalf("expected []byte to be rejected as strict JSON")
		}
		if IsJsonValue(math.Inf(1)) {
			t.Fatalf("expected +Inf to be rejected as strict JSON")
		}
		if IsJsonValue(math.NaN()) {
			t.Fatalf("expected NaN to be rejected as strict JSON")
		}
		if IsJsonValue(1) {
			t.Fatalf("expected untyped Go int to be rejected as strict JSON")
		}
		if IsJsonValue((*jsonx.Object)(nil)) {
			t.Fatalf("expected typed nil *jsonx.Object to be rejected as strict JSON")
		}
		if IsJsonValue([]any(nil)) {
			t.Fatalf("expected nil []any slice to be rejected as strict JSON")
		}

		cyclic := jsonx.NewObject()
		cyclic.Set("self", cyclic)
		if IsJsonValue(cyclic) {
			t.Fatalf("expected cyclic object to be rejected as strict JSON")
		}

		cyclicArr := make([]any, 1)
		cyclicArr[0] = cyclicArr
		if IsJsonValue(cyclicArr) {
			t.Fatalf("expected cyclic slice to be rejected as strict JSON")
		}
	})
}

func TestCopyJson(t *testing.T) {
	t.Run("copies strict JSON without retaining aliases", func(t *testing.T) {
		shared := jsonx.ObjectOf("value", float64(1))
		input := jsonx.ObjectOf("left", shared, "right", shared)
		copiedAny, err := CopyJson(input, nil)
		if err != nil {
			t.Fatalf("CopyJson failed: %v", err)
		}
		copied, ok := copiedAny.(*jsonx.Object)
		if !ok {
			t.Fatalf("expected *jsonx.Object, got %T", copiedAny)
		}
		if !jsonx.DeepEqual(copied, input) {
			t.Fatalf("copied object differs from input")
		}
		if copied == input {
			t.Fatalf("expected copied object not to alias input")
		}
		copiedLeftAny, _ := copied.Get("left")
		copiedRightAny, _ := copied.Get("right")
		copiedLeft := copiedLeftAny.(*jsonx.Object)
		copiedRight := copiedRightAny.(*jsonx.Object)
		if copiedLeft == shared || copiedRight == shared || copiedLeft == copiedRight {
			t.Fatalf("expected copied sub-objects not to alias shared or each other")
		}
	})

	t.Run("preserves own __proto__ data properties", func(t *testing.T) {
		input := jsonx.NewObject()
		input.Set("__proto__", jsonx.ObjectOf("safe", true))
		copiedAny, err := CopyJson(input, nil)
		if err != nil {
			t.Fatalf("CopyJson failed: %v", err)
		}
		copied := copiedAny.(*jsonx.Object)
		if !copied.Has("__proto__") {
			t.Fatalf("expected __proto__ property to be preserved")
		}
		protoVal, _ := copied.Get("__proto__")
		if !jsonx.DeepEqual(protoVal, jsonx.ObjectOf("safe", true)) {
			t.Fatalf("unexpected __proto__ value: %#v", protoVal)
		}
	})

	t.Run("rejects cycles and non-strict values", func(t *testing.T) {
		cyclic := jsonx.NewObject()
		cyclic.Set("self", cyclic)

		cyclicSlice := make([]any, 1)
		cyclicSlice[0] = cyclicSlice

		invalids := []any{
			cyclic,
			cyclicSlice,
			[]byte{1},
			math.Inf(-1),
			math.NaN(),
			struct{ A int }{A: 1},
			(*jsonx.Object)(nil),
			[]any(nil),
		}
		for _, invalid := range invalids {
			if IsJsonValue(invalid) {
				t.Fatalf("expected IsJsonValue(%T) to be false", invalid)
			}
			_, err := CopyJson(invalid, nil)
			if err == nil {
				t.Fatalf("expected CopyJson(%T) to fail", invalid)
			}
			var jsErr *js.JSError
			if !errors.As(err, &jsErr) || jsErr.Name() != "TypeError" {
				t.Fatalf("expected TypeError for %T, got %v", invalid, err)
			}
			if !strings.Contains(err.Error(), "strict JSON") {
				t.Fatalf("expected strict JSON error message for %T, got %q", invalid, err.Error())
			}
		}
	})
}
