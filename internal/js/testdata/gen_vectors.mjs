// Generates Node 24 reference vectors for internal/js tests.
import fs from "node:fs";
const out = process.argv[2];
const wf = (s) => s.toWellFormed();
const bits = (x) => { const dv = new DataView(new ArrayBuffer(8)); dv.setFloat64(0, x); return dv.getBigUint64(0).toString(16).padStart(16, "0"); };
let seed = 12345;
const rnd = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
const pick = (a) => a[Math.floor(rnd() * a.length)];

// ---------- strings ----------
const strs = ["", "a", "abc", "héllo", "日本語テキスト", "a😀b", "😀", "😀😁", "x́y", "ab😀cd😀", "\u{10FFFF}z", "tab\tnew\nline", "  padded  ", "ÄÖÜäöüß"];
const strCases = [];
for (const s of strs) {
  const n = s.length;
  const slices = [];
  for (let a = -n - 2; a <= n + 2; a++) for (let b = -n - 2; b <= n + 2; b++) slices.push([a, b, wf(s.slice(a, b)), wf(s.substring(a, b))]);
  const from = []; for (let a = -n - 2; a <= n + 2; a++) from.push([a, wf(s.slice(a)), wf(s.substring(a)), s.charCodeAt(a), s.codePointAt(a) ?? -1, wf(s.charAt(a))]);
  const subs = ["", "a", "b", "😀", "l", "lo", "語", "d", "x", "zz", "́", " "];
  const idx = [];
  for (const sub of subs) {
    const fr = [];
    for (let a = -2; a <= n + 2; a++) fr.push([a, s.indexOf(sub, a), s.lastIndexOf(sub, a)]);
    idx.push({ sub, indexOf: fr, lastIndexOf: s.lastIndexOf(sub) });
  }
  const splits = ["", "a", "😀", " ", "b", "l", "xyz"].map((sep) => [sep, s.split(sep).map(wf)]);
  const pads = [];
  for (const p of [" ", "ab", "😀", "-=", ""]) for (let k = 0; k <= n + 5; k++) pads.push([p, k, wf(s.padStart(k, p)), wf(s.padEnd(k, p))]);
  strCases.push({ s, len: n, slices, from, idx, splits, pads, utf16: Array.from({ length: n }, (_, i) => s.charCodeAt(i)) });
}

const caseStrs = ["hello", "HELLO", "straße", "ǅ", "ﬁ", "ŉ", "ΐ", "İstanbul", "ΑΣ", "ΑΣ ΒΣ", "Σ", "ΌΣΟΣ", "ΟΔΥΣΣΕΥΣ", "aΣb", "Σa", "a.Σ", "ǰ", "ΐ", "Ꭰ", "ꭰ", "𐐀𐐨", "Ⅻ", "ⓐ", "ﬀ", "ı", "ſ", "K", "Å", "Ω", "Ǆǅǆ", "i̇", "ÉCOLE", "ß", "ẞ", "Ǉ", "ǈ", "ǉ"];
const caseCases = caseStrs.map((s) => [s, s.toUpperCase(), s.toLowerCase()]);
const normStrs = ["é", "é", "ﬁ", "Å", "Å", "①", "가", "가", "ẛ̣", "ｶ", "ẋ̣", "Ω"];
const normCases = normStrs.map((s) => [s, s.normalize("NFC"), s.normalize("NFD"), s.normalize("NFKC"), s.normalize("NFKD"), s.normalize()]);
const trimStrs = [" a ", "\t\n\v\f\r a  ", "    x     　﻿", "᠎ x ᠎", "\u0085x\u0085", "​x​", "x"];
const trimCases = trimStrs.map((s) => [s, s.trim(), s.trimStart(), s.trimEnd()]);

const sortWords = ["b", "a", "B", "A", "_", "-", "a-b", "a_b", "ab", "aB", "Ab", "AB", "é", "e", "f", "E", "ä", "z", "Z", "1", "10", "2", "a1", "a10", "a2", "😀", "￿", "", "日本", "中", "x y", "x-y", "x.y", "x/y", ".hidden", "README.md", "readme.md", "Readme.md", "file.ts", "file.test.ts", "file-utils.ts", "file_utils.ts", "fileUtils.ts", "claude-3-5-sonnet", "claude-3-5-sonnet-20241022", "claude-3-5-sonnet-latest", "gpt-4o", "gpt-4o-mini", "gpt-4.1", "o1", "o3-mini", "anthropic", "openai", "google", "Ångström", "resume", "résumé", "Résumé", "coop", "co-op", "côte", "cote", "côté", "coté", "", " ", "  ", "a ", " a", "@scope/pkg", "~home", "#hash", "$dollar", "(paren)", "[bracket]", "{brace}", "a\u0000b", "ab\u0000", "ǅ", "ı", "I", "i"];
const localePairs = [];
for (const a of sortWords) for (const b of sortWords) localePairs.push([a, b, a.localeCompare(b)]);
const defaultSort = [...sortWords].sort();

// ---------- numbers ----------
const nums = [0, -0, 1, -1, 0.1, 0.2, 0.3, 0.1 + 0.2, 1 / 3, 2 / 3, 0.5, 1.5, 2.5, -0.5, -1.5, -2.5, 1.005, 1.045, 1.0049999999999999, 1.45, 8.345, 1.255, 1234.5678, 123456789.123, 1e21, 1e20, 9.99e20, 123e-20, 1e-7, 1e-6, 1.5e-7, 0.000001234, 0.00001, 5e-324, 1.7976931348623157e308, 2 ** 53, 2 ** 53 + 2, -(2 ** 53), 4503599627370495.5, -4503599627370495.5, 0.49999999999999994, -0.49999999999999994, 99.995, 0.045, 0.0005, 1.0000000000000002, 123.456, -123.456, 1e15, 1e16, 1e17, 123456789012345680000, 0.1234567890123, 3.14159, Math.PI, Math.E, 255, 255.5, -255.5, 1024, 65535.99, 0.000123, 7.0000000000000001e-5, 1e100, 1.23e-100, NaN, Infinity, -Infinity, 42, 1234567, 12345.6789, 999.9995, 0.9999, 9.5, 99.5, 999.5, 0.05, 0.15, 0.25, 0.35, 1e-10, 12, 100, 1000000, 0.07, 2.675, 1.1, 1.01];
for (let i = 0; i < 150; i++) nums.push(Number((rnd() * 10 ** Math.floor(rnd() * 12 - 4)).toPrecision(1 + Math.floor(rnd() * 17))) * (rnd() < 0.3 ? -1 : 1));
for (let i = 0; i < 50; i++) { const dv = new DataView(new ArrayBuffer(8)); dv.setUint32(0, Math.floor(rnd() * 2 ** 32)); dv.setUint32(4, Math.floor(rnd() * 2 ** 32)); const x = dv.getFloat64(0); if (Number.isFinite(x)) nums.push(x); }
const numCases = nums.map((x) => {
  const fixed = []; for (const d of [0, 1, 2, 3, 4, 5, 8, 10, 15, 20, 25, 50, 100]) fixed.push(x.toFixed(d));
  const prec = []; for (let p = 1; p <= 21; p++) prec.push(x.toPrecision(p)); prec.push(x.toPrecision(50)); prec.push(x.toPrecision(100));
  const radix = [2, 3, 7, 8, 16, 32, 36].map((r) => x.toString(r));
  const locale = [x.toLocaleString(), x.toLocaleString("en-US", { maximumFractionDigits: 0 }), x.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 }), x.toLocaleString("en-US", { maximumFractionDigits: 1 }), x.toLocaleString("en-US", { minimumFractionDigits: 1, maximumFractionDigits: 4 })];
  return { x: bits(x), str: String(x), fixed, prec, radix, round: bits(Math.round(x)), locale };
});
const parseStrs = ["", " ", "0", "-0", "+0", "42", "  42  ", " ﻿42", "42px", "4.2e3", "4.2e", "4.2e+", ".5", "-.5", "+.5", ".", "-", "+", "5.", "0x1F", "0X1f", "-0x1F", "0x", "0xg", "0o17", "0b101", "0O17", "0B11", "1_000", "Infinity", "-Infinity", "+Infinity", "infinity", "Infinityx", "1e400", "-1e400", "1e-400", "123456789012345678901234567890", "9007199254740993", "0.1", "1,000", "١٢", "  -12.5e-3xyz", "\n\t12", "12 34", "0012", "08", "0.0000001", "1e21", "z", "zz", "Z1", "10", "777", "-ff", "ff", "0xff", "1e3", "abc", "  7", "7 "];
const radixes = [0, 2, 8, 10, 16, 36, 1, 37, 3];
const parseCases = parseStrs.map((s) => ({ s, int: radixes.map((r) => bits(parseInt(s, r === 0 ? undefined : r))), float: bits(parseFloat(s)), number: bits(Number(s)) }));
parseCases.radixes = radixes;

// ---------- uri ----------
const uriStrs = ["", "abc", "a b", "a+b=c&d", "日本", "😀", "-_.!~*'()", ";/?:@&=+$,#", "100%", "%41", "%", "%4", "%zz", "%E6%97%A5", "%E6%97", "%C0%AF", "%ED%A0%80", "%F4%90%80%80", "%F0%9F%98%80", "%3B%2F%3F%3A%40%26%3D%2B%24%2C%23", "%e6%97%a5", "%80", "%FF", "a%20b%2", "%EF%BF%BD", "%C2%A0", "é%C3%A9", "%25", "http://x.com/a b?q=1#f", "%41%42%43"];
const tryf = (f, s) => { try { return { ok: f(s) }; } catch (e) { return { err: String(e) }; } };
const uriCases = uriStrs.map((s) => ({ s, encC: encodeURIComponent(s), enc: encodeURI(s), decC: tryf(decodeURIComponent, s), dec: tryf(decodeURI, s) }));

// ---------- text decoder ----------
const byteInputs = [[0x41], [0xef, 0xbb, 0xbf, 0x41], [0xef, 0xbb, 0xbf], [0xef, 0xbb, 0xbf, 0xef, 0xbb, 0xbf, 0x41], [0xff], [0xc0, 0x80], [0xe2, 0x82], [0xe2, 0x82, 0xac], [0xf0, 0x9f, 0x98, 0x80], [0xf0, 0x9f, 0x98], [0xed, 0xa0, 0x80], [0xf4, 0x90, 0x80, 0x80], [0xe0, 0x80, 0x80], [0x61, 0xe2, 0x82, 0x62], [0xc3], [0x80, 0x80], [0xf8, 0x88, 0x80, 0x80, 0x80], [0xe2, 0x28, 0xa1], [0xf0, 0x28, 0x8c, 0xbc], [0xf0, 0x90, 0x28, 0xbc], [0x68, 0xc3, 0xa9, 0x6c, 0x6c, 0x6f], [0xef, 0xbb], [0xc2, 0xa0, 0xc2]];
const decoderCases = [];
for (const bytes of byteInputs) {
  const u8 = new Uint8Array(bytes);
  const whole = new TextDecoder().decode(u8);
  const ignore = new TextDecoder("utf-8", { ignoreBOM: true }).decode(u8);
  let fatal; try { fatal = { ok: new TextDecoder("utf-8", { fatal: true }).decode(u8) }; } catch (e) { fatal = { err: String(e), code: e.code }; }
  // all split points into 1..3 chunks
  const chunked = [];
  for (let i = 0; i <= bytes.length; i++) for (let j = i; j <= bytes.length; j++) {
    const d = new TextDecoder();
    const parts = [d.decode(u8.subarray(0, i), { stream: true }), d.decode(u8.subarray(i, j), { stream: true }), d.decode(u8.subarray(j), { stream: true }), d.decode()];
    chunked.push([i, j, parts]);
  }
  decoderCases.push({ bytes, whole, ignore, fatal, chunked });
}
// decoder reuse after non-stream decode resets BOM state
const reuse = (() => { const d = new TextDecoder(); return [d.decode(new Uint8Array([0xef, 0xbb, 0xbf, 0x41])), d.decode(new Uint8Array([0xef, 0xbb, 0xbf, 0x42])), d.decode(new Uint8Array([0xe2]), { stream: true }), d.decode(new Uint8Array([0x82, 0xac]))]; })();

// ---------- dates ----------
const dateStrs = ["2024-01-02T03:04:05.678Z", "2024-01-02T03:04:05Z", "2024-01-02T03:04Z", "2024-01-02T03:04", "2024-01-02T03:04:05", "2024-01-02", "2024-01", "2024", "+002024-01-02T00:00:00Z", "-000001-01-01T00:00:00Z", "-000000-01-01T00:00:00Z", "2024-01-02T24:00:00Z", "2024-01-02T24:00:01Z", "2024-01-02T03:04:05.6Z", "2024-01-02T03:04:05.67891234Z", "2024-01-02T03:04:05+05:30", "2024-01-02T03:04:05-0800", "2024-01-02T03:04:05+5:30", "2024-02-30", "2024-13-01", "2024-00-10", "2024-01-32", "2024-1-2", "2024/01/02", "2024/1/2 10:20", "01/02/2024", "1/2/24", "1/2/99", "1/2/50", "1/2/49", "Wed, 21 Oct 2015 07:28:00 GMT", "Wednesday, 21-Oct-15 07:28:00 GMT", "Wed Oct 21 07:28:00 2015", "Wed Oct 21 2015 07:28:00 GMT+0200 (Central European Summer Time)", "Oct 21, 2015", "21 Oct 2015", "October 21, 2015 10:00 PM", "Oct 21", "21 October", "2015 Oct 21", "Thu, 01 Jan 1970 00:00:00 GMT", "Thu, 01 Jan 1970 00:00:00 GMT+1", "Thu, 01 Jan 1970 00:00:00 UTC-0130", "Thu, 01 Jan 1970 00:00:00 EST", "Thu, 01 Jan 1970 00:00:00 PDT", "12:30 PM Jan 5 2020", "Jan 5 2020 12:30:45.5 am", "Jan 5 2020 13:30 PM", "Jan 5 2020 00:30 AM", "2020-01-05 10:00", "2020-01-05 10:00Z", "2020-01-05 10:00 GMT", "2020-01-05T10:00:00.000+00:00", "", "   ", "garbage", "Tue 2020", "2020 Tue", "abc 1 2 2020", "1 abc 2020", "Jan 1 2020 (comment) 10:00", "Jan 1 2020 10:00 (comment", "Jan 1 2020 10:00)", "Jan 1 2020 10::", "10:20:30 Jan 1 2020", "Jan 1 2020 10.20.30", "Jan 1 2020 10:20.30", "Jan 1 2020 10:20:30.123456", "Jan 1, 2020 -0500", "Jan 1 2020 10:00 -05:00", "Jan 1 2020 10:00 +5", "Jan 1 2020 10:00 +12345", "2020-01-01T10:00:00.000Zjunk", "2020-06-15T12:00:00.000+0000", "Mon Jan 01 2020", "1970-01-01T00:00:00.000Z", "275760-09-13", "+275760-09-13T00:00:00.000Z", "+275760-09-13T00:00:00.001Z", "-271821-04-20T00:00:00.000Z", "-271821-04-19T23:59:59.999Z", "Sat, 01-Jan-2000 08:00:00 GMT", "2000-01-01T00:00:00.000z", "2000-01-01t00:00:00Z", "2000-01-01 T 00:00", "Jan 2000", "2000 Jan", "1 2 3", "1 2 3 4", "13/1/2000", "2000/13/1", "0/1/2000", "Jan 0 2000", "Jan 31 2000 23:59:59.999", "Feb 29 2001", "12/31/1999 23:59:59 GMT", "12/31/1999 11:59:59 PM PST", "2020-01-01T10:00:00+24:00", "2020-01-01T10:00:00+23:59", "2020-01-01T10:60:00Z", "2020-01-01T10:00:60Z", " Jan 1 2020", "Jan 1 2020", "Jan-1-2020", "1-Jan-2020", "2020-Jan-01", "Jan 1 2020 GMT+0530", "Jan 1 2020 Z", "Jan 1 2020 10:00Z", "Jan 1 2020 10:00 z", "Jan 1 2020 utc", "Jan 1 2020 ut", "Jan 1 2020 10:00 am pm", "Janx 1 2020", "Ja 1 2020", "January 1 2020 AD", "1 2020 Jan", "99999 Jan 1", "Jan 1 999999999", "Jan 1 1000000", "2020-01-01T00:00:00.000-00:00"];
const randTokens = ["2020", "20", "1", "12", "31", "07", "123", "1999", "0", "00", "999", "123456", "Jan", "Oct", "December", "Mon", "Tue,", "GMT", "UTC", "Z", "z", "PST", "am", "PM", "T", "t", " ", " ", " ", ",", "-", "+", ":", "::", ".", "/", "(", ")", "(x)", "x", "_", "\t", "+0530", "-08:00"];
for (let i = 0; i < 3000; i++) { let s = ""; const n = 1 + Math.floor(rnd() * 8); for (let k = 0; k < n; k++) s += pick(randTokens); dateStrs.push(s); }
const dateCases = dateStrs.map((s) => { const v = Date.parse(s); return [s, Number.isNaN(v) ? null : v]; });
const isoMs = [0, 1, -1, 1700000000123, -62198755200000, -62198755200001, 253402300799999, 253402300800000, 8.64e15, -8.64e15, 1e12 + 0.5];
const isoCases = isoMs.map((ms) => [ms, new Date(ms).toISOString()]);
const localeDateMs = [0, 1700000000123, 1720000000000, 946684800000, 1710054000000, 1710050400000, 1730612400000, -1e11];
const localeDateCases = localeDateMs.map((ms) => [ms, new Date(ms).toLocaleString()]);

fs.writeFileSync(out + "/strings.json", JSON.stringify({ strCases, caseCases, normCases, trimCases }));
fs.writeFileSync(out + "/collation.json", JSON.stringify({ words: sortWords, localePairs, defaultSort }));
fs.writeFileSync(out + "/numbers.json", JSON.stringify({ numCases, parseCases, radixes }));
fs.writeFileSync(out + "/uri.json", JSON.stringify({ uriCases }));
fs.writeFileSync(out + "/decoder.json", JSON.stringify({ decoderCases, reuse }));
fs.writeFileSync(out + "/dates.json", JSON.stringify({ tz: process.env.TZ, dateCases, isoCases, localeDateCases }));
