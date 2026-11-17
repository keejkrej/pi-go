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
const ig = ignore();
ig.add("*");
ig.add("foo/bar");
const paths = ["C:/foo", "c:/foo", "foo\\bar", "\\\\?\\C:\\foo", "foo\"bar", "foo/bar", "foo\\bar\\baz"];
for (const p of paths) {
  try {
    console.log(JSON.stringify(p), ig.ignores(p));
  } catch (e) {
    console.log(JSON.stringify(p), "ERR", e.message);
  }
}
