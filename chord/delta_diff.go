// Ported from packages/chord/src/delta/diff.ts (pi v1.0.0).

package chord

import (
	"slices"
	"strconv"
	"unsafe"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	ddDefaultOverlapScan    = 65_536
	ddMaxDeltaOperations    = 4_096
	ddMaxIdentityCandidates = 200_000
	ddMaxSemanticCells      = 65_536
)

type ddEmitter struct {
	ops        OpList
	overflowed bool
}

func (e *ddEmitter) emit(op Op) {
	if e.overflowed {
		return
	}
	if len(e.ops) >= ddMaxDeltaOperations {
		e.overflowed = true
		return
	}
	e.ops = append(e.ops, op)
}

func ddClonePath(path Path, extra ...Seg) Path {
	out := make(Path, len(path)+len(extra))
	copy(out, path)
	copy(out[len(path):], extra)
	return out
}

func ddEmitSet(path Path, value JsonValue, emitter *ddEmitter) {
	if len(path) == 0 {
		emitter.emit(OpReplace{Value: value})
	} else {
		emitter.emit(OpSet{Path: ddClonePath(path), Value: value})
	}
}

func ddIsContainer(value JsonValue) bool {
	switch c := value.(type) {
	case *jsonx.Object:
		return c != nil
	case []any:
		return c != nil
	default:
		return false
	}
}

type ddKeyKind uint8

const (
	ddKeyNil ddKeyKind = iota
	ddKeyBool
	ddKeyNum
	ddKeyStr
	ddKeyObj
	ddKeySlice
	ddKeyOther
)

type ddIdentityKey struct {
	kind  ddKeyKind
	b     bool
	num   float64
	str   string
	obj   *jsonx.Object
	sPtr  *any
	sLen  int
	other any
}

func ddMakeIdentityKey(v JsonValue) ddIdentityKey {
	if v == nil {
		return ddIdentityKey{kind: ddKeyNil}
	}
	switch x := v.(type) {
	case bool:
		return ddIdentityKey{kind: ddKeyBool, b: x}
	case float64:
		if x == 0 {
			x = 0 // normalize -0 to +0 like JS SameValueZero / ===
		}
		return ddIdentityKey{kind: ddKeyNum, num: x}
	case string:
		return ddIdentityKey{kind: ddKeyStr, str: x}
	case *jsonx.Object:
		return ddIdentityKey{kind: ddKeyObj, obj: x}
	case []any:
		return ddIdentityKey{kind: ddKeySlice, sPtr: unsafe.SliceData(x), sLen: len(x)}
	default:
		return ddIdentityKey{kind: ddKeyOther, other: x}
	}
}

func ddRefEqual(left, right JsonValue) bool {
	return ddMakeIdentityKey(left) == ddMakeIdentityKey(right)
}

func ddEqualJson(left, right JsonValue) bool {
	if ddRefEqual(left, right) {
		return true
	}
	if !ddIsContainer(left) || !ddIsContainer(right) {
		return false
	}
	leftArr, leftIsArr := left.([]any)
	rightArr, rightIsArr := right.([]any)
	if leftIsArr != rightIsArr {
		return false
	}
	if leftIsArr {
		if len(leftArr) != len(rightArr) {
			return false
		}
		for i := range leftArr {
			if !ddEqualJson(leftArr[i], rightArr[i]) {
				return false
			}
		}
		return true
	}
	leftObj := left.(*jsonx.Object)
	rightObj := right.(*jsonx.Object)
	if leftObj.Len() != rightObj.Len() {
		return false
	}
	for k, lv := range leftObj.All() {
		rv, ok := rightObj.Get(k)
		if !ok || !ddEqualJson(lv, rv) {
			return false
		}
	}
	return true
}

func ddSameValue(left, right JsonValue) bool {
	return ddRefEqual(left, right) || ddEqualJson(left, right)
}

func ddPermutation(before, after []any) []int {
	if len(before) != len(after) {
		return nil
	}
	type bucket struct {
		indices []int
		used    int
	}
	positions := make(map[ddIdentityKey]*bucket, len(before))
	for i, val := range before {
		key := ddMakeIdentityKey(val)
		entry := positions[key]
		if entry == nil {
			positions[key] = &bucket{indices: []int{i}}
		} else {
			entry.indices = append(entry.indices, i)
		}
	}
	result := make([]int, len(after))
	for i, val := range after {
		entry := positions[ddMakeIdentityKey(val)]
		if entry == nil || entry.used == len(entry.indices) {
			return nil
		}
		result[i] = entry.indices[entry.used]
		entry.used++
	}
	return result
}

func ddEmitString(before, after string, path NonEmptyPath, emitter *ddEmitter) {
	if before == after {
		return
	}
	beforeLen := js.Len(before)
	afterLen := js.Len(after)
	if afterLen > beforeLen && js.Slice(after, 0, beforeLen) == before {
		emitter.emit(OpAppend{Path: ddClonePath(path), Text: js.SliceFrom(after, beforeLen)})
		return
	}
	shared := Overlap(before, after, ddDefaultOverlapScan)
	if shared == 0 {
		emitter.emit(OpSet{Path: ddClonePath(path), Value: after})
		return
	}
	p := ddClonePath(path)
	emitter.emit(OpTruncate{Path: p, Count: beforeLen - shared})
	if afterLen > shared {
		emitter.emit(OpAppend{Path: p, Text: js.SliceFrom(after, shared)})
	}
}

type ddArrayMatch struct {
	before int
	after  int
}

type ddMatchCandidate struct {
	before   int
	after    int
	previous int
}

func ddLcsMatches(before, after []any, equal func(left, right JsonValue) bool, maxCells int) ([]ddArrayMatch, bool) {
	if len(before) == 0 || len(after) == 0 {
		return []ddArrayMatch{}, true
	}
	if len(before)*len(after) > maxCells {
		return nil, false
	}
	width := len(after) + 1
	lengths := make([]uint32, (len(before)+1)*width)
	for left := len(before) - 1; left >= 0; left-- {
		for right := len(after) - 1; right >= 0; right-- {
			at := left*width + right
			if equal(before[left], after[right]) {
				lengths[at] = lengths[(left+1)*width+right+1] + 1
			} else {
				down := lengths[(left+1)*width+right]
				across := lengths[left*width+right+1]
				if down >= across {
					lengths[at] = down
				} else {
					lengths[at] = across
				}
			}
		}
	}
	var matches []ddArrayMatch
	left, right := 0, 0
	for left < len(before) && right < len(after) {
		if equal(before[left], after[right]) && lengths[left*width+right] == lengths[(left+1)*width+right+1]+1 {
			matches = append(matches, ddArrayMatch{before: left, after: right})
			left++
			right++
		} else if lengths[(left+1)*width+right] >= lengths[left*width+right+1] {
			left++
		} else {
			right++
		}
	}
	return matches, true
}

func ddSemanticallyAligned(left, right JsonValue) bool {
	if ddSameValue(left, right) {
		return true
	}
	if !ddIsContainer(left) || !ddIsContainer(right) {
		return false
	}
	leftArr, leftIsArr := left.([]any)
	rightArr, rightIsArr := right.([]any)
	if leftIsArr != rightIsArr {
		return false
	}
	if leftIsArr {
		if len(leftArr) != len(rightArr) {
			return false
		}
		for i := range leftArr {
			if ddIsContainer(leftArr[i]) && ddRefEqual(leftArr[i], rightArr[i]) {
				return true
			}
		}
		return false
	}
	leftObj := left.(*jsonx.Object)
	rightObj := right.(*jsonx.Object)
	for key, val := range leftObj.All() {
		if ddIsContainer(val) {
			if rv, ok := rightObj.Get(key); ok && ddRefEqual(val, rv) {
				return true
			}
		}
	}
	return false
}

func ddLowerBound(values []int, value int) int {
	low, high := 0, len(values)
	for low < high {
		mid := int(uint(low+high) >> 1)
		if values[mid] < value {
			low = mid + 1
		} else {
			high = mid
		}
	}
	return low
}

func ddIdentitySubsequence(before []any, beforeStart, beforeEnd int, after []any, afterStart, afterEnd int) ([]ddArrayMatch, bool) {
	beforeCount := beforeEnd - beforeStart
	afterCount := afterEnd - afterStart
	if afterCount < beforeCount {
		matches := make([]ddArrayMatch, 0, afterCount)
		beforeIndex := beforeStart
		for afterIndex := afterStart; afterIndex < afterEnd; afterIndex++ {
			for beforeIndex < beforeEnd && !ddRefEqual(before[beforeIndex], after[afterIndex]) {
				beforeIndex++
			}
			if beforeIndex == beforeEnd {
				return nil, false
			}
			matches = append(matches, ddArrayMatch{before: beforeIndex, after: afterIndex})
			beforeIndex++
		}
		return matches, true
	}
	if beforeCount < afterCount {
		matches := make([]ddArrayMatch, 0, beforeCount)
		afterIndex := afterStart
		for beforeIndex := beforeStart; beforeIndex < beforeEnd; beforeIndex++ {
			for afterIndex < afterEnd && !ddRefEqual(before[beforeIndex], after[afterIndex]) {
				afterIndex++
			}
			if afterIndex == afterEnd {
				return nil, false
			}
			matches = append(matches, ddArrayMatch{before: beforeIndex, after: afterIndex})
			afterIndex++
		}
		return matches, true
	}
	return nil, false
}

func ddGreedyIdentityAnchors(positions map[ddIdentityKey][]int, after []any, afterStart, afterEnd int) []ddArrayMatch {
	var matches []ddArrayMatch
	previous := -1
	for afterIndex := afterStart; afterIndex < afterEnd; afterIndex++ {
		candidates := positions[ddMakeIdentityKey(after[afterIndex])]
		if len(candidates) == 0 {
			continue
		}
		at := ddLowerBound(candidates, previous+1)
		if at >= len(candidates) {
			continue
		}
		beforeIndex := candidates[at]
		matches = append(matches, ddArrayMatch{before: beforeIndex, after: afterIndex})
		previous = beforeIndex
	}
	return matches
}

func ddIdentityAnchors(before []any, beforeStart, beforeEnd int, after []any, afterStart, afterEnd int) []ddArrayMatch {
	positions := make(map[ddIdentityKey][]int)
	for i := beforeStart; i < beforeEnd; i++ {
		key := ddMakeIdentityKey(before[i])
		positions[key] = append(positions[key], i)
	}
	candidateCount := 0
	for i := afterStart; i < afterEnd; i++ {
		candidateCount += len(positions[ddMakeIdentityKey(after[i])])
		if candidateCount > ddMaxIdentityCandidates {
			return ddGreedyIdentityAnchors(positions, after, afterStart, afterEnd)
		}
	}
	if candidateCount == 0 {
		return nil
	}

	var candidates []ddMatchCandidate
	var tails []int
	var tailValues []int
	for afterIndex := afterStart; afterIndex < afterEnd; afterIndex++ {
		beforePositions := positions[ddMakeIdentityKey(after[afterIndex])]
		if len(beforePositions) == 0 {
			continue
		}
		for i := len(beforePositions) - 1; i >= 0; i-- {
			beforeIndex := beforePositions[i]
			at := ddLowerBound(tailValues, beforeIndex)
			candidateIndex := len(candidates)
			prev := -1
			if at > 0 {
				prev = tails[at-1]
			}
			candidates = append(candidates, ddMatchCandidate{before: beforeIndex, after: afterIndex, previous: prev})
			if at == len(tails) {
				tails = append(tails, candidateIndex)
				tailValues = append(tailValues, beforeIndex)
			} else {
				tails[at] = candidateIndex
				tailValues[at] = beforeIndex
			}
		}
	}
	var matches []ddArrayMatch
	candidateIndex := -1
	if len(tails) > 0 {
		candidateIndex = tails[len(tails)-1]
	}
	for candidateIndex >= 0 {
		c := candidates[candidateIndex]
		matches = append(matches, ddArrayMatch{before: c.before, after: c.after})
		candidateIndex = c.previous
	}
	slices.Reverse(matches)
	return matches
}

func ddProcessArrayMatches(
	before []any,
	after []any,
	path Path,
	emitter *ddEmitter,
	beforeStart int,
	beforeEnd int,
	afterStart int,
	afterEnd int,
	outputStart int,
	matches []ddArrayMatch,
) {
	beforeAt := beforeStart
	afterAt := afterStart
	outputAt := outputStart
	for _, m := range matches {
		if emitter.overflowed {
			return
		}
		ddDiffArrayRegion(before, after, path, emitter, beforeAt, m.before, afterAt, m.after, outputAt)
		outputAt += m.after - afterAt
		if !ddSameValue(before[m.before], after[m.after]) {
			ddDiffValue(before[m.before], after[m.after], ddClonePath(path, SegNumber(outputAt)), emitter)
		}
		outputAt++
		beforeAt = m.before + 1
		afterAt = m.after + 1
	}
	if !emitter.overflowed {
		ddDiffArrayRegion(before, after, path, emitter, beforeAt, beforeEnd, afterAt, afterEnd, outputAt)
	}
}

func ddDiffArrayRegion(
	before []any,
	after []any,
	path Path,
	emitter *ddEmitter,
	beforeStart int,
	beforeEnd int,
	afterStart int,
	afterEnd int,
	outputStart int,
) {
	if emitter.overflowed {
		return
	}
	for beforeStart < beforeEnd && afterStart < afterEnd && ddSameValue(before[beforeStart], after[afterStart]) {
		beforeStart++
		afterStart++
		outputStart++
	}
	for beforeStart < beforeEnd && afterStart < afterEnd && ddSameValue(before[beforeEnd-1], after[afterEnd-1]) {
		beforeEnd--
		afterEnd--
	}
	beforeCount := beforeEnd - beforeStart
	afterCount := afterEnd - afterStart
	if beforeCount == 0 && afterCount == 0 {
		return
	}
	if beforeCount == 0 || afterCount == 0 {
		items := make([]JsonValue, afterCount)
		copy(items, after[afterStart:afterEnd])
		emitter.emit(OpSplice{
			Path:   ddClonePath(path),
			Index:  outputStart,
			Remove: beforeCount,
			Items:  items,
		})
		return
	}

	if beforeCount == afterCount {
		var positional []ddArrayMatch
		for offset := 0; offset < beforeCount; offset++ {
			if ddSameValue(before[beforeStart+offset], after[afterStart+offset]) {
				positional = append(positional, ddArrayMatch{before: beforeStart + offset, after: afterStart + offset})
			}
		}
		if len(positional) > 0 {
			ddProcessArrayMatches(before, after, path, emitter, beforeStart, beforeEnd, afterStart, afterEnd, outputStart, positional)
			return
		}
	}

	if subsequence, ok := ddIdentitySubsequence(before, beforeStart, beforeEnd, after, afterStart, afterEnd); ok && len(subsequence) > 0 {
		ddProcessArrayMatches(before, after, path, emitter, beforeStart, beforeEnd, afterStart, afterEnd, outputStart, subsequence)
		return
	}

	if identity := ddIdentityAnchors(before, beforeStart, beforeEnd, after, afterStart, afterEnd); len(identity) > 0 {
		ddProcessArrayMatches(before, after, path, emitter, beforeStart, beforeEnd, afterStart, afterEnd, outputStart, identity)
		return
	}

	if semantic, ok := ddLcsMatches(before[beforeStart:beforeEnd], after[afterStart:afterEnd], ddSemanticallyAligned, ddMaxSemanticCells); ok && len(semantic) > 0 {
		absolute := make([]ddArrayMatch, len(semantic))
		for i, m := range semantic {
			absolute[i] = ddArrayMatch{before: beforeStart + m.before, after: afterStart + m.after}
		}
		ddProcessArrayMatches(before, after, path, emitter, beforeStart, beforeEnd, afterStart, afterEnd, outputStart, absolute)
		return
	}

	if beforeCount == 1 && afterCount == 1 {
		ddDiffValue(before[beforeStart], after[afterStart], ddClonePath(path, SegNumber(outputStart)), emitter)
		return
	}
	items := make([]JsonValue, afterCount)
	copy(items, after[afterStart:afterEnd])
	emitter.emit(OpSplice{
		Path:   ddClonePath(path),
		Index:  outputStart,
		Remove: beforeCount,
		Items:  items,
	})
}

func ddDiffArray(before, after []any, path Path, emitter *ddEmitter) {
	if ddRefEqual(before, after) || ddEqualJson(before, after) {
		return
	}
	if len(before) == len(after) &&
		len(before) > 1 &&
		!ddSameValue(before[0], after[0]) &&
		!ddSameValue(before[len(before)-1], after[len(after)-1]) {
		if order := ddPermutation(before, after); order != nil {
			emitter.emit(OpMove{Path: ddClonePath(path), Permutation: order})
			return
		}
	}
	ddDiffArrayRegion(before, after, path, emitter, 0, len(before), 0, len(after), 0)
}

func ddDiffObject(before, after *jsonx.Object, path Path, emitter *ddEmitter) {
	beforeKeys := before.Keys()
	afterKeys := after.Keys()
	for _, k := range beforeKeys {
		if _, reserved := ReservedSegments[k]; reserved {
			if !ddEqualJson(before, after) {
				ddEmitSet(path, after, emitter)
			}
			return
		}
	}
	for _, k := range afterKeys {
		if _, reserved := ReservedSegments[k]; reserved {
			if !ddEqualJson(before, after) {
				ddEmitSet(path, after, emitter)
			}
			return
		}
	}
	for _, key := range afterKeys {
		if emitter.overflowed {
			return
		}
		afterVal, _ := after.Get(key)
		if beforeVal, ok := before.Get(key); ok {
			ddDiffValue(beforeVal, afterVal, ddClonePath(path, SegString(key)), emitter)
		} else {
			ddEmitSet(ddClonePath(path, SegString(key)), afterVal, emitter)
		}
	}
	for _, key := range beforeKeys {
		if emitter.overflowed {
			return
		}
		if !after.Has(key) {
			emitter.emit(OpDelete{Path: ddClonePath(path, SegString(key))})
		}
	}
}

func ddDiffValue(before, after JsonValue, path Path, emitter *ddEmitter) {
	if ddRefEqual(before, after) || emitter.overflowed {
		return
	}
	if bs, okB := before.(string); okB {
		if as, okA := after.(string); okA && len(path) > 0 {
			ddEmitString(bs, as, path, emitter)
			return
		}
	}
	if ba, okB := before.([]any); okB && ba != nil {
		if aa, okA := after.([]any); okA && aa != nil {
			ddDiffArray(ba, aa, path, emitter)
			return
		}
	}
	if bo, okB := before.(*jsonx.Object); okB && bo != nil {
		if ao, okA := after.(*jsonx.Object); okA && ao != nil {
			ddDiffObject(bo, ao, path, emitter)
			return
		}
	}
	ddEmitSet(path, after, emitter)
}

func ddJsonCost(value JsonValue) int {
	if value == nil {
		return 4
	}
	switch v := value.(type) {
	case string:
		return js.Len(v) + 2
	case float64:
		return len(js.NumberToString(v))
	case bool:
		if v {
			return 4
		}
		return 5
	case []any:
		cost := 2
		for i, elem := range v {
			if i > 0 {
				cost++
			}
			cost += ddJsonCost(elem)
		}
		return cost
	case *jsonx.Object:
		if v == nil {
			return 4
		}
		cost := 2
		index := 0
		for key, val := range v.All() {
			if index > 0 {
				cost++
			}
			index++
			cost += js.Len(key) + 3 + ddJsonCost(val)
		}
		return cost
	default:
		return 4
	}
}

func ddPathCost(path Path) int {
	cost := 2
	for i, seg := range path {
		if i > 0 {
			cost++
		}
		switch s := seg.(type) {
		case SegString:
			cost += js.Len(string(s)) + 2
		case SegNumber:
			cost += len(strconv.Itoa(int(s)))
		}
	}
	return cost
}

func ddOperationCost(op Op) int {
	switch o := op.(type) {
	case OpReplace:
		return 6 + ddJsonCost(o.Value)
	case OpSet:
		return 7 + ddPathCost(o.Path) + ddJsonCost(o.Value)
	case OpDelete:
		return 6 + ddPathCost(o.Path)
	case OpAppend:
		return 7 + ddPathCost(o.Path) + js.Len(o.Text) + 2
	case OpTruncate:
		return 7 + ddPathCost(o.Path) + len(strconv.Itoa(o.Count))
	case OpSplice:
		itemsCost := 2
		for i, item := range o.Items {
			if i > 0 {
				itemsCost++
			}
			itemsCost += ddJsonCost(item)
		}
		return 10 + ddPathCost(o.Path) + len(strconv.Itoa(o.Index)) + len(strconv.Itoa(o.Remove)) + itemsCost
	case OpMove:
		permCost := 2
		for i, p := range o.Permutation {
			if i > 0 {
				permCost++
			}
			permCost += len(strconv.Itoa(p))
		}
		return 7 + ddPathCost(o.Path) + permCost
	default:
		return 0
	}
}

// DiffRevisions computes a compact operation batch from two immutable JSON revisions.
func DiffRevisions(before JsonValue, after JsonValue) OpList {
	emitter := &ddEmitter{ops: make(OpList, 0)}
	ddDiffValue(before, after, nil, emitter)
	if emitter.overflowed {
		return OpList{OpReplace{Value: after}}
	}
	if len(emitter.ops) == 0 || IsBase(emitter.ops) {
		return emitter.ops
	}
	deltaCost := 2
	for _, op := range emitter.ops {
		deltaCost += ddOperationCost(op) + 1
	}
	if deltaCost < 65_536 {
		return emitter.ops
	}
	snapshotCost := ddJsonCost(after) + 6
	if deltaCost >= snapshotCost {
		return OpList{OpReplace{Value: after}}
	}
	return emitter.ops
}
