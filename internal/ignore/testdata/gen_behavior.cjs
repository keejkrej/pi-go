const fs = require("fs");
const path = require("path");
const ignore = require("C:/Users/ctyja/workspace/pi/node_modules/ignore");

const cases = [
  {
    name: "basic",
    patterns: ["*.log", "!important.log", "dir/", "/root", "foo/**/bar", "# cmt", "", "   ", "\\"],
    paths: ["a.log", "dir/a.log", "important.log", "dir/important.log", "dir/x", "dir", "root", "a/root", "foo/bar", "foo/a/bar", "foo/a/b/bar", "foo/bar/baz", "keep.txt"],
  },
  {
    name: "stars",
    patterns: ["*", "**", "***", "****", "a***", "***b", "a***b", "foo*", "*foo", "?.log", "foo*bar*baz*", "*x*y*z"],
    paths: ["", "a", "a.log", "ab", "foo", "foobar", "x.log", "fooXbarYbaz", "fooXbarYbazZ", "xyz", "axbycz", "xaybz", "aaab", "b", "aXb", "aXXXb", "foo*"],
  },
  {
    name: "globstar",
    patterns: ["**/foo", "**/*", "**/*.js", "**/**", "foo/**", "a/**/b", "a/**/", "a/**/**/b", "foo/**/bar", "foo/**/bar/", "a/***/b", "foo/***", "dir/**", "foo/*/bar", "a/**/b/**"],
    paths: ["foo", "a/foo", "foo/a", "a.js", ".js", "a/b.js", "foo/a", "foo/a/b", "a/b", "a/x/b", "a/b/c", "a/", "a/b/", "foo/bar", "foo/a/bar", "foo/bar/", "foo/a/bar/", "a/b/c/d", "dir/a", "dir", "foo/x/bar", "a/c/b", "a/b/x"],
  },
  {
    name: "negation",
    patterns: ["*", "!keep", "dir/*", "!dir/keep", "!dir/sub/keep", "secret/**", "!secret/ok"],
    paths: ["a", "keep", "dir/a", "dir/keep", "dir/sub/keep", "dir/sub/a", "secret/a", "secret/ok", "secret/ok/no"],
  },
  {
    name: "class",
    patterns: ["[a-c]", "[[:alpha:]]", "[c-a]", "[]]", "[]", "[[:digit:]].txt", "[a-c-e]", "[.-0]", "[/]", "[!a]", "[^a]", "[[]", "[a-]", "[-a]", "foo[abc]bar", "[[:blank:]]", "[[:space:]]", "[[:punct:]]", "[[:graph:]]", "[[:print:]]", "[[:alnum:]]", "[[:xdigit:]]", "*[a-c]*"],
    paths: ["a", "b", "c", "d", "e", "A", "1", "]", "[", "/", ".", "0", "fooabar", "foobbar", "foodbar", "1.txt", "a.txt", " ", "\t", "!", "~", "ab", "za"],
  },
  {
    name: "escape",
    patterns: ["\\*", "foo\\*", "\\#c", "foo\\ ", "foo\\\\ bar", "a b", "\\!", "\\#hash", "foo\\[bar]", "foo\\?", "\\?", "a\\*b", "foo\\ bar", "\\\\\\", "\\\\\\\\", "a\\\\\\"],
    paths: ["*", "foo*", "foo", "#c", "foo ", "foo", "foo\\ bar", "foo bar", "a b", "!foo", "#hash", "foo[bar]", "foo?bar", "?", "a*b", "ab", "undefined", "\\undefined", "\\\\"],
  },
  {
    name: "anchor",
    patterns: ["foo/", "/foo", "foo/*", "!foo", "!dir/file", "foo/bar", "foo/bar/", "f[o]o"],
    paths: ["foo", "foo/", "foo/a", "foo/a/b", "a/foo", "dir/file", "dir/file/x", "foo/bar", "foo/bar/", "foo/bar/baz", "fo"],
  },
  {
    name: "multiline",
    raw: "*.o\n*.a\n!keep.o\r\n#c\n\n  \nfoo\\\nbar",
    paths: ["a.o", "b.a", "keep.o", "dir/a.o", "bar", "foo"],
  },
  {
    name: "tabs",
    patterns: ["\t", "foo\t", "foo \t"],
    paths: ["\t", "foo\t", "foo", "foo \t", "foo "],
  },
];

const panics = ["", "/", "/foo", ".", "..", "./a", "../a", "./", "../", ".foo", "..a", "foo/bar", "foo//bar"];

function addAll(ig, c) {
  if (c.raw != null) ig.add(c.raw);
  else ig.add(c.patterns);
}
function one(c, p) {
  const ig = ignore();
  addAll(ig, c);
  const out = { path: p };
  try {
    out.ignores = ig.ignores(p);
  } catch (e) {
    out.ignoresError = e.message;
  }
  try {
    const t = ig.test(p);
    out.test = { ignored: !!t.ignored, unignored: !!t.unignored, hasRule: !!t.rule };
    if (t.rule) out.test.pattern = t.pattern ? t.rule.pattern : t.rule.pattern;
    if (t.rule) {
      out.test.pattern = t.rule.pattern;
      out.test.negative = !!t.rule.negative;
    }
  } catch (e) {
    out.testError = e.message;
  }
  return out;
}

const results = cases.map((c) => {
  const ig = ignore();
  addAll(ig, c);
  let filtered;
  let filterError;
  try {
    filtered = ig.filter(c.paths);
  } catch (e) {
    filterError = e.message;
  }
  return {
    name: c.name,
    patterns: c.patterns || null,
    raw: c.raw || null,
    paths: c.paths.map((p) => one(c, p)),
    filtered,
    filterError,
  };
});

const panicResults = panics.map((p) => {
  const ig = ignore();
  ig.add("*");
  try {
    ig.ignores(p);
    return { path: p, ok: true };
  } catch (e) {
    return { path: p, ok: false, message: e.message };
  }
});

fs.writeFileSync(path.join(__dirname, "behavior.json"), JSON.stringify({ results, panicResults }, null, 2));
console.log("cases", results.length, "panics", panicResults.length);
