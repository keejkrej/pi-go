// Package marked is a hand port of the npm package marked 18.0.11
// (node_modules/marked). It lexes Markdown into the same token tree marked's
// Lexer produces: block and inline tokenizers, tokenizer overrides, and
// block/inline TokenizerExtensions. Pi's TUI walks those tokens itself, so
// this package does not render HTML.
//
// Defaults match marked and pi's markdown component: GFM on, breaks off,
// pedantic off. A single trailing newline is folded into the previous token's
// raw text, the same way marked does.
//
// JavaScript string indexes in marked are UTF-16 code units. This port counts
// runes. The two agree for BMP text; emoji and other non-BMP characters can
// diverge where marked mixes code-unit length with code-point length.
package marked
