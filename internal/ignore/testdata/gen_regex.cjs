const fs = require("fs");
const path = require("path");

function loadIgnore() {
  const candidates = [
    process.env.IGNORE_PKG_PATH,
    process.env.NODE_MODULES && path.join(process.env.NODE_MODULES, "ignore"),
    process.env.PI_REPO_DIR && path.join(process.env.PI_REPO_DIR, "node_modules/ignore"),
    process.env.PI_DIR && path.join(process.env.PI_DIR, "node_modules/ignore"),
    "ignore",
  ].filter(Boolean);
  for (const c of candidates) {
    try {
      return require(c);
    } catch {}
  }
  throw new Error("unable to locate npm ignore package; set IGNORE_PKG_PATH, NODE_MODULES, or PI_REPO_DIR");
}

const ignore = loadIgnore();

const patterns = [
  "*", "**", "***", "****", "a***", "***b", "a***b", "**/foo", "**/*", "**/*.js",
  "**/**", "foo/**", "a/**/b", "a/**/", "a/**/**/b", "foo/**/bar", "foo/**/bar/",
  "*.log", "*foo", "foo/", "/foo", "foo/*", "?.log", "foo*bar*baz*", "*x*y*z",
  "[a-c]", "[[:alpha:]]", "[c-a]", "[]]", "[]", "[[:digit:]].txt", "\\*", "foo\\*",
  "\\#c", "foo\\ ", "foo\\\\ bar", "a b", "!foo", "!dir/file", "foo*", "a/***/b",
  "\\\\\\", "\\\\\\\\", "a\\\\\\", "foo\\\\ ", "foo\\ ", "# comment", "   ", "",
  "\\", "a\\", "[[:blank:]]", "[[:space:]]", "[[:cntrl:]]", "[[:graph:]]",
  "[[:print:]]", "[[:punct:]]", "[[:lower:]]", "[[:upper:]]", "[[:alnum:]]",
  "[[:xdigit:]]", "foo/***", "a/***/", "**/**/b", "dir/**", "/foo/bar", "foo/bar",
  "foo/bar/", "*[a-c]*", "a?b", "foo bar", "foo\\ bar", "\\!", "\\#hash",
  "!important", "foo\\[bar]", "[/]", "[.-0]", "[a-c-e]", "[[:digit:][:alpha:]]",
  "foo\\?", "\\?", "a\\*b", "***foo***", "a/**/**/", "foo/**/bar/**/baz",
  " ", "\t", "foo\t", "foo \t", "\\ ", "a b c", "foo\\*bar",
  "[!a]", "[^a]", "[[]", "[a-]", "[-a]", "foo[abc]bar", "a/**/b/**",
  "**/", "/**", "/*", "f[o]o", "foo/*/bar", "a/***/c/d",
  "foo\\ ", "a\\\\\\b", "foo\\\\bar", "*x*", "a*b*c*", "****b",
];

const ig = ignore();
ig.add(patterns.join("\n"));
const rules = [];
for (const r of ig._rules._rules) {
  rules.push({ pattern: r.pattern, source: r.regex.source, negative: !!r.negative });
}
const out = path.join(__dirname, "regex.json");
fs.writeFileSync(out, JSON.stringify({ rules }, null, 2));
console.log("rules", rules.length);
