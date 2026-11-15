package core

import (
	"math"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func TestSettingsSchemaValidation(t *testing.T) {
	v, err := typebox.Compile(SettingsSchema)
	if err != nil {
		t.Fatalf("SettingsSchema compile failed: %v", err)
	}

	validDoc := jsonx.ObjectOf(
		"$schema", "https://pi.dev/schemas/settings.schema.json",
		"theme", "dark",
		"cacheWarming", "idle",
		"extensionSetting", jsonx.ObjectOf("enabled", true),
		"queueMode", "all",
		"websockets", true,
		"skills", jsonx.ObjectOf("enableSkillCommands", true, "customDirectories", []any{"./skills"}),
		"retry", jsonx.ObjectOf("maxDelayMs", float64(1000)),
	)
	if !v.Check(validDoc) {
		t.Fatalf("expected representative settings doc to validate: %v", typebox.Errors(SettingsSchema, validDoc))
	}

	if v.Check(jsonx.ObjectOf("cacheWarming", "always")) {
		t.Fatal("expected cacheWarming='always' rejected")
	}

	invalids := []*jsonx.Object{
		jsonx.ObjectOf("compaction", jsonx.ObjectOf("reserveTokens", float64(-1))),
		jsonx.ObjectOf("compaction", jsonx.ObjectOf("keepRecentTokens", 1.5)),
		jsonx.ObjectOf("compaction", jsonx.ObjectOf("modelOverrides", jsonx.ObjectOf("provider/model", jsonx.ObjectOf("reserveTokens", float64(9007199254740992))))),
		jsonx.ObjectOf("httpIdleTimeoutMs", float64(-1)),
		jsonx.ObjectOf("websocketConnectTimeoutMs", float64(-1)),
		jsonx.ObjectOf("httpIdleTimeoutMs", "bogus"),
		jsonx.ObjectOf("websocketConnectTimeoutMs", "30000"),
		jsonx.ObjectOf("codemode", jsonx.ObjectOf("inlineBudget", float64(-1))),
	}
	for i, inv := range invalids {
		if v.Check(inv) {
			t.Fatalf("invalid case #%d unexpectedly validated", i)
		}
	}

	maxSafe := math.Floor(9007199254740991)
	validEdge := jsonx.ObjectOf(
		"compaction", jsonx.ObjectOf(
			"reserveTokens", float64(0),
			"keepRecentTokens", maxSafe,
			"modelOverrides", jsonx.ObjectOf("provider/model", jsonx.ObjectOf("reserveTokens", float64(0))),
		),
		"httpIdleTimeoutMs", float64(0),
		"websocketConnectTimeoutMs", "disabled",
		"codemode", jsonx.ObjectOf("inlineBudget", float64(0)),
	)
	if !v.Check(validEdge) {
		t.Fatalf("expected valid edge case doc to validate: %v", typebox.Errors(SettingsSchema, validEdge))
	}
}

func TestSettingsUnionValueRoundTrip(t *testing.T) {
	t.Run("QuietStartup", func(t *testing.T) {
		for _, in := range []string{`true`, `false`, `"header"`} {
			var q QuietStartup
			if err := jsonx.Unmarshal([]byte(in), &q); err != nil {
				t.Fatalf("unmarshal %s: %v", in, err)
			}
			out, err := jsonx.Stringify(q)
			if err != nil || out != in {
				t.Fatalf("roundtrip %s -> %s, err=%v", in, out, err)
			}
		}
	})

	t.Run("TimeoutSetting", func(t *testing.T) {
		for _, in := range []string{`"disabled"`, `0`, `30000`} {
			var ts TimeoutSetting
			if err := jsonx.Unmarshal([]byte(in), &ts); err != nil {
				t.Fatalf("unmarshal %s: %v", in, err)
			}
			out, err := jsonx.Stringify(ts)
			if err != nil || out != in {
				t.Fatalf("roundtrip %s -> %s, err=%v", in, out, err)
			}
		}
	})

	t.Run("PackageSource", func(t *testing.T) {
		var pStr, pObj PackageSource
		if err := jsonx.Unmarshal([]byte(`"npm:foo"`), &pStr); err != nil {
			t.Fatal(err)
		}
		if s, err := jsonx.Stringify(pStr); err != nil || s != `"npm:foo"` {
			t.Fatalf("string PackageSource = %s, err=%v", s, err)
		}
		inObj := `{"source":"npm:bar","autoload":false,"skills":["s1"]}`
		if err := jsonx.Unmarshal([]byte(inObj), &pObj); err != nil {
			t.Fatal(err)
		}
		if s, err := jsonx.Stringify(pObj); err != nil || s != inObj {
			t.Fatalf("obj PackageSource = %s, want %s, err=%v", s, inObj, err)
		}
	})
}
