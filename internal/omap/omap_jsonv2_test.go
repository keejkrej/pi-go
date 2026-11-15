//go:build goexperiment.jsonv2

package omap_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func omapEncodeNoEscape[K comparable, V any](t *testing.T, m *omap.Map[K, V]) string {
	t.Helper()
	// Under goexperiment.jsonv2 (default in Go 1.27), json.Encoder.SetEscapeHTML(false)
	// only clears jsontext.EscapeForHTML and leaves jsontext.EscapeForJS enabled,
	// re-escaping raw U+2028/U+2029 from Map.MarshalJSON. Verify json.Encoder still
	// succeeds without escaping HTML, and verify jsonx.Stringify preserves raw
	// U+2028/U+2029 end-to-end per PORTING.md §5.1.
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		t.Fatal(err)
	}
	got, err := jsonx.Stringify(m)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
