// Ported from packages/agent/test/utils/calculate.ts (pi v1.0.0).

package testkit

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// CalculateResult is returned as Details from CalculateTool executions.
type CalculateResult struct {
	Expression string  `json:"expression"`
	Result     float64 `json:"result"`
}

// Calculate evaluates a mathematical arithmetic expression supporting numbers,
// +, -, *, /, unary +/-, and parentheses.
func Calculate(expression string) (float64, error) {
	p := &ucParser{src: expression}
	p.skipSpaces()
	if p.pos >= len(p.src) {
		return 0, fmt.Errorf("Failed to evaluate expression: empty expression")
	}
	val, err := p.parseExpr()
	if err != nil {
		return 0, fmt.Errorf("Failed to evaluate expression: %w", err)
	}
	p.skipSpaces()
	if p.pos < len(p.src) {
		return 0, fmt.Errorf("Failed to evaluate expression: unexpected character %q", p.src[p.pos])
	}
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, fmt.Errorf("Expression did not evaluate to a finite number: %s", expression)
	}
	return val, nil
}

type ucParser struct {
	src string
	pos int
}

func (p *ucParser) skipSpaces() {
	for p.pos < len(p.src) && unicode.IsSpace(rune(p.src[p.pos])) {
		p.pos++
	}
}

func (p *ucParser) parseExpr() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.src) {
			return left, nil
		}
		op := p.src[p.pos]
		if op != '+' && op != '-' {
			return left, nil
		}
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
}

func (p *ucParser) parseTerm() (float64, error) {
	left, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.src) {
			return left, nil
		}
		op := p.src[p.pos]
		if op != '*' && op != '/' {
			return left, nil
		}
		p.pos++
		right, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		if op == '*' {
			left *= right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		}
	}
}

func (p *ucParser) parseFactor() (float64, error) {
	p.skipSpaces()
	if p.pos >= len(p.src) {
		return 0, fmt.Errorf("unexpected end of expression")
	}
	ch := p.src[p.pos]
	if ch == '+' {
		p.pos++
		return p.parseFactor()
	}
	if ch == '-' {
		p.pos++
		val, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		return -val, nil
	}
	if ch == '(' {
		p.pos++
		val, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpaces()
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return val, nil
	}
	return p.parseNumber()
}

func (p *ucParser) parseNumber() (float64, error) {
	p.skipSpaces()
	start := p.pos
	hasDot := false
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c >= '0' && c <= '9' {
			p.pos++
			continue
		}
		if c == '.' && !hasDot {
			hasDot = true
			p.pos++
			continue
		}
		break
	}
	if p.pos == start {
		return 0, fmt.Errorf("expected number at position %d", p.pos)
	}
	val, err := strconv.ParseFloat(p.src[start:p.pos], 64)
	if err != nil {
		return 0, err
	}
	return val, nil
}

func ucExtractExpression(params any) (string, error) {
	switch p := params.(type) {
	case *jsonx.Object:
		if p == nil {
			return "", fmt.Errorf("missing expression argument")
		}
		v, ok := p.Get("expression")
		if !ok {
			return "", fmt.Errorf("missing expression argument")
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("expression must be a string")
		}
		return s, nil
	case map[string]any:
		s, _ := p["expression"].(string)
		if s == "" {
			return "", fmt.Errorf("missing expression argument")
		}
		return s, nil
	default:
		return "", fmt.Errorf("unexpected params type %T", params)
	}
}

func ucFormatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// CalculateTool returns the standard calculator test AgentTool.
func CalculateTool() *agent.AgentTool {
	return ucBuildCalculateTool(nil)
}

// CreateCalculateToolWithUsage returns a calculator AgentTool that attaches tool execution usage.
func CreateCalculateToolWithUsage(usage ai.Usage) *agent.AgentTool {
	u := usage
	return ucBuildCalculateTool(&u)
}

func ucBuildCalculateTool(usage *ai.Usage) *agent.AgentTool {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("expression", typebox.String(jsonx.ObjectOf("description", "The mathematical expression to evaluate")))
	schema := typebox.Object(props)

	return &agent.AgentTool{
		Name:        "calculate",
		Label:       "Calculator",
		Description: "Evaluate a mathematical expression",
		Parameters:  schema,
		Execute: func(_ context.Context, _ string, params any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			expr, err := ucExtractExpression(params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			val, err := Calculate(strings.TrimSpace(expr))
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			res := agent.AgentToolResult{
				Content: ai.ContentList{
					&ai.TextContent{
						Type: "text",
						Text: fmt.Sprintf("%s = %s", expr, ucFormatNumber(val)),
					},
				},
				Details: CalculateResult{
					Expression: expr,
					Result:     val,
				},
			}
			if usage != nil {
				u := *usage
				res.Usage = &u
			}
			return res, nil
		},
	}
}
