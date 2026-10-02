// Package jsdiff is a hand port of the npm package diff (jsdiff) 8.0.4
// (node_modules/diff/libesm): the Myers O(ND) diff with the character, word, line,
// sentence, CSS, JSON and array tokenizers, unified patch creation (structuredPatch,
// formatPatch, createTwoFilesPatch, createPatch), parsing, applying and reversing, and
// the DMP/XML converters. Output is identical to the JavaScript library: the same change
// objects, the same hunks and the same patch text. testdata/vectors.json pins this
// against the JavaScript library (see testdata/gen.cjs).
//
// JavaScript string semantics are kept where they are observable: tokenizers split on
// code points, `\s` and trim use the ECMAScript whitespace set, toLowerCase uses full
// Unicode case mapping, and lengths compared by the library are UTF-16 lengths.
//
// Differences forced by Go:
//   - The callback (async) mode is not ported; call the functions from a goroutine.
//   - "undefined" results (maxEditLength or timeout exceeded) are nil slices, nil
//     patches, or "" for patch text (a real patch always ends with "\n").
//   - Thrown errors that signal bad input (malformed patches, an invalid fuzz factor,
//     values JSON.stringify rejects) are returned as errors. API misuse that TS also
//     throws on panics with the TS message: NewlineIsToken with a patch-creation
//     function, an IntlSegmenter whose granularity is not "word", and the internal
//     "this is a bug" assertions.
//   - TS overloads are separate functions: FormatPatch/FormatPatchList,
//     ReversePatch/ReversePatchList, ApplyPatch/ApplyPatchStructured/ApplyPatchList,
//     ApplyPatches/ApplyPatchesList.
//   - TS structuredPatch is NewStructuredPatch (the type keeps the name StructuredPatch).
//   - ParsePatch reads a missing file name as "" (TS: undefined) and a hunk header
//     without line ranges as start 0 (TS: NaN).
//   - DiffJson has no undefinedReplacement option (the jsonx value model has no
//     undefined); where TS canonicalize stores undefined, Canonicalize leaves the
//     object property out or stores nil in the array.
package jsdiff
