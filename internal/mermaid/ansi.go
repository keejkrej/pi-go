package mermaid

const esc = "\x1b"

// DefaultTheme is a dim frame with cyan connectors (TS DEFAULT_THEME).
var DefaultTheme = map[Cls]string{
	ClsBorder:    "2",
	ClsEdge:      "36",
	ClsEdgeLabel: "2;36",
	ClsTitle:     "1",
}

// ToAnsi renders art to ANSI-coloured lines. A nil theme uses DefaultTheme;
// an empty map leaves every class unstyled.
func ToAnsi(art MermaidArt, theme map[Cls]string) []string {
	if theme == nil {
		theme = DefaultTheme
	}
	out := make([]string, len(art.Styled))
	for i, row := range art.Styled {
		var b []byte
		for _, span := range row {
			if sgr, ok := theme[span.Cls]; ok {
				b = append(b, esc...)
				b = append(b, '[')
				b = append(b, sgr...)
				b = append(b, 'm')
				b = append(b, span.Text...)
				b = append(b, esc...)
				b = append(b, "[0m"...)
			} else {
				b = append(b, span.Text...)
			}
		}
		out[i] = string(b)
	}
	return out
}
