package js

import (
	"strings"
	"unicode/utf8"
)

// TextDecoderOptions mirrors the options of new TextDecoder("utf-8", options).
type TextDecoderOptions struct {
	// Fatal makes malformed input an error instead of U+FFFD.
	Fatal bool
	// IgnoreBOM keeps a leading U+FEFF in the output instead of stripping it.
	IgnoreBOM bool
}

// UTF8StreamDecoder is Node 24's UTF-8 TextDecoder (the "UTF-8 fast path" in
// lib/internal/encoding.js): malformed input becomes U+FFFD per maximal
// invalid subsequence (WHATWG), a sequence cut at a chunk boundary is
// completed by the next streamed chunk, and a leading byte order mark is
// stripped unless IgnoreBOM is set. BOM handling follows Node's
// implementation exactly, including where it deviates from the Encoding spec.
type UTF8StreamDecoder struct {
	fatal     bool
	ignoreBOM bool
	// chunk holds the unfinished tail of the previous streamed input (Node's
	// kChunk); nil when there is none.
	chunk   []byte
	bomSeen bool
}

// NewUTF8StreamDecoder returns new TextDecoder() (UTF-8, non-fatal, BOM
// stripped).
func NewUTF8StreamDecoder() *UTF8StreamDecoder {
	return NewTextDecoder(nil)
}

// NewTextDecoder returns new TextDecoder("utf-8", opts); nil opts means
// defaults.
func NewTextDecoder(opts *TextDecoderOptions) *UTF8StreamDecoder {
	d := &UTF8StreamDecoder{}
	if opts != nil {
		d.fatal = opts.Fatal
		d.ignoreBOM = opts.IgnoreBOM
	}
	return d
}

// errInvalidEncodedData is the TypeError Node throws for a fatal decoder.
func errInvalidEncodedData() *JSError {
	return &JSError{
		ErrName: "TypeError",
		Message: "The encoded data was not valid for encoding utf-8",
		Code:    "ERR_ENCODING_INVALID_ENCODED_DATA",
	}
}

// Decode returns decoder.decode(chunk, {stream: true}). Bytes of an incomplete
// trailing sequence are kept for the next call. For a fatal decoder, use
// DecodeChunk to observe errors; Decode then returns "".
func (d *UTF8StreamDecoder) Decode(chunk []byte) string {
	s, _ := d.DecodeChunk(chunk, true)
	return s
}

// Flush returns decoder.decode(): it ends the stream, turning an incomplete
// trailing sequence into U+FFFD, and resets the decoder for a new stream.
func (d *UTF8StreamDecoder) Flush() string {
	s, _ := d.DecodeChunk(nil, false)
	return s
}

// DecodeFinal returns decoder.decode(chunk): chunk is the last input of the
// stream, and the decoder is reset afterwards.
func (d *UTF8StreamDecoder) DecodeFinal(chunk []byte) string {
	s, _ := d.DecodeChunk(chunk, false)
	return s
}

// DecodeChunk returns decoder.decode(input, {stream}). A fatal decoder returns
// Node's TypeError ("The encoded data was not valid for encoding utf-8", code
// ERR_ENCODING_INVALID_ENCODED_DATA) on malformed input.
func (d *UTF8StreamDecoder) DecodeChunk(input []byte, stream bool) (string, error) {
	chunk := d.chunk
	ignoreBOM := d.ignoreBOM || d.bomSeen
	if !stream {
		d.bomSeen = false
		if chunk == nil {
			return decodeUTF8Binding(input, ignoreBOM, d.fatal)
		}
	}
	u := input
	if len(u) == 0 && stream {
		return "", nil
	}
	var prefix []byte
	if chunk != nil {
		merged := mergePrefixUtf8(u, chunk)
		if len(u) < 3 {
			u = merged
		} else {
			prefix = merged
			if add := len(prefix) - len(chunk); add > 0 {
				u = u[add:]
			}
		}
		d.chunk = nil
	}
	if stream {
		if trail := unfinishedBytesUtf8(u, len(u)); trail > 0 {
			d.chunk = append([]byte(nil), u[len(u)-trail:]...)
			if prefix == nil && trail == len(u) {
				return "", nil
			}
			u = u[:len(u)-trail]
		}
	}
	res := ""
	if prefix != nil {
		s, err := decodeUTF8Binding(prefix, ignoreBOM, d.fatal)
		if err != nil {
			d.chunk = nil
			return "", err
		}
		res = s
	}
	// TS parity: Node passes `ignoreBom || prefix` here; the binding only
	// honours a literal true, so a prefix does not suppress BOM stripping.
	s, err := decodeUTF8Binding(u, ignoreBOM, d.fatal)
	if err != nil {
		d.chunk = nil
		return "", err
	}
	if stream {
		d.bomSeen = true
	}
	return res + s, nil
}

// unfinishedBytesUtf8 is Node's helper of the same name: the number (0-3) of
// trailing bytes of data[:n] that start an incomplete but so far valid
// UTF-8 sequence.
func unfinishedBytesUtf8(data []byte, n int) int {
	pos := 0
	for pos < 2 && pos < n && data[n-pos-1]&0xc0 == 0x80 {
		pos++
	}
	if pos == n {
		return 0
	}
	lead := data[n-pos-1]
	if lead < 0xc2 || lead > 0xf4 {
		return 0
	}
	if pos == 0 {
		return 1
	}
	if lead < 0xe0 || (lead < 0xf0 && pos >= 2) {
		return 0
	}
	lower, upper := byte(0x80), byte(0xbf)
	switch lead {
	case 0xf0:
		lower = 0x90
	case 0xe0:
		lower = 0xa0
	}
	switch lead {
	case 0xf4:
		upper = 0x8f
	case 0xed:
		upper = 0x9f
	}
	next := data[n-pos]
	if next >= lower && next <= upper {
		return pos + 1
	}
	return 0
}

// mergePrefixUtf8 is Node's helper of the same name.
func mergePrefixUtf8(data, chunk []byte) []byte {
	if len(data) == 0 {
		return chunk
	}
	if len(data) < 3 {
		res := make([]byte, 0, len(chunk)+len(data))
		res = append(res, chunk...)
		return append(res, data...)
	}
	temp := make([]byte, len(chunk)+3)
	copy(temp, chunk)
	copy(temp[len(chunk):], data[:3])
	for i := 1; i <= 3; i++ {
		unfinished := unfinishedBytesUtf8(temp, len(chunk)+i)
		if unfinished <= i {
			if add := i - unfinished; add > 0 {
				return temp[:len(chunk)+add]
			}
			return chunk
		}
	}
	return nil
}

// decodeUTF8Binding is Node's encoding binding decodeUTF8(data, ignoreBOM,
// fatal): validate when fatal, strip a leading EF BB BF unless ignoreBOM,
// then decode with WHATWG replacement.
func decodeUTF8Binding(b []byte, ignoreBOM, fatal bool) (string, error) {
	if fatal && !utf8.Valid(b) {
		return "", errInvalidEncodedData()
	}
	if !ignoreBOM && len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	return decodeWHATWG(b), nil
}

// decodeWHATWG decodes b as UTF-8, replacing each maximal invalid subsequence
// (WHATWG Encoding "UTF-8 decoder") with U+FFFD.
func decodeWHATWG(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b) + 8)
	needed, seen := 0, 0
	lower, upper := byte(0x80), byte(0xBF)
	start := 0
	for i := 0; i < len(b); {
		c := b[i]
		if needed == 0 {
			switch {
			case c < 0x80:
				out.WriteByte(c)
			case c >= 0xC2 && c <= 0xDF:
				needed = 1
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				}
				if c == 0xED {
					upper = 0x9F
				}
				needed = 2
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				}
				if c == 0xF4 {
					upper = 0x8F
				}
				needed = 3
			default:
				out.WriteRune(utf8.RuneError)
			}
			start = i
			seen = 0
			i++
			continue
		}
		if c < lower || c > upper {
			needed, seen = 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(utf8.RuneError)
			continue // reprocess c
		}
		lower, upper = 0x80, 0xBF
		seen++
		i++
		if seen == needed {
			out.Write(b[start:i])
			needed, seen = 0, 0
		}
	}
	if needed != 0 {
		out.WriteRune(utf8.RuneError)
	}
	return out.String()
}

// DecodeUTF8 returns new TextDecoder().decode(b): WHATWG UTF-8 decoding with
// U+FFFD replacement and a leading BOM stripped.
func DecodeUTF8(b []byte) string {
	s, _ := decodeUTF8Binding(b, false, false)
	return s
}
