package mermaid

import "strings"

type seqHead string

const (
	seqHeadArrow seqHead = "arrow"
	seqHeadCross seqHead = "cross"
)

type seqOp struct {
	op     string
	dashed bool
	head   seqHead
}

var seqOps = []seqOp{
	{"-->>", true, seqHeadArrow},
	{"->>", false, seqHeadArrow},
	{"--x", true, seqHeadCross},
	{"-x", false, seqHeadCross},
	{"--)", true, seqHeadArrow},
	{"-)", false, seqHeadArrow},
	{"-->", true, seqHeadArrow},
	{"->", false, seqHeadArrow},
}

const maxSeqOp = 4

type NoteAnchor struct {
	Kind         string // over, left, right
	From, To, At int
}

type SeqItem struct {
	Kind   string // message, note, divider
	From   int
	To     int
	Text   *string
	Dashed bool
	Head   seqHead
	Anchor NoteAnchor
}

type Sequence struct {
	labels []string
	index  map[string]int
	items  []SeqItem
}

func (s *Sequence) participant(id string, label *string) (int, bool) {
	if existing, ok := s.index[id]; ok {
		if label != nil {
			s.labels[existing] = *label
		}
		return existing, true
	}
	if len(s.labels) >= maxNodes {
		return 0, false
	}
	s.index[id] = len(s.labels)
	lab := id
	if label != nil {
		lab = *label
	}
	s.labels = append(s.labels, lab)
	return len(s.labels) - 1, true
}

func parseSequence(src string) *Sequence {
	statements := statementsOf(src)
	if headerKind(statements) != "sequencediagram" {
		return nil
	}
	seq := &Sequence{index: map[string]int{}}
	autonumber := false
	msgCount := 0
	var blocks []bool
	for _, st := range statements[1:] {
		first := firstWord(st)
		lower := asciiLower(first)
		switch {
		case lower == "participant" || lower == "actor":
			rest := jsTrim(st[len(first):])
			if rest == "" {
				return nil
			}
			var id string
			var label *string
			if l, r, ok := splitOnce(rest, " as "); ok {
				id = jsTrim(l)
				cleaned := cleanLabel(r)
				label = &cleaned
			} else {
				id = rest
			}
			if _, ok := seq.participant(id, label); !ok {
				return nil
			}
		case lower == "autonumber":
			autonumber = true
		case lower == "activate" || lower == "deactivate" || lower == "create" || lower == "destroy" ||
			lower == "title" || lower == "acctitle" || lower == "accdescr" || lower == "links" ||
			lower == "link" || lower == "properties":
			continue
		case lower == "note":
			note := parseNoteAnchor(jsTrim(st[len(first):]), seq)
			if note == nil {
				return nil
			}
			if len(seq.items) >= maxEdges {
				return nil
			}
			text := note.text
			seq.items = append(seq.items, SeqItem{Kind: "note", Anchor: note.anchor, Text: &text})
		case lower == "loop" || lower == "alt" || lower == "opt" || lower == "par" ||
			lower == "critical" || lower == "break" || lower == "else" || lower == "and" || lower == "option":
			if lower == "else" || lower == "and" || lower == "option" {
				if len(blocks) == 0 || !blocks[len(blocks)-1] {
					continue
				}
			} else {
				blocks = append(blocks, true)
			}
			if len(seq.items) >= maxEdges {
				return nil
			}
			text := decodeHTMLEntities(st)
			seq.items = append(seq.items, SeqItem{Kind: "divider", Text: &text})
		case lower == "rect" || lower == "box":
			blocks = append(blocks, false)
		case lower == "end":
			if len(blocks) == 0 {
				continue
			}
			top := blocks[len(blocks)-1]
			blocks = blocks[:len(blocks)-1]
			if top {
				if len(seq.items) >= maxEdges {
					return nil
				}
				text := "end"
				seq.items = append(seq.items, SeqItem{Kind: "divider", Text: &text})
			}
		default:
			msg := parseSeqMessage(st, seq)
			if msg == nil {
				return nil
			}
			text := msg.text
			if autonumber {
				msgCount++
				if text == nil {
					s := itoa(msgCount) + "."
					text = &s
				} else {
					s := itoa(msgCount) + ". " + *text
					text = &s
				}
			}
			if len(seq.items) >= maxEdges {
				return nil
			}
			seq.items = append(seq.items, SeqItem{
				Kind: "message", From: msg.from, To: msg.to, Text: text, Dashed: msg.dashed, Head: msg.head,
			})
		}
	}
	if len(seq.labels) == 0 {
		return nil
	}
	return seq
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

type parsedNote struct {
	text   string
	anchor NoteAnchor
}

func parseNoteAnchor(rest string, seq *Sequence) *parsedNote {
	lower := asciiLower(rest)
	var kind string
	var idsAndText string
	switch {
	case strings.HasPrefix(lower, "over "):
		kind = "over"
		idsAndText = rest[len("over "):]
	case strings.HasPrefix(lower, "left of "):
		kind = "left"
		idsAndText = rest[len("left of "):]
	case strings.HasPrefix(lower, "right of "):
		kind = "right"
		idsAndText = rest[len("right of "):]
	default:
		return nil
	}
	lhs, rhs, ok := splitOnce(idsAndText, ":")
	if !ok {
		return nil
	}
	text := decodeHTMLEntities(jsTrim(rhs))
	rawParts := strings.Split(lhs, ",")
	var parts []string
	for _, p := range rawParts {
		p = jsTrim(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	a, ok := seq.participant(parts[0], nil)
	if !ok {
		return nil
	}
	if kind != "over" {
		return &parsedNote{text: text, anchor: NoteAnchor{Kind: kind, At: a}}
	}
	b := a
	if len(parts) > 1 {
		second, ok := seq.participant(parts[1], nil)
		if !ok {
			return nil
		}
		b = second
	}
	from, to := a, b
	if from > to {
		from, to = to, from
	}
	return &parsedNote{text: text, anchor: NoteAnchor{Kind: "over", From: from, To: to}}
}

type parsedMsg struct {
	from, to int
	text     *string
	dashed   bool
	head     seqHead
}

func parseSeqMessage(st string, seq *Sequence) *parsedMsg {
	chars := []rune(st)
	type foundOp struct {
		pos    int
		op     string
		dashed bool
		head   seqHead
	}
	var found *foundOp
outer:
	for pos := 0; pos < len(chars); pos++ {
		tail := string(chars[pos:min(pos+maxSeqOp, len(chars))])
		for _, op := range seqOps {
			if strings.HasPrefix(tail, op.op) {
				found = &foundOp{pos, op.op, op.dashed, op.head}
				break outer
			}
		}
	}
	if found == nil {
		return nil
	}
	fromID := jsTrim(string(chars[:found.pos]))
	if fromID == "" {
		return nil
	}
	opRunes := []rune(found.op)
	rest := string(chars[found.pos+len(opRunes):])
	rest = jsTrimLeft(rest)
	rest = strings.TrimLeft(rest, "+-")
	toID := rest
	var text *string
	if l, r, ok := splitOnce(rest, ":"); ok {
		toID = jsTrim(l)
		text = nonEmpty(decodeHTMLEntities(jsTrim(r)))
	} else {
		toID = jsTrim(rest)
	}
	if toID == "" {
		return nil
	}
	from, ok := seq.participant(fromID, nil)
	if !ok {
		return nil
	}
	to, ok := seq.participant(toID, nil)
	if !ok {
		return nil
	}
	return &parsedMsg{from: from, to: to, text: text, dashed: found.dashed, head: found.head}
}
