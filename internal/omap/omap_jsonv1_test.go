//go:build !goexperiment.jsonv2

package omap_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/omap"
)

func omapEncodeNoEscape[K comparable, V any](t *testing.T, m *omap.Map[K, V]) string {
	t.Helper()
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
