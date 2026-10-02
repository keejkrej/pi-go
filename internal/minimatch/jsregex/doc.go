// Package jsregex is a small backtracking implementation of JavaScript (V8)
// regular expressions, shared by the hand ports of minimatch, ignore and
// semver. Those libraries build regular expression sources at run time and
// rely on JS-only semantics (lookahead, lazy quantifiers, UTF-16 code unit
// matching, JS case folding, Annex B syntax), so stdlib regexp cannot run
// them unchanged.
//
// Supported: alternation, groups (capturing and (?:...)), lookahead (?= and
// (?!, greedy and lazy quantifiers including {n,m}, character classes with
// ranges and class escapes, \d \w \s and their negations, \b \B, ^ $, back
// references, \p{...} general categories with the u flag, and the flags
// g i m s u y. Not supported (compile error): lookbehind, named groups, the
// v flag.
//
// String model: JS strings are UTF-16. Go strings passed in are decoded as
// WTF-8 (UTF-8 that may also hold lone surrogates as 3-byte sequences; any
// other invalid byte reads as U+FFFD). ToUTF16 and FromUTF16 convert between
// the two, so code that needs to split surrogate pairs (as JS code indexing a
// string by code unit does) can carry the halves in Go strings and the
// halves still match as the code units they are. All indices are UTF-16 code
// unit indices.
//
// Without the u flag a pattern and its input are sequences of code units;
// with it, code points.
package jsregex
