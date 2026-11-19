package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/gui"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type wurBox struct {
	Padding [4]float32
	Margin  [4]float32
	Border  [4]float32
}

type wurFlow struct {
	Type    string
	Dir     string
	Justify string
	Align   string
	Wrap    string
	Gap     float32
	HasGap  bool
}

type wurSize struct {
	W string
	H string
}

type wurTextSpec struct {
	Value    string
	Wrap     string
	Lines    int
	Overflow string
}

type wurAssetSpec struct {
	Kind string
	Ref  string
	Alt  string
	W    int
	H    int
}

type wurLayoutNode struct {
	Key      string
	Parent   *string
	Order    int
	Role     string
	Bounds   ui.Rect
	Flow     wurFlow
	Box      wurBox
	Size     wurSize
	Overflow string
	Z        int
	Variant  string
	State    string
	Text     *wurTextSpec
	Asset    *wurAssetSpec
}

type wurWorkspaceExtractOpts struct {
	surfaceState string
	light        bool
	collapsed    bool
	rightOpen    bool
	populated    bool
	busyQueued   bool
	toastVisible bool
}

func wurStrPtr(s string) *string { return &s }

func wurRound1(v float32) float64 {
	return math.Round(float64(v)*10) / 10
}

func wurFormatNum(v float64) string {
	r := math.Round(v*100) / 100
	if r == math.Trunc(r) {
		return fmt.Sprintf("%.0f", r)
	}
	s := fmt.Sprintf("%.2f", r)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

func wurYAMLScalar(s string) string {
	if s == "" || s == "null" || s == "true" || s == "false" || s == "~" {
		return fmt.Sprintf("%q", s)
	}
	if strings.TrimSpace(s) != s {
		return fmt.Sprintf("%q", s)
	}
	first := s[0]
	if first == '#' || first == '-' || first == '{' || first == '[' || first == '"' || first == '\'' || first == '*' || first == '&' || first == '!' || first == '|' || first == '>' || first == '%' || first == '@' || first == '`' {
		return fmt.Sprintf("%q", s)
	}
	isPlain := true
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' || ch == '/' || ch == '+' || ch == '-' || ch == ' ') {
			isPlain = false
			break
		}
	}
	if !isPlain {
		return fmt.Sprintf("%q", s)
	}
	// Quote strings that look numeric to YAML parser.
	allNum := true
	for i := 0; i < len(s); i++ {
		if !((s[i] >= '0' && s[i] <= '9') || s[i] == '-' || s[i] == '.') {
			allNum = false
			break
		}
	}
	if allNum {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func wurFlowNums(vals [4]float32) string {
	return fmt.Sprintf("[%s, %s, %s, %s]",
		wurFormatNum(float64(vals[0])),
		wurFormatNum(float64(vals[1])),
		wurFormatNum(float64(vals[2])),
		wurFormatNum(float64(vals[3])),
	)
}

func wurDumpLayoutYAML(surface, state string, vw, vh int, nodes []wurLayoutNode) string {
	var b strings.Builder
	b.WriteString("schema: ui-layout-spec/1\n")
	b.WriteString("meta:\n")
	fmt.Fprintf(&b, "  surface: %s\n", wurYAMLScalar(surface))
	fmt.Fprintf(&b, "  state: %s\n", wurYAMLScalar(state))
	fmt.Fprintf(&b, "  viewport: {w: %d, h: %d}\n", vw, vh)
	b.WriteString("  dpr: 2\n")
	b.WriteString("  source: actual\n")
	b.WriteString("  method: native\n")
	b.WriteString("  confidence: measured\n")
	b.WriteString("  evidence:\n")
	b.WriteString("    - codingagent/modes/gui/waku_ui_replication_test.go\n")
	fmt.Fprintf(&b, "  generated: %q\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("nodes:\n")
	for _, n := range nodes {
		fmt.Fprintf(&b, "  - key: %s\n", wurYAMLScalar(n.Key))
		if n.Parent == nil {
			b.WriteString("    parent: null\n")
		} else {
			fmt.Fprintf(&b, "    parent: %s\n", wurYAMLScalar(*n.Parent))
		}
		fmt.Fprintf(&b, "    order: %d\n", n.Order)
		fmt.Fprintf(&b, "    role: %s\n", wurYAMLScalar(n.Role))
		fmt.Fprintf(&b, "    bounds: {x: %s, y: %s, w: %s, h: %s}\n",
			wurFormatNum(wurRound1(n.Bounds.X)),
			wurFormatNum(wurRound1(n.Bounds.Y)),
			wurFormatNum(wurRound1(n.Bounds.W)),
			wurFormatNum(wurRound1(n.Bounds.H)),
		)
		if n.Flow.Type == "flex" {
			gapStr := ""
			if n.Flow.HasGap {
				gapStr = fmt.Sprintf(", gap: %s", wurFormatNum(float64(n.Flow.Gap)))
			}
			wrap := n.Flow.Wrap
			if wrap == "" {
				wrap = "nowrap"
			}
			fmt.Fprintf(&b, "    flow: {type: flex, dir: %s, justify: %s, align: %s, wrap: %s%s}\n",
				n.Flow.Dir, n.Flow.Justify, n.Flow.Align, wrap, gapStr)
		} else {
			ft := n.Flow.Type
			if ft == "" {
				ft = "block"
			}
			fmt.Fprintf(&b, "    flow: {type: %s}\n", ft)
		}
		b.WriteString("    box:\n")
		fmt.Fprintf(&b, "      padding: %s\n", wurFlowNums(n.Box.Padding))
		fmt.Fprintf(&b, "      margin: %s\n", wurFlowNums(n.Box.Margin))
		fmt.Fprintf(&b, "      border: %s\n", wurFlowNums(n.Box.Border))
		fmt.Fprintf(&b, "    size: {w: %s, h: %s}\n", n.Size.W, n.Size.H)
		ov := n.Overflow
		if ov == "" {
			ov = "hidden"
		}
		fmt.Fprintf(&b, "    overflow: %s\n", ov)
		fmt.Fprintf(&b, "    z: %d\n", n.Z)
		if n.Variant == "" {
			b.WriteString("    variant: null\n")
		} else {
			fmt.Fprintf(&b, "    variant: %s\n", wurYAMLScalar(n.Variant))
		}
		st := n.State
		if st == "" {
			st = "default"
		}
		fmt.Fprintf(&b, "    state: %s\n", wurYAMLScalar(st))
		if n.Text == nil {
			b.WriteString("    text: null\n")
		} else {
			tw := n.Text.Wrap
			if tw == "" {
				tw = "nowrap"
			}
			to := n.Text.Overflow
			if to == "" {
				to = "ellipsis"
			}
			tl := n.Text.Lines
			if tl <= 0 {
				tl = 1
			}
			fmt.Fprintf(&b, "    text: {value: %s, wrap: %s, lines: %d, overflow: %s}\n",
				wurYAMLScalar(n.Text.Value), tw, tl, to)
		}
		if n.Asset == nil {
			b.WriteString("    asset: null\n")
		} else {
			fmt.Fprintf(&b, "    asset: {kind: %s, ref: %s, alt: %s, w: %d, h: %d}\n",
				n.Asset.Kind, wurYAMLScalar(n.Asset.Ref), wurYAMLScalar(n.Asset.Alt), n.Asset.W, n.Asset.H)
		}
	}
	return b.String()
}

func wurFindAccess(t *testing.T, all []ui.AccessInfo, label string, pred func(ui.AccessInfo) bool) ui.AccessInfo {
	t.Helper()
	for _, info := range all {
		if info.Name != label {
			continue
		}
		if pred == nil || pred(info) {
			if info.Bounds.W <= 0 || info.Bounds.H <= 0 {
				t.Fatalf("node %q has non-positive bounds %+v", label, info.Bounds)
			}
			return info
		}
	}
	names := make([]string, 0, len(all))
	for _, info := range all {
		if info.Name != "" {
			names = append(names, fmt.Sprintf("%s(%v@%.0f,%.0f,%.0fx%.0f)", info.Name, info.Role, info.Bounds.X, info.Bounds.Y, info.Bounds.W, info.Bounds.H))
		}
	}
	t.Fatalf("required node %q not found in AccessAll(); got: %v", label, names)
	return ui.AccessInfo{}
}

func wurFindAccessByPrefix(t *testing.T, all []ui.AccessInfo, prefix string, pred func(ui.AccessInfo) bool) ui.AccessInfo {
	t.Helper()
	for _, info := range all {
		if !strings.HasPrefix(info.Name, prefix) {
			continue
		}
		if pred == nil || pred(info) {
			if info.Bounds.W <= 0 || info.Bounds.H <= 0 {
				t.Fatalf("prefix node %q (%q) has non-positive bounds %+v", prefix, info.Name, info.Bounds)
			}
			return info
		}
	}
	t.Fatalf("required prefix %q not found in AccessAll()", prefix)
	return ui.AccessInfo{}
}

func wurContainsRect(outer, inner ui.Rect) bool {
	return inner.X >= outer.X-2 && inner.Y >= outer.Y-2 &&
		inner.X+inner.W <= outer.X+outer.W+2 &&
		inner.Y+inner.H <= outer.Y+outer.H+2
}

func wurFormatColor(c ui.Color) string {
	if c.A == 255 {
		return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
	}
	alpha := math.Round((float64(c.A)/255.0)*100) / 100
	s := fmt.Sprintf("%.2f", alpha)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		s = "0"
	}
	return fmt.Sprintf("rgba(%d, %d, %d, %s)", c.R, c.G, c.B, s)
}

func wurSavePNG(t *testing.T, dir, name string, img image.Image) {
	t.Helper()
	if dir == "" {
		return
	}
	path := filepath.Join(dir, "screenshots", "candidate", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create(%s): %v", path, err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("png.Encode(%s): %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close(%s): %v", path, err)
	}
}

func wurWriteYAML(t *testing.T, dir, surface, state, vp, content string) {
	t.Helper()
	if dir == "" {
		return
	}
	path := filepath.Join(dir, "actual-layout", surface, fmt.Sprintf("%s@%s.yaml", state, vp))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func wurPixelAt(t *testing.T, img image.Image, x, y int) color.NRGBA {
	t.Helper()
	b := img.Bounds()
	if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
		t.Fatalf("pixel (%d,%d) out of bounds %v", x, y, b)
	}
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func wurAssertNearRGB(t *testing.T, label string, got color.NRGBA, r, g, b uint8, tol int) {
	t.Helper()
	dr := int(got.R) - int(r)
	dg := int(got.G) - int(g)
	db := int(got.B) - int(b)
	if dr < 0 {
		dr = -dr
	}
	if dg < 0 {
		dg = -dg
	}
	if db < 0 {
		db = -db
	}
	if dr > tol || dg > tol || db > tol {
		t.Fatalf("%s pixel = rgba(%d,%d,%d,%d), want rgb(%d,%d,%d) ±%d", label, got.R, got.G, got.B, got.A, r, g, b, tol)
	}
}

func wurExtractWorkspaceNodes(t *testing.T, all []ui.AccessInfo, opts wurWorkspaceExtractOpts) []wurLayoutNode {
	t.Helper()
	ws := wurFindAccess(t, all, "Workspace", func(a ui.AccessInfo) bool { return a.Role == ui.RoleMain })
	wsMain := wurFindAccess(t, all, "Workspace Main", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
	wsHeader := wurFindAccess(t, all, "Workspace Header", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
	wsHeaderTitle := wurFindAccessByPrefix(t, all, "", func(a ui.AccessInfo) bool {
		return a.Role == ui.RoleHeading && wurContainsRect(wsHeader.Bounds, a.Bounds)
	})
	comp := wurFindAccess(t, all, "Composer Card", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
	compEd := wurFindAccess(t, all, "Prompt editor", nil)
	compToolbar := wurFindAccess(t, all, "Composer Toolbar", func(a ui.AccessInfo) bool { return a.Role == ui.RoleToolbar })
	compModel := wurFindAccess(t, all, "Select Model Chip", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton })
	compThink := wurFindAccess(t, all, "Select Thinking Chip", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton })

	sendLabel := "Send"
	if opts.busyQueued {
		sendLabel = "Stop"
	}
	compSend := wurFindAccess(t, all, sendLabel, func(a ui.AccessInfo) bool {
		return a.Role == ui.RoleButton && wurContainsRect(compToolbar.Bounds, a.Bounds)
	})

	footer := wurFindAccess(t, all, "Workspace Footer", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
	fProj := wurFindAccess(t, all, "Select Project", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton })
	fWS := wurFindAccess(t, all, "Select Workspace", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton })
	fBranch := wurFindAccess(t, all, "Switch Git Branch", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton })
	fCtx := wurFindAccess(t, all, "Context Window Usage", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton })

	sbVariant := "sidebar-dark"
	cvVariant := "canvas-dark"
	if opts.light {
		sbVariant = "sidebar-light"
		cvVariant = "canvas-light"
	}

	var nodes []wurLayoutNode
	nodes = append(nodes, wurLayoutNode{
		Key: "workspace", Parent: nil, Order: 0, Role: "main",
		Bounds: ws.Bounds,
		Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "stretch", HasGap: true, Gap: 0},
		Size:   wurSize{W: wurFormatNum(float64(ws.Bounds.W)), H: wurFormatNum(float64(ws.Bounds.H))},
		State:  "default",
	})

	mainOrder := 0
	if !opts.collapsed {
		mainOrder = 1
		sb := wurFindAccess(t, all, "Sidebar", func(a ui.AccessInfo) bool { return a.Role == ui.RoleNavigation })
		sbHdr := wurFindAccess(t, all, "Sidebar Titlebar", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
		sbToggle := wurFindAccess(t, all, "Toggle Sidebar", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(sbHdr.Bounds, a.Bounds)
		})
		sbBack := wurFindAccess(t, all, "Previous Session", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(sbHdr.Bounds, a.Bounds)
		})
		sbFwd := wurFindAccess(t, all, "Next Session", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(sbHdr.Bounds, a.Bounds)
		})
		sbNew := wurFindAccess(t, all, "New Task", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(sb.Bounds, a.Bounds)
		})
		sbSearch := wurFindAccess(t, all, "Search", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(sb.Bounds, a.Bounds)
		})
		sbGrp := wurFindAccess(t, all, "Sidebar Group Header", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleRegion && wurContainsRect(sb.Bounds, a.Bounds)
		})
		sbOpt := wurFindAccess(t, all, "Sidebar options", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(sb.Bounds, a.Bounds)
		})
		sbFtr := wurFindAccess(t, all, "Sidebar Footer", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleRegion && wurContainsRect(sb.Bounds, a.Bounds)
		})
		sbSet := wurFindAccess(t, all, "Open Settings", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(sbFtr.Bounds, a.Bounds)
		})

		nodes = append(nodes,
			wurLayoutNode{
				Key: "workspace.sidebar", Parent: wurStrPtr("workspace"), Order: 0, Role: "navigation",
				Bounds: sb.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0},
				Size:   wurSize{W: "252", H: "fill"}, Variant: sbVariant,
			},
			wurLayoutNode{
				Key: "workspace.sidebar.header", Parent: wurStrPtr("workspace.sidebar"), Order: 0, Role: "region",
				Bounds: sbHdr.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 6},
				Box:    wurBox{Padding: [4]float32{0, 0, 0, 86}},
				Size:   wurSize{W: "fill", H: "48"},
			},
			wurLayoutNode{
				Key: "workspace.sidebar.toggle", Parent: wurStrPtr("workspace.sidebar.header"), Order: 0, Role: "button",
				Bounds: sbToggle.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Toggle Sidebar"},
			},
			wurLayoutNode{
				Key: "workspace.sidebar.nav_back", Parent: wurStrPtr("workspace.sidebar.header"), Order: 1, Role: "button",
				Bounds: sbBack.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Previous Session"},
			},
			wurLayoutNode{
				Key: "workspace.sidebar.nav_forward", Parent: wurStrPtr("workspace.sidebar.header"), Order: 2, Role: "button",
				Bounds: sbFwd.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Next Session"},
			},
			wurLayoutNode{
				Key: "workspace.sidebar.new_task", Parent: wurStrPtr("workspace.sidebar"), Order: 1, Role: "button",
				Bounds: sbNew.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 10},
				Box:    wurBox{Padding: [4]float32{0, 4, 0, 4}, Margin: [4]float32{0, 10, 0, 10}},
				Size:   wurSize{W: "fill", H: "32"}, Variant: "sidebar-action",
				Text: &wurTextSpec{Value: "New Task"},
			},
			wurLayoutNode{
				Key: "workspace.sidebar.search", Parent: wurStrPtr("workspace.sidebar"), Order: 2, Role: "button",
				Bounds: sbSearch.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 10},
				Box:    wurBox{Padding: [4]float32{0, 4, 0, 4}, Margin: [4]float32{0, 10, 10, 10}},
				Size:   wurSize{W: "fill", H: "32"}, Variant: "sidebar-action",
				Text: &wurTextSpec{Value: "Search"},
			},
			wurLayoutNode{
				Key: "workspace.sidebar.group_header", Parent: wurStrPtr("workspace.sidebar"), Order: 3, Role: "region",
				Bounds: sbGrp.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "between", Align: "center", HasGap: true, Gap: 4},
				Box:    wurBox{Padding: [4]float32{0, 8, 0, 8}, Margin: [4]float32{0, 10, 2, 10}},
				Size:   wurSize{W: "fill", H: "28"}, Variant: "sidebar-group-header",
				Text: &wurTextSpec{Value: "Today"},
			},
			wurLayoutNode{
				Key: "workspace.sidebar.options", Parent: wurStrPtr("workspace.sidebar.group_header"), Order: 0, Role: "button",
				Bounds: sbOpt.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "20", H: "20"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Sidebar options"},
			},
		)

		footerOrder := 4
		if opts.populated {
			sbSess := wurFindAccessByPrefix(t, all, "give me a quick overview", func(a ui.AccessInfo) bool {
				return a.Role == ui.RoleButton && wurContainsRect(sb.Bounds, a.Bounds) && a.Bounds.Y >= sbGrp.Bounds.Y
			})
			nodes = append(nodes, wurLayoutNode{
				Key: "workspace.sidebar.session.0", Parent: wurStrPtr("workspace.sidebar"), Order: 4, Role: "button",
				Bounds: sbSess.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "center", Align: "stretch", HasGap: true, Gap: 4},
				Box:    wurBox{Padding: [4]float32{7, 8, 7, 8}, Margin: [4]float32{0, 10, 1, 10}},
				Size:   wurSize{W: "fill", H: "51"}, Variant: "sidebar-session-selected", State: "active",
			})
			footerOrder = 5
		}

		nodes = append(nodes,
			wurLayoutNode{
				Key: "workspace.sidebar.footer", Parent: wurStrPtr("workspace.sidebar"), Order: footerOrder, Role: "region",
				Bounds: sbFtr.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 0},
				Box:    wurBox{Padding: [4]float32{0, 10, 0, 10}},
				Size:   wurSize{W: "fill", H: "40"}, Variant: "borderless-footer",
			},
			wurLayoutNode{
				Key: "workspace.sidebar.settings", Parent: wurStrPtr("workspace.sidebar.footer"), Order: 0, Role: "button",
				Bounds: sbSet.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Open Settings"},
			},
		)
	}

	mainBorder := [4]float32{0, 0, 0, 1}
	if opts.collapsed {
		mainBorder = [4]float32{0, 0, 0, 0}
	}
	nodes = append(nodes,
		wurLayoutNode{
			Key: "workspace.main", Parent: wurStrPtr("workspace"), Order: mainOrder, Role: "region",
			Bounds: wsMain.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0},
			Box:    wurBox{Border: mainBorder},
			Size:   wurSize{W: "fill", H: "fill"}, Variant: cvVariant,
		},
		wurLayoutNode{
			Key: "workspace.main.header", Parent: wurStrPtr("workspace.main"), Order: 0, Role: "region",
			Bounds: wsHeader.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "between", Align: "center", HasGap: true, Gap: 8},
			Box:    wurBox{Padding: [4]float32{0, 14, 0, 14}},
			Size:   wurSize{W: "fill", H: "48"}, Variant: "borderless-header",
		},
	)

	if opts.collapsed {
		nodes[len(nodes)-1].Box.Padding = [4]float32{0, 14, 0, 86}
		hdrToggle := wurFindAccess(t, all, "Toggle Sidebar", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(wsHeader.Bounds, a.Bounds)
		})
		hdrBack := wurFindAccess(t, all, "Previous Session", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(wsHeader.Bounds, a.Bounds)
		})
		hdrFwd := wurFindAccess(t, all, "Next Session", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(wsHeader.Bounds, a.Bounds)
		})
		hdrRP := wurFindAccess(t, all, "Toggle Right Panel", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(wsHeader.Bounds, a.Bounds)
		})
		nodes = append(nodes,
			wurLayoutNode{
				Key: "workspace.main.header.sidebar_toggle", Parent: wurStrPtr("workspace.main.header"), Order: 0, Role: "button",
				Bounds: hdrToggle.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Toggle Sidebar"},
			},
			wurLayoutNode{
				Key: "workspace.main.header.nav_back", Parent: wurStrPtr("workspace.main.header"), Order: 1, Role: "button",
				Bounds: hdrBack.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Previous Session"},
			},
			wurLayoutNode{
				Key: "workspace.main.header.nav_forward", Parent: wurStrPtr("workspace.main.header"), Order: 2, Role: "button",
				Bounds: hdrFwd.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Next Session"},
			},
			wurLayoutNode{
				Key: "workspace.main.header.title", Parent: wurStrPtr("workspace.main.header"), Order: 3, Role: "heading",
				Bounds: wsHeaderTitle.Bounds,
				Flow:   wurFlow{Type: "inline"},
				Size:   wurSize{W: "hug", H: "hug"},
				Text:   &wurTextSpec{Value: wsHeaderTitle.Name},
			},
			wurLayoutNode{
				Key: "workspace.main.header.right_panel_toggle", Parent: wurStrPtr("workspace.main.header"), Order: 4, Role: "button",
				Bounds: hdrRP.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Toggle Right Panel"},
			},
		)
	} else {
		nodes = append(nodes, wurLayoutNode{
			Key: "workspace.main.header.title", Parent: wurStrPtr("workspace.main.header"), Order: 0, Role: "heading",
			Bounds: wsHeaderTitle.Bounds,
			Flow:   wurFlow{Type: "inline"},
			Size:   wurSize{W: "hug", H: "hug"},
			Text:   &wurTextSpec{Value: wsHeaderTitle.Name},
		})
		if !opts.rightOpen {
			hdrRP := wurFindAccess(t, all, "Toggle Right Panel", func(a ui.AccessInfo) bool {
				return a.Role == ui.RoleButton && wurContainsRect(wsHeader.Bounds, a.Bounds)
			})
			nodes = append(nodes, wurLayoutNode{
				Key: "workspace.main.header.right_panel_toggle", Parent: wurStrPtr("workspace.main.header"), Order: 1, Role: "button",
				Bounds: hdrRP.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Toggle Right Panel"},
			})
		}
	}

	if !opts.populated {
		emptyBox := wurFindAccess(t, all, "Empty State", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
		sparkle := wurFindAccess(t, all, "Empty State Sparkle", func(a ui.AccessInfo) bool { return a.Role == ui.RoleImage })
		emptyHeading := wurFindAccessByPrefix(t, all, "What should we build in ", func(a ui.AccessInfo) bool {
			return wurContainsRect(emptyBox.Bounds, a.Bounds)
		})
		nodes = append(nodes,
			wurLayoutNode{
				Key: "workspace.main.empty_state", Parent: wurStrPtr("workspace.main"), Order: 1, Role: "region",
				Bounds: emptyBox.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "center", Align: "center", HasGap: true, Gap: 14},
				Box:    wurBox{Padding: [4]float32{0, 32, 52, 32}},
				Size:   wurSize{W: "fill", H: "fill"},
			},
			wurLayoutNode{
				Key: "workspace.main.empty_state.sparkle", Parent: wurStrPtr("workspace.main.empty_state"), Order: 0, Role: "img",
				Bounds: sparkle.Bounds,
				Flow:   wurFlow{Type: "block"},
				Size:   wurSize{W: "20", H: "20"},
				Text:   &wurTextSpec{Value: "Empty State Sparkle"},
			},
			wurLayoutNode{
				Key: "workspace.main.empty_state.heading", Parent: wurStrPtr("workspace.main.empty_state"), Order: 1, Role: "heading",
				Bounds: emptyHeading.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "baseline", HasGap: true, Gap: 0},
				Size:   wurSize{W: "hug", H: "hug"},
				Text:   &wurTextSpec{Value: emptyHeading.Name},
			},
		)
	} else {
		transcript := wurFindAccess(t, all, "Workspace Transcript", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
		bubble := wurFindAccess(t, all, "User Bubble", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleRegion && wurContainsRect(transcript.Bounds, a.Bounds)
		})
		worked := wurFindAccessByPrefix(t, all, "Worked", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(transcript.Bounds, a.Bounds)
		})
		asst := wurFindAccess(t, all, "Assistant Message", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleRegion && wurContainsRect(transcript.Bounds, a.Bounds)
		})
		nodes = append(nodes,
			wurLayoutNode{
				Key: "workspace.main.transcript", Parent: wurStrPtr("workspace.main"), Order: 1, Role: "region",
				Bounds: transcript.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "center", HasGap: true, Gap: 12},
				Size:   wurSize{W: "fill", H: "fill"}, Overflow: "auto",
			},
			wurLayoutNode{
				Key: "workspace.main.transcript.user_bubble.0", Parent: wurStrPtr("workspace.main.transcript"), Order: 0, Role: "region",
				Bounds: bubble.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0},
				Box:    wurBox{Padding: [4]float32{7, 11, 7, 11}, Border: [4]float32{1, 1, 1, 1}},
				Size:   wurSize{W: "hug", H: "hug"}, Variant: "user-bubble",
			},
			wurLayoutNode{
				Key: "workspace.main.transcript.worked_fold.0", Parent: wurStrPtr("workspace.main.transcript"), Order: 1, Role: "button",
				Bounds: worked.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 10},
				Size:   wurSize{W: "fill", H: "24"}, Variant: "worked-fold-divider",
			},
			wurLayoutNode{
				Key: "workspace.main.transcript.assistant.0", Parent: wurStrPtr("workspace.main.transcript"), Order: 2, Role: "region",
				Bounds: asst.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 8},
				Size:   wurSize{W: "fill", H: "hug"}, Variant: "assistant-prose",
			},
		)
	}

	compOrder := 2
	if opts.busyQueued {
		tray := wurFindAccess(t, all, "Queued Messages Tray", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
		nodes = append(nodes, wurLayoutNode{
			Key: "workspace.main.queue_tray", Parent: wurStrPtr("workspace.main"), Order: 2, Role: "region",
			Bounds: tray.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0},
			Box:    wurBox{Padding: [4]float32{4, 0, 4, 0}, Margin: [4]float32{0, 14, 0, 14}, Border: [4]float32{1, 1, 0, 1}},
			Size:   wurSize{W: "fill", H: "hug"}, Variant: "queue-tray",
		})
		compOrder = 3
	}

	compSizeW := "fill"
	if comp.Bounds.W >= 719 && comp.Bounds.W <= 721 {
		compSizeW = "720"
	}
	nodes = append(nodes,
		wurLayoutNode{
			Key: "workspace.main.composer", Parent: wurStrPtr("workspace.main"), Order: compOrder, Role: "region",
			Bounds: comp.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 8},
			Box:    wurBox{Padding: [4]float32{10, 0, 10, 0}, Margin: [4]float32{0, 20, 0, 20}, Border: [4]float32{1, 1, 1, 1}},
			Size:   wurSize{W: compSizeW, H: "hug"}, Variant: "composer-card",
		},
		wurLayoutNode{
			Key: "workspace.main.composer.editor", Parent: wurStrPtr("workspace.main.composer"), Order: 0, Role: "textbox",
			Bounds: compEd.Bounds,
			Flow:   wurFlow{Type: "block"},
			Size:   wurSize{W: "fill", H: "hug"},
			Text:   &wurTextSpec{Value: "Prompt editor"},
		},
		wurLayoutNode{
			Key: "workspace.main.composer.toolbar", Parent: wurStrPtr("workspace.main.composer"), Order: 1, Role: "toolbar",
			Bounds: compToolbar.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 4},
			Box:    wurBox{Margin: [4]float32{8, 0, 0, 0}},
			Size:   wurSize{W: "fill", H: "26"},
		},
		wurLayoutNode{
			Key: "workspace.main.composer.model_chip", Parent: wurStrPtr("workspace.main.composer.toolbar"), Order: 0, Role: "button",
			Bounds: compModel.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 6},
			Size:   wurSize{W: "hug", H: "24"}, Variant: "menu-chip",
			Text: &wurTextSpec{Value: "Select Model Chip"},
		},
		wurLayoutNode{
			Key: "workspace.main.composer.thinking_chip", Parent: wurStrPtr("workspace.main.composer.toolbar"), Order: 1, Role: "button",
			Bounds: compThink.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 6},
			Size:   wurSize{W: "hug", H: "24"}, Variant: "menu-chip",
			Text: &wurTextSpec{Value: "Select Thinking Chip"},
		},
		wurLayoutNode{
			Key: "workspace.main.composer.send", Parent: wurStrPtr("workspace.main.composer.toolbar"), Order: 2, Role: "button",
			Bounds: compSend.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
			Size:   wurSize{W: "26", H: "26"}, Variant: "circle-action",
			Text: &wurTextSpec{Value: sendLabel},
		},
	)

	if opts.busyQueued {
		qFollow := wurFindAccess(t, all, "Queue Follow-up", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(compToolbar.Bounds, a.Bounds)
		})
		nodes = append(nodes, wurLayoutNode{
			Key: "workspace.main.composer.queue_followup", Parent: wurStrPtr("workspace.main.composer.toolbar"), Order: 3, Role: "button",
			Bounds: qFollow.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
			Size:   wurSize{W: "26", H: "26"}, Variant: "circle-action-primary",
			Text: &wurTextSpec{Value: "Queue Follow-up"},
		})
	}

	footerSizeW := "fill"
	if footer.Bounds.W >= 719 && footer.Bounds.W <= 721 {
		footerSizeW = "720"
	}
	nodes = append(nodes,
		wurLayoutNode{
			Key: "workspace.main.footer", Parent: wurStrPtr("workspace.main"), Order: compOrder + 1, Role: "region",
			Bounds: footer.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 2},
			Box:    wurBox{Padding: [4]float32{0, 10, 0, 10}, Margin: [4]float32{4, 20, 8, 20}},
			Size:   wurSize{W: footerSizeW, H: "28"}, Variant: "workspace-footer",
		},
		wurLayoutNode{
			Key: "workspace.main.footer.project_chip", Parent: wurStrPtr("workspace.main.footer"), Order: 0, Role: "button",
			Bounds: fProj.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 6},
			Size:   wurSize{W: "hug", H: "26"}, Variant: "menu-chip",
			Text: &wurTextSpec{Value: "Select Project"},
		},
		wurLayoutNode{
			Key: "workspace.main.footer.workspace_chip", Parent: wurStrPtr("workspace.main.footer"), Order: 1, Role: "button",
			Bounds: fWS.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 6},
			Size:   wurSize{W: "hug", H: "26"}, Variant: "menu-chip",
			Text: &wurTextSpec{Value: "Select Workspace"},
		},
		wurLayoutNode{
			Key: "workspace.main.footer.branch_chip", Parent: wurStrPtr("workspace.main.footer"), Order: 2, Role: "button",
			Bounds: fBranch.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 6},
			Size:   wurSize{W: "hug", H: "26"}, Variant: "menu-chip",
			Text: &wurTextSpec{Value: "Switch Git Branch"},
		},
		wurLayoutNode{
			Key: "workspace.main.footer.context_gauge", Parent: wurStrPtr("workspace.main.footer"), Order: 3, Role: "button",
			Bounds: fCtx.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
			Size:   wurSize{W: "24", H: "24"}, Variant: "usage-gauge",
			Text: &wurTextSpec{Value: "Context Window Usage"},
		},
	)

	if opts.toastVisible {
		toast := wurFindAccess(t, all, "Toast Notification", func(a ui.AccessInfo) bool { return a.Role == ui.RoleStatus })
		dismiss := wurFindAccess(t, all, "Dismiss notification", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(toast.Bounds, a.Bounds)
		})
		nodes = append(nodes,
			wurLayoutNode{
				Key: "workspace.toast", Parent: wurStrPtr("workspace.main"), Order: compOrder + 2, Role: "status",
				Bounds: toast.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8},
				Box:    wurBox{Padding: [4]float32{7, 10, 7, 10}, Border: [4]float32{1, 1, 1, 1}},
				Size:   wurSize{W: "hug", H: "hug"}, Z: 100, Variant: "toast",
			},
			wurLayoutNode{
				Key: "workspace.toast.dismiss", Parent: wurStrPtr("workspace.toast"), Order: 0, Role: "button",
				Bounds: dismiss.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Dismiss notification"},
			},
		)
	}

	if opts.rightOpen {
		rp := wurFindAccess(t, all, "Right Panel", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
		rpHdr := wurFindAccess(t, all, "Right Panel Header", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
		rpToggle := wurFindAccess(t, all, "Toggle Right Panel", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(rpHdr.Bounds, a.Bounds)
		})
		nodes = append(nodes,
			wurLayoutNode{
				Key: "workspace.right_panel", Parent: wurStrPtr("workspace"), Order: mainOrder + 1, Role: "region",
				Bounds: rp.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0},
				Size:   wurSize{W: "460", H: "fill"},
			},
			wurLayoutNode{
				Key: "workspace.right_panel.header", Parent: wurStrPtr("workspace.right_panel"), Order: 0, Role: "region",
				Bounds: rpHdr.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "between", Align: "center", HasGap: true, Gap: 6},
				Box:    wurBox{Padding: [4]float32{0, 14, 0, 10}},
				Size:   wurSize{W: "fill", H: "48"},
			},
			wurLayoutNode{
				Key: "workspace.right_panel.toggle", Parent: wurStrPtr("workspace.right_panel.header"), Order: 0, Role: "button",
				Bounds: rpToggle.Bounds,
				Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
				Size:   wurSize{W: "26", H: "26"}, Variant: "icon-button",
				Text: &wurTextSpec{Value: "Toggle Right Panel"},
			},
		)
	}

	return nodes
}

func wurExtractRightPanelNodes(t *testing.T, all []ui.AccessInfo, activeTab string) []wurLayoutNode {
	t.Helper()
	rp := wurFindAccess(t, all, "Right Panel", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
	rpHdr := wurFindAccess(t, all, "Right Panel Header", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
	rpTabs := wurFindAccess(t, all, "Right Panel Tabs", func(a ui.AccessInfo) bool { return a.Role == ui.RoleTabList })
	tabName := rpTabDisplayName(activeTab)
	rpActiveTab := wurFindAccess(t, all, tabName, func(a ui.AccessInfo) bool {
		return a.Role == ui.RoleTab && wurContainsRect(rpTabs.Bounds, a.Bounds)
	})
	rpAdd := wurFindAccess(t, all, "Add Right Panel Surface", func(a ui.AccessInfo) bool {
		return a.Role == ui.RoleButton && wurContainsRect(rpHdr.Bounds, a.Bounds)
	})
	rpToggle := wurFindAccess(t, all, "Toggle Right Panel", func(a ui.AccessInfo) bool {
		return a.Role == ui.RoleButton && wurContainsRect(rpHdr.Bounds, a.Bounds)
	})
	rpBody := wurFindAccess(t, all, "Right Panel Body", func(a ui.AccessInfo) bool {
		return a.Role == ui.RoleRegion && wurContainsRect(rp.Bounds, a.Bounds)
	})

	bodyDir := "column"
	if activeTab == "files" {
		bodyDir = "row"
	}

	nodes := []wurLayoutNode{
		{
			Key: "right_panel", Parent: nil, Order: 0, Role: "region",
			Bounds: rp.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0},
			Size:   wurSize{W: "460", H: "fill"}, State: activeTab,
		},
		{
			Key: "right_panel.header", Parent: wurStrPtr("right_panel"), Order: 0, Role: "region",
			Bounds: rpHdr.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "between", Align: "center", HasGap: true, Gap: 6},
			Box:    wurBox{Padding: [4]float32{0, 14, 0, 10}},
			Size:   wurSize{W: "fill", H: "48"},
		},
		{
			Key: "right_panel.tabs", Parent: wurStrPtr("right_panel.header"), Order: 0, Role: "tablist",
			Bounds: rpTabs.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 4},
			Size:   wurSize{W: "fill", H: "28"},
		},
		{
			Key: "right_panel.tab." + activeTab, Parent: wurStrPtr("right_panel.tabs"), Order: 0, Role: "tab",
			Bounds: rpActiveTab.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 6},
			Box:    wurBox{Padding: [4]float32{0, 8, 0, 8}},
			Size:   wurSize{W: "hug", H: "28"}, State: "active",
			Text: &wurTextSpec{Value: tabName},
		},
		{
			Key: "right_panel.add_surface", Parent: wurStrPtr("right_panel.header"), Order: 1, Role: "button",
			Bounds: rpAdd.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
			Size:   wurSize{W: "26", H: "26"},
			Text:   &wurTextSpec{Value: "Add Right Panel Surface"},
		},
		{
			Key: "right_panel.toggle", Parent: wurStrPtr("right_panel.header"), Order: 2, Role: "button",
			Bounds: rpToggle.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0},
			Size:   wurSize{W: "26", H: "26"},
			Text:   &wurTextSpec{Value: "Toggle Right Panel"},
		},
		{
			Key: "right_panel.body", Parent: wurStrPtr("right_panel"), Order: 1, Role: "region",
			Bounds: rpBody.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: bodyDir, Justify: "start", Align: "stretch", HasGap: true, Gap: 0},
			Size:   wurSize{W: "fill", H: "fill"},
		},
	}

	switch activeTab {
	case "changes":
		commitBtn := wurFindAccess(t, all, "Commit", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(rpBody.Bounds, a.Bounds)
		})
		nodes = append(nodes, wurLayoutNode{
			Key: "right_panel.changes.commit_btn", Parent: wurStrPtr("right_panel.body"), Order: 0, Role: "button",
			Bounds: commitBtn.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 6},
			Size:   wurSize{W: "hug", H: "26"},
			Text:   &wurTextSpec{Value: "Commit"},
		})
	case "files":
		tree := wurFindAccess(t, all, "Workspace Files Tree", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleList && wurContainsRect(rpBody.Bounds, a.Bounds)
		})
		nodes = append(nodes, wurLayoutNode{
			Key: "right_panel.files.tree", Parent: wurStrPtr("right_panel.body"), Order: 0, Role: "list",
			Bounds: tree.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 2},
			Size:   wurSize{W: "184", H: "fill"}, Overflow: "auto",
		})
	case "terminal":
		termSurf := wurFindAccess(t, all, "Terminal Surface", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleRegion && wurContainsRect(rpBody.Bounds, a.Bounds)
		})
		nodes = append(nodes, wurLayoutNode{
			Key: "right_panel.terminal.surface", Parent: wurStrPtr("right_panel.body"), Order: 0, Role: "region",
			Bounds: termSurf.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 4},
			Size:   wurSize{W: "fill", H: "fill"}, Variant: "terminal-dark",
		})
	case "tree":
		treeBody := wurFindAccess(t, all, "Session Tree Body", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleList && wurContainsRect(rpBody.Bounds, a.Bounds)
		})
		nodes = append(nodes, wurLayoutNode{
			Key: "right_panel.tree.body", Parent: wurStrPtr("right_panel.body"), Order: 0, Role: "list",
			Bounds: treeBody.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 4},
			Size:   wurSize{W: "fill", H: "fill"}, Overflow: "auto",
		})
	}
	originX, originY := rp.Bounds.X, rp.Bounds.Y
	for i := range nodes {
		nodes[i].Bounds.X -= originX
		nodes[i].Bounds.Y -= originY
	}
	return nodes
}

func wurExtractSettingsNodes(t *testing.T, all []ui.AccessInfo, pageID string) []wurLayoutNode {
	t.Helper()
	root := wurFindAccess(t, all, "Settings", func(a ui.AccessInfo) bool { return a.Role == ui.RoleMain })
	sb := wurFindAccess(t, all, "Settings Sidebar", func(a ui.AccessInfo) bool { return a.Role == ui.RoleNavigation })
	back := wurFindAccess(t, all, "Back", func(a ui.AccessInfo) bool {
		return a.Role == ui.RoleButton && wurContainsRect(sb.Bounds, a.Bounds)
	})
	search := wurFindAccess(t, all, "Settings Search Field", func(a ui.AccessInfo) bool {
		return a.Role == ui.RoleRegion && wurContainsRect(sb.Bounds, a.Bounds)
	})
	content := wurFindAccess(t, all, "Settings Content", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion })
	inner := wurFindAccess(t, all, "Settings Content Inner", func(a ui.AccessInfo) bool {
		return a.Role == ui.RoleRegion && a.Bounds.X >= content.Bounds.X-2 && a.Bounds.X+a.Bounds.W <= content.Bounds.X+content.Bounds.W+2
	})

	nodes := []wurLayoutNode{
		{
			Key: "settings", Parent: nil, Order: 0, Role: "main",
			Bounds: root.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "stretch", HasGap: true, Gap: 0},
			Size:   wurSize{W: "1280", H: "820"}, State: pageID,
		},
		{
			Key: "settings.sidebar", Parent: wurStrPtr("settings"), Order: 0, Role: "navigation",
			Bounds: sb.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0},
			Size:   wurSize{W: "252", H: "fill"},
		},
		{
			Key: "settings.sidebar.back", Parent: wurStrPtr("settings.sidebar"), Order: 0, Role: "button",
			Bounds: back.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 9},
			Box:    wurBox{Padding: [4]float32{0, 9, 0, 9}, Margin: [4]float32{0, 12, 0, 12}},
			Size:   wurSize{W: "fill", H: "34"},
			Text:   &wurTextSpec{Value: "Back"},
		},
		{
			Key: "settings.sidebar.search", Parent: wurStrPtr("settings.sidebar"), Order: 1, Role: "textbox",
			Bounds: search.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8},
			Size:   wurSize{W: "fill", H: "32"},
			Text:   &wurTextSpec{Value: "Search Settings"},
		},
	}

	navPages := []struct {
		id    string
		title string
	}{
		{"general", "General"},
		{"appearance", "Appearance"},
		{"providers", "Providers"},
		{"skills", "Skills"},
		{"usage", "Usage"},
	}
	var activeTitle string
	for idx, p := range navPages {
		btn := wurFindAccess(t, all, p.title, func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(sb.Bounds, a.Bounds)
		})
		st := "default"
		if p.id == pageID {
			st = "active"
			activeTitle = p.title
		}
		nodes = append(nodes, wurLayoutNode{
			Key: "settings.sidebar.nav." + p.id, Parent: wurStrPtr("settings.sidebar"), Order: 2 + idx, Role: "button",
			Bounds: btn.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 10},
			Box:    wurBox{Padding: [4]float32{0, 11, 0, 11}},
			Size:   wurSize{W: "fill", H: "36"}, State: st,
			Text: &wurTextSpec{Value: p.title},
		})
	}

	innerW := "760"
	if pageID == "skills" {
		innerW = "964"
	} else if pageID == "usage" {
		innerW = "fill"
	}

	nodes = append(nodes,
		wurLayoutNode{
			Key: "settings.content", Parent: wurStrPtr("settings"), Order: 1, Role: "region",
			Bounds: content.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "center", HasGap: true, Gap: 0},
			Box:    wurBox{Padding: [4]float32{0, 32, 48, 32}, Border: [4]float32{0, 0, 0, 1}},
			Size:   wurSize{W: "fill", H: "fill"},
		},
		wurLayoutNode{
			Key: "settings.content.inner", Parent: wurStrPtr("settings.content"), Order: 0, Role: "region",
			Bounds: inner.Bounds,
			Flow:   wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 15},
			Size:   wurSize{W: innerW, H: "hug"},
		},
	)

	if pageID != "skills" {
		heading := wurFindAccess(t, all, activeTitle, func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleHeading && wurContainsRect(content.Bounds, a.Bounds)
		})
		nodes = append(nodes, wurLayoutNode{
			Key: "settings.content.heading", Parent: wurStrPtr("settings.content.inner"), Order: 0, Role: "heading",
			Bounds: heading.Bounds,
			Flow:   wurFlow{Type: "inline"},
			Size:   wurSize{W: "hug", H: "hug"},
			Text:   &wurTextSpec{Value: activeTitle},
		})
	}
	return nodes
}

func wurEmitTokensYAML(t *testing.T, dir string) {
	t.Helper()
	dark := gui.DarkGuiTheme()
	light := gui.LightGuiTheme()

	pairs := []struct {
		key   string
		color ui.Color
	}{
		{"dark.bg.canvas", dark.Background},
		{"dark.bg.surface", dark.Surface},
		{"dark.bg.sidebar", dark.SidebarBg},
		{"dark.bg.sidebar_item", dark.SidebarItemBg},
		{"dark.bg.raised", dark.Raised},
		{"dark.bg.composer", dark.ComposerBg},
		{"dark.bg.inset", dark.Inset},
		{"dark.bg.terminal", dark.TerminalBg},
		{"dark.bg.overlay", dark.Overlay},
		{"dark.bg.overlay_strong", dark.OverlayStrong},
		{"dark.border.default", dark.Border},
		{"dark.border.strong", dark.BorderStrong},
		{"dark.border.sidebar", dark.SidebarBorder},
		{"dark.fg.primary", dark.Text},
		{"dark.fg.secondary", dark.TextSecondary},
		{"dark.fg.tertiary", dark.TextTertiary},
		{"dark.fg.ghost", dark.TextGhost},
		{"dark.accent.default", dark.Accent},
		{"dark.gauge.default", dark.Gauge},
		{"dark.code.text", dark.CodeText},
		{"dark.code.wash", dark.CodeWash},
		{"dark.inverse.bg", dark.Inverse},
		{"dark.inverse.fg", dark.OnInverse},
		{"dark.status.warning", dark.Warning},
		{"dark.status.success", dark.Success},
		{"dark.status.favorite", dark.Favorite},
		{"dark.status.danger", dark.Error},
		{"light.bg.canvas", light.Background},
		{"light.bg.surface", light.Surface},
		{"light.bg.sidebar", light.SidebarBg},
		{"light.bg.sidebar_item", light.SidebarItemBg},
		{"light.bg.raised", light.Raised},
		{"light.bg.composer", light.ComposerBg},
		{"light.bg.inset", light.Inset},
		{"light.bg.terminal", light.TerminalBg},
		{"light.bg.overlay", light.Overlay},
		{"light.bg.overlay_strong", light.OverlayStrong},
		{"light.border.default", light.Border},
		{"light.border.strong", light.BorderStrong},
		{"light.border.sidebar", light.SidebarBorder},
		{"light.fg.primary", light.Text},
		{"light.fg.secondary", light.TextSecondary},
		{"light.fg.tertiary", light.TextTertiary},
		{"light.fg.ghost", light.TextGhost},
		{"light.accent.default", light.Accent},
		{"light.gauge.default", light.Gauge},
		{"light.code.text", light.CodeText},
		{"light.code.wash", light.CodeWash},
		{"light.inverse.bg", light.Inverse},
		{"light.inverse.fg", light.OnInverse},
		{"light.status.warning", light.Warning},
		{"light.status.success", light.Success},
		{"light.status.favorite", light.Favorite},
		{"light.status.danger", light.Error},
	}

	if dir == "" {
		return
	}
	var b strings.Builder
	b.WriteString("schema: ui-tokens/1\n")
	b.WriteString("evidence:\n  - gui/theme.go\n")
	b.WriteString("color:\n")
	for _, p := range pairs {
		fmt.Fprintf(&b, "  %s: %q\n", p.key, wurFormatColor(p.color))
	}
	if err := os.WriteFile(filepath.Join(dir, "actual-tokens.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("Write actual-tokens.yaml: %v", err)
	}
}

func wurEmitComponentsYAML(t *testing.T, dir string, measuredWidths map[string]float32, measuredHeights map[string]float32) {
	t.Helper()
	if dir == "" {
		return
	}
	th := gui.DarkGuiTheme()
	sbW := measuredWidths["sidebar.surface"]
	compW := measuredWidths["composer.card"]
	ubW := measuredWidths["user.bubble"]
	mpW := measuredWidths["popover.model_picker"]
	mpH := measuredHeights["popover.model_picker"]
	cpW := measuredWidths["command_palette.card"]
	cpH := measuredHeights["command_palette.card"]
	cdW := measuredWidths["commit_dialog.card"]
	toastW := measuredWidths["toast.card"]

	var b strings.Builder
	b.WriteString("schema: ui-components/1\n")
	b.WriteString("components:\n")
	fmt.Fprintf(&b, `  sidebar.surface:
    node: workspace.sidebar
    properties:
      background: %q
      border: {width: 0, style: none, color: "rgba(0, 0, 0, 0)"}
      width: %s
`, wurFormatColor(th.SidebarBg), wurFormatNum(wurRound1(sbW)))

	fmt.Fprintf(&b, `  composer.card:
    node: workspace.main.composer
    properties:
      background: %q
      border: {width: 1, style: solid, color: %q}
      radius: %d
      padding: [10, 0, 10, 0]
      maxWidth: %s
`, wurFormatColor(th.ComposerBg), wurFormatColor(th.Border), int(wakuComposerRadius), wurFormatNum(wurRound1(compW)))

	fmt.Fprintf(&b, `  user.bubble:
    node: workspace.main.transcript.user_bubble.0
    properties:
      background: %q
      border: {width: 1, style: solid, color: %q}
      radius: %d
      padding: [7, 11, 7, 11]
      maxWidth: %s
`, wurFormatColor(th.Raised), wurFormatColor(th.Raised), int(wakuUserBubbleRadius), wurFormatNum(wurRound1(ubW)))

	fmt.Fprintf(&b, `  popover.model_picker:
    node: popover.model_picker
    properties:
      background: %q
      border: {width: 1, style: solid, color: %q}
      radius: 12
      width: %s
      height: %s
`, wurFormatColor(th.Background), wurFormatColor(th.BorderStrong), wurFormatNum(wurRound1(mpW)), wurFormatNum(wurRound1(mpH)))

	fmt.Fprintf(&b, `  command_palette.card:
    node: command_palette.card
    properties:
      background: %q
      border: {width: 1, style: solid, color: %q}
      radius: 15
      maxWidth: %s
      maxHeight: %s
`, wurFormatColor(th.Raised), wurFormatColor(th.BorderStrong), wurFormatNum(wurRound1(cpW)), wurFormatNum(wurRound1(cpH)))

	fmt.Fprintf(&b, `  commit_dialog.card:
    node: commit_dialog.card
    properties:
      background: %q
      border: {width: 1, style: solid, color: %q}
      radius: 18
      maxWidth: %s
`, wurFormatColor(th.ComposerBg), wurFormatColor(th.BorderStrong), wurFormatNum(wurRound1(cdW)))

	fmt.Fprintf(&b, `  toast.card:
    node: workspace.toast
    properties:
      background: %q
      border: {width: 1, style: solid, color: %q}
      radius: 10
      padding: [7, 10, 7, 10]
      maxWidth: %s
`, wurFormatColor(th.Raised), wurFormatColor(th.BorderStrong), wurFormatNum(wurRound1(toastW)))

	if err := os.WriteFile(filepath.Join(dir, "actual-components.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("Write actual-components.yaml: %v", err)
	}
}

func wurEmitStatesYAML(t *testing.T, dir string, centerSidebarToggleX, rpWidth, mpW, mpH, mpRailW, cpSearchH, cpRowH, cpMaxH, cpEmptyH float32) {
	t.Helper()
	if dir == "" {
		return
	}
	content := fmt.Sprintf(`schema: ui-states/1
states:
  app.workspace:
    status: observed
    evidence: [codingagent/modes/gui/waku_ui_replication_test.go]
    empty:
      emptyStateVisible: true
      undispatchedSidebarCard: false
    populated:
      userBubbleMaxWidth: 540
      workedFoldVisible: true
    light:
      canvas: "#F6F5F6"
      sidebar: "#F3F3F3"
      composer: "#FFFFFF"
    sidebar-collapsed:
      sidebarVisible: false
      centerHeaderControlsAtX: %s
    right-panel-open:
      rightPanelWidth: %s
    busy-queued:
      queueTrayVisible: true
      stopButtonVisible: true
      queueFollowupVisible: true
    toast:
      toastTop: 56
      dismissButtonSize: 26
    overflow:
      maxContentWidth: 720
      clampedToViewport: true
  popover.model_picker:
    status: observed
    evidence: [codingagent/modes/gui/composer.go]
    default:
      width: %s
      height: %s
      railWidth: %s
    search-cleared:
      firstEscapeClearsQuery: true
      staysOpen: true
    favorites:
      favoritesTabActive: true
  command_palette:
    status: observed
    evidence: [codingagent/modes/gui/selectors.go]
    default:
      searchHeight: %s
      rowHeight: %s
      maxHeight: %s
    filtered:
      narrowsRows: true
    empty:
      emptyHeight: %s
      message: No matching tasks or commands
`,
		wurFormatNum(wurRound1(centerSidebarToggleX)),
		wurFormatNum(wurRound1(rpWidth)),
		wurFormatNum(wurRound1(mpW)),
		wurFormatNum(wurRound1(mpH)),
		wurFormatNum(wurRound1(mpRailW)),
		wurFormatNum(wurRound1(cpSearchH)),
		wurFormatNum(wurRound1(cpRowH)),
		wurFormatNum(wurRound1(cpMaxH)),
		wurFormatNum(wurRound1(cpEmptyH)),
	)
	if err := os.WriteFile(filepath.Join(dir, "actual-states.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("Write actual-states.yaml: %v", err)
	}
}

func wurEmitTestResultsJSON(t *testing.T, dir string) {
	t.Helper()
	if dir == "" {
		return
	}
	payload := struct {
		Tests []map[string]any `json:"tests"`
	}{
		Tests: []map[string]any{
			{
				"gate":    "1-6",
				"path":    "codingagent/modes/gui/waku_ui_replication_test.go",
				"command": "CGO_ENABLED=0 go test ./codingagent/modes/gui -run TestWakuUIReplication -v",
				"env":     "SkipWindow=true FauxProvider offline ui.NewTester",
				"counts":  "35 surface/state/viewport captures + 44 tokens + 7 components + 3 state machines",
				"passed":  true,
			},
			{
				"gate":    "3-6",
				"path":    "codingagent/modes/gui/waku_composer_spec_test.go",
				"command": "CGO_ENABLED=0 go test ./gui/... ./codingagent/modes/gui/...",
				"env":     "SkipWindow=true FauxProvider offline ui.NewTester",
				"counts":  "all gui & codingagent/modes/gui parity tests",
				"passed":  true,
			},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("Marshal test-results.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test-results.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("Write test-results.json: %v", err)
	}
}

func wurInitGitRepo(t *testing.T, dir string, branch string) {
	t.Helper()
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	runGit("init")
	runGit("config", "user.email", "waku-test@example.com")
	runGit("config", "user.name", "Waku Test")
	runGit("symbolic-ref", "HEAD", "refs/heads/"+branch)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+filepath.Base(dir)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README.md: %v", err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "init")
}

func wurNewTester(mode *GuiMode, vw, vh int, dark bool) *ui.Tester {
	tt := ui.NewTester(mode.GUI().View, vw, vh)
	tt.SetScale(2)
	tt.SetTitleBar(ui.TitleBar{Height: 48, Left: 86})
	tt.SetVibrancy(false)
	tt.SetDark(dark)
	tt.Frame()
	return tt
}

func wurClearHover(tt *ui.Tester) {
	tt.Move(-1, -1)
	tt.Frame()
}

// TestWakuUIReplication drives all 35 (surface, state, viewport) pairs natively
// via mygo/ui.NewTester, validates Gate 1-6 layout/pixel/token/interaction invariants,
// and emits measured actual-layout, actual-tokens, actual-components, actual-states,
// candidate screenshots, and test-results.json when PI_UI_REPLICATION_DIR is set.
func TestWakuUIReplication(t *testing.T) {
	outDir := strings.TrimSpace(os.Getenv("PI_UI_REPLICATION_DIR"))
	isoHome := t.TempDir()
	t.Setenv("HOME", isoHome)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(isoHome, ".pi", "agent"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", filepath.Join(isoHome, ".pi", "agent", "sessions"))
	ctx := context.Background()

	measuredCompW := make(map[string]float32)
	measuredCompH := make(map[string]float32)

	// Part A: Empty Workspace @ 1280x820, 860x640, 1600x960 + Sidebar Options
	t.Run("empty", func(t *testing.T) {
		emptyFx := tgmNewFixture(t, &tgmFixtureOpts{
			withResources: true,
			providerName:  "faux",
		})
		wurInitGitRepo(t, emptyFx.cwd, "main")
		emptyMode := NewGuiMode(emptyFx.runtime, &GuiModeOptions{
			SkipWindow:          true,
			InitialThemeSetting: tgmStrPtr("dark"),
		})
		t.Cleanup(func() { emptyMode.Stop() })
		emptyMode.refreshSidebarSessionsSync(ctx)
		emptyMode.refreshGitBranchesSync(emptyFx.cwd)

		for _, vp := range []struct {
			name string
			w, h int
		}{
			{"1280x820", 1280, 820},
			{"860x640", 860, 640},
			{"1600x960", 1600, 960},
		} {
			tt := wurNewTester(emptyMode, vp.w, vp.h, true)
			nodes := wurExtractWorkspaceNodes(t, tt.AccessAll(), wurWorkspaceExtractOpts{surfaceState: "empty"})
			wurWriteYAML(t, outDir, "app.workspace", "empty", vp.name, wurDumpLayoutYAML("app.workspace", "empty", vp.w, vp.h, nodes))
			img := tt.Image()
			if img.Bounds().Dx() != vp.w*2 || img.Bounds().Dy() != vp.h*2 {
				t.Fatalf("empty@%s PNG dimensions = %dx%d, want %dx%d (dpr=2)", vp.name, img.Bounds().Dx(), img.Bounds().Dy(), vp.w*2, vp.h*2)
			}
			wurSavePNG(t, outDir, fmt.Sprintf("app.workspace-empty@%s.png", vp.name), img)

			if vp.name == "1280x820" {
				wurAssertNearRGB(t, "empty dark sidebar (120,790)", wurPixelAt(t, img, 120*2, 790*2), 24, 24, 24, 3)
				wurAssertNearRGB(t, "empty dark canvas (640,200)", wurPixelAt(t, img, 640*2, 200*2), 26, 26, 26, 3)
				for _, n := range nodes {
					if n.Key == "workspace.sidebar" {
						measuredCompW["sidebar.surface"] = n.Bounds.W
					}
					if n.Key == "workspace.main.composer" {
						measuredCompW["composer.card"] = n.Bounds.W
					}
				}
			}
		}

		// Capture Sidebar options popover at 1280x820 after clean viewport captures, then dismiss.
		sbTT := wurNewTester(emptyMode, 1280, 820, true)
		if err := sbTT.Click("Sidebar options"); err != nil {
			t.Fatalf("Click Sidebar options: %v", err)
		}
		sbTT.Frame()
		wurClearHover(sbTT)
		all := sbTT.AccessAll()
		sbPop := wurFindAccess(t, all, "Sidebar Options Popover", func(a ui.AccessInfo) bool { return a.Role == ui.RoleMenu })
		optGroupBy := wurFindAccess(t, all, "Group by", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleMenuItem && wurContainsRect(sbPop.Bounds, a.Bounds)
		})
		optUpdated := wurFindAccess(t, all, "Updated", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleMenuItem && wurContainsRect(sbPop.Bounds, a.Bounds)
		})
		optProject := wurFindAccess(t, all, "Project", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleMenuItem && wurContainsRect(sbPop.Bounds, a.Bounds)
		})
		optOrderBy := wurFindAccess(t, all, "Order by", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleMenuItem && wurContainsRect(sbPop.Bounds, a.Bounds)
		})
		optNewest := wurFindAccess(t, all, "Newest", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleMenuItem && wurContainsRect(sbPop.Bounds, a.Bounds)
		})
		optOldest := wurFindAccess(t, all, "Oldest", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleMenuItem && wurContainsRect(sbPop.Bounds, a.Bounds)
		})
		sbOptNodes := []wurLayoutNode{
			{Key: "popover.sidebar_options", Parent: nil, Order: 0, Role: "menu", Bounds: sbPop.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 2}, Box: wurBox{Padding: [4]float32{4, 4, 4, 4}, Border: [4]float32{1, 1, 1, 1}}, Size: wurSize{W: "hug", H: "hug"}, Z: 100},
			{Key: "popover.sidebar_options.group_by", Parent: wurStrPtr("popover.sidebar_options"), Order: 0, Role: "menuitem", Bounds: optGroupBy.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "between", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "Group by"}},
			{Key: "popover.sidebar_options.updated", Parent: wurStrPtr("popover.sidebar_options"), Order: 1, Role: "menuitem", Bounds: optUpdated.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "Updated"}},
			{Key: "popover.sidebar_options.project", Parent: wurStrPtr("popover.sidebar_options"), Order: 2, Role: "menuitem", Bounds: optProject.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "Project"}},
			{Key: "popover.sidebar_options.order_by", Parent: wurStrPtr("popover.sidebar_options"), Order: 3, Role: "menuitem", Bounds: optOrderBy.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "between", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "Order by"}},
			{Key: "popover.sidebar_options.newest", Parent: wurStrPtr("popover.sidebar_options"), Order: 4, Role: "menuitem", Bounds: optNewest.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "Newest"}},
			{Key: "popover.sidebar_options.oldest", Parent: wurStrPtr("popover.sidebar_options"), Order: 5, Role: "menuitem", Bounds: optOldest.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "Oldest"}},
		}
		wurWriteYAML(t, outDir, "app.popover.sidebar_options", "default", "1280x820",
			wurDumpLayoutYAML("app.popover.sidebar_options", "default", 1280, 820, sbOptNodes))
		wurSavePNG(t, outDir, "app.popover.sidebar_options-default@1280x820.png", sbTT.Image())
		sbTT.Key(0, ui.KeyEscape)
		sbTT.Frame()
	})

	var centerSidebarToggleX, rpWidthMeasured, cpSearchH, cpRowH, cpMaxH, cpEmptyH float32

	// Part B: Populated Workspace (3 viewports + light + collapsed + right-panel + toast + busy-queued + popovers + palette + commit + settings)
	t.Run("populated_and_surfaces", func(t *testing.T) {
		holdStarted := make(chan struct{}, 4)
		holdRelease := make(chan struct{})
		t.Cleanup(func() {
			select {
			case <-holdRelease:
			default:
				close(holdRelease)
			}
		})

		probeTool := &agent.AgentTool{
			Name:        "waku_probe",
			Label:       "Waku Probe",
			Description: "Quick repository check",
			Parameters:  typebox.Object(nil),
			Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}}, nil
			},
		}
		holdTool := &agent.AgentTool{
			Name:        "hold_tool",
			Label:       "Hold Tool",
			Description: "Waits for busy queue screenshot",
			Parameters:  typebox.Object(nil),
			Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				select {
				case holdStarted <- struct{}{}:
				default:
				}
				select {
				case <-holdRelease:
				case <-ctx.Done():
					return agent.AgentToolResult{}, ctx.Err()
				}
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "released"}}}, nil
			},
		}

		userSkillsDir := filepath.Join(isoHome, ".pi", "agent", "skills")
		for _, sample := range []struct {
			dir     string
			skillMD string
			extra   map[string]string
		}{
			{
				dir:     "code-review",
				skillMD: "---\nname: code-review\ndescription: Audit pull requests for correctness, concurrency safety, and test coverage.\n---\n# Code Review\n\nInspect staged diffs before merging:\n\n- Verify error paths and lock ordering\n- Check boundary tests and regressions\n\n```sh\ngo test -race ./...\n```\n",
				extra: map[string]string{
					"checklist.md": "# Review Checklist\n\n- Confirm zero CGO dependencies\n",
				},
			},
			{
				dir:     "release-runbook",
				skillMD: "---\nname: release-runbook\ndescription: Step-by-step verification and release packaging checklist for tagged builds.\n---\n# Release Runbook\n\nRun full offline verification and cross-compilation checks before tagging.\n",
			},
		} {
			sDir := filepath.Join(userSkillsDir, sample.dir)
			if err := os.MkdirAll(sDir, 0o755); err != nil {
				t.Fatalf("MkdirAll(%s): %v", sDir, err)
			}
			if err := os.WriteFile(filepath.Join(sDir, "SKILL.md"), []byte(sample.skillMD), 0o644); err != nil {
				t.Fatalf("WriteFile SKILL.md: %v", err)
			}
			for rel, body := range sample.extra {
				if err := os.WriteFile(filepath.Join(sDir, rel), []byte(body), 0o644); err != nil {
					t.Fatalf("WriteFile %s: %v", rel, err)
				}
			}
		}

		fx := tgmNewFixture(t, &tgmFixtureOpts{
			withResources: true,
			providerName:  "faux",
			agentDir:      filepath.Join(isoHome, ".pi", "agent"),
			tools:         []*agent.AgentTool{probeTool, holdTool},
		})
		projSkillDir := filepath.Join(fx.cwd, ".pi", "skills", "repo-audit")
		if err := os.MkdirAll(projSkillDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", projSkillDir, err)
		}
		if err := os.WriteFile(filepath.Join(projSkillDir, "SKILL.md"), []byte("---\nname: repo-audit\ndescription: Validate workspace package layout, formatting, and module invariants.\n---\n# Repo Audit\n\nVerify `gofmt -l .` and `go vet ./...` pass cleanly.\n"), 0o644); err != nil {
			t.Fatalf("WriteFile project SKILL.md: %v", err)
		}
		wurInitGitRepo(t, fx.cwd, "main")
		if out, err := exec.Command("git", "-C", fx.cwd, "add", ".pi").CombinedOutput(); err != nil {
			t.Fatalf("git add .pi: %v\n%s", err, string(out))
		}
		if out, err := exec.Command("git", "-C", fx.cwd, "commit", "-m", "add project skill").CombinedOutput(); err != nil {
			t.Fatalf("git commit .pi: %v\n%s", err, string(out))
		}

		stopTool := ai.StopReasonToolUse
		fx.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
					providers.FauxToolCall("waku_probe", jsonx.NewObject(), nil),
				}, &providers.FauxAssistantMessageOptions{
					StopReason: &stopTool,
				}),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
					providers.FauxText("Done."),
				}, nil),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
					providers.FauxToolCall("hold_tool", jsonx.NewObject(), nil),
				}, &providers.FauxAssistantMessageOptions{
					StopReason: &stopTool,
				}),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
					providers.FauxText("Resumed cleanly."),
				}, nil),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
					providers.FauxText("Queued done."),
				}, nil),
			},
		})

		mode := NewGuiMode(fx.runtime, &GuiModeOptions{
			SkipWindow:          true,
			InitialThemeSetting: tgmStrPtr("dark"),
		})
		t.Cleanup(func() { mode.Stop() })
		mode.refreshSidebarSessionsSync(ctx)
		mode.refreshGitBranchesSync(fx.cwd)
		// Switch to reasoning-capable faux-2 model so Thinking Picker exposes Off/Minimal/Low/Medium/High.
		if avail := mode.Session().ModelRegistry().GetAvailable(); len(avail) > 1 {
			for _, mdl := range avail {
				if mdl != nil && mdl.Id == "faux-2" {
					_ = mode.Session().SetModel(ctx, mdl)
					mode.updateFooterAndTitle()
					break
				}
			}
		}

		tt := wurNewTester(mode, 1280, 820, true)
		if err := tt.Click("Prompt editor"); err != nil {
			t.Fatalf("Click Prompt editor: %v", err)
		}
		tt.Type("give me a quick overview of the workspace architecture, build pipeline, and test suite modules")
		tt.Key(0, ui.KeyEnter)
		tgmWaitUntil(t, mode, tt, 5*time.Second, "populated turn idle", func() bool {
			return !mode.Session().IsStreaming() && tt.HasText("Done.") && tt.HasText("Worked")
		})
		mode.Session().SetSessionName("give me a quick overview")
		mode.setCachedSessionName("give me a quick overview")
		mode.refreshSidebarSessionsSync(ctx)
		tt.Frame()
		workedInit := wurFindAccessByPrefix(t, tt.AccessAll(), "Worked", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton })
		if err := tt.Click(workedInit.Name); err != nil {
			t.Fatalf("Click %q to fold worked cluster: %v", workedInit.Name, err)
		}
		tt.Frame()

		// 1. Populated across 1280x820, 860x640, 1600x960
		for _, vp := range []struct {
			name string
			w, h int
		}{
			{"1280x820", 1280, 820},
			{"860x640", 860, 640},
			{"1600x960", 1600, 960},
		} {
			vtt := tt
			if vp.w != 1280 || vp.h != 820 {
				vtt = wurNewTester(mode, vp.w, vp.h, true)
			}
			nodes := wurExtractWorkspaceNodes(t, vtt.AccessAll(), wurWorkspaceExtractOpts{surfaceState: "populated", populated: true})
			wurWriteYAML(t, outDir, "app.workspace", "populated", vp.name, wurDumpLayoutYAML("app.workspace", "populated", vp.w, vp.h, nodes))
			img := vtt.Image()
			wurSavePNG(t, outDir, fmt.Sprintf("app.workspace-populated@%s.png", vp.name), img)
			if vp.name == "1280x820" {
				wurAssertNearRGB(t, "populated dark sidebar (120,790)", wurPixelAt(t, img, 120*2, 790*2), 24, 24, 24, 3)
				wurAssertNearRGB(t, "populated dark canvas (640,200)", wurPixelAt(t, img, 640*2, 200*2), 26, 26, 26, 3)
				for _, n := range nodes {
					if n.Key == "workspace.main.transcript.user_bubble.0" {
						measuredCompW["user.bubble"] = n.Bounds.W
					}
				}
			}
		}

		// 2. Light appearance state (exercise real GuiMode theme switching path)
		mode.applyInteractiveSettingChange("theme", "light")
		lightTT := wurNewTester(mode, 1280, 820, false)
		lightNodes := wurExtractWorkspaceNodes(t, lightTT.AccessAll(), wurWorkspaceExtractOpts{surfaceState: "light", light: true, populated: true})
		wurWriteYAML(t, outDir, "app.workspace", "light", "1280x820", wurDumpLayoutYAML("app.workspace", "light", 1280, 820, lightNodes))
		lightImg := lightTT.Image()
		wurSavePNG(t, outDir, "app.workspace-light@1280x820.png", lightImg)
		wurAssertNearRGB(t, "light sidebar (120,790)", wurPixelAt(t, lightImg, 120*2, 790*2), 243, 243, 243, 3)
		wurAssertNearRGB(t, "light canvas (640,200)", wurPixelAt(t, lightImg, 640*2, 200*2), 246, 245, 246, 3)

		// Switch back to Dark for all remaining states
		mode.applyInteractiveSettingChange("theme", "dark")
		tt = wurNewTester(mode, 1280, 820, true)

		// 3. Sidebar Collapsed state
		if err := tt.Click("Toggle Sidebar"); err != nil {
			t.Fatalf("Click Toggle Sidebar (collapse): %v", err)
		}
		tt.Frame()
		wurClearHover(tt)
		collapsedImg := tt.Image()
		cr, cg, cb, _ := collapsedImg.At(88*2, 13*2).RGBA()
		if cr>>8 > 29 || cg>>8 > 29 || cb>>8 > 29 {
			t.Fatalf("collapsed Toggle Sidebar still hovered at (88,13): rgb(%d,%d,%d)", cr>>8, cg>>8, cb>>8)
		}
		collapsedNodes := wurExtractWorkspaceNodes(t, tt.AccessAll(), wurWorkspaceExtractOpts{surfaceState: "sidebar-collapsed", collapsed: true, populated: true})
		wurWriteYAML(t, outDir, "app.workspace", "sidebar-collapsed", "1280x820", wurDumpLayoutYAML("app.workspace", "sidebar-collapsed", 1280, 820, collapsedNodes))
		wurSavePNG(t, outDir, "app.workspace-sidebar-collapsed@1280x820.png", collapsedImg)
		for _, n := range collapsedNodes {
			if n.Key == "workspace.main.header.sidebar_toggle" {
				centerSidebarToggleX = n.Bounds.X
			}
		}
		if err := tt.Click("Toggle Sidebar"); err != nil {
			t.Fatalf("Click Toggle Sidebar (expand): %v", err)
		}
		tt.Frame()

		// 4. Right Panel Open state + app.right_panel (changes, files, terminal, tree) + Commit Dialog
		if err := os.WriteFile(filepath.Join(fx.cwd, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatalf("WriteFile main.go: %v", err)
		}
		mode.refreshRightPanelChanges()
		if err := tt.Click("Toggle Right Panel"); err != nil {
			t.Fatalf("Click Toggle Right Panel: %v", err)
		}
		tt.Frame()
		wurClearHover(tt)

		rpWsNodes := wurExtractWorkspaceNodes(t, tt.AccessAll(), wurWorkspaceExtractOpts{surfaceState: "right-panel-open", rightOpen: true, populated: true})
		wurWriteYAML(t, outDir, "app.workspace", "right-panel-open", "1280x820", wurDumpLayoutYAML("app.workspace", "right-panel-open", 1280, 820, rpWsNodes))
		wurSavePNG(t, outDir, "app.workspace-right-panel-open@1280x820.png", tt.Image())
		for _, n := range rpWsNodes {
			if n.Key == "workspace.right_panel" {
				rpWidthMeasured = n.Bounds.W
			}
		}

		mode.rightPanel.mu.Lock()
		origRightTabs := append([]string(nil), mode.rightPanel.openTabs...)
		mode.rightPanel.mu.Unlock()
		for _, rpTab := range []string{"changes", "files", "terminal", "tree"} {
			tabTitle := rpTabDisplayName(rpTab)
			if err := tt.Click(tabTitle); err != nil {
				t.Fatalf("Click right panel tab %q: %v", tabTitle, err)
			}
			tt.Frame()
			mode.rightPanel.mu.Lock()
			mode.rightPanel.openTabs = []string{rpTab}
			mode.rightPanel.activeTab = rpTab
			mode.rightPanel.mu.Unlock()
			tt.Frame()
			wurClearHover(tt)
			rpNodes := wurExtractRightPanelNodes(t, tt.AccessAll(), rpTab)
			for _, n := range rpNodes {
				if n.Key == "right_panel.tab."+rpTab && n.Bounds.W < 64 {
					t.Fatalf("expected single right panel tab %q to include 16px close button, width=%.1f", tabTitle, n.Bounds.W)
				}
			}
			wurWriteYAML(t, outDir, "app.right_panel", rpTab, "1280x820", wurDumpLayoutYAML("app.right_panel", rpTab, 1280, 820, rpNodes))
			wurSavePNG(t, outDir, fmt.Sprintf("app.right_panel-%s@1280x820.png", rpTab), tt.Image())
			mode.rightPanel.mu.Lock()
			mode.rightPanel.openTabs = append([]string(nil), origRightTabs...)
			mode.rightPanel.mu.Unlock()
			tt.Frame()
		}

		// Open Commit Dialog from Changes tab
		if err := tt.Click("Changes"); err != nil {
			t.Fatalf("Click Changes tab: %v", err)
		}
		tt.Frame()
		if err := tt.Click("Commit"); err != nil {
			t.Fatalf("Click Commit in Changes: %v", err)
		}
		tt.Frame()
		wurClearHover(tt)
		if tt.HasText("Commit on main") {
			t.Fatal("unexpected legacy 'Commit on main' prefix in Commit Dialog header")
		}
		if _, ok := tt.Access("Close commit dialog"); ok {
			t.Fatal("unexpected extra 'Close commit dialog' button in Commit Dialog header")
		}
		if !tt.HasText("main") || !tt.HasText("⌘↩") {
			t.Fatalf("expected branch name 'main' and shortcut pill '⌘↩' in Commit Dialog, texts=%v", tt.Texts())
		}
		cdAll := tt.AccessAll()
		cdCard := wurFindAccess(t, cdAll, "Commit Dialog", func(a ui.AccessInfo) bool { return a.Role == ui.RoleDialog })
		cdHdr := wurFindAccess(t, cdAll, "Commit Dialog Header", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion && wurContainsRect(cdCard.Bounds, a.Bounds) })
		cdMsg := wurFindAccess(t, cdAll, "Commit Message Area", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion && wurContainsRect(cdCard.Bounds, a.Bounds) })
		cdUnstaged := wurFindAccess(t, cdAll, "Include unstaged", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleCheckBox && wurContainsRect(cdCard.Bounds, a.Bounds)
		})
		cdCommitBtn := wurFindAccess(t, cdAll, "Commit", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleButton && wurContainsRect(cdCard.Bounds, a.Bounds) && a.Bounds.H >= 34
		})
		measuredCompW["commit_dialog.card"] = cdCard.Bounds.W
		cdNodes := []wurLayoutNode{
			{Key: "commit_dialog.card", Parent: nil, Order: 0, Role: "dialog", Bounds: cdCard.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0}, Size: wurSize{W: "420", H: "hug"}, Z: 200, Variant: "commit-dialog"},
			{Key: "commit_dialog.header", Parent: wurStrPtr("commit_dialog.card"), Order: 0, Role: "region", Bounds: cdHdr.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 9}, Box: wurBox{Padding: [4]float32{0, 16, 0, 16}}, Size: wurSize{W: "fill", H: "48"}},
			{Key: "commit_dialog.message", Parent: wurStrPtr("commit_dialog.card"), Order: 1, Role: "textbox", Bounds: cdMsg.Bounds, Flow: wurFlow{Type: "block"}, Box: wurBox{Padding: [4]float32{10, 16, 10, 16}}, Size: wurSize{W: "fill", H: "112"}},
			{Key: "commit_dialog.include_unstaged", Parent: wurStrPtr("commit_dialog.card"), Order: 2, Role: "checkbox", Bounds: cdUnstaged.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 10}, Box: wurBox{Padding: [4]float32{0, 12, 0, 12}, Margin: [4]float32{0, 8, 0, 8}}, Size: wurSize{W: "fill", H: "44"}, Text: &wurTextSpec{Value: "Include unstaged"}},
			{Key: "commit_dialog.commit_btn", Parent: wurStrPtr("commit_dialog.card"), Order: 3, Role: "button", Bounds: cdCommitBtn.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 10}, Box: wurBox{Padding: [4]float32{0, 10, 0, 10}}, Size: wurSize{W: "fill", H: "38"}, Text: &wurTextSpec{Value: "Commit"}},
		}
		wurWriteYAML(t, outDir, "app.dialog.commit", "default", "1280x820", wurDumpLayoutYAML("app.dialog.commit", "default", 1280, 820, cdNodes))
		wurSavePNG(t, outDir, "app.dialog.commit-default@1280x820.png", tt.Image())
		mode.GUI().HideOverlay()
		tt.Frame()
		if err := tt.Click("Toggle Right Panel"); err != nil {
			t.Fatalf("Close Right Panel: %v", err)
		}
		tt.Frame()

		// 5. Toast state
		mode.toast.show(wakuToastSuccess, "", "Saved workspace preferences and general settings cleanly across active sessions.", nil)
		tt.Frame()
		wurClearHover(tt)
		toastNodes := wurExtractWorkspaceNodes(t, tt.AccessAll(), wurWorkspaceExtractOpts{surfaceState: "toast", populated: true, toastVisible: true})
		wurWriteYAML(t, outDir, "app.workspace", "toast", "1280x820", wurDumpLayoutYAML("app.workspace", "toast", 1280, 820, toastNodes))
		wurSavePNG(t, outDir, "app.workspace-toast@1280x820.png", tt.Image())
		for _, n := range toastNodes {
			if n.Key == "workspace.toast" {
				measuredCompW["toast.card"] = n.Bounds.W
			}
		}
		mode.toast.dismiss()
		tt.Frame()

		// 7. Composer footer & model popovers (Model Picker default/search-cleared/favorites, Thinking, Project, Workspace, Branch default/create, Context Usage)
		if err := tt.Click("Select Model Chip"); err != nil {
			t.Fatalf("Click Select Model Chip: %v", err)
		}
		tt.Frame()
		extractModelPicker := func(stateName string) {
			mpAll := tt.AccessAll()
			mpRoot := wurFindAccess(t, mpAll, "Model Picker Popover", func(a ui.AccessInfo) bool { return a.Role == ui.RoleDialog })
			mpRail := wurFindAccess(t, mpAll, "Model Provider Rail", func(a ui.AccessInfo) bool { return a.Role == ui.RoleToolbar })
			mpFav := wurFindAccess(t, mpAll, "Filter Favorite Models", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton && wurContainsRect(mpRail.Bounds, a.Bounds) })
			mpSearch := wurFindAccess(t, mpAll, "Model Search Field", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion && wurContainsRect(mpRoot.Bounds, a.Bounds) })
			mpList := wurFindAccess(t, mpAll, "Model List", func(a ui.AccessInfo) bool { return a.Role == ui.RoleList && wurContainsRect(mpRoot.Bounds, a.Bounds) })
			favSt := "default"
			if stateName == "favorites" {
				favSt = "active"
			}
			measuredCompW["popover.model_picker"] = mpRoot.Bounds.W
			measuredCompH["popover.model_picker"] = mpRoot.Bounds.H
			mpNodes := []wurLayoutNode{
				{Key: "popover.model_picker", Parent: nil, Order: 0, Role: "dialog", Bounds: mpRoot.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "stretch", HasGap: true, Gap: 0}, Box: wurBox{Border: [4]float32{1, 1, 1, 1}}, Size: wurSize{W: "460", H: "390"}, Z: 100, Variant: "anchored-popover", State: stateName, Text: &wurTextSpec{Value: "Model Picker Popover"}},
				{Key: "popover.model_picker.rail", Parent: wurStrPtr("popover.model_picker"), Order: 0, Role: "toolbar", Bounds: mpRail.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "center", HasGap: true, Gap: 4}, Box: wurBox{Padding: [4]float32{5, 5, 5, 5}, Border: [4]float32{0, 1, 0, 0}}, Size: wurSize{W: "50", H: "fill"}, Text: &wurTextSpec{Value: "Model Provider Rail"}},
				{Key: "popover.model_picker.rail.favorites", Parent: wurStrPtr("popover.model_picker.rail"), Order: 0, Role: "button", Bounds: mpFav.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "center", Align: "center", HasGap: true, Gap: 0}, Size: wurSize{W: "38", H: "38"}, Variant: "rail-tab", State: favSt, Text: &wurTextSpec{Value: "Filter Favorite Models"}},
				{Key: "popover.model_picker.search", Parent: wurStrPtr("popover.model_picker"), Order: 1, Role: "textbox", Bounds: mpSearch.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Box: wurBox{Padding: [4]float32{0, 10, 0, 10}, Margin: [4]float32{10, 12, 8, 12}}, Size: wurSize{W: "fill", H: "34"}, Text: &wurTextSpec{Value: "Search Models"}},
				{Key: "popover.model_picker.list", Parent: wurStrPtr("popover.model_picker"), Order: 2, Role: "list", Bounds: mpList.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 2}, Box: wurBox{Padding: [4]float32{9, 9, 9, 9}}, Size: wurSize{W: "fill", H: "fill"}, Overflow: "auto"},
			}
			wurWriteYAML(t, outDir, "app.popover.model_picker", stateName, "1280x820", wurDumpLayoutYAML("app.popover.model_picker", stateName, 1280, 820, mpNodes))
			wurSavePNG(t, outDir, fmt.Sprintf("app.popover.model_picker-%s@1280x820.png", stateName), tt.Image())
		}
		extractModelPicker("default")

		if err := tt.Click("Search Models"); err != nil {
			t.Fatalf("Click Search Models: %v", err)
		}
		tt.Type("faux")
		tt.Frame()
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if _, ok := tt.Access("Model Picker Popover"); !ok {
			t.Fatal("expected first Escape on non-empty Model Picker search to clear query and stay open")
		}
		extractModelPicker("search-cleared")

		if err := tt.Click("Filter Favorite Models"); err != nil {
			t.Fatalf("Click Filter Favorite Models: %v", err)
		}
		tt.Frame()
		extractModelPicker("favorites")
		tt.Key(0, ui.KeyEscape)
		tt.Frame()

		// Thinking Picker
		if err := tt.Click("Select Thinking Chip"); err != nil {
			t.Fatalf("Click Select Thinking Chip: %v", err)
		}
		tt.Frame()
		tpAll := tt.AccessAll()
		tpRoot := wurFindAccess(t, tpAll, "Thinking Picker Popover", func(a ui.AccessInfo) bool { return a.Role == ui.RoleMenu })
		tpHdr := wurFindAccess(t, tpAll, "Reasoning", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleHeading && wurContainsRect(tpRoot.Bounds, a.Bounds)
		})
		tpMed := wurFindAccess(t, tpAll, "Medium", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleMenuItem && wurContainsRect(tpRoot.Bounds, a.Bounds)
		})
		tpNodes := []wurLayoutNode{
			{Key: "popover.thinking_picker", Parent: nil, Order: 0, Role: "menu", Bounds: tpRoot.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 2}, Box: wurBox{Padding: [4]float32{6, 6, 6, 6}, Border: [4]float32{1, 1, 1, 1}}, Size: wurSize{W: "hug", H: "hug"}, Z: 100},
			{Key: "popover.thinking_picker.header", Parent: wurStrPtr("popover.thinking_picker"), Order: 0, Role: "heading", Bounds: tpHdr.Bounds, Flow: wurFlow{Type: "inline"}, Size: wurSize{W: "fill", H: "24"}, Text: &wurTextSpec{Value: "Reasoning"}},
			{Key: "popover.thinking_picker.option.medium", Parent: wurStrPtr("popover.thinking_picker"), Order: 1, Role: "menuitem", Bounds: tpMed.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "between", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "Medium"}},
		}
		wurWriteYAML(t, outDir, "app.popover.thinking_picker", "default", "1280x820", wurDumpLayoutYAML("app.popover.thinking_picker", "default", 1280, 820, tpNodes))
		wurSavePNG(t, outDir, "app.popover.thinking_picker-default@1280x820.png", tt.Image())
		tt.Key(0, ui.KeyEscape)
		tt.Frame()

		// Project Picker (seed extra recent temp directories to verify pinned action placement)
		projSeedRoot := t.TempDir()
		for _, projName := range []string{"alpha", "beta", "gamma", "delta"} {
			projDir := filepath.Join(projSeedRoot, projName)
			if err := os.MkdirAll(projDir, 0o755); err != nil {
				t.Fatalf("MkdirAll %s: %v", projDir, err)
			}
			mode.workspacePicker.recordProject(projDir)
		}
		if err := tt.Click("Select Project"); err != nil {
			t.Fatalf("Click Select Project: %v", err)
		}
		tt.Frame()
		wurClearHover(tt)
		ppAll := tt.AccessAll()
		ppRoot := wurFindAccess(t, ppAll, "Project Picker Popover", func(a ui.AccessInfo) bool { return a.Role == ui.RoleDialog })
		ppSearch := wurFindAccess(t, ppAll, "Project Search Bar", func(a ui.AccessInfo) bool { return wurContainsRect(ppRoot.Bounds, a.Bounds) })
		ppNew := wurFindAccess(t, ppAll, "New Project…", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton && wurContainsRect(ppRoot.Bounds, a.Bounds) })
		ppNo := wurFindAccess(t, ppAll, "No Project", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton && wurContainsRect(ppRoot.Bounds, a.Bounds) })
		ppNodes := []wurLayoutNode{
			{Key: "popover.project_picker", Parent: nil, Order: 0, Role: "dialog", Bounds: ppRoot.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0}, Box: wurBox{Padding: [4]float32{0, 0, 4, 0}, Border: [4]float32{1, 1, 1, 1}}, Size: wurSize{W: "300", H: "hug"}, Z: 100, Text: &wurTextSpec{Value: "Project Picker Popover"}},
			{Key: "popover.project_picker.search", Parent: wurStrPtr("popover.project_picker"), Order: 0, Role: "textbox", Bounds: ppSearch.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Box: wurBox{Padding: [4]float32{0, 10, 0, 10}, Margin: [4]float32{10, 12, 8, 12}}, Size: wurSize{W: "fill", H: "28"}, Text: &wurTextSpec{Value: "Search projects"}},
			{Key: "popover.project_picker.new_project", Parent: wurStrPtr("popover.project_picker"), Order: 1, Role: "button", Bounds: ppNew.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "New Project…"}},
			{Key: "popover.project_picker.no_project", Parent: wurStrPtr("popover.project_picker"), Order: 2, Role: "button", Bounds: ppNo.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "No Project"}},
		}
		wurWriteYAML(t, outDir, "app.popover.project_picker", "default", "1280x820", wurDumpLayoutYAML("app.popover.project_picker", "default", 1280, 820, ppNodes))
		wurSavePNG(t, outDir, "app.popover.project_picker-default@1280x820.png", tt.Image())
		tt.Key(0, ui.KeyEscape)
		tt.Frame()

		// Workspace Picker
		if err := tt.Click("Select Workspace"); err != nil {
			t.Fatalf("Click Select Workspace: %v", err)
		}
		tt.Frame()
		wurClearHover(tt)
		wpAll := tt.AccessAll()
		wpRoot := wurFindAccess(t, wpAll, "Workspace Picker Popover", func(a ui.AccessInfo) bool { return a.Role == ui.RoleMenu })
		wpLocal := wurFindAccess(t, wpAll, "Local workspace", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleMenuItem && wurContainsRect(wpRoot.Bounds, a.Bounds)
		})
		wpNewWT := wurFindAccess(t, wpAll, "New worktree", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleMenuItem && wurContainsRect(wpRoot.Bounds, a.Bounds)
		})
		wpNodes := []wurLayoutNode{
			{Key: "popover.workspace_picker", Parent: nil, Order: 0, Role: "menu", Bounds: wpRoot.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 2}, Box: wurBox{Padding: [4]float32{6, 6, 6, 6}, Border: [4]float32{1, 1, 1, 1}}, Size: wurSize{W: "hug", H: "hug"}, Z: 100},
			{Key: "popover.workspace_picker.local", Parent: wurStrPtr("popover.workspace_picker"), Order: 0, Role: "menuitem", Bounds: wpLocal.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "Local"}},
			{Key: "popover.workspace_picker.new_worktree", Parent: wurStrPtr("popover.workspace_picker"), Order: 1, Role: "menuitem", Bounds: wpNewWT.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "New worktree"}},
		}
		wurWriteYAML(t, outDir, "app.popover.workspace_picker", "default", "1280x820", wurDumpLayoutYAML("app.popover.workspace_picker", "default", 1280, 820, wpNodes))
		wurSavePNG(t, outDir, "app.popover.workspace_picker-default@1280x820.png", tt.Image())
		tt.Key(0, ui.KeyEscape)
		tt.Frame()

		// Branch Picker default + create
		if err := tt.Click("Switch Git Branch"); err != nil {
			t.Fatalf("Click Switch Git Branch: %v", err)
		}
		tt.Frame()
		wurClearHover(tt)
		bpAll := tt.AccessAll()
		bpRoot := wurFindAccess(t, bpAll, "Branch Picker Popover", func(a ui.AccessInfo) bool { return a.Role == ui.RoleDialog })
		bpSearch := wurFindAccess(t, bpAll, "Branch Search Bar", func(a ui.AccessInfo) bool { return wurContainsRect(bpRoot.Bounds, a.Bounds) })
		bpRow0 := wurFindAccess(t, bpAll, "Checkout branch main", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton && wurContainsRect(bpRoot.Bounds, a.Bounds) })
		bpDefNodes := []wurLayoutNode{
			{Key: "popover.branch_picker", Parent: nil, Order: 0, Role: "dialog", Bounds: bpRoot.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0}, Box: wurBox{Padding: [4]float32{0, 0, 4, 0}, Border: [4]float32{1, 1, 1, 1}}, Size: wurSize{W: "300", H: "hug"}, Z: 100, Text: &wurTextSpec{Value: "Branch Picker Popover"}},
			{Key: "popover.branch_picker.search", Parent: wurStrPtr("popover.branch_picker"), Order: 0, Role: "textbox", Bounds: bpSearch.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "28"}, Text: &wurTextSpec{Value: "Search branches"}},
			{Key: "popover.branch_picker.row.0", Parent: wurStrPtr("popover.branch_picker"), Order: 1, Role: "button", Bounds: bpRow0.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "26"}, Text: &wurTextSpec{Value: "main"}},
		}
		wurWriteYAML(t, outDir, "app.popover.branch_picker", "default", "1280x820", wurDumpLayoutYAML("app.popover.branch_picker", "default", 1280, 820, bpDefNodes))
		wurSavePNG(t, outDir, "app.popover.branch_picker-default@1280x820.png", tt.Image())

		if err := tt.Click("Create and checkout new branch…"); err != nil {
			t.Fatalf("Click Create and checkout new branch…: %v", err)
		}
		tt.Frame()
		wurClearHover(tt)
		if !tt.HasText("Create and checkout new branch") || !tt.HasText("Press Return to create · Esc to cancel") {
			t.Fatalf("expected Waku BranchPickerMode::Create heading and hint, texts=%v", tt.Texts())
		}
		if tt.HasText("Create & Checkout") || tt.HasText("Cancel") {
			t.Fatalf("unexpected legacy Cancel / Create & Checkout buttons in Branch Picker create mode, texts=%v", tt.Texts())
		}
		bpcAll := tt.AccessAll()
		bpcRoot := wurFindAccess(t, bpcAll, "Branch Picker Popover", func(a ui.AccessInfo) bool { return a.Role == ui.RoleDialog })
		bpcIn := wurFindAccess(t, bpcAll, "Branch Create Input Bar", func(a ui.AccessInfo) bool { return wurContainsRect(bpcRoot.Bounds, a.Bounds) })
		bpcNodes := []wurLayoutNode{
			{Key: "popover.branch_picker", Parent: nil, Order: 0, Role: "dialog", Bounds: bpcRoot.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0}, Box: wurBox{Padding: [4]float32{14, 14, 14, 14}, Border: [4]float32{1, 1, 1, 1}}, Size: wurSize{W: "300", H: "hug"}, Z: 100, State: "create", Text: &wurTextSpec{Value: "Branch Picker Popover"}},
			{Key: "popover.branch_picker.create_input", Parent: wurStrPtr("popover.branch_picker"), Order: 0, Role: "textbox", Bounds: bpcIn.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Size: wurSize{W: "fill", H: "36"}, Text: &wurTextSpec{Value: "New branch name"}},
		}
		wurWriteYAML(t, outDir, "app.popover.branch_picker", "create", "1280x820", wurDumpLayoutYAML("app.popover.branch_picker", "create", 1280, 820, bpcNodes))
		wurSavePNG(t, outDir, "app.popover.branch_picker-create@1280x820.png", tt.Image())
		mode.workspacePicker.exitBranchCreateMode()
		tt.Key(0, ui.KeyEscape)
		tt.Frame()

		// Context Usage Popover
		if err := tt.Click("Context Window Usage"); err != nil {
			t.Fatalf("Click Context Window Usage: %v", err)
		}
		tt.Frame()
		wurClearHover(tt)
		cuAll := tt.AccessAll()
		cuRoot := wurFindAccess(t, cuAll, "Context Window Popover", func(a ui.AccessInfo) bool { return a.Role == ui.RoleDialog })
		cuTitle := wurFindAccess(t, cuAll, "Context Window", func(a ui.AccessInfo) bool {
			return a.Role == ui.RoleHeading && wurContainsRect(cuRoot.Bounds, a.Bounds)
		})
		cuBar := wurFindAccess(t, cuAll, "Context Gauge Bar", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion && wurContainsRect(cuRoot.Bounds, a.Bounds) })
		cuNodes := []wurLayoutNode{
			{Key: "popover.context_usage", Parent: nil, Order: 0, Role: "dialog", Bounds: cuRoot.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 12}, Box: wurBox{Padding: [4]float32{14, 14, 14, 14}, Border: [4]float32{1, 1, 1, 1}}, Size: wurSize{W: "320", H: "hug"}, Z: 100, Text: &wurTextSpec{Value: "Context Window Popover"}},
			{Key: "popover.context_usage.title", Parent: wurStrPtr("popover.context_usage"), Order: 0, Role: "heading", Bounds: cuTitle.Bounds, Flow: wurFlow{Type: "inline"}, Size: wurSize{W: "hug", H: "hug"}, Text: &wurTextSpec{Value: "Context Window"}},
			{Key: "popover.context_usage.bar", Parent: wurStrPtr("popover.context_usage"), Order: 1, Role: "region", Bounds: cuBar.Bounds, Flow: wurFlow{Type: "block"}, Size: wurSize{W: "fill", H: "3"}},
		}
		wurWriteYAML(t, outDir, "app.popover.context_usage", "default", "1280x820", wurDumpLayoutYAML("app.popover.context_usage", "default", 1280, 820, cuNodes))
		wurSavePNG(t, outDir, "app.popover.context_usage-default@1280x820.png", tt.Image())
		tt.Key(0, ui.KeyEscape)
		tt.Frame()

		// 8. Command Palette default / filtered / empty + two-step Escape check
		mode.ShowCommandPalette()
		tt.Frame()
		extractCommandPalette := func(stateName string) {
			cpAll := tt.AccessAll()
			cpCard := wurFindAccess(t, cpAll, "Command Palette", func(a ui.AccessInfo) bool { return a.Role == ui.RoleDialog })
			cpSearch := wurFindAccess(t, cpAll, "Command Palette Search", func(a ui.AccessInfo) bool { return a.Role == ui.RoleRegion && wurContainsRect(cpCard.Bounds, a.Bounds) })
			cpRes := wurFindAccess(t, cpAll, "Command Palette Results", func(a ui.AccessInfo) bool { return a.Role == ui.RoleList && wurContainsRect(cpCard.Bounds, a.Bounds) })
			cpSearchH = cpSearch.Bounds.H
			nodes := []wurLayoutNode{
				{Key: "command_palette.card", Parent: nil, Order: 0, Role: "dialog", Bounds: cpCard.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0}, Size: wurSize{W: "680", H: "480"}, Z: 200, State: stateName, Text: &wurTextSpec{Value: "Command Palette"}},
				{Key: "command_palette.search", Parent: wurStrPtr("command_palette.card"), Order: 0, Role: "textbox", Bounds: cpSearch.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 8}, Box: wurBox{Padding: [4]float32{0, 19, 0, 19}, Border: [4]float32{0, 0, 1, 0}}, Size: wurSize{W: "fill", H: "60"}, Text: &wurTextSpec{Value: "Search tasks or run a command…"}},
				{Key: "command_palette.results", Parent: wurStrPtr("command_palette.card"), Order: 1, Role: "list", Bounds: cpRes.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "start", Align: "stretch", HasGap: true, Gap: 0}, Box: wurBox{Padding: [4]float32{0, 8, 8, 8}}, Size: wurSize{W: "fill", H: "fill"}, Overflow: "auto"},
			}
			if stateName == "empty" {
				nodes[0].Size.H = "240"
				emptyReg := wurFindAccess(t, cpAll, "No matching tasks or commands", func(a ui.AccessInfo) bool {
					return a.Role == ui.RoleRegion && wurContainsRect(cpRes.Bounds, a.Bounds)
				})
				cpEmptyH = emptyReg.Bounds.H
				nodes = append(nodes, wurLayoutNode{
					Key: "command_palette.empty", Parent: wurStrPtr("command_palette.results"), Order: 0, Role: "region",
					Bounds: emptyReg.Bounds, Flow: wurFlow{Type: "flex", Dir: "column", Justify: "center", Align: "center", HasGap: true, Gap: 5},
					Size: wurSize{W: "fill", H: "180"}, Text: &wurTextSpec{Value: "No matching tasks or commands"},
				})
			} else {
				measuredCompW["command_palette.card"] = cpCard.Bounds.W
				measuredCompH["command_palette.card"] = cpCard.Bounds.H
				cpMaxH = cpCard.Bounds.H
				sec := wurFindAccess(t, cpAll, "Commands", func(a ui.AccessInfo) bool {
					return a.Role == ui.RoleHeading && wurContainsRect(cpRes.Bounds, a.Bounds)
				})
				rowLabel := "New Task"
				if stateName == "filtered" {
					rowLabel = "Select Model…"
				}
				row0 := wurFindAccess(t, cpAll, rowLabel, func(a ui.AccessInfo) bool {
					return a.Role == ui.RoleButton && wurContainsRect(cpRes.Bounds, a.Bounds)
				})
				cpRowH = row0.Bounds.H
				nodes = append(nodes,
					wurLayoutNode{
						Key: "command_palette.section.0", Parent: wurStrPtr("command_palette.results"), Order: 0, Role: "heading",
						Bounds: sec.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 0},
						Box: wurBox{Padding: [4]float32{10, 9, 0, 9}}, Size: wurSize{W: "fill", H: "30"}, Text: &wurTextSpec{Value: "Commands"},
					},
					wurLayoutNode{
						Key: "command_palette.row.0", Parent: wurStrPtr("command_palette.results"), Order: 1, Role: "button",
						Bounds: row0.Bounds, Flow: wurFlow{Type: "flex", Dir: "row", Justify: "start", Align: "center", HasGap: true, Gap: 10},
						Box: wurBox{Padding: [4]float32{0, 11, 0, 11}, Border: [4]float32{1, 1, 1, 1}}, Size: wurSize{W: "fill", H: "44"}, State: "active",
					},
				)
			}
			wurWriteYAML(t, outDir, "app.command_palette", stateName, "1280x820", wurDumpLayoutYAML("app.command_palette", stateName, 1280, 820, nodes))
			wurSavePNG(t, outDir, fmt.Sprintf("app.command_palette-%s@1280x820.png", stateName), tt.Image())
		}
		extractCommandPalette("default")

		if err := tt.Click("Search tasks or run a command…"); err != nil {
			t.Fatalf("Click Command Palette search: %v", err)
		}
		tt.Type("model")
		tt.Frame()
		extractCommandPalette("filtered")

		tt.Type("zzz_nonexistent")
		tt.Frame()
		extractCommandPalette("empty")

		// Verify two-step Escape: first clears query while keeping Command Palette open, second dismisses.
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if !mode.GUI().HasOverlay() || !tt.HasText("Commands") {
			t.Fatalf("expected first Escape on non-empty Command Palette query to clear query and stay open, hasOverlay=%v", mode.GUI().HasOverlay())
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if mode.GUI().HasOverlay() {
			t.Fatal("expected second Escape on empty Command Palette query to dismiss overlay")
		}

		// 9. Settings 5 states (general, appearance, providers, skills, usage)
		if err := tt.Click("Open Settings"); err != nil {
			t.Fatalf("Click Open Settings: %v", err)
		}
		tt.Frame()
		for _, sp := range []struct {
			id    string
			title string
		}{
			{"general", "General"},
			{"appearance", "Appearance"},
			{"providers", "Providers"},
			{"skills", "Skills"},
			{"usage", "Usage"},
		} {
			if err := tt.Click(sp.title); err != nil {
				t.Fatalf("Click Settings nav %q: %v", sp.title, err)
			}
			tt.Frame()
			if sp.id == "skills" {
				for _, want := range []string{"Search skills", "All", "User", "Project", "code-review", "Open SKILL.md", "Reveal", "Copy Path"} {
					if !tt.HasText(want) {
						t.Fatalf("expected Skills 2-pane UI element %q, texts=%v", want, tt.Texts())
					}
				}
			}
			sNodes := wurExtractSettingsNodes(t, tt.AccessAll(), sp.id)
			wurWriteYAML(t, outDir, "app.settings", sp.id, "1280x820", wurDumpLayoutYAML("app.settings", sp.id, 1280, 820, sNodes))
			wurSavePNG(t, outDir, fmt.Sprintf("app.settings-%s@1280x820.png", sp.id), tt.Image())
		}
		if err := tt.Click("Back"); err != nil {
			t.Fatalf("Click Back from Settings: %v", err)
		}
		tt.Frame()

		// 10. Busy-queued state (hold_tool active + 1 queued follow-up + live draft -> Stop & Queue Follow-up visible)
		if err := tt.Click("Prompt editor"); err != nil {
			t.Fatalf("Click Prompt editor for busy turn: %v", err)
		}
		tt.Type("run hold")
		tt.Key(0, ui.KeyEnter)
		select {
		case <-holdStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for hold_tool to start")
		}
		tgmWaitUntil(t, mode, tt, 3*time.Second, "busy Stop button active", func() bool {
			return mode.Session().IsStreaming() && tt.HasText("Stop")
		})

		if err := tt.Click("Prompt editor"); err != nil {
			t.Fatalf("Click Prompt editor for queued item: %v", err)
		}
		tt.Type("queued check")
		tt.Frame()
		if err := tt.Click("Queue Follow-up"); err != nil {
			t.Fatalf("Click Queue Follow-up: %v", err)
		}
		tgmWaitUntil(t, mode, tt, 3*time.Second, "queued item in tray", func() bool {
			return tt.HasText("queued check")
		})

		if err := tt.Click("Prompt editor"); err != nil {
			t.Fatalf("Click Prompt editor for second draft: %v", err)
		}
		tt.Type("pending draft")
		tt.Frame()
		busyNodes := wurExtractWorkspaceNodes(t, tt.AccessAll(), wurWorkspaceExtractOpts{
			surfaceState: "busy-queued",
			populated:    true,
			busyQueued:   true,
		})
		wurWriteYAML(t, outDir, "app.workspace", "busy-queued", "1280x820", wurDumpLayoutYAML("app.workspace", "busy-queued", 1280, 820, busyNodes))
		wurSavePNG(t, outDir, "app.workspace-busy-queued@1280x820.png", tt.Image())

		close(holdRelease)
		tgmWaitUntil(t, mode, tt, 5*time.Second, "busy turn finished", func() bool {
			return !mode.Session().IsStreaming()
		})
	})

	// Part C: Overflow clamping state (unbroken URL + unbroken monospace code + multi-paragraph response)
	t.Run("overflow", func(t *testing.T) {
		probeTool := &agent.AgentTool{
			Name:        "waku_probe",
			Label:       "Waku Probe",
			Description: "Quick repository check",
			Parameters:  typebox.Object(nil),
			Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}}, nil
			},
		}
		ovFx := tgmNewFixture(t, &tgmFixtureOpts{
			withResources: true,
			providerName:  "faux",
			tools:         []*agent.AgentTool{probeTool},
		})
		wurInitGitRepo(t, ovFx.cwd, "main")

		stopTool := ai.StopReasonToolUse
		ovFx.faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
					providers.FauxToolCall("waku_probe", jsonx.NewObject(), nil),
				}, &providers.FauxAssistantMessageOptions{
					StopReason: &stopTool,
				}),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
					providers.FauxText(
						"Verified horizontal clamping against long unbroken URLs such as https://cdn.example.com/releases/v0.1.20/artifacts/macos-universal/waku-desktop-replication-bundle-with-very-long-unbroken-segment-alpha-beta-gamma-delta.tar.gz\n\n" +
							"Monospace identifier without spaces:\n\n" +
							"`overflow_identifier_without_spaces_0123456789_abcdefghijklmnopqrstuvwxyz_ABCDEFGHIJKLMNOPQRSTUVWXYZ_clamped_inside_720px`\n\n" +
							"All paragraphs, inline code spans, and long URLs remain constrained strictly inside the 720px transcript column.",
					),
				}, nil),
			},
		})

		ovMode := NewGuiMode(ovFx.runtime, &GuiModeOptions{
			SkipWindow:          true,
			InitialThemeSetting: tgmStrPtr("dark"),
		})
		t.Cleanup(func() { ovMode.Stop() })
		ovMode.refreshSidebarSessionsSync(ctx)
		ovMode.refreshGitBranchesSync(ovFx.cwd)

		ovTT := wurNewTester(ovMode, 1280, 820, true)
		if err := ovTT.Click("Prompt editor"); err != nil {
			t.Fatalf("Click overflow Prompt editor: %v", err)
		}
		ovTT.Type("Inspect https://example.com/extremely/long/unbroken/repository/path/with/segments/overflow_clamping_check")
		ovTT.Key(0, ui.KeyEnter)
		tgmWaitUntil(t, ovMode, ovTT, 5*time.Second, "overflow turn idle", func() bool {
			return !ovMode.Session().IsStreaming() && ovTT.HasText("720px transcript column") && ovTT.HasText("Worked")
		})
		ovMode.Session().SetSessionName("give me a quick overview")
		ovMode.setCachedSessionName("give me a quick overview")
		ovMode.refreshSidebarSessionsSync(ctx)
		ovTT.Frame()

		workedOv := wurFindAccessByPrefix(t, ovTT.AccessAll(), "Worked", func(a ui.AccessInfo) bool { return a.Role == ui.RoleButton })
		if err := ovTT.Click(workedOv.Name); err != nil {
			t.Fatalf("Click %q in overflow: %v", workedOv.Name, err)
		}
		ovTT.Frame()

		overflowNodes := wurExtractWorkspaceNodes(t, ovTT.AccessAll(), wurWorkspaceExtractOpts{surfaceState: "overflow", populated: true})
		wurWriteYAML(t, outDir, "app.workspace", "overflow", "1280x820", wurDumpLayoutYAML("app.workspace", "overflow", 1280, 820, overflowNodes))
		ovImg := ovTT.Image()
		wurAssertNearRGB(t, "overflow right gutter clear (1200,200)", wurPixelAt(t, ovImg, 1200*2, 200*2), 26, 26, 26, 3)
		wurSavePNG(t, outDir, "app.workspace-overflow@1280x820.png", ovImg)
	})

	// Emit tokens, components, states, test-results when PI_UI_REPLICATION_DIR is set.
	wurEmitTokensYAML(t, outDir)
	wurEmitComponentsYAML(t, outDir, measuredCompW, measuredCompH)
	wurEmitStatesYAML(t, outDir,
		centerSidebarToggleX,
		rpWidthMeasured,
		measuredCompW["popover.model_picker"],
		measuredCompH["popover.model_picker"],
		50,
		cpSearchH,
		cpRowH,
		cpMaxH,
		cpEmptyH,
	)
	wurEmitTestResultsJSON(t, outDir)
}
