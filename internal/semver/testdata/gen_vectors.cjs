const fs = require("fs");
const path = require("path");
const semver = require("C:/Users/ctyja/workspace/pi/node_modules/semver");

const versions = [
  "0.0.0", "0.0.1", "0.0.2", "0.1.0", "0.1.2", "0.1.3", "0.2.0",
  "1.0.0", "1.0.1", "1.2.0", "1.2.3", "1.2.4", "1.3.0", "1.9.9", "1.10.0",
  "2.0.0", "2.0.1", "2.3.4", "3.0.0", "10.0.0",
  "v1.2.3", "V1.2.3", "=1.2.3",
  "1.2.3-0", "1.2.3-alpha", "1.2.3-alpha.1", "1.2.3-beta", "1.2.3-beta.1",
  "1.2.3-beta.2", "1.2.3-beta.10", "1.2.3-rc.1",
  "1.0.0-0", "1.0.0-alpha", "1.0.0-alpha.beta", "2.0.0-0", "2.0.0-alpha",
  "1.2.3+build", "1.2.3+build.5", "1.2.3-beta.1+build.5", "1.0.0+aaa", "1.0.0+zzz",
  "1.2", "1", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "1.2.3-",
  "1.2.3+", "1.2.3-beta.01", "", " ", "nope", "v", "1.2.3-beta.1.a",
  "9007199254740991.0.0", "9007199254740992.0.0",
  "1.0.0-9007199254740991", "1.0.0-9007199254740992", "1.0.0-9007199254740993",
  "1.0.0-99999999999999999", "1.0.0-a", "1.0.0-0a",
];

const ranges = [
  "", " ", "*", "x", "X", ">=0.0.0", "||", "1 ||", "|| 1.2.3", "1.2.3 ||",
  "1.2.3", "v1.2.3", "=1.2.3", "==1.2.3", "1.2.3+build",
  "1.2.3-beta.1+build.5",
  "^1.2.3", "^0.0.1", "^0.1.2", "^0.0.x", "^1.2.3-beta.1", "^1", "^1.2", "^0",
  "~1.2.3", "~0.0.1", "~1.2", "~1", "~>1.2.3", "~>1.2",
  "1.2", "1", "1.x", "1.X", "1.*", "1.2.x", "1.2.*", "1.x.2", "x.1",
  "1.2.3 - 2.3.4", "1.2 - 3.4", "1.2.3 - 3.4", "1.2.3 - 2",
  "1.2.3-beta - 2.0.0", "v1.2.3 - 2.0.0", "=1.2.3 - 2.0.0",
  "1.2.3+build - 2.0.0+zzz",
  "1.2.3 || 2.0.0", "^1.2.3 || ^2.0.0", "~1.2 || ~2",
  ">1 || <0", "<0.0.0-0 || 1.2.3", "1.2.3 || <0.0.0-0",
  "<0.0.0-0", "* || 1.2.3", "1.2.3 || *", ">=0.0.0 || 1.2.3",
  ">1", ">1.2", "<=1", "<1", ">=1", "<=1.2.x", ">=1.0.0-0",
  "> 1.2.3", ">=  1.2.3", "~ 1.2.3", "^ 1.2.3", "  ^1.2.3  ",
  "1.2.3  2.0.0", "1.2.3 2.0.0",
  "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "V1.2.3", "1.2.3-", "1.2.3+",
  "1.2.3.4", "1.2.3-beta.01",
  "not a range", "^1.2.3-0", "~1.2.3-beta",
  ">=1.2.3 <2.0.0", ">1.2.3", "<=1.2.3",
  "^0.0.0", "~0", "1.2.3 || 1.2.3", ">=1.0.0 <1.0.0",
  "1.0.0 - 1.0.0", "*",
  ">=1.2.3-beta.1 <1.2.4",
];

function call(fn) {
  try {
    return { ok: true, v: fn() };
  } catch (e) {
    return { ok: false, err: String(e && e.message ? e.message : e) };
  }
}

const valid = versions.map((v) => {
  const r = call(() => semver.valid(v));
  return { v, ok: r.ok, value: r.ok ? r.v : null, err: r.err || "" };
});

const validRange = ranges.map((r) => {
  const out = call(() => semver.validRange(r));
  return { r, ok: out.ok, value: out.ok ? out.v : null, err: out.err || "" };
});

const pairs = [];
const throws = [];
for (let i = 0; i < versions.length; i++) {
  for (let j = i; j < versions.length; j++) {
    const a = versions[i];
    const b = versions[j];
    const c = call(() => semver.compare(a, b));
    const rc = call(() => semver.rcompare(a, b));
    const gt = call(() => semver.gt(a, b));
    const gtba = call(() => semver.gt(b, a));
    if (!c.ok || !rc.ok || !gt.ok || !gtba.ok) {
      throws.push({ a, b, compare: c.ok, rcompare: rc.ok, gt: gt.ok, gtba: gtba.ok });
      continue;
    }
    pairs.push({ a, b, compare: c.v, rcompare: rc.v, gt: gt.v, gtba: gtba.v });
  }
}

const satisfies = [];
for (const version of versions) {
  for (const range of ranges) {
    const r = call(() => semver.satisfies(version, range));
    satisfies.push({
      version,
      range,
      ok: r.ok,
      value: r.ok ? r.v : null,
      err: r.err || "",
    });
  }
}

const lists = [
  ["1.2.3", "1.2.4", "2.0.0", "1.3.0-beta"],
  ["1.0.0", "1.0.0+build", "1.0.0+aaa"],
  ["1.0.0+zzz", "1.0.0+aaa"],
  ["1.2.3-beta", "1.2.3", "1.2.4-alpha"],
  ["1.2.3-beta.1", "1.2.3-beta.2"],
  ["a", "1.0.0", "bad", "2.0.0"],
  ["1.0.0"],
  [],
  ["2.0.0", "1.9.9", "1.2.4", "1.2.3"],
  ["1.0.0-alpha", "1.0.0", "1.0.1-0"],
  ["0.0.1", "0.0.2", "0.1.0"],
];
const maxSat = [];
for (const versions of lists) {
  for (const range of ranges) {
    const r = call(() => semver.maxSatisfying(versions, range));
    maxSat.push({
      versions,
      range,
      ok: r.ok,
      value: r.ok ? r.v : null,
      err: r.err || "",
    });
  }
}

const out = {
  valid,
  validRange,
  pairs,
  throws,
  satisfies,
  maxSat,
};
const dest = path.join(__dirname, "vectors.json");
fs.writeFileSync(dest, JSON.stringify(out));
console.log("valid", valid.length, "ranges", validRange.length, "pairs", pairs.length, "throws", throws.length, "sat", satisfies.length, "max", maxSat.length);
console.log("bytes", fs.statSync(dest).size);
