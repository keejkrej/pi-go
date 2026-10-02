const fs = require("fs");
const path = require("path");
const { minimatch, Minimatch } = require("C:/Users/ctyja/workspace/pi/node_modules/minimatch");

function encode(part) {
  if (part === minimatch.GLOBSTAR) return "**";
  if (typeof part === "string") return "s:" + part;
  return "r:" + part.flags + ":" + part._src;
}

const patterns = [
  "", "*", "**", "*.js", ".*", ".*.js", "?", "??", "???",
  "a", "a/b", "a/b/", "a/", "/a", "a/b/c",
  "a/**", "**/a", "a/**/b", "foo/**/bar", "foo/**/**/bar",
  "a/**/b/**/c", "**/*", "a/**/*", "*/*", "a/*/c",
  ".", "..",
  "{a,b}", "{a,b}/c", "a{b,c}d", "{1..3}", "{01..03}", "{a..c}", "{c..a}",
  "{1..5..2}", "a{b}c", "a{},b}c", "a{b,c}d{e,f}",
  "{a,b}{c,d}", "x{a,{b,c},d}y", "{{a,b}}",
  "{,}", "a{,b}", "{a,,b}", "pre{d,e}{1..2}", "a{b,c{d,e}f}g", "{a..c..2}",
  "a\\*", "\\#foo", "#foo", "# comment",
  "!(foo)", "dir/!(foo)", "+(a|b)", "*(a|b)", "?(a|b)", "@(a|b)",
  "+(a|*(b))", "+(a|?(b))", "@(a|*(b))", "*(a|+(b))",
  "+(a|+(b|+(c)))", "*(a|b|+(c))", "a*(b|c)", "*(a|b)*",
  "+(a|)", "*(|a)", "!()", "@()", "*()", "+()", "?()",
  "!(a|)", "!(a|b)", "!(a|b)c", "a!(b)c",
  "[ab]", "[a-c]", "[!a]", "[^a]", "[a]", "[*]", "[]]", "[.]",
  "[[:alpha:]]", "[[:digit:]]", "[[:word:]]", "[[:ascii:]]",
  "[[:lower:]]", "[[:upper:]]", "[[:alnum:]]", "[[:xdigit:]]",
  "[[:space:]]", "[[:punct:]]", "[[:graph:]]", "[[:print:]]", "[[:blank:]]", "[[:cntrl:]]",
  "[c-a]", "[]", "[a", "[a-c-e]",
  "a/b/../c", "a/../b", "C:/foo/../bar", "C:/../foo",
  "a//b",
  "!a", "!a/b", "!!a", "!*.js", "!",
  "a\\b", "a\\\\b",
  "*.JS", "Foo", "f*", "F*",
  "foo/**/bar/**", "**/.git", "**/.*",
  "*a*", "a*", "*a", "a?c",
  "C:/foo", "c:/foo", "//server/share/*", "//?/C:/*",
  "a b", "a{b,}", "{Z..a}",
  "café*", "a/**/b/**/c/**/d",
];

const paths = [
  "", "a", "b", "a.js", "b.js", ".js", ".a", ".a.js", "a/b", "a/b/c",
  "a/", "a/b/", "/", "foo", "foo/bar", "foo/a/bar", "foo/a/b/bar",
  "foo/bar/", "a/c", "a/.b", "a/.", "a/..", ".", "..", ".abc",
  "abc", "ab", "ac", "bb", "aa", "a}c", "ba", "c",
  "1", "2", "3", "01", "02", "03", "4", "5", "10",
  "a*", "#foo", "foo.js",
  "dir/foo", "dir/bar", "dir/foo/x", "dir/a",
  "C:/foo", "c:/foo", "C:/bar", "c:/Foo", "C:/foo/bar",
  "a\\b", "a/b", "//server/share", "//server/share/a",
  "//?/C:/foo", "//?/c:/foo", "//?/C:/a",
  "a//b", "x/y/z", "a/b/../c",
  "A.JS", "foo.JS", "Foo", "FOO",
  ".git", "a/.git", "a/.x/c", "a/x/c", "a/.x",
  "a/b/c/", "abbc", "acd", "abd",
  "xay", "xby", "xcy", "xdy", "x{a}", "a{b}c",
  "A", "Z", "[", "\\", "]", " ", "a b",
  "ab", "abb", "abc", "abbb", "b", "bbb",
  "café", "caféx", "caf",
  "foo/bar/baz", "a/b/c/d",
  "*", "?", "a?", "axc",
];

const optSets = [
  { platform: "posix" },
  { platform: "posix", dot: true },
  { platform: "posix", nocase: true },
  { platform: "posix", matchBase: true },
  { platform: "posix", nobrace: true },
  { platform: "posix", noext: true },
  { platform: "posix", dot: true, nocase: true },
  { platform: "win32" },
  { platform: "win32", nocase: true },
  { platform: "win32", dot: true },
  { platform: "win32", nocase: true, dot: true },
];

const cases = [];
for (const opts of optSets) {
  for (const pattern of patterns) {
    let mm;
    let thrown = "";
    try {
      mm = new Minimatch(pattern, opts);
    } catch (e) {
      thrown = String(e && e.message ? e.message : e);
    }
    if (thrown) {
      cases.push({
        pattern,
        platform: opts.platform,
        nocase: !!opts.nocase,
        dot: !!opts.dot,
        matchBase: !!opts.matchBase,
        nobrace: !!opts.nobrace,
        noext: !!opts.noext,
        throw: thrown,
      });
      continue;
    }
    const set = (mm.set || []).map((row) => row.map(encode));
    const outPaths = [];
    for (const p of paths) {
      let m;
      try {
        m = mm.match(p);
      } catch (e) {
        outPaths.push({ p, throw: String(e && e.message ? e.message : e) });
        continue;
      }
      // The public function and the matcher agree.
      const fn = minimatch(p, pattern, opts);
      if (fn !== m) {
        outPaths.push({ p, m, fnMismatch: fn });
      } else {
        outPaths.push({ p, m });
      }
    }
    cases.push({
      pattern,
      platform: opts.platform,
      nocase: !!opts.nocase,
      dot: !!opts.dot,
      matchBase: !!opts.matchBase,
      nobrace: !!opts.nobrace,
      noext: !!opts.noext,
      comment: !!mm.comment,
      empty: !!mm.empty,
      negate: !!mm.negate,
      set,
      paths: outPaths,
    });
  }
}

const out = path.join(__dirname, "vectors.json");
fs.writeFileSync(out, JSON.stringify({ cases }));
console.log("cases", cases.length, "bytes", fs.statSync(out).size);
