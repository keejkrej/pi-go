// Ported from packages/protocol/src/cbor/encoder.ts (pi v1.0.0).

package cbor

// encoAncestors is the encode-time cycle set (TS Set<object>).
type encoAncestors struct{}

// encoCborWriter is a growing definite-length CBOR buffer.
type encoCborWriter struct {
	buffer        []byte
	offset        int
	maxByteLength int
}

func encoNewCborWriter(maxByteLength int) *encoCborWriter {
	panic("unported: encoNewCborWriter")
}

func (w *encoCborWriter) WriteByte(value int) error {
	panic("unported: encoCborWriter.WriteByte")
}

func (w *encoCborWriter) WriteBytes(b []byte) error {
	panic("unported: encoCborWriter.WriteBytes")
}

func (w *encoCborWriter) WriteUint16(value int) error {
	panic("unported: encoCborWriter.WriteUint16")
}

func (w *encoCborWriter) WriteUint32(value int) error {
	panic("unported: encoCborWriter.WriteUint32")
}

func (w *encoCborWriter) WriteUint64(value int) error {
	panic("unported: encoCborWriter.WriteUint64")
}

func (w *encoCborWriter) WriteFloat64(value float64) error {
	panic("unported: encoCborWriter.WriteFloat64")
}

func (w *encoCborWriter) Finish() []byte {
	panic("unported: encoCborWriter.Finish")
}

func (w *encoCborWriter) ensureCapacity(additionalBytes int) error {
	panic("unported: encoCborWriter.ensureCapacity")
}

func encoWriteArgument(writer *encoCborWriter, majorType int, value int) error {
	panic("unported: encoWriteArgument")
}

func encoIsPlainObject(value any) bool {
	panic("unported: encoIsPlainObject")
}

func encoEncodeText(writer *encoCborWriter, value string, options ResolvedCborOptions) error {
	panic("unported: encoEncodeText")
}

func encoEncodeValue(writer *encoCborWriter, value any, options ResolvedCborOptions, depth int, ancestors *encoAncestors) error {
	panic("unported: encoEncodeValue")
}

// EncodeCbor encodes the protocol's strict, definite-length RFC 8949 subset.
// A nil options pointer means the defaults.
// Accepted values are nil, bool, float64 (finite; integers are float64, and -0 is preserved),
// string, []byte, []any, and *jsonx.Object. Other types return CborError
// "Unsupported CBOR value type: <typeof>".
func EncodeCbor(value any, options *CborOptions) ([]byte, error) {
	panic("unported: EncodeCbor")
}
