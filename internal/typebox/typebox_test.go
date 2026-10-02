package typebox

import (
	"os"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func TestVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonx.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	root, ok := doc.(*jsonx.Object)
	if !ok {
		t.Fatal("vectors root")
	}
	builds, _ := root.GetArray("builds")
	for _, el := range builds {
		b, _ := el.(*jsonx.Object)
		name, _ := b.GetString("name")
		want, _ := b.GetString("json")
		s := mustBuild(t, b, "dsl")
		if got := jsonx.MustStringify(s); got != want {
			t.Errorf("build %s\n got %s\nwant %s", name, got, want)
		}
	}
	ops, _ := root.GetArray("ops")
	for _, el := range ops {
		b, _ := el.(*jsonx.Object)
		name, _ := b.GetString("name")
		wantJSON, _ := b.GetString("json")
		s := mustBuild(t, b, "dsl")
		if got := jsonx.MustStringify(s); got != wantJSON {
			t.Errorf("op schema %s\n got %s\nwant %s", name, got, wantJSON)
			continue
		}
		value, _ := b.Get("value")
		wantCheck, _ := b.GetBool("check")
		if got := Check(s, value); got != wantCheck {
			t.Errorf("check %s got %v want %v", name, got, wantCheck)
		}
		wantErrs, _ := b.GetArray("errors")
		cmpErrors(t, name, Errors(s, value), wantErrs)
		fresh := cloneJSON(value)
		gotConv := Convert(s, fresh)
		wantConv, _ := b.Get("convert")
		if gs, ws := jsonx.MustStringify(gotConv), jsonx.MustStringify(wantConv); gs != ws {
			t.Errorf("convert %s\n got %s\nwant %s", name, gs, ws)
		}
		wantCC, _ := b.GetBool("checkConverted")
		if got := Check(s, gotConv); got != wantCC {
			t.Errorf("checkConverted %s got %v want %v", name, got, wantCC)
		}
		wantEC, _ := b.GetArray("errorsConverted")
		cmpErrors(t, name+"/converted", Errors(s, gotConv), wantEC)
	}
}

func cmpErrors(t *testing.T, name string, got []ValidationError, want []any) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("errors %s len got %d want %d\n got %s", name, len(got), len(want), dumpErrs(got))
		return
	}
	for i, w := range want {
		wo, _ := w.(*jsonx.Object)
		g := got[i]
		if sp, _ := wo.GetString("schemaPath"); g.SchemaPath != sp {
			t.Errorf("errors %s[%d] schemaPath got %q want %q", name, i, g.SchemaPath, sp)
		}
		if ip, _ := wo.GetString("instancePath"); g.InstancePath != ip {
			t.Errorf("errors %s[%d] instancePath got %q want %q", name, i, g.InstancePath, ip)
		}
		if kw, _ := wo.GetString("keyword"); g.Keyword != kw {
			t.Errorf("errors %s[%d] keyword got %q want %q", name, i, g.Keyword, kw)
		}
		if msg, _ := wo.GetString("message"); g.Message != msg {
			t.Errorf("errors %s[%d] message got %q want %q", name, i, g.Message, msg)
		}
		ps, _ := wo.GetString("params")
		if gp := jsonx.MustStringify(g.Params); gp != ps {
			t.Errorf("errors %s[%d] params got %s want %s", name, i, gp, ps)
		}
	}
}

func dumpErrs(errs []ValidationError) string {
	arr := make([]any, len(errs))
	for i, e := range errs {
		o := jsonx.NewObject()
		o.Set("schemaPath", e.SchemaPath)
		o.Set("instancePath", e.InstancePath)
		o.Set("keyword", e.Keyword)
		o.Set("message", e.Message)
		o.Set("params", jsonx.MustStringify(e.Params))
		arr[i] = o
	}
	return jsonx.MustStringify(arr)
}

func cloneJSON(v any) any {
	s := jsonx.MustStringify(v)
	out, err := jsonx.ParseString(s)
	if err != nil {
		panic(err)
	}
	return out
}

func mustBuild(t *testing.T, parent *jsonx.Object, key string) *Schema {
	t.Helper()
	v, ok := parent.Get(key)
	if !ok {
		t.Fatalf("missing %s", key)
	}
	s, err := buildDSL(v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func buildDSL(v any) (*Schema, error) {
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, errDSL("schema")
	}
	t, _ := o.GetString("t")
	switch t {
	case "Raw":
		raw, _ := o.Get("schema")
		return FromJSON(raw)
	case "Bool":
		b, ok := o.GetBool("value")
		if !ok {
			return nil, errDSL("bool")
		}
		return FromJSON(b)
	}
	var opts []*jsonx.Object
	if op := optObject(o); op != nil {
		opts = []*jsonx.Object{op}
	}
	var s *Schema
	switch t {
	case "String":
		s = String(opts...)
	case "Number":
		s = Number(opts...)
	case "Integer":
		s = Integer(opts...)
	case "Boolean":
		s = Boolean(opts...)
	case "Null":
		s = Null(opts...)
	case "Unknown":
		s = Unknown(opts...)
	case "Any":
		s = Any(opts...)
	case "Literal":
		val, _ := o.Get("value")
		s = Literal(val, opts...)
	case "Enum":
		vals, _ := o.GetArray("values")
		s = Enum(vals, opts...)
	case "Union":
		s = Union(buildList(o, "items"), opts...)
	case "Intersect":
		s = Intersect(buildList(o, "items"), opts...)
	case "Array":
		item, _ := o.Get("item")
		built, err := buildDSL(item)
		if err != nil {
			return nil, err
		}
		s = Array(built, opts...)
	case "Tuple":
		s = Tuple(buildList(o, "items"), opts...)
	case "Object":
		props, _ := o.GetArray("props")
		m := omap.NewMap[string, *Schema]()
		for _, p := range props {
			po, _ := p.(*jsonx.Object)
			k, _ := po.GetString("k")
			sv, _ := po.Get("s")
			sch, err := buildDSL(sv)
			if err != nil {
				return nil, err
			}
			if b, ok := po.GetBool("optional"); ok && b {
				sch = Optional(sch)
			}
			m.Set(k, sch)
		}
		s = Object(m, opts...)
	case "Record":
		key, _ := o.Get("key")
		val, _ := o.Get("value")
		ks, err := buildDSL(key)
		if err != nil {
			return nil, err
		}
		vs, err := buildDSL(val)
		if err != nil {
			return nil, err
		}
		s = Record(ks, vs, opts...)
	case "Ref":
		ref, _ := o.GetString("ref")
		s = Ref(ref, opts...)
	case "Unsafe":
		raw, _ := o.GetObject("raw")
		s = Unsafe(raw)
	default:
		return nil, errDSL(t)
	}
	if b, ok := o.GetBool("optional"); ok && b {
		s = Optional(s)
	}
	return s, nil
}

func buildList(o *jsonx.Object, key string) []*Schema {
	arr, _ := o.GetArray(key)
	out := make([]*Schema, len(arr))
	for i, el := range arr {
		s, err := buildDSL(el)
		if err != nil {
			panic(err)
		}
		out[i] = s
	}
	return out
}

func optObject(o *jsonx.Object) *jsonx.Object {
	arr, ok := o.GetArray("opts")
	if !ok || len(arr) == 0 {
		return nil
	}
	out := jsonx.NewObject()
	for _, el := range arr {
		e, _ := el.(*jsonx.Object)
		k, _ := e.GetString("k")
		if s, ok := e.Get("s"); ok {
			built, err := buildDSL(s)
			if err != nil {
				panic(err)
			}
			out.Set(k, built)
			continue
		}
		if m, ok := e.GetObject("map"); ok {
			built := jsonx.NewObject()
			for _, mk := range m.Keys() {
				mv, _ := m.Get(mk)
				sch, err := buildDSL(mv)
				if err != nil {
					panic(err)
				}
				built.Set(mk, sch)
			}
			out.Set(k, built)
			continue
		}
		v, _ := e.Get("v")
		out.Set(k, v)
	}
	return out
}

type dslError string

func (e dslError) Error() string { return "typebox test dsl: " + string(e) }

func errDSL(s string) error { return dslError(s) }

func TestNilCompileMutate(t *testing.T) {
	if Check(nil, 1) {
		t.Fatal("Check(nil)")
	}
	if errs := Errors(nil, 1); errs == nil || len(errs) != 0 {
		t.Fatalf("Errors(nil) %#v", errs)
	}
	if _, err := Compile(nil); err == nil {
		t.Fatal("Compile(nil)")
	}
	bad := String(jsonx.ObjectOf("pattern", "("))
	if _, err := Compile(bad); err == nil {
		t.Fatal("Compile invalid pattern")
	}
	ok, err := Compile(String())
	if err != nil {
		t.Fatal(err)
	}
	if !ok.Check("a") || ok.Check(1) {
		t.Fatal("validator check")
	}
	if errs := ok.Errors("a"); len(errs) != 0 {
		t.Fatalf("validator errors on match %#v", errs)
	}
	if errs := ok.Errors(1); len(errs) != 1 || errs[0].Message != "must be string" {
		t.Fatalf("validator errors %#v", errs)
	}

	props := omap.NewMap[string, *Schema]()
	props.Set("count", Number())
	schema := Object(props)
	input := jsonx.ObjectOf("count", "42", "extra", true)
	got := Convert(schema, input)
	if got != any(input) {
		t.Fatal("object convert did not return the same object")
	}
	if n, ok := input.GetNumber("count"); !ok || n != 42 {
		t.Fatalf("mutated count %#v", mustGet(input, "count"))
	}
	if b, ok := input.GetBool("extra"); !ok || !b {
		t.Fatal("extra dropped")
	}

	in := []any{"a", float64(1)}
	out := Convert(Array(String()), in).([]any)
	out[0] = "z"
	if in[0] != "a" {
		t.Fatal("array convert aliased the input")
	}

	base := String()
	m1 := omap.NewMap[string, *Schema]()
	m1.Set("a", base)
	m2 := omap.NewMap[string, *Schema]()
	m2.Set("a", Optional(base))
	if jsonx.MustStringify(Object(m1)) != `{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}` {
		t.Fatal(jsonx.MustStringify(Object(m1)))
	}
	if jsonx.MustStringify(Object(m2)) != `{"type":"object","properties":{"a":{"type":"string"}}}` {
		t.Fatal(jsonx.MustStringify(Object(m2)))
	}
}

func mustGet(o *jsonx.Object, k string) any {
	v, _ := o.Get(k)
	return v
}
