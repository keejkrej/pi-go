// Generates internal/partialjson/testdata/vectors.json from partial-json 0.1.7:
//
//	NODE_MODULES=/path/to/pi/node_modules node gen.cjs > vectors.json
const path = require("path");
const req = (m) => require(process.env.NODE_MODULES ? path.join(process.env.NODE_MODULES, m) : m);
const { parse, Allow, PartialJSON, MalformedJSON } = req("partial-json");
const pkg = req("partial-json/package.json");

function repr(v) {
	if (v === null) return "null";
	if (v === undefined) return "undefined";
	if (typeof v === "boolean") return String(v);
	if (typeof v === "number") return Object.is(v, -0) ? "-0" : String(v);
	if (typeof v === "string") return JSON.stringify(v);
	if (Array.isArray(v)) return "[" + v.map(repr).join(",") + "]";

	return "{" + Object.keys(v).map((k) => JSON.stringify(k) + ":" + repr(v[k])).join(",") + "}";
}

const masks = {
	ALL: Allow.ALL,
	notSTR: ~Allow.STR,
	notOBJ: ~Allow.OBJ,
	notARR: ~Allow.ARR,
	notNUM: ~Allow.NUM,
	notSPECIAL: ~Allow.SPECIAL,
	STR_OBJ: Allow.STR | Allow.OBJ,
	OBJ: Allow.OBJ,
	ARR: Allow.ARR,
	COLLECTION: Allow.COLLECTION,
	ATOM: Allow.ATOM,
	NONE: 0,
};

const docs = [
	'{"key":"value"}',
	'[ {"key1": "value1", "key2": [ "value2", 12.5e3, -0.25, true, false, null ] } ]',
	'{"path": "src/a.ts", "edits": [{"oldText": "a\\nb", "newText": "c\\td\\u00e9\\ud83d\\ude00"}], "n": -12.75E-2}',
	'{"a": {"b": {"c": [1, [2, [3, {"d": "e"}]]]}}, "f": "g"}',
	'  {"spaced" :  [ 1 , 2 ,  3 ] , "x" : "y" }  ',
	'"just a string with \\"quotes\\" and \\\\ backslashes"',
	'[Infinity, -Infinity, NaN, 1e5, 0, -0]',
	'{"emoji": "😀 snowman ☃ é", "cjk": "汉字"}',
	'{"dup": 1, "dup": 2, "10": "ten", "2": "two", "z": 0}',
	'{"esc": "\\u0041\\u00e9\\u4e2d", "nl": "line1\\nline2"}',
	'[1.5, 2.25, -3, 4e-2, 5E+3]',
	'{"command":"ls -la","timeout":30}',
	'{"__proto__": {"x": 1}, "y": 2}',
];

const misc = [
	"", "   ", "\t\n", " ", "﻿{}﻿",
	"-", "-I", "-In", "-Inf", "-Infinity", "Inf", "Infinity", "N", "Na", "NaN",
	"n", "nu", "nul", "null", "t", "tr", "tru", "true", "f", "fa", "fal", "fals", "false",
	"123", "123.", "12e", "12e+", "1.5e-", "-12", "-12.", "0x10", "01", "1 2",
	"wrong", "abc", "[abc]", "[a", "{a:1}", "{a", '{"a"', '{"a":', '{"a": ', '{"a": 1', '{"a": 1,', '{"a": 1, "b"',
	'{"a" 1}', '{"a"é1}', '{"a"😀1}', '{"a":1}}', "[1,]", "[,1]", "[1,,2]", "[1 2]", "[[[", "[{", "{[", "]", "}",
	'"', '"abc', '"abc\\', '"abc\\\\', '"abc\\u12', '"abc\\u123', '"abc\\x"', '"\\x\\n', '"a\\"b', '"a\\\\"b',
	'"\\ud83d', '"\\ud83d\\ude00', '"😀', '"tab\there"', '"new\nline"', '["new\nline"', '{"k": "new\nline"',
	'[1, "abc\\', '{"a": "abc\\u12', '[nu', '[tr', '[-', '[-1', '[-1.', '[1e', '[1.5e', '[-Inf', '[Na', '[Infin',
	'{"a": -}', '{"a": tru}', '{"a": nul, "b": 1}', '[t]', '[true false]', '[nullx]', '[--1]', '[1-]',
	'{"a\\": 1}', '{"a":"b"', '{"a":"b\\', '{"":""}', '{"a":{}}', '{"a":[]', '[{}', '[[]',
	'{"x": [1, {"y": "z', '{"x": "\\u00', '{ "key" : "va', '[1, 2, 3', '[1, 2, 3.', '[1, 2, 3.4',
	'  [1]  ', '[1]x', '{}x', '"a"x', 'nullx', 'truex', '1x', '[1]]', '[[1]', '{"a": [1}',
	' [1]', '[1,   2]', '{"a":1 , }', '{,"a":1}', '{"a"::1}', '{"a":1"b":2}',
	'"\\', '"\\\\', '"\\u', '["a\\"', '["\\\\"', '{"\\\\":1', '"\ud800"', '"x\udc00y',
	'[1e999]', '[-1e999]', '1e999', '[0.1e1]', '[1E2]', '[.5]', '[+1]', '[1.]', '[1..2]',
	'{"a": Infinity, "b": -Infinity, "c": NaN}', '{"a": Inf', '{"a": -Infinity', '{"a": NaN',
];

const inputs = new Set(misc);
const prefixInputs = new Set();
for (const doc of docs) {
	inputs.add(doc);
	const units = [...doc]; // code points; also add code-unit prefixes below
	for (let i = 1; i < doc.length; i++) prefixInputs.add(doc.slice(0, i));
	for (let i = 1; i < units.length; i++) prefixInputs.add(units.slice(0, i).join(""));
}
for (const input of inputs) prefixInputs.delete(input);
const prefixMasks = ["ALL", "notSTR", "notOBJ", "notNUM", "OBJ", "NONE"];

const cases = [];
const work = [];
for (const input of inputs) for (const maskName of Object.keys(masks)) work.push([input, maskName]);
for (const input of prefixInputs) for (const maskName of prefixMasks) work.push([input, maskName]);
{
	for (const [input, maskName] of work) {
		const mask = masks[maskName];
		let out;
		try {
			const v = parse(input, mask);
			out = { ok: true, value: repr(v) };
		} catch (e) {
			const kind = e instanceof PartialJSON ? "PartialJSON" : e instanceof MalformedJSON ? "MalformedJSON" : e.name;
			out = { ok: false, kind, message: e.message };
		}
		cases.push({ input, mask, ...out });
	}
}
// default-argument behaviour (allow omitted == ALL)
const defaults = ['{"a": [1, 2', '"abc', "-Inf", "tr"].map((input) => ({ input, value: repr(parse(input)) }));

process.stdout.write('{"generator": ' + JSON.stringify("partial-json@" + pkg.version + " via node " + process.version + "; testdata/gen.cjs") + ',\n"defaults": [\n' + defaults.map((d) => JSON.stringify(d)).join(",\n") + '\n],\n"cases": [\n' + cases.map((c) => JSON.stringify(c)).join(",\n") + "\n]}\n");
