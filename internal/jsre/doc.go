// Package jsre implements JavaScript (ECMAScript 2025, V8 13.6) regular
// expressions on top of github.com/dlclark/regexp2/v2.
//
// Patterns are parsed by a port of V8's regexp parser, so the accepted
// syntax (including Annex B web-compatibility syntax, named groups,
// duplicate named groups in different alternatives, and the (?ims-ims:...)
// modifiers) and the SyntaxError messages match Node. The parsed pattern is
// translated into an equivalent regexp2 program that spells out the JS
// semantics: UTF-16 code unit matching without /u and code point matching
// with /u, JS case folding for /i, JS \s \w \b . ^ $, back references to
// non-participating groups matching empty, capture reset on each quantifier
// iteration, and the empty-iteration check of quantifiers.
//
// Strings are Go strings; all indices (Exec's from, Match.Index, End,
// GroupIndex) are UTF-16 code unit indices, as in JS. A Go string is treated
// as the JS string obtained by decoding it as UTF-8 with each invalid byte
// becoming U+FFFD. A match boundary inside a surrogate pair (possible
// without /u) yields U+FFFD for the lone half in returned substrings.
//
// The regexp2 program is built on first use, so Compile only parses and
// validates the pattern.
//
// Known differences from V8:
//   - The v flag (unicodeSets) is rejected with ErrUnicodeSets.
//   - Case-insensitive back references compare with regexp2's simple
//     lowercase mapping rather than JS canonicalization.
//   - Unicode property escapes (\p, \P) use the Go toolchain's Unicode
//     tables, which are older than V8's Unicode 17.
//   - There is no lastIndex state; see Regexp.
package jsre
