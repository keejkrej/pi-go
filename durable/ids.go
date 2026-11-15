// Ported from packages/durable/src/ids.ts (pi v1.0.0).

package durable

// IdFromNumber applies an erased ID brand at a trusted numeric allocation or decoding boundary.
func IdFromNumber[I ~int64](value int64) I {
	return I(value)
}

// SeqFromNumber applies the erased commit-sequence brand at a trusted storage boundary.
func SeqFromNumber(value int64) Seq {
	return Seq(value)
}
