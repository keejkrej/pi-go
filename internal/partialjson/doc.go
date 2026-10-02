// Package partialjson is a hand port of the npm package partial-json 0.1.7
// (node_modules/partial-json/dist/index.js and options.js): a parser for incomplete JSON
// as produced by streaming LLM output.
//
// Parse works on the UTF-16 code units of the input, exactly like the JavaScript
// original, and delegates every complete fragment to jsonx (JSON.parse semantics), so
// results, quirks and error messages match the npm package. Values use the jsonx value
// model: nil, bool, float64 (including NaN and ±Inf for the partial-json extensions),
// string, []any and *jsonx.Object.
package partialjson
