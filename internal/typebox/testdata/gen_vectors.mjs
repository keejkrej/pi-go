import { writeFileSync } from "node:fs";
import { Type } from "file:///C:/Users/ctyja/workspace/pi/node_modules/typebox/build/index.mjs";
import { Value } from "file:///C:/Users/ctyja/workspace/pi/node_modules/typebox/build/value/index.mjs";

function optsOf(d) {
  if (!d.opts || d.opts.length === 0) return undefined;
  const o = {};
  for (const ent of d.opts) {
    if (ent.s) o[ent.k] = build(ent.s);
    else if (ent.map) {
      const m = {};
      for (const [k, v] of Object.entries(ent.map)) m[k] = build(v);
      o[ent.k] = m;
    } else o[ent.k] = ent.v;
  }
  return o;
}

function build(d) {
  if (d.t === "Raw") return d.schema;
  if (d.t === "Bool") return d.value;
  const opts = optsOf(d);
  let s;
  switch (d.t) {
    case "String":
      s = opts ? Type.String(opts) : Type.String();
      break;
    case "Number":
      s = opts ? Type.Number(opts) : Type.Number();
      break;
    case "Integer":
      s = opts ? Type.Integer(opts) : Type.Integer();
      break;
    case "Boolean":
      s = opts ? Type.Boolean(opts) : Type.Boolean();
      break;
    case "Null":
      s = opts ? Type.Null(opts) : Type.Null();
      break;
    case "Unknown":
      s = opts ? Type.Unknown(opts) : Type.Unknown();
      break;
    case "Any":
      s = opts ? Type.Any(opts) : Type.Any();
      break;
    case "Literal":
      s = opts ? Type.Literal(d.value, opts) : Type.Literal(d.value);
      break;
    case "Enum":
      s = opts ? Type.Enum(d.values, opts) : Type.Enum(d.values);
      break;
    case "Union":
      s = opts ? Type.Union(d.items.map(build), opts) : Type.Union(d.items.map(build));
      break;
    case "Intersect":
      s = opts ? Type.Intersect(d.items.map(build), opts) : Type.Intersect(d.items.map(build));
      break;
    case "Array":
      s = opts ? Type.Array(build(d.item), opts) : Type.Array(build(d.item));
      break;
    case "Tuple":
      s = opts ? Type.Tuple(d.items.map(build), opts) : Type.Tuple(d.items.map(build));
      break;
    case "Object": {
      const props = {};
      for (const p of d.props || []) {
        let sch = build(p.s);
        if (p.optional) sch = Type.Optional(sch);
        props[p.k] = sch;
      }
      s = opts ? Type.Object(props, opts) : Type.Object(props);
      break;
    }
    case "Record":
      s = opts ? Type.Record(build(d.key), build(d.value), opts) : Type.Record(build(d.key), build(d.value));
      break;
    case "Ref":
      s = opts ? Type.Ref(d.ref, opts) : Type.Ref(d.ref);
      break;
    case "Unsafe":
      s = Type.Unsafe(d.raw);
      break;
    default:
      throw new Error("unknown dsl " + d.t);
  }
  if (d.optional) s = Type.Optional(s);
  return s;
}

function slim(e) {
  return {
    schemaPath: e.schemaPath,
    instancePath: e.instancePath,
    keyword: e.keyword,
    message: e.message,
    params: JSON.stringify(e.params),
  };
}

const builds = [];
const ops = [];

function snap(name, dsl) {
  builds.push({ name, dsl, json: JSON.stringify(build(dsl)) });
}

function op(name, dsl, value) {
  const schema = build(dsl);
  const json = JSON.stringify(schema);
  const check = Value.Check(schema, structuredClone(value));
  const errors = Value.Errors(schema, structuredClone(value)).map(slim);
  const convert = Value.Convert(schema, structuredClone(value));
  const checkConverted = Value.Check(schema, convert);
  const errorsConverted = Value.Errors(schema, structuredClone(convert)).map(slim);
  ops.push({ name, dsl, json, value, check, errors, convert, checkConverted, errorsConverted });
}

const S = (opts) => ({ t: "String", ...(opts ? { opts } : {}) });
const N = (opts) => ({ t: "Number", ...(opts ? { opts } : {}) });
const I = (opts) => ({ t: "Integer", ...(opts ? { opts } : {}) });
const B = () => ({ t: "Boolean" });
const Nu = () => ({ t: "Null" });
const obj = (props, opts) => ({ t: "Object", props, ...(opts ? { opts } : {}) });
const prop = (k, s, optional) => ({ k, s, ...(optional ? { optional: true } : {}) });

snap("literal-string", { t: "Literal", value: "a" });
snap("literal-number", { t: "Literal", value: 1 });
snap("literal-bool", { t: "Literal", value: true });
snap("object-index-order", obj([prop("b", S()), prop("a", N()), prop("2", B())]));
snap("object-optional", obj([prop("a", S(), true), prop("b", N())]));
snap("object-all-optional", obj([prop("a", S(), true)]));
snap("object-empty", obj([]));
snap("record-string", { t: "Record", key: S(), value: N() });
snap("record-integer", { t: "Record", key: I(), value: S() });
snap("record-number", { t: "Record", key: N(), value: S() });
snap("record-pattern", { t: "Record", key: S([{ k: "pattern", v: "^[a-z]+$" }]), value: B() });
snap("record-enum", { t: "Record", key: { t: "Enum", values: ["a", "b"] }, value: N() });
snap("record-literal", { t: "Record", key: { t: "Literal", value: "id" }, value: S() });
snap("record-literal-number", { t: "Record", key: { t: "Literal", value: 1 }, value: S() });
snap("record-boolean", { t: "Record", key: B(), value: S() });
snap("record-any", { t: "Record", key: { t: "Any" }, value: N() });
snap("record-unknown", { t: "Record", key: { t: "Unknown" }, value: S() });
snap("record-union-literals", {
  t: "Record",
  key: { t: "Union", items: [{ t: "Literal", value: "a" }, { t: "Literal", value: "b" }] },
  value: N(),
});
snap("record-union-string", {
  t: "Record",
  key: { t: "Union", items: [S(), { t: "Literal", value: "a" }] },
  value: N(),
});
snap("tuple", { t: "Tuple", items: [S(), N()] });
snap("tuple-empty", { t: "Tuple", items: [] });
snap("intersect", { t: "Intersect", items: [obj([prop("a", S())]), obj([prop("b", N())])] });
snap("enum", { t: "Enum", values: ["a", "b"] });
snap("enum-numbers", { t: "Enum", values: [1, 2] });
snap("union", { t: "Union", items: [S(), Nu()] });
snap("unknown", { t: "Unknown" });
snap("any", { t: "Any" });
snap("string-opts", S([
  { k: "description", v: "d" },
  { k: "minLength", v: 1 },
  { k: "pattern", v: "x" },
  { k: "format", v: "uri" },
]));
snap("object-opts", obj([prop("a", S())], [
  { k: "additionalProperties", v: false },
  { k: "description", v: "d" },
]));
snap("number-opts", N([
  { k: "minimum", v: 0 },
  { k: "maximum", v: 10 },
  { k: "exclusiveMinimum", v: 0 },
  { k: "exclusiveMaximum", v: 10 },
  { k: "multipleOf", v: 0.1 },
]));
snap("array-opts", {
  t: "Array",
  item: S(),
  opts: [
    { k: "minItems", v: 1 },
    { k: "maxItems", v: 3 },
    { k: "uniqueItems", v: true },
  ],
});
snap("ref", { t: "Ref", ref: "#/$defs/num" });
snap("unsafe-string-enum", {
  t: "Unsafe",
  raw: { type: "string", enum: ["add", "subtract"], description: "The operation", default: "add" },
});
snap("integer", I());
snap("boolean", B());
snap("null", Nu());
snap("object-defs", obj([prop("value", { t: "Ref", ref: "#/$defs/num" })], [
  { k: "$defs", map: { num: N() } },
]));

op("count-coerce", obj([prop("count", N())]), { count: "42", extra: true });
op("count-bad", obj([prop("count", N())]), { count: "nope" });
op("optional-nulls", obj([
  prop("path", S()),
  prop("offset", N(), true),
  prop("nullable", { t: "Union", items: [S(), Nu()] }, true),
  prop("metadata", obj([prop("enabled", B(), true)])),
]), { path: "file.txt", offset: null, nullable: null, metadata: { enabled: null } });
op("union-null-stays", obj([prop("value", { t: "Union", items: [N(), Nu()] })]), { value: null });
op("union-coerce", { t: "Union", items: [N(), Nu()] }, "42");
op("number-string", N(), "42");
op("number-true", N(), true);
op("number-null", N(), null);
op("number-empty", N(), "");
op("number-infinity", N(), "Infinity");
op("number-1n", N(), "1n");
op("number-false-word", N(), "FALSE");
op("integer-trunc", I(), "42.9");
op("integer-true", I(), true);
op("integer-float", I(), 1.9);
op("integer-null", I(), null);
op("boolean-TRUE", B(), "TRUE");
op("boolean-false", B(), "false");
op("boolean-1", B(), 1);
op("boolean-0", B(), 0);
op("boolean-null", B(), null);
op("boolean-str-1", B(), "1");
op("boolean-word-1", B(), "1");
op("string-null", S(), null);
op("string-true", S(), true);
op("string-num", S(), 1);
op("string-false", S(), false);
op("null-empty", Nu(), "");
op("null-0", Nu(), 0);
op("null-false", Nu(), false);
op("null-word", Nu(), "NULL");
op("null-undefined", Nu(), "undefined");
op("null-string-false", Nu(), "false");
op("array-wrap", { t: "Array", item: S() }, 1);
op("array-items", { t: "Array", item: S() }, ["1", 2]);
op("tuple-convert", { t: "Tuple", items: [S(), N()] }, [1, "2", true]);
op("record-convert", { t: "Record", key: S(), value: N() }, { a: "1", b: "x" });
op("record-newline-key", { t: "Record", key: S(), value: N() }, { "a\nb": "1" });
op("object-additional", obj([prop("a", N())], [{ k: "additionalProperties", s: S() }]), { a: "1", z: 2 });
op("object-additional-two", obj([prop("a", N()), prop("b", N())], [{ k: "additionalProperties", s: S() }]), { a: "1", b: "2", z: 3 });
op("object-dot-key", obj([prop("a.b", N())]), { axb: "1", "a.b": "2" });
op("intersect-convert", { t: "Intersect", items: [obj([prop("a", S())]), obj([prop("b", N())])] }, { a: 1, b: "2" });
op("enum-keep", { t: "Enum", values: ["a", "b"] }, "a");
op("enum-miss", { t: "Enum", values: ["a", "b"] }, 1);
op("literal-coerce", { t: "Literal", value: 1 }, "1");
op("literal-keep", { t: "Literal", value: "a" }, "a");
op("literal-miss", { t: "Literal", value: "a" }, "b");
op("raw-number-identity", { t: "Raw", schema: { type: "number" } }, "42");
op("raw-bool-true", { t: "Raw", schema: { type: "boolean" } }, "true");
op("raw-bool-1", { t: "Raw", schema: { type: "boolean" } }, 1);
op("raw-null-empty", { t: "Raw", schema: { type: "null" } }, "");
op("raw-integer", { t: "Raw", schema: { type: "integer" } }, "42.1");
op("raw-string-null", { t: "Raw", schema: { type: "string" } }, null);
op("unsafe-enum", { t: "Unsafe", raw: { type: "string", enum: ["add", "subtract"], description: "op" } }, "add");
op("unsafe-enum-miss", { t: "Unsafe", raw: { type: "string", enum: ["add", "subtract"] } }, "nope");

op("type-string", S(), 1);
op("type-number", N(), "x");
op("type-integer-1.5", I(), 1.5);
op("type-integer-1", I(), 1);
op("type-bool", B(), "true");
op("type-null", Nu(), 0);
op("type-null-ok", Nu(), null);
op("type-object-array", { t: "Raw", schema: { type: "object" } }, []);
op("type-object-null", { t: "Raw", schema: { type: "object" } }, null);
op("type-array-object", { t: "Raw", schema: { type: "array" } }, {});
op("type-union-names", { t: "Raw", schema: { type: ["string", "number"] } }, true);
op("type-union-ok", { t: "Raw", schema: { type: ["string", "number"] } }, "a");
op("type-empty", { t: "Raw", schema: { type: [] } }, 1);
op("minlength-number", { t: "Raw", schema: { minLength: 1 } }, 5);
op("empty-schema", { t: "Raw", schema: {} }, { a: 1 });
op("bool-schema", { t: "Bool", value: false }, 1);
op("bool-schema-true", { t: "Bool", value: true }, 1);
op("default-ignored", { t: "Raw", schema: { type: "string", default: "x" } }, 1);

op("required-missing", { t: "Raw", schema: { type: "object", properties: { a: { type: "string" } }, required: ["a", "b"], additionalProperties: false } }, { c: 1 });
op("additional-false", obj([prop("a", S())], [{ k: "additionalProperties", v: false }]), { a: "ok", c: 1 });
op("additional-true", obj([prop("a", S())], [{ k: "additionalProperties", v: true }]), { a: "ok", c: 1 });
op("additional-schema", obj([prop("a", S())], [{ k: "additionalProperties", s: N() }]), { a: "ok", c: "1" });
op("pattern-props", { t: "Raw", schema: { type: "object", patternProperties: { "^a": { type: "number" } }, additionalProperties: false } }, { a: 1, a2: "x", b: "ok" });
op("properties-missing-ok", obj([prop("a", S(), true)]), {});
op("properties-present-bad", obj([prop("a", S(), true)]), { a: 1 });

const many = {};
const manyProps = [];
for (const k of ["a", "b", "c", "d", "e", "f", "g", "h", "i"]) {
  many[k] = 1;
  manyProps.push(prop(k, S()));
}
op("max-errors", obj(manyProps), many);

op("items-schema", { t: "Array", item: S() }, ["a", 1]);
op("items-tuple", { t: "Raw", schema: { items: [{ type: "string" }, { type: "number" }] } }, ["a", "b"]);
op("tuple-extra", { t: "Tuple", items: [S()] }, ["a", "b"]);
op("tuple-short", { t: "Tuple", items: [S(), N()] }, ["a"]);
op("prefix-empty", { t: "Raw", schema: { prefixItems: [{ type: "string" }], items: { type: "number" } } }, []);
op("prefix-ok", { t: "Raw", schema: { prefixItems: [{ type: "string" }], items: { type: "number" } } }, ["a", 1, 2]);
op("prefix-bad", { t: "Raw", schema: { prefixItems: [{ type: "string" }], items: { type: "number" } } }, [1, 2]);
op("prefix-item-bad", { t: "Raw", schema: { prefixItems: [{ type: "string" }], items: { type: "number" } } }, ["a", "b"]);
op("min-items", { t: "Raw", schema: { type: "array", minItems: 2 } }, [1]);
op("max-items", { t: "Raw", schema: { type: "array", maxItems: 1 } }, [1, 2]);
op("unique-dup", { t: "Raw", schema: { uniqueItems: true } }, [1, 2, 1]);
op("unique-objects", { t: "Raw", schema: { uniqueItems: true } }, [{ a: 1, b: 2 }, { b: 2, a: 1 }]);
op("unique-ok", { t: "Raw", schema: { uniqueItems: true } }, [1, 2]);
op("unique-false", { t: "Raw", schema: { uniqueItems: false } }, [1, 1]);

op("enum-bad", { t: "Enum", values: ["a", "b"] }, "c");
op("enum-num-ok", { t: "Raw", schema: { enum: ["a", 1] } }, 1);
op("const-scalar", { t: "Literal", value: "a" }, "b");
op("const-object", { t: "Raw", schema: { const: { a: 1, b: 2 } } }, { b: 2, a: 1 });
op("const-object-bad", { t: "Raw", schema: { const: { a: 1, b: 2 } } }, { a: 1 });

op("anyof-fail", { t: "Union", items: [S(), B()] }, 1);
op("anyof-pass", { t: "Union", items: [S(), B()] }, "a");
op("oneof-two", { t: "Raw", schema: { oneOf: [{ type: "number" }, { type: "number" }] } }, 1);
op("oneof-zero", { t: "Raw", schema: { oneOf: [{ type: "string" }, { type: "boolean" }] } }, 1);
op("oneof-one", { t: "Raw", schema: { oneOf: [{ type: "string" }, { type: "number" }] } }, "a");
op("allof-fail", { t: "Raw", schema: { allOf: [{ type: "number", minimum: 5 }, { type: "number", maximum: 3 }] } }, 4);
op("allof-pass", { t: "Intersect", items: [obj([prop("a", S())]), obj([prop("b", N())])] }, { a: "x", b: 1 });
op("not-fail", { t: "Raw", schema: { not: { type: "string" } } }, "a");
op("not-pass", { t: "Raw", schema: { not: { type: "string" } } }, 1);

op("minlen-ab", S([{ k: "minLength", v: 2 }]), "ab");
op("minlen-thumb", S([{ k: "minLength", v: 2 }]), "👍");
op("minlen-thumb-1", S([{ k: "minLength", v: 1 }]), "👍");
op("minlen-thumbs", S([{ k: "minLength", v: 2 }]), "👍👍");
op("minlen-combining", S([{ k: "minLength", v: 2 }]), "a\u0301");
op("maxlen-thumb", S([{ k: "maxLength", v: 1 }]), "👍");
op("maxlen-thumbs", S([{ k: "maxLength", v: 1 }]), "👍👍");
op("minlen-neg", S([{ k: "minLength", v: -1 }]), "");
op("pattern-dot", S([{ k: "pattern", v: "^.$" }]), "👍");
op("pattern-combining", S([{ k: "pattern", v: "^.$" }]), "a\u0301");
op("pattern-fail", S([{ k: "pattern", v: "^[a-z]+$" }]), "A1");
op("pattern-ok", S([{ k: "pattern", v: "^[a-z]+$" }]), "ab");

function fmt(name, value, bad) {
  op("fmt-" + name + (bad ? "-bad" : ""), S([{ k: "format", v: name }]), value);
}
fmt("uri", "https://example.com");
fmt("uri", "not a uri", true);
fmt("regex", "a+");
fmt("regex", "(", true);
fmt("email", "a@b.co");
fmt("email", "not-email", true);
fmt("date", "2020-02-29");
fmt("date", "2019-02-29", true);
fmt("date", "2020-02-30", true);
fmt("date-time", "2020-02-29T23:59:60Z");
fmt("date-time", "2020-02-29T23:59:60", true);
fmt("time", "23:59:59Z");
fmt("time", "23:59:59", true);
fmt("uuid", "550e8400-e29b-41d4-a716-446655440000");
fmt("uuid", "nope", true);
fmt("ipv4", "127.0.0.1");
fmt("ipv4", "127.0.0", true);
fmt("ipv6", "::1");
fmt("ipv6", "gggg", true);
fmt("hostname", "example.com");
fmt("hostname", "example.com.", true);
fmt("hostname", "-bad", true);
fmt("duration", "P1DT2H");
fmt("duration", "nope", true);
fmt("json-pointer", "/a/b");
fmt("json-pointer", "a", true);
fmt("uri-reference", "https://example.com/a");
fmt("uri-template", "https://example.com/{id}");
fmt("email", "A@B.CO");
fmt("idn-email", "user@example.com");
fmt("nope", "whatever");

op("exclusive-min", N([{ k: "exclusiveMinimum", v: 0 }]), 0);
op("exclusive-min-ok", N([{ k: "exclusiveMinimum", v: 0 }]), 0.1);
op("exclusive-max", N([{ k: "exclusiveMaximum", v: 1 }]), 1);
op("minimum", N([{ k: "minimum", v: 0 }]), 0);
op("minimum-bad", N([{ k: "minimum", v: 0 }]), -1);
op("maximum", N([{ k: "maximum", v: 1 }]), 2);
op("multiple-tenth", N([{ k: "multipleOf", v: 0.1 }]), 1);
op("multiple-point3", N([{ k: "multipleOf", v: 0.1 }]), 0.3);
op("multiple-zero", N([{ k: "multipleOf", v: 0 }]), 1);
op("multiple-two", N([{ k: "multipleOf", v: 2 }]), 5);
op("multiple-two-ok", N([{ k: "multipleOf", v: 2 }]), 4);

op("ref-type", {
  t: "Raw",
  schema: { type: "object", properties: { value: { $ref: "#/$defs/num" } }, $defs: { num: { type: "number" } } },
}, { value: "x" });
op("ref-ok", {
  t: "Raw",
  schema: { type: "object", properties: { value: { $ref: "#/$defs/num" } }, $defs: { num: { type: "number" } } },
}, { value: 1 });
op("definitions-required", {
  t: "Raw",
  schema: {
    type: "object",
    properties: { user: { $ref: "#/definitions/user" } },
    definitions: { user: { type: "object", required: ["name"], properties: { name: { type: "string" } } } },
  },
}, { user: {} });
op("ref-unresolved", { t: "Raw", schema: { $ref: "#/missing" } }, 1);
op("ref-root", { t: "Raw", schema: { type: "object", properties: { a: { $ref: "#" } } } }, { a: { a: 1 } });

op("unknown-convert", { t: "Unknown" }, "42");
op("any-convert", { t: "Any" }, 1);
op("ref-convert", { t: "Ref", ref: "#/x" }, "42");

const out = JSON.stringify({ builds, ops });
writeFileSync(new URL("./vectors.json", import.meta.url), out);
console.log("builds", builds.length, "ops", ops.length, "bytes", out.length);
