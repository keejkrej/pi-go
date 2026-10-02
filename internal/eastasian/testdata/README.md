# internal/eastasian test data

`gen.mjs` reads get-east-asian-width@1.6.0 from the pi TS checkout and writes two files:

- `../lookup_data.go`: the range tables of `lookup-data.js`, verbatim.
- `vectors.json`: what Node v24.21.0 returns from `eastAsianWidthType(cp)`, `eastAsianWidth(cp)` and
  `eastAsianWidth(cp, {ambiguousAsWide: true})` for every code point from 0 to U+110010, stored as
  runs `[start, type, width, widthAmbiguousAsWide]`, plus a few out-of-range values in `extra`.

Regenerate from this directory:

```sh
node gen.mjs <path to the pi TS checkout>
gofmt -w ../lookup_data.go
```
