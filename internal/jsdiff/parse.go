package jsdiff

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const patchUnderline = "==================================================================="

// hasPrefixThenSpace reports whether line starts with prefix followed by a \s character.
func hasPrefixThenSpace(line, prefix string) bool {
	if !strings.HasPrefix(line, prefix) || len(line) == len(prefix) {
		return false
	}
	r, _ := decodeChar(line, len(prefix))
	return isJSSpace(r)
}

func isASCIIWordChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// indexHeaderMatchLen is the length of the match of /^(?:Index:|diff(?: -r \w+)+)\s+/ in
// line, or -1.
func indexHeaderMatchLen(line string) int {
	spaceRunEnd := func(p int) int {
		if p >= len(line) {
			return -1
		}
		end := scanRun(line, p, isJSSpace)
		if end == p {
			return -1
		}
		return end
	}
	if strings.HasPrefix(line, "Index:") {
		return spaceRunEnd(len("Index:"))
	}
	if !strings.HasPrefix(line, "diff") {
		return -1
	}
	// (?: -r \w+)+ is greedy; on failure of \s+ the regex backtracks to fewer groups
	// (a shorter \w+ is always followed by a word character, so it cannot help).
	var ends []int
	p := len("diff")
	for strings.HasPrefix(line[p:], " -r ") {
		q := p + len(" -r ")
		w := q
		for w < len(line) && isASCIIWordChar(line[w]) {
			w++
		}
		if w == q {
			break
		}
		ends = append(ends, w)
		p = w
	}
	for k := len(ends) - 1; k >= 0; k-- {
		if end := spaceRunEnd(ends[k]); end >= 0 {
			return end
		}
	}
	return -1
}

var hunkHeaderRe = regexp.MustCompile(`@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParsePatch parses a patch into structured data, in the same structure returned by
// NewStructuredPatch. It returns one entry per file (always at least one).
//
// Errors carry the TS messages ("Unknown line ...", "Hunk at line ... contained invalid
// line ...", "Added/Removed line count did not match for hunk at line ...").
//
// Differences forced by Go: a hunk header without line ranges gives NaN starts in JS;
// the Go fields are ints and read 0. A patch without ---/+++ lines leaves the file names
// undefined in JS and "" in Go.
func ParsePatch(uniDiff string) ([]*StructuredPatch, error) {
	diffstr := strings.Split(uniDiff, "\n")
	list := []*StructuredPatch{}
	i := 0

	// parseFileHeader parses the --- and +++ headers; if none are found, no lines are
	// consumed.
	parseFileHeader := func(index *StructuredPatch) {
		if i >= len(diffstr) {
			return
		}
		line := diffstr[i]
		var prefix string
		switch {
		case hasPrefixThenSpace(line, "---"):
			prefix = "---"
		case hasPrefixThenSpace(line, "+++"):
			prefix = "+++"
		default:
			return
		}
		data := strings.Split(jsTrim(line[3:]), "\t")
		header := ""
		if len(data) > 1 {
			header = jsTrim(data[1])
		}
		fileName := strings.ReplaceAll(data[0], `\\`, `\`)
		if strings.HasPrefix(fileName, `"`) && strings.HasSuffix(fileName, `"`) {
			if len(fileName) >= 2 {
				fileName = fileName[1 : len(fileName)-1]
			} else {
				fileName = ""
			}
		}
		if prefix == "---" {
			index.OldFileName = fileName
			index.OldHeader = &header
		} else {
			index.NewFileName = fileName
			index.NewHeader = &header
		}
		i++
	}

	// parseHunk parses a hunk. This assumes that we are at the start of a hunk.
	parseHunk := func() (StructuredPatchHunk, error) {
		chunkHeaderIndex := i
		chunkHeaderLine := diffstr[i]
		i++
		var group [5]string
		var present [5]bool
		if m := hunkHeaderRe.FindStringSubmatchIndex(chunkHeaderLine); m != nil {
			for g := 1; g <= 4; g++ {
				if m[2*g] >= 0 {
					group[g] = chunkHeaderLine[m[2*g]:m[2*g+1]]
					present[g] = true
				}
			}
		}
		number := func(g int) int {
			if !present[g] {
				return 0 // JS: +undefined is NaN
			}
			n, err := strconv.Atoi(group[g])
			if err != nil {
				return math.MaxInt
			}
			return n
		}
		count := func(g int) int {
			if !present[g] {
				return 1
			}
			return number(g)
		}
		hunk := StructuredPatchHunk{
			OldStart: number(1),
			OldLines: count(2),
			NewStart: number(3),
			NewLines: count(4),
			Lines:    []string{},
		}
		// Unified Diff Format quirk: If the chunk size is 0,
		// the first number is one lower than one would expect.
		// https://www.artima.com/weblogs/viewpost.jsp?thread=164293
		if hunk.OldLines == 0 {
			hunk.OldStart++
		}
		if hunk.NewLines == 0 {
			hunk.NewStart++
		}
		addCount, removeCount := 0, 0
		for ; i < len(diffstr) && (removeCount < hunk.OldLines || addCount < hunk.NewLines || strings.HasPrefix(diffstr[i], `\`)); i++ {
			line := diffstr[i]
			var operation byte
			if len(line) == 0 && i != len(diffstr)-1 {
				operation = ' '
			} else if len(line) > 0 {
				operation = line[0]
			}
			if operation == '+' || operation == '-' || operation == ' ' || operation == '\\' {
				hunk.Lines = append(hunk.Lines, line)
				switch operation {
				case '+':
					addCount++
				case '-':
					removeCount++
				case ' ':
					addCount++
					removeCount++
				}
			} else {
				return hunk, errors.New("Hunk at line " + strconv.Itoa(chunkHeaderIndex+1) + " contained invalid line " + line)
			}
		}
		// Handle the empty block count case
		if addCount == 0 && hunk.NewLines == 1 {
			hunk.NewLines = 0
		}
		if removeCount == 0 && hunk.OldLines == 1 {
			hunk.OldLines = 0
		}
		// Perform sanity checking
		if addCount != hunk.NewLines {
			return hunk, errors.New("Added line count did not match for hunk at line " + strconv.Itoa(chunkHeaderIndex+1))
		}
		if removeCount != hunk.OldLines {
			return hunk, errors.New("Removed line count did not match for hunk at line " + strconv.Itoa(chunkHeaderIndex+1))
		}
		return hunk, nil
	}

	parseIndex := func() error {
		index := &StructuredPatch{}
		list = append(list, index)
		// Parse diff metadata
		for i < len(diffstr) {
			line := diffstr[i]
			// File header found, end parsing diff metadata
			if hasPrefixThenSpace(line, "---") || hasPrefixThenSpace(line, "+++") || hasPrefixThenSpace(line, "@@") {
				break
			}
			// Try to parse the line as a diff header, like
			//     Index: README.md
			// or
			//     diff -r 9117c6561b0b -r 273ce12ad8f1 .hgignore
			// or
			//     Index: something with multiple words
			// and extract the filename (or whatever else is used as an index name) from
			// the end (i.e. 'README.md', '.hgignore', or 'something with multiple words'
			// in the examples above).
			if n := indexHeaderMatchLen(line); n >= 0 {
				name := jsTrim(line[n:])
				index.Index = &name
			}
			i++
		}
		// Parse file headers if they are defined. Unified diff requires them, but
		// there's no technical issues to have an isolated hunk without file header
		parseFileHeader(index)
		parseFileHeader(index)
		// Parse hunks
		index.Hunks = []StructuredPatchHunk{}
		for i < len(diffstr) {
			line := diffstr[i]
			if hasPrefixThenSpace(line, "Index:") || hasPrefixThenSpace(line, "diff") ||
				hasPrefixThenSpace(line, "---") || hasPrefixThenSpace(line, "+++") ||
				strings.HasPrefix(line, patchUnderline) {
				break
			} else if strings.HasPrefix(line, "@@") {
				hunk, err := parseHunk()
				if err != nil {
					return err
				}
				index.Hunks = append(index.Hunks, hunk)
			} else if line != "" {
				return errors.New("Unknown line " + strconv.Itoa(i+1) + " " + jsonString(line))
			} else {
				i++
			}
		}
		return nil
	}

	for i < len(diffstr) {
		if err := parseIndex(); err != nil {
			return nil, err
		}
	}
	return list, nil
}
