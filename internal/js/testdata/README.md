# internal/js test vectors

Reference outputs recorded from Node v24.21.0 (ICU 78.3) and consumed by
`vectors_test.go`. Regenerate from this directory with:

```sh
TZ=America/New_York node gen_vectors.mjs .
TZ=America/New_York node gen_decoder_stream.mjs .
```

| File | Generator | Contents |
| --- | --- | --- |
| strings.json | gen_vectors.mjs | slice/substring/charCodeAt/codePointAt/charAt, indexOf/lastIndexOf, split, padStart/padEnd, toUpperCase/toLowerCase, normalize, trim |
| collation.json | gen_vectors.mjs | localeCompare pairs and default (UTF-16) sort order |
| numbers.json | gen_vectors.mjs | Number toString (all radixes), toFixed, toPrecision, Math.round, toLocaleString("en-US"), parseInt, parseFloat, Number(string) |
| uri.json | gen_vectors.mjs | encodeURI(Component), decodeURI(Component) including URIError cases |
| decoder.json | gen_vectors.mjs | TextDecoder decode with fatal/ignoreBOM, decoder reuse |
| dates.json | gen_vectors.mjs | Date.parse, toISOString, toLocaleString("en-US") in the recorded TZ |
| decoder_stream.json | gen_decoder_stream.mjs | randomized streamed TextDecoder chunks, String.fromCodePoint |

Strings that may contain lone surrogates are passed through
`String.prototype.toWellFormed()` before recording, matching the Go model where
a lone surrogate is U+FFFD. Floats are recorded as IEEE 754 bit patterns (hex)
where exactness matters. Date vectors depend on the TZ, which is recorded in
dates.json and applied by the test.
