const ignore = require("C:/Users/ctyja/workspace/pi/node_modules/ignore");
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
