// Ported from packages/coding-agent/src/core/tools/output-accumulator.ts (pi v1.0.0).

package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// OutputAccumulatorOptions configures an OutputAccumulator.
type OutputAccumulatorOptions struct {
	MaxLines       *int
	MaxBytes       *int
	TempFilePrefix string
}

// SnapshotOptions configures OutputAccumulator.Snapshot.
type SnapshotOptions struct {
	PersistIfTruncated bool
}

// OutputSnapshot is a bounded display view of accumulated streaming output.
type OutputSnapshot struct {
	Content        string           `json:"content"`
	Truncation     TruncationResult `json:"truncation"`
	FullOutputPath *string          `json:"fullOutputPath,omitzero"`
}

// FullOutput holds the full (or head+tail bounded) output read back after completion.
type FullOutput struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// OutputAccumulator incrementally tracks streaming command output with bounded memory.
// Appends decode chunks with a streaming UTF-8 decoder, retain a bounded decoded tail for
// display snapshots, and spill raw bytes to a private temp file once thresholds are exceeded.
type OutputAccumulator struct {
	mu                       sync.Mutex
	maxLines                 int
	maxBytes                 int
	maxRollingBytes          int
	tempFilePrefix           string
	decoder                  *js.UTF8StreamDecoder
	rawChunks                [][]byte
	tailText                 string
	tailBytes                int
	tailStartsAtLineBoundary bool
	totalRawBytes            int
	totalDecodedBytes        int
	completedLines           int
	totalLines               int
	currentLineBytes         int
	hasOpenLine              bool
	finished                 bool
	tempFilePath             *string
	tempFile                 *os.File
}

// NewOutputAccumulator creates a streaming output accumulator with optional limits.
func NewOutputAccumulator(options ...*OutputAccumulatorOptions) *OutputAccumulator {
	maxLines := DefaultMaxLines
	maxBytes := DefaultMaxBytes
	tempFilePrefix := "pi-output"
	if len(options) > 0 && options[0] != nil {
		if options[0].MaxLines != nil {
			maxLines = *options[0].MaxLines
		}
		if options[0].MaxBytes != nil {
			maxBytes = *options[0].MaxBytes
		}
		if options[0].TempFilePrefix != "" {
			tempFilePrefix = options[0].TempFilePrefix
		}
	}
	return &OutputAccumulator{
		maxLines:                 maxLines,
		maxBytes:                 maxBytes,
		maxRollingBytes:          max(maxBytes*2, 1),
		tempFilePrefix:           tempFilePrefix,
		decoder:                  js.NewUTF8StreamDecoder(),
		tailStartsAtLineBoundary: true,
	}
}

// Append ingests a raw byte chunk into the accumulator.
func (a *OutputAccumulator) Append(data []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return errors.New("Cannot append to a finished output accumulator")
	}

	a.totalRawBytes += len(data)
	a.appendDecodedText(a.decoder.Decode(data))

	if a.tempFile != nil || a.shouldUseTempFile() {
		a.ensureTempFile()
		if a.tempFile != nil && len(data) > 0 {
			_, _ = a.tempFile.Write(data)
		}
	} else if len(data) > 0 {
		a.rawChunks = append(a.rawChunks, bytes.Clone(data))
	}
	return nil
}

// Finish flushes the streaming UTF-8 decoder and opens a temp file if thresholds were crossed.
func (a *OutputAccumulator) Finish() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.finished = true
	a.appendDecodedText(a.decoder.Flush())
	if a.shouldUseTempFile() {
		a.ensureTempFile()
	}
}

// Snapshot computes the current tail-truncated display snapshot.
func (a *OutputAccumulator) Snapshot(options ...*SnapshotOptions) OutputSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	snapshotText := a.getSnapshotText()
	if snapshotText == "" {
		snapshotText = a.tailText
	}
	tailTruncation := TruncateTail(snapshotText, &TruncationOptions{
		MaxLines: &a.maxLines,
		MaxBytes: &a.maxBytes,
	})
	truncated := a.totalLines > a.maxLines || a.totalDecodedBytes > a.maxBytes
	truncatedBy := jsonx.Null[string]()
	if truncated {
		if tb, ok := tailTruncation.TruncatedBy.Get(); ok {
			truncatedBy = jsonx.Some(tb)
		} else if a.totalDecodedBytes > a.maxBytes {
			truncatedBy = jsonx.Some("bytes")
		} else {
			truncatedBy = jsonx.Some("lines")
		}
	}

	truncation := TruncationResult{
		Content:               tailTruncation.Content,
		Truncated:             truncated,
		TruncatedBy:           truncatedBy,
		TotalLines:            a.totalLines,
		TotalBytes:            a.totalDecodedBytes,
		OutputLines:           tailTruncation.OutputLines,
		OutputBytes:           tailTruncation.OutputBytes,
		LastLinePartial:       tailTruncation.LastLinePartial,
		FirstLineExceedsLimit: tailTruncation.FirstLineExceedsLimit,
		MaxLines:              a.maxLines,
		MaxBytes:              a.maxBytes,
	}

	persist := len(options) > 0 && options[0] != nil && options[0].PersistIfTruncated
	if persist && truncation.Truncated {
		a.ensureTempFile()
	}

	return OutputSnapshot{
		Content:        truncation.Content,
		Truncation:     truncation,
		FullOutputPath: a.tempFilePath,
	}
}

// CloseTempFile closes the backing temporary output file if open.
func (a *OutputAccumulator) CloseTempFile() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tempFile == nil {
		return nil
	}
	f := a.tempFile
	a.tempFile = nil
	return f.Close()
}

// ReadFullOutput reads back the complete output (or head+tail halves when size exceeds maxBytes).
// Call after Finish() and CloseTempFile().
func (a *OutputAccumulator) ReadFullOutput(maxBytes int) (FullOutput, error) {
	a.mu.Lock()
	tempFilePath := a.tempFilePath
	var rawChunksCopy [][]byte
	if tempFilePath == nil && len(a.rawChunks) > 0 {
		rawChunksCopy = make([][]byte, len(a.rawChunks))
		copy(rawChunksCopy, a.rawChunks)
	}
	a.mu.Unlock()
	if tempFilePath == nil {
		dec := js.NewUTF8StreamDecoder()
		var buf bytes.Buffer
		for _, c := range rawChunksCopy {
			buf.Write(c)
		}
		return FullOutput{
			Content:   dec.DecodeFinal(buf.Bytes()),
			Truncated: false,
		}, nil
	}

	f, err := os.Open(*tempFilePath)
	if err != nil {
		return FullOutput{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return FullOutput{}, err
	}
	size := info.Size()
	if size <= int64(maxBytes) {
		raw, err := io.ReadAll(f)
		if err != nil {
			return FullOutput{}, err
		}
		dec := js.NewUTF8StreamDecoder()
		return FullOutput{
			Content:   dec.DecodeFinal(raw),
			Truncated: false,
		}, nil
	}

	headBytes := maxBytes / 2
	tailBytes := maxBytes - headBytes
	head := make([]byte, headBytes)
	tail := make([]byte, tailBytes)

	if headBytes > 0 {
		if _, err := io.ReadFull(io.NewSectionReader(f, 0, int64(headBytes)), head); err != nil {
			return FullOutput{}, err
		}
	}
	if tailBytes > 0 {
		if _, err := io.ReadFull(io.NewSectionReader(f, size-int64(tailBytes), int64(tailBytes)), tail); err != nil {
			return FullOutput{}, err
		}
	}

	headText := js.NewUTF8StreamDecoder().Decode(head)
	tailStart := 0
	for tailStart < len(tail) && (tail[tailStart]&0xc0) == 0x80 {
		tailStart++
	}
	tailText := js.NewUTF8StreamDecoder().DecodeFinal(tail[tailStart:])
	omitted := size - int64(headBytes) - int64(tailBytes)
	return FullOutput{
		Content:   fmt.Sprintf("%s\n\n[... %d bytes omitted ...]\n\n%s", headText, omitted, tailText),
		Truncated: true,
	}, nil
}

// GetLastLineBytes returns the decoded UTF-8 byte length of the current trailing partial line.
func (a *OutputAccumulator) GetLastLineBytes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentLineBytes
}

func (a *OutputAccumulator) appendDecodedText(text string) {
	if len(text) == 0 {
		return
	}

	byteCount := len(text)
	a.totalDecodedBytes += byteCount
	a.tailText += text
	a.tailBytes += byteCount
	if a.tailBytes > a.maxRollingBytes*2 {
		a.trimTail()
	}

	newlines := 0
	lastNewline := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			newlines++
			lastNewline = i
		}
	}
	if newlines == 0 {
		a.currentLineBytes += byteCount
		a.hasOpenLine = true
	} else {
		a.completedLines += newlines
		tail := text[lastNewline+1:]
		a.currentLineBytes = len(tail)
		a.hasOpenLine = len(tail) > 0
	}
	if a.hasOpenLine {
		a.totalLines = a.completedLines + 1
	} else {
		a.totalLines = a.completedLines
	}
}

func (a *OutputAccumulator) trimTail() {
	if len(a.tailText) <= a.maxRollingBytes {
		a.tailBytes = len(a.tailText)
		return
	}

	raw := a.tailText
	start := len(raw) - a.maxRollingBytes
	for start < len(raw) && (raw[start]&0xc0) == 0x80 {
		start++
	}

	if start > 0 {
		a.tailStartsAtLineBoundary = raw[start-1] == '\n'
	}
	// TS parity: raw subarray decoded cleanly without corrupting tail
	a.tailText = js.NewUTF8StreamDecoder().DecodeFinal([]byte(raw[start:]))
	a.tailBytes = len(a.tailText)
}

func (a *OutputAccumulator) getSnapshotText() string {
	if a.tailStartsAtLineBoundary {
		return a.tailText
	}
	idx := strings.IndexByte(a.tailText, '\n')
	if idx == -1 {
		return a.tailText
	}
	return a.tailText[idx+1:]
}

func (a *OutputAccumulator) shouldUseTempFile() bool {
	return a.totalRawBytes > a.maxBytes || a.totalDecodedBytes > a.maxBytes || a.totalLines > a.maxLines
}

func (a *OutputAccumulator) ensureTempFile() {
	if a.tempFilePath != nil {
		return
	}
	p, f, err := utils.CreateOutputFileStream(a.tempFilePrefix, ".log")
	if err != nil {
		return
	}
	a.tempFilePath = &p
	a.tempFile = f
	for _, chunk := range a.rawChunks {
		if len(chunk) > 0 {
			_, _ = a.tempFile.Write(chunk)
		}
	}
	a.rawChunks = nil
}
