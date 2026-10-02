// Package js reproduces JavaScript (V8/Node 24) semantics that pi's TS code
// relies on (PORTING.md sections 6.1 and 6.2): UTF-16 string indices and
// lengths, Unicode string operations, Number formatting and parsing, Date
// formatting and parsing, process.platform/arch values, URI encoding, error
// stringification, TextDecoder, and an abortable sleep.
//
// String model: Go strings hold UTF-8. Every helper measures and indexes them
// in UTF-16 code units, as JS does: a code point above U+FFFF counts as two
// units and every other code point as one. Bytes that are not valid UTF-8
// count as one unit each and read as U+FFFD (what Node produces when it
// decodes such bytes). A slice boundary that falls between the two halves of
// a surrogate pair yields U+FFFD for the lone half, which is what Node writes
// when such a string is encoded as UTF-8.
package js
