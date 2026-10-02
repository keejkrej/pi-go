// Ported from packages/protocol/src/cbor/decoder.ts (pi v1.0.0).

package cbor

// decoCborReader decodes one definite-length CBOR item.
// Field order follows the TS constructor fields.
type decoCborReader struct {
	bytes   []byte
	offset  int
	options ResolvedCborOptions
}

func decoNewCborReader(bytes []byte, options ResolvedCborOptions) *decoCborReader {
	panic("unported: decoNewCborReader")
}

func (r *decoCborReader) Decode() (any, error) {
	panic("unported: decoCborReader.Decode")
}

func (r *decoCborReader) readItem(depth int) (any, error) {
	panic("unported: decoCborReader.readItem")
}

func (r *decoCborReader) readSimple(additionalInformation int) (any, error) {
	panic("unported: decoCborReader.readSimple")
}

func (r *decoCborReader) readLength(additionalInformation int, kind string, limit int) (int, error) {
	panic("unported: decoCborReader.readLength")
}

func (r *decoCborReader) readArgument(additionalInformation int) (int, error) {
	panic("unported: decoCborReader.readArgument")
}

func (r *decoCborReader) readByte() (int, error) {
	panic("unported: decoCborReader.readByte")
}

// readBytes returns the next length bytes. TS returns a subarray view and advances offset by length.
func (r *decoCborReader) readBytes(length int) ([]byte, error) {
	panic("unported: decoCborReader.readBytes")
}

// DecodeCbor decodes exactly one item from the protocol's strict RFC 8949 subset.
// A nil options pointer means the defaults.
// Numbers are float64, including integers and -0. Maps are *jsonx.Object in insertion
// order with string keys. Arrays are []any. Byte strings are []byte. Null is nil.
// A non-empty tail returns CborError "CBOR payload contains trailing data".
func DecodeCbor(bytes []byte, options *CborOptions) (any, error) {
	panic("unported: DecodeCbor")
}
