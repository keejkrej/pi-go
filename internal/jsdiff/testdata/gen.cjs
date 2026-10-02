// Generates internal/jsdiff/testdata/vectors.json from diff@8.0.4:
//
//	NODE_MODULES=/path/to/pi/node_modules node gen.cjs vectors.json
//
// Optional arguments [scale] [seed] produce a larger differential run with another seed
// (for local checks; not committed).
const fs = require("fs");
const path = require("path");
const req = (m) => require(process.env.NODE_MODULES ? path.join(process.env.NODE_MODULES, m) : m);
const Diff = req("diff");
const pkg = req("diff/package.json");

const cases = [];
const segments = {};

// Deterministic PRNG (mulberry32).
const scale = Number(process.argv[3] || 1);
let seed = Number(process.argv[4] || 0x5eed1234);
function rand() {
	seed |= 0;
	seed = (seed + 0x6d2b79f5) | 0;
	let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
	t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
	return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
}
const ri = (n) => Math.floor(rand() * n);
const pick = (arr) => arr[ri(arr.length)];

const comparators = {
	trimEq: (l, r) => l.trim() === r.trim(),
};
const replacers = {
	dropSecret: (k, v) => (k === "secret" ? undefined : v),
	upperStrings: (k, v) => (typeof v === "string" ? v.toUpperCase() : v),
};
const compareLines = {
	ignoreWs: (n, line, op, content) => line !== undefined && line.replace(/\s+/g, "") === content.replace(/\s+/g, ""),
};
const headerOptions = {
	INCLUDE_HEADERS: Diff.INCLUDE_HEADERS,
	FILE_HEADERS_ONLY: Diff.FILE_HEADERS_ONLY,
	OMIT_HEADERS: Diff.OMIT_HEADERS,
};

// Real Intl.Segmenter whose every call is recorded so the Go test can replay it.
const realSegmenter = new Intl.Segmenter("en", { granularity: "word" });
const recordingSegmenter = {
	resolvedOptions: () => realSegmenter.resolvedOptions(),
	segment(s) {
		const segs = Array.from(realSegmenter.segment(s), (x) => x.segment);
		segments[s] = segs;
		return segs.map((segment) => ({ segment }));
	},
};

// opts in the vectors are JSON; named functions are resolved here.
function realOpts(opts) {
	if (!opts) return undefined;
	const o = { ...opts };
	if (o.comparator) o.comparator = comparators[o.comparator];
	if (o.stringifyReplacer) o.stringifyReplacer = replacers[o.stringifyReplacer];
	if (o.compareLine) o.compareLine = compareLines[o.compareLine];
	if (o.headerOptions) o.headerOptions = headerOptions[o.headerOptions];
	if (o.intlSegmenter) o.intlSegmenter = recordingSegmenter;
	return o;
}

function run(c, f) {
	try {
		const r = f();
		c.result = r === undefined ? null : r;
	} catch (e) {
		c.error = e.message;
	}
	cases.push(c);
}

const diffFns = {
	diffChars: Diff.diffChars,
	diffWords: Diff.diffWords,
	diffWordsWithSpace: Diff.diffWordsWithSpace,
	diffLines: Diff.diffLines,
	diffTrimmedLines: Diff.diffTrimmedLines,
	diffSentences: Diff.diffSentences,
	diffCss: Diff.diffCss,
};
function diffCase(fn, a, b, opts) {
	run({ fn, a, b, opts: opts ?? null }, () => diffFns[fn](a, b, realOpts(opts)));
}

// ---- Hand-written string pairs ----
const linePairs = [
	["", ""],
	["", "a\n"],
	["a\n", ""],
	["a\nb\nc\n", "a\nb\nc\n"],
	["a\nb\nc\n", "a\nB\nc\n"],
	["a\nb\nc", "a\nb\nc\n"],
	["a\nb\nc\n", "a\nb\nc"],
	["a\r\nb\r\nc\r\n", "a\nb\nc\n"],
	["a\r\nb\r\n", "a\r\nb\r\nd\r\n"],
	["  foo\nbar  \n\tbaz\n", "foo\nbar\nbaz\n"],
	["foo\nbar\n", "FOO\nBar\n"],
	["line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n", "line1\nline2\nline3 changed\nline4\nline5\nline6\nline7\nline8 changed\nline9\nline10\n"],
	["\n\n\n", "\n\n"],
	["a\n\nb\n", "a\nb\n\n"],
	["x\ry\rz", "x\ry\nz"],
	["héllo\nwörld\n😀\n", "héllo\nworld\n😀😀\n"],
	["one\ntwo\nthree", "zero\none\ntwo\nthree\nfour"],
	["a\nb\nc\nd\ne\nf\n", "f\ne\nd\nc\nb\na\n"],
	["same\n", "same\n\n"],
	[" x\n", "x\n"],
	["x y\n", "x\ny\n"],
];
const lineOptSets = [
	null,
	{ ignoreWhitespace: true },
	{ newlineIsToken: true },
	{ stripTrailingCr: true },
	{ ignoreNewlineAtEof: true },
	{ ignoreCase: true },
	{ oneChangePerToken: true },
	{ maxEditLength: 1 },
	{ maxEditLength: 3 },
	{ ignoreWhitespace: true, newlineIsToken: true },
	{ ignoreWhitespace: true, ignoreNewlineAtEof: true },
	{ newlineIsToken: true, ignoreNewlineAtEof: true },
	{ comparator: "trimEq" },
	{ timeout: 1e9 },
];
for (const [a, b] of linePairs) {
	for (const opts of lineOptSets) diffCase("diffLines", a, b, opts);
	diffCase("diffTrimmedLines", a, b, null);
	diffCase("diffTrimmedLines", a, b, { ignoreWhitespace: false });
}

const wordPairs = [
	["", ""],
	["foo", ""],
	["", "foo"],
	["foo bar", "foo baz"],
	["New Value", "New  ValueMoreData"],
	["foo bar baz", "foo baz"],
	["  leading and trailing  ", "leading and trailing"],
	["foo\nbar baz", "foo\nbaz qux"],
	["The quick brown fox.", "The quick, brown fox!"],
	["héllo wörld", "hello world"],
	["café naïve résumé", "cafe naive résumé"],
	["a × b ÷ c", "a * b / c"],
	["😀 smile", "😃 smile"],
	["foo\tbar", "foo  bar"],
	["foo bar", "foo bar"],
	["one two three four", "one 2 three 4"],
	["  ", "   "],
	[" \n ", "\n"],
	["const x = 1;", "const y = 2;"],
	["function foo(a, b) {", "function foo(a, b, c) {"],
	["ΣΑΣ σας", "σας ΣΑΣ"],
	["İstanbul", "istanbul"],
	["Hello World", "hello world"],
	["a b c d e f g", "a c e g x"],
	["word line", "word line"],
	["x  y  z", "x y z"],
	["foo bar", "foo  bar  "],
	["   foo", "foo   "],
	["abc def", "abcdef"],
	["ǅ ǈ", "ǆ ǉ"],
	["ˇ˘x", "ˈx"],
	["soft­hyphen", "soft hyphen"],
	["ẞtraße", "sstrasse"],
	["日本語 テキスト", "日本語のテキスト"],
	["The cat sat. The dog ran!  Then? Yes", "The cat sat. A dog ran! Then? No"],
	["a.b.c", "a. b. c"],
	["body { color: red; margin: 0 }", "body { color: blue; padding: 0 }"],
	["a{b:c}", "a{b:d;}"],
	["\ud83d", "😀"],
	["x\udc00y", "xy"],
];
const wordOptSets = [null, { ignoreCase: true }, { oneChangePerToken: true }, { maxEditLength: 2 }];
for (const [a, b] of wordPairs) {
	for (const fn of ["diffWords", "diffWordsWithSpace", "diffChars", "diffSentences", "diffCss"]) {
		for (const opts of wordOptSets) diffCase(fn, a, b, opts);
	}
	diffCase("diffWords", a, b, { ignoreWhitespace: false });
	diffCase("diffWords", a, b, { ignoreWhitespace: true });
	diffCase("diffWords", a, b, { intlSegmenter: true });
	diffCase("diffWords", a, b, { intlSegmenter: true, ignoreCase: true });
	diffCase("diffChars", a, b, { comparator: "trimEq" });
}

// ---- Random fuzz ----
const lineVocab = ["a", "b", "c", "foo", "bar", "  foo", "foo  ", "", "Foo", "x\r", "}", "\t{", "héllo", "😀"];
function randLines(n) {
	const out = [];
	for (let i = 0; i < n; i++) out.push(pick(lineVocab));
	return out;
}
function mutateLines(lines) {
	const out = lines.slice();
	const edits = ri(4) + 1;
	for (let e = 0; e < edits; e++) {
		const op = ri(3);
		const pos = ri(out.length + 1);
		if (op === 0) out.splice(pos, 0, pick(lineVocab));
		else if (op === 1 && out.length) out.splice(Math.min(pos, out.length - 1), 1);
		else if (out.length) out[Math.min(pos, out.length - 1)] = pick(lineVocab);
	}
	return out;
}
function joinLines(lines, eol, trailing) {
	return lines.join(eol) + (trailing && lines.length ? eol : "");
}
for (let k = 0; k < 120 * scale; k++) {
	const la = randLines(ri(14));
	const lb = mutateLines(la);
	const eol = rand() < 0.15 ? "\r\n" : "\n";
	const a = joinLines(la, eol, rand() < 0.8);
	const b = joinLines(lb, eol, rand() < 0.8);
	diffCase("diffLines", a, b, null);
	const opts = pick(lineOptSets);
	if (opts) diffCase("diffLines", a, b, opts);
	const context = ri(6);
	const ho = pick(["FILE_HEADERS_ONLY", "INCLUDE_HEADERS", "OMIT_HEADERS"]);
	const popts = { context, headerOptions: ho };
	if (rand() < 0.2) popts.ignoreWhitespace = true;
	if (rand() < 0.1) popts.stripTrailingCr = true;
	run({ fn: "createTwoFilesPatch", oldName: "f.txt", newName: "f.txt", a, b, opts: popts }, () =>
		Diff.createTwoFilesPatch("f.txt", "f.txt", a, b, undefined, undefined, realOpts(popts)),
	);
	const patch = Diff.createTwoFilesPatch("f.txt", "f.txt", a, b, undefined, undefined, { context });
	run({ fn: "applyPatch", a, patch, opts: null }, () => Diff.applyPatch(a, patch));
	// Apply onto a perturbed source, with and without fuzz.
	const lc = mutateLines(la);
	const c = joinLines(lc, eol, rand() < 0.8);
	for (const fuzzFactor of [0, 1, 2]) {
		run({ fn: "applyPatch", a: c, patch, opts: { fuzzFactor } }, () => Diff.applyPatch(c, patch, { fuzzFactor }));
	}
	run({ fn: "reversePatch", patch }, () => Diff.formatPatch(Diff.reversePatch(parseFilled(patch))));
	run({ fn: "applyReversePatch", a: b, patch }, () => Diff.applyPatch(b, Diff.reversePatch(Diff.parsePatch(patch))));
}
const wordVocab = ["foo", "bar", "Baz", "x", "  ", " ", "\n", "\t", ".", ",", "!", "(", ")", "héllo", "naïve", "😀", "日本", "ΣΑΣ", "σας", "-", " ", "1", "22"];
function randWords(n) {
	let s = "";
	for (let i = 0; i < n; i++) s += pick(wordVocab) + (rand() < 0.6 ? " " : "");
	return s;
}
function mutateWords(s) {
	const parts = s.split(/(\s+)/);
	const edits = ri(3) + 1;
	for (let e = 0; e < edits; e++) {
		const pos = ri(parts.length + 1);
		const op = ri(3);
		if (op === 0) parts.splice(pos, 0, pick(wordVocab));
		else if (op === 1 && parts.length) parts.splice(Math.min(pos, parts.length - 1), 1);
		else if (parts.length) parts[Math.min(pos, parts.length - 1)] = pick(wordVocab);
	}
	return parts.join("");
}
for (let k = 0; k < 150 * scale; k++) {
	const a = randWords(ri(12));
	const b = mutateWords(a);
	diffCase("diffWords", a, b, null);
	diffCase("diffWordsWithSpace", a, b, null);
	diffCase("diffChars", a, b, null);
	if (k % 3 === 0) diffCase("diffWords", a, b, { ignoreCase: true });
	if (k % 3 === 1) diffCase("diffWords", a, b, { intlSegmenter: true });
	if (k % 5 === 0) diffCase("diffSentences", a, b, null);
}

// ---- Character-level fuzz ----
{
	const chars = ["a", "b", "A", "B", " ", "  ", "\n", "\r\n", "\r", "\t", "\u00a0", "\u2028", "\u3000", "é", "😀", "\ud83d", "\ude00", ".", "!", "?", "{", "}", ":", ";", ",", "Σ", "ς", "σ", "İ", "ǅ", "\u0301", "x", "foo", "bar"];
	const randStr = (n) => {
		let s = "";
		for (let i = 0; i < n; i++) s += pick(chars);
		return s;
	};
	const mut = (s) => {
		const arr = Array.from(s);
		const edits = ri(4) + 1;
		for (let e = 0; e < edits; e++) {
			const pos = ri(arr.length + 1);
			const op = ri(3);
			if (op === 0) arr.splice(pos, 0, pick(chars));
			else if (op === 1 && arr.length) arr.splice(Math.min(pos, arr.length - 1), 1);
			else if (arr.length) arr[Math.min(pos, arr.length - 1)] = pick(chars);
		}
		return arr.join("");
	};
	const optSets = [null, { ignoreCase: true }, { oneChangePerToken: true }, { ignoreWhitespace: true }, { ignoreWhitespace: false }, { newlineIsToken: true }, { stripTrailingCr: true }, { ignoreNewlineAtEof: true }, { maxEditLength: 3 }, { comparator: "trimEq" }, { intlSegmenter: true }];
	for (let k = 0; k < (scale > 1 ? 400 * scale : 100); k++) {
		const a = randStr(ri(16));
		const b = rand() < 0.2 ? randStr(ri(16)) : mut(a);
		const fn = pick(["diffChars", "diffWords", "diffWordsWithSpace", "diffLines", "diffTrimmedLines", "diffSentences", "diffCss"]);
		let opts = pick(optSets);
		if (opts && opts.intlSegmenter && fn !== "diffWords") opts = null;
		diffCase(fn, a, b, opts);
		if (k % 4 === 0) {
			const context = ri(5);
			let popts = { context, headerOptions: pick(["FILE_HEADERS_ONLY", "INCLUDE_HEADERS", "OMIT_HEADERS"]) };
			if (rand() < 0.3) popts.ignoreWhitespace = true;
			if (rand() < 0.3) popts.stripTrailingCr = true;
			if (rand() < 0.3) popts.ignoreCase = true;
			run({ fn: "createTwoFilesPatch", oldName: "p", newName: "q", a, b, opts: popts }, () =>
				Diff.createTwoFilesPatch("p", "q", a, b, undefined, undefined, realOpts(popts)),
			);
			const patch = Diff.createTwoFilesPatch("p", "p", a, b, undefined, undefined, { context });
			const c = rand() < 0.5 ? a : mut(a);
			const aopts = { fuzzFactor: ri(3) };
			if (rand() < 0.2) aopts.autoConvertLineEndings = false;
			run({ fn: "applyPatch", a: c, patch, opts: aopts }, () => Diff.applyPatch(c, patch, aopts));
		}
	}
}

// ---- Arrays ----
const arrayPairs = [
	[[], []],
	[["a", "b", "c"], ["a", "c", "d"]],
	[[1, 2, 3, 4], [1, 3, 4, 5, 2]],
	[["a", "", "b"], ["", "a", "b"]],
	[["x", 1, "1", 2], [1, "x", 2, "2"]],
];
for (const [a, b] of arrayPairs) {
	run({ fn: "diffArrays", ja: a, jb: b, opts: null }, () => Diff.diffArrays(a, b));
	run({ fn: "diffArrays", ja: a, jb: b, opts: { oneChangePerToken: true } }, () => Diff.diffArrays(a, b, { oneChangePerToken: true }));
	run({ fn: "diffArrays", ja: a, jb: b, opts: { maxEditLength: 1 } }, () => Diff.diffArrays(a, b, { maxEditLength: 1 }));
}

// ---- JSON ----
const jsonPairs = [
	[{ a: 1, b: 2 }, { b: 2, a: 1 }],
	[{ a: 1, b: [1, 2, 3] }, { a: 1, b: [1, 2, 4], c: null }],
	[{ name: "x", secret: "s1", nested: { secret: "s2", keep: true } }, { name: "y", secret: "s3", nested: { keep: false } }],
	[[1, { z: 1, a: 2 }], [1, { a: 2, z: 1 }, 3]],
	["{\n  \"a\": 1\n}", "{\n  \"a\": 1,\n  \"b\": 2\n}"],
	[{ "10": 1, "2": 2, b: 3, a: 4, "-1": 5 }, { "2": 2, a: 4 }],
	[{ s: "héllo 😀", n: -0, f: 1.5e300 }, { s: "hello", n: 0, f: 1e-7 }],
	[null, { a: null }],
	[true, false],
	[123, "123"],
	[{ "é": 1, "e": 2, "😀": 3, "｡": 4 }, {}],
];
for (const [a, b] of jsonPairs) {
	run({ fn: "diffJson", ja: a, jb: b, opts: null }, () => Diff.diffJson(a, b));
	run({ fn: "diffJson", ja: a, jb: b, opts: { stringifyReplacer: "dropSecret" } }, () => Diff.diffJson(a, b, realOpts({ stringifyReplacer: "dropSecret" })));
	run({ fn: "diffJson", ja: a, jb: b, opts: { stringifyReplacer: "upperStrings" } }, () => Diff.diffJson(a, b, realOpts({ stringifyReplacer: "upperStrings" })));
	run({ fn: "canonicalize", ja: a }, () => Diff.canonicalize(a));
}

// ---- Patches ----
const patchPairs = [
	["", "a\n"],
	["a\n", ""],
	["a\nb\nc\n", "a\nb\nc"],
	["a\nb\nc", "a\nb\nc\n"],
	["a\nb\nc", "a\nB\nc"],
	["l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nl16\nl17\nl18\nl19\nl20\n", "l1\nl2 x\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nl16\nl17\nl18\nl19 y\nl20\n"],
	["same\n", "same\n"],
	["a\r\nb\r\n", "a\r\nc\r\n"],
	["tab\there\n", "tab\tthere\n"],
];
for (const [a, b] of patchPairs) {
	for (const context of [undefined, 0, 1, 3, 4, 10]) {
		for (const ho of [undefined, "INCLUDE_HEADERS", "FILE_HEADERS_ONLY", "OMIT_HEADERS"]) {
			const opts = {};
			if (context !== undefined) opts.context = context;
			if (ho !== undefined) opts.headerOptions = ho;
			run({ fn: "createTwoFilesPatch", oldName: "old/file.txt", newName: "new/file.txt", a, b, opts }, () =>
				Diff.createTwoFilesPatch("old/file.txt", "new/file.txt", a, b, undefined, undefined, realOpts(opts)),
			);
		}
	}
	run({ fn: "createTwoFilesPatch", oldName: "a.txt", newName: "a.txt", a, b, oldHeader: "old header", newHeader: "", opts: null }, () =>
		Diff.createTwoFilesPatch("a.txt", "a.txt", a, b, "old header", "", undefined),
	);
	run({ fn: "createPatch", oldName: "same.txt", a, b, oldHeader: "h1", newHeader: "h2", opts: { context: 2 } }, () =>
		Diff.createPatch("same.txt", a, b, "h1", "h2", { context: 2 }),
	);
	run({ fn: "structuredPatch", oldName: "o", newName: "n", a, b, opts: null }, () => Diff.structuredPatch("o", "n", a, b));
	run({ fn: "structuredPatch", oldName: "o", newName: "n", a, b, oldHeader: "x", newHeader: "y", opts: { context: 1 } }, () =>
		Diff.structuredPatch("o", "n", a, b, "x", "y", { context: 1 }),
	);
	run({ fn: "structuredPatch", oldName: "o", newName: "n", a, b, opts: { maxEditLength: 0 } }, () =>
		Diff.structuredPatch("o", "n", a, b, undefined, undefined, { maxEditLength: 0 }),
	);
}
run({ fn: "structuredPatch", oldName: "o", newName: "n", a: "a\n", b: "b\n", opts: { newlineIsToken: true } }, () =>
	Diff.structuredPatch("o", "n", "a\n", "b\n", undefined, undefined, { newlineIsToken: true }),
);

const patchTexts = [
	"Index: test\n===================================================================\n--- test\theader1\n+++ test\theader2\n@@ -1,3 +1,4 @@\n line2\n line3\n+line4\n line5\n",
	"@@ -1 +1 @@\n-a\n+b\n",
	"@@ -1,0 +1 @@\n+b\n",
	"@@ -0,0 +1,2 @@\n+x\n+y\n",
	"--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n-foo\n+bar\n baz\n\\ No newline at end of file\n",
	"diff -r 9117c6561b0b -r 273ce12ad8f1 .hgignore\n--- a/.hgignore\n+++ b/.hgignore\n@@ -1 +1,2 @@\n x\n+y\n",
	"Index: a\n--- a\n+++ a\n@@ -1 +1 @@\n-1\n+2\nIndex: b\n--- b\n+++ b\n@@ -1 +1 @@\n-3\n+4\n",
	"--- \"quoted name\"\t2020-01-01\n+++ \"quoted name\"\t2020-01-02\n@@ -1 +1 @@\n-a\n+b\n",
	"--- back\\\\slash\n+++ back\\\\slash\n@@ -1 +1 @@\n-a\n+b\n",
	"@@ -1,3 +1,3 @@\n a\n-b\n+c\n",
	"@@ -1,2 +1,3 @@\n a\n+b\n",
	"@@ -1 +1 @@\n-a\n+b\ngarbage\n",
	"@@ -1,2 +1,2 @@\n-a\n*b\n+c\n",
	"random text\n@@ -1 +1 @@\n-a\n+b\n",
	"Index:   spaced name  \n@@ -1 +1 @@\n-a\n+b\n",
	"",
	"\n",
	"@@ -1,2 +1,2 @@\n-a\r\n-b\r\n+c\r\n+d\r\n",
	"@@ -5,3 +5,3 @@\n x\n-y\n+Y\n z\n@@ -20,2 +20,3 @@\n p\n+q\n r\n",
	"--- a\n+++ b\n@@ -1,3 +1,2 @@\n a\n-b\n c\n\\ No newline at end of file\n",
	"@@ -1 +1 @@\n-x\n\\ No newline at end of file\n+x\n",
	"@@ -1 +1 @@\n-x\n+y\n\\ No newline at end of file\n",
	"@@ -1 +1 @@\n-x\n\\ No newline at end of file\n+y\n\\ No newline at end of file\n",
	"diff --git a/x b/x\nindex 123..456 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n",
	"@@ -1,1 +1,1 @@\n-a\n+b\n\n",
];
// The Go ParsePatch reads a missing file name as "" (JS: undefined, which formatPatch
// would print as "undefined"); format the JS patches with the same "" names.
function parseFilled(patch) {
	const list = Diff.parsePatch(patch);
	for (const p of list) {
		p.oldFileName ??= "";
		p.newFileName ??= "";
	}
	return list;
}
function normPatch(p) {
	const n = {};
	n.index = p.index === undefined ? null : p.index;
	n.oldFileName = p.oldFileName ?? "";
	n.oldHeader = p.oldHeader === undefined ? null : p.oldHeader;
	n.newFileName = p.newFileName ?? "";
	n.newHeader = p.newHeader === undefined ? null : p.newHeader;
	n.hunks = p.hunks.map((h) => ({
		oldStart: Number.isNaN(h.oldStart) ? 0 : h.oldStart,
		oldLines: h.oldLines,
		newStart: Number.isNaN(h.newStart) ? 0 : h.newStart,
		newLines: h.newLines,
		lines: h.lines,
	}));
	return n;
}
for (const patch of patchTexts) {
	run({ fn: "parsePatch", patch }, () => Diff.parsePatch(patch).map(normPatch));
	for (const ho of [undefined, "INCLUDE_HEADERS", "FILE_HEADERS_ONLY", "OMIT_HEADERS"]) {
		run({ fn: "formatPatch", patch, opts: ho ? { headerOptions: ho } : null }, () =>
			Diff.formatPatch(parseFilled(patch), ho ? headerOptions[ho] : undefined),
		);
	}
	run({ fn: "reversePatch", patch }, () => Diff.formatPatch(Diff.reversePatch(parseFilled(patch))));
}

// ---- parsePatch fuzz ----
{
	const frags = ["Index: f", "Index:f", "Index:\tg h ", "Index:\u00a0x", "diff -r abc -r def x", "diff -r abc -r def-x", "diff -r a_1 file", "diff -rx", "diff --git a/x b/x", "diff\u3000x",
		"===================================================================", "--- a\tH", "--- a\tH\tI", "--- \"q\"", "--- \"", "+++ b", "+++\tb", "---x", "+++ \\\\s", "--- \u00a0n",
		"@@ -1,2 +1,2 @@", "@@ -1 +1 @@", "@@ -0,0 +1 @@", "@@ -3,0 +3,1 @@", "@@ -2,1 +2 @@ trailing", "@@-1 +1 @@", "@@ -a +b @@", "@@ -01,2 +1,02 @@",
		" ctx", "-del", "+add", "\\ No newline at end of file", "", "", "garbage", "-", "+", " ", "é", "\r"];
	const hasNaN = (list) => list.some((p) => p.hunks.some((h) => Number.isNaN(h.oldStart) || Number.isNaN(h.newStart)));
	for (let k = 0; k < (scale > 1 ? 300 * scale : 80); k++) {
		const n = ri(12) + 1;
		const lines = [];
		for (let i = 0; i < n; i++) lines.push(pick(frags));
		const patch = lines.join(rand() < 0.1 ? "\r\n" : "\n") + (rand() < 0.7 ? "\n" : "");
		run({ fn: "parsePatch", patch }, () => Diff.parsePatch(patch).map(normPatch));
		let parsed;
		try {
			parsed = Diff.parsePatch(patch);
		} catch {
			continue;
		}
		if (hasNaN(parsed)) continue;
		const ho = pick([undefined, "INCLUDE_HEADERS", "FILE_HEADERS_ONLY", "OMIT_HEADERS"]);
		run({ fn: "formatPatch", patch, opts: ho ? { headerOptions: ho } : null }, () =>
			Diff.formatPatch(parseFilled(patch), ho ? headerOptions[ho] : undefined),
		);
		run({ fn: "reversePatch", patch }, () => Diff.formatPatch(Diff.reversePatch(parseFilled(patch))));
		const src = ["del", "ctx", "x", "", "del", "ctx"].slice(0, ri(7)).join("\n");
		const aopts = { fuzzFactor: ri(3) };
		run({ fn: "applyPatch", a: src, patch, opts: aopts }, () => Diff.applyPatch(src, patch, aopts));
	}
}

// ---- applyPatch scenarios ----
const applyCases = [
	["a\nb\nc\n", "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"],
	["x\na\nb\nc\n", "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"],
	["a\nb\nc\nd\ne\nf\ng\n", "@@ -5,3 +5,3 @@\n e\n-f\n+F\n g\n"],
	["a\nX\nb\nc\n", "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"],
	["a\nb\nc\n", "@@ -1,3 +1,4 @@\n a\n b\n+x\n c\n"],
	["a\nb\nY\n", "@@ -1,3 +1,4 @@\n a\n b\n+x\n c\n"],
	["a\nZ\nc\nd\n", "@@ -1,4 +1,4 @@\n a\n b\n c\n-d\n+D\n"],
	["a\nb", "@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n"],
	["a\nb\n", "@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n"],
	["a\nb\n", "@@ -1,2 +1,2 @@\n a\n-b\n+b\n\\ No newline at end of file\n"],
	["a\nb", "@@ -1,2 +1,2 @@\n a\n-b\n+b\n\\ No newline at end of file\n"],
	["a\nb", "@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+c\n\\ No newline at end of file\n"],
	["a\nb\n", "@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+c\n\\ No newline at end of file\n"],
	["a\r\nb\r\nc\r\n", "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"],
	["a\nb\nc\n", "@@ -1,3 +1,3 @@\n a\r\n-b\r\n+B\r\n c\r\n"],
	["", "@@ -0,0 +1,2 @@\n+x\n+y\n"],
	["", "@@ -1,0 +1,1 @@\n+x\n"],
	["keep\n", ""],
	["a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n", "@@ -2,3 +2,3 @@\n b\n-c\n+C\n d\n@@ -7,3 +7,3 @@\n g\n-h\n+H\n i\n"],
	["0\n1\na\nb\nc\nd\ne\nf\ng\nh\ni\nj\n", "@@ -2,3 +2,3 @@\n b\n-c\n+C\n d\n@@ -7,3 +7,3 @@\n g\n-h\n+H\n i\n"],
	["a\nb\nc\n", "@@ -1,3 +1,3 @@\n a\n-q\n+B\n c\n"],
	["  a\nb  \nc\n", "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"],
	["a\n\nc\n", "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"],
	["a\nb\nc\n", "@@ -1,2 +1,2 @@\n-a\n+A\n b\n@@ -1,2 +1,2 @@\n-a\n+A\n b\n"],
	["a\n", "Index: a\n--- a\n+++ a\n@@ -1 +1 @@\n-a\n+b\nIndex: b\n--- b\n+++ b\n@@ -1 +1 @@\n-3\n+4\n"],
	["x\n", "@@ -1 +1 @@\n-x\n+y\ngarbage\n"],
	["a\nb\nc\nd\ne\n", "@@ -10,3 +10,3 @@\n c\n-d\n+D\n e\n"],
	["p\nq\nr\n", "@@ -1,3 +1,2 @@\n p\n-q\n r\n"],
	["p\nr\n", "@@ -1,3 +1,4 @@\n p\n q\n+Q\n r\n"],
];
for (const [src, patch] of applyCases) {
	for (const opts of [null, { fuzzFactor: 1 }, { fuzzFactor: 2 }, { autoConvertLineEndings: false }, { compareLine: "ignoreWs" }, { fuzzFactor: -1 }]) {
		run({ fn: "applyPatch", a: src, patch, opts }, () => Diff.applyPatch(src, patch, realOpts(opts)));
	}
}

// ---- convert ----
const convertPairs = [
	["<a href=\"x\">&amp; b</a>", "<a href='y'>& c</a>"],
	["foo bar", "foo baz qux"],
	["same", "same"],
];
for (const [a, b] of convertPairs) {
	const changes = Diff.diffWords(a, b);
	run({ fn: "convertChangesToXML", a, b }, () => Diff.convertChangesToXML(changes));
	run({ fn: "convertChangesToDMP", a, b }, () => Diff.convertChangesToDMP(changes));
}

// ---- pi usage: edit-diff generateUnifiedPatch ----
const piPairs = [
	["export function foo() {\n\treturn 1;\n}\n", "export function foo() {\n\treturn 2;\n}\n"],
	["a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\no\np\n", "a\nb\nC\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nN\no\np\n"],
	["no newline", "no newline\n"],
	["line\n", "line\nnew last line"],
];
for (const [a, b] of piPairs) {
	for (const context of [4, 3, 0]) {
		const opts = { context, headerOptions: "FILE_HEADERS_ONLY" };
		run({ fn: "createTwoFilesPatch", oldName: "src/x.ts", newName: "src/x.ts", a, b, opts }, () =>
			Diff.createTwoFilesPatch("src/x.ts", "src/x.ts", a, b, undefined, undefined, realOpts(opts)),
		);
	}
	diffCase("diffLines", a, b, null);
	diffCase("diffWords", a, b, null);
}

const out = { generator: `diff@${pkg.version} via node ${process.version}; testdata/gen.cjs`, segments, cases };
fs.writeFileSync(
	process.argv[2],
	`{"generator":${JSON.stringify(out.generator)},\n"segments":{\n${Object.entries(segments)
		.map(([k, v]) => JSON.stringify(k) + ":" + JSON.stringify(v))
		.join(",\n")}\n},\n"cases":[\n${cases.map((c) => JSON.stringify(c)).join(",\n")}\n]}\n`,
);
console.log(cases.length, "cases", Object.keys(segments).length, "segment entries");
