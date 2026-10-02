// Generates Node 24 TextDecoder streaming fuzz vectors and fromCodePoint vectors for internal/js tests.
import fs from "node:fs";
const out = process.argv[2];
let seed = 987654;
const rnd = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
const pool = [0x41, 0x42, 0xef, 0xbb, 0xbf, 0xe2, 0x82, 0xac, 0xf0, 0x9f, 0x98, 0x80, 0xc3, 0xa9, 0xff, 0xc0, 0xed, 0xa0, 0xf4, 0x90, 0x80, 0xbf, 0xe0, 0xa4];
const cases = [];
for (let n = 0; n < 700; n++) {
  const fatal = rnd() < 0.3;
  const ignoreBOM = rnd() < 0.2;
  const d = new TextDecoder("utf-8", { fatal, ignoreBOM });
  const calls = [];
  const ncalls = 1 + Math.floor(rnd() * 6);
  for (let c = 0; c < ncalls; c++) {
    const len = Math.floor(rnd() * 7);
    const bytes = Array.from({ length: len }, () => pool[Math.floor(rnd() * pool.length)]);
    const stream = rnd() < 0.75;
    let res;
    try { res = { ok: d.decode(new Uint8Array(bytes), { stream }) }; } catch (e) { res = { err: String(e) }; }
    calls.push({ bytes, stream, res });
  }
  let res;
  try { res = { ok: d.decode() }; } catch (e) { res = { err: String(e) }; }
  calls.push({ bytes: null, stream: false, res });
  cases.push({ fatal, ignoreBOM, calls });
}
const cps = [0, 0x41, 0xe9, 0xffff, 0x10000, 0x1f600, 0x10ffff, 0x110000, -1, 0xd800, 0xdfff, 1.5, 0x2028];
const fromCodePoint = cps.map((cp) => { try { return [cp, { ok: String.fromCodePoint(cp).toWellFormed() }]; } catch (e) { return [cp, { err: String(e) }]; } });
fs.writeFileSync(out + "/decoder_stream.json", JSON.stringify({ cases, fromCodePoint }));
