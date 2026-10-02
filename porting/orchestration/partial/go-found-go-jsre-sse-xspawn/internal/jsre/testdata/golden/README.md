# jsre golden data

Generated with Node v24.21.0 (V8 13.6.233.17-node.53, Unicode 17). The
generator scripts were run from /tmp and deleted afterwards (PORTING.md 15).

- `regexp.json`: written by `gen.mjs`. It compiles hand-written patterns
  (syntax errors, Annex B syntax, named groups, modifiers, case folding,
  lookbehind, surrogates) and about 7000 random token sequences (mulberry32
  PRNG, seed 12345) with `new RegExp(pattern, flags)`, and records the
  SyntaxError message or success (`errors`), `source`/`toString()`
  (`source`), `exec` results with `lastIndex` = `from` (index, end, groups,
  named groups, `d` indices) on random subjects (`exec`),
  `String.prototype.replace` with a `$&|$1|$`|$'|$<n>` template (`replace`),
  and `String.prototype.split` (`split`). Subjects that are not well-formed
  UTF-16 are dropped, since a Go string cannot hold a lone surrogate.
- `caseclasses.json`: written by `caseclasses.mjs`. For every code point
  with a case mapping (and its single-code-point mappings) it matches
  `new RegExp("[<c>]", "gi")` (BMP, code units) and `"giu"` (all code
  points) against a string of all candidates and records the classes with
  more than one member. `canon_table.go` was generated from the same data by
  `gentable.mjs`.
- `pi.json`: written by `pi.mjs`. Regexps that pi builds (tui CJK
  punctuation and autocomplete patterns, the ANSI escape pattern, OKLCH/OKHSL
  color parsing, the MCP `WWW-Authenticate` parameter pattern, paste markers,
  escaped search queries, provider error patterns) with `test`, `matchAll`,
  `replace(re, "<$&>")` and `split` results.
