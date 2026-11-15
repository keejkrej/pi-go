package gui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/gui"
)

const (
	wakuRightPanelDefaultWidth float32 = 460
	wakuRightPanelMinWidth     float32 = 280
	wakuRightPanelMaxWidth     float32 = 1000
)

type wakuChangedFile struct {
	path   string
	status string // "M", "A", "D", "U"
	adds   int
	dels   int
	patch  string
}

type wakuRightPanelState struct {
	mu             sync.Mutex
	open           bool
	width          float32
	activeTab      string // "changes" or "tree"
	files          []wakuChangedFile
	selectedPath   string
	diffViewer     *gui.DiffViewer
	diffSig        string
	diffSideBySide bool
	loading        bool
	isGitRepo      bool
	branches       []string
	currentBranch  string
	treeItems      []wakuTreeFlatItem
	treeLeafID     string
	hasTreeSession bool
	split          wakuSplitDragTracker
	refreshSeq     atomic.Uint64
}

func newWakuRightPanelState(prefs *wakuLayoutPrefs) *wakuRightPanelState {
	rp := &wakuRightPanelState{
		open:      false,
		width:     wakuRightPanelDefaultWidth,
		activeTab: "changes",
	}
	if prefs != nil {
		rp.open = prefs.RightPanelOpen
		if prefs.RightPanelWidth >= wakuRightPanelMinWidth && prefs.RightPanelWidth <= wakuRightPanelMaxWidth {
			rp.width = prefs.RightPanelWidth
		}
		if prefs.RightPanelTab == "changes" || prefs.RightPanelTab == "tree" {
			rp.activeTab = prefs.RightPanelTab
		}
	}
	return rp
}

func (r *wakuRightPanelState) snapshotMeta() (open bool, width float32, tab string, count int) {
	if r == nil {
		return false, wakuRightPanelDefaultWidth, "changes", 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.open, r.width, r.activeTab, len(r.files)
}

func (r *wakuRightPanelState) gitBranchesSnapshot() ([]string, string) {
	if r == nil {
		return nil, ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.branches...), r.currentBranch
}

func (m *GuiMode) refreshRightPanelTreeSnapshot() {
	if m == nil {
		return
	}
	m.ensureWakuState()
	rp := m.rightPanel
	sess := m.Session()
	if sess == nil || sess.SessionManager == nil {
		rp.mu.Lock()
		rp.hasTreeSession = false
		rp.treeItems = nil
		rp.treeLeafID = ""
		rp.mu.Unlock()
		return
	}
	if sess.IsStreaming() || sess.IsCompacting() || sess.IsRetrying() || m.sessionMutationDepth.Load() > 0 {
		return
	}
	nodes := sess.SessionManager.GetTree()
	leafId := ""
	if lid := sess.SessionManager.GetLeafId(); lid != nil {
		leafId = *lid
	}
	items := wakuFlattenTree(nodes, 0)
	rp.mu.Lock()
	rp.hasTreeSession = true
	rp.treeItems = items
	rp.treeLeafID = leafId
	rp.mu.Unlock()
}

func (m *GuiMode) openRightPanelFileDiff(relPath string) {
	if m == nil {
		return
	}
	m.ensureWakuState()
	rp := m.rightPanel
	rp.mu.Lock()
	rp.open = true
	rp.activeTab = "changes"
	if relPath != "" {
		rp.selectedPath = relPath
	}
	rp.mu.Unlock()
	m.saveLayoutPrefs()
	m.refreshRightPanelChanges()
}

func (m *GuiMode) refreshRightPanelChanges() {
	if m == nil {
		return
	}
	m.ensureWakuState()
	rp := m.rightPanel

	sess := m.Session()
	cwd := gmRuntimeCwd(m.runtime)
	if sess != nil && sess.Cwd() != "" {
		cwd = sess.Cwd()
	}

	// Also snapshot session edit/write tool cards so non-git sandboxes show changed files too.
	m.mu.Lock()
	entries := append([]gmTranscriptEntry(nil), m.entries...)
	m.mu.Unlock()

	var toolEdits []wakuChangedFile
	seenTool := make(map[string]int)
	for _, e := range entries {
		if e.kind != "tool" {
			continue
		}
		if tc, ok := e.card.(*ToolExecutionCard); ok && tc != nil {
			fp, adds, dels := tc.ModifiedFileStats()
			if fp == "" {
				continue
			}
			var patch string
			tc.mu.Lock()
			if det, ok := tvDecodeDetails[core.EditToolDetails](tc.result.Details); ok {
				patch = det.Patch
				if patch == "" {
					patch = det.Diff
				}
			}
			tc.mu.Unlock()
			if idx, exists := seenTool[fp]; exists {
				toolEdits[idx].adds += adds
				toolEdits[idx].dels += dels
				if patch != "" {
					toolEdits[idx].patch = patch
				}
			} else {
				seenTool[fp] = len(toolEdits)
				toolEdits = append(toolEdits, wakuChangedFile{
					path:   fp,
					status: "M",
					adds:   adds,
					dels:   dels,
					patch:  patch,
				})
			}
		}
	}

	rp.mu.Lock()
	selected := rp.selectedPath
	rp.loading = true
	rp.mu.Unlock()

	seq := rp.refreshSeq.Add(1)
	go func() {
		files, isGit := wakuLoadGitChangedFiles(cwd, selected, toolEdits)
		if rp.refreshSeq.Load() != seq {
			return
		}
		rp.mu.Lock()
		if rp.refreshSeq.Load() == seq {
			rp.files = files
			rp.isGitRepo = isGit
			rp.loading = false
			if len(files) > 0 {
				foundSel := false
				for _, f := range files {
					if f.path == rp.selectedPath {
						foundSel = true
						break
					}
				}
				if !foundSel {
					rp.selectedPath = files[0].path
				}
			} else {
				rp.selectedPath = ""
			}
		}
		rp.mu.Unlock()

		m.postUI(func() {
			if m.host != nil {
				m.host.RequestRender()
			}
		})
	}()
}

func (m *GuiMode) refreshGitBranchesAsync() {
	if m == nil {
		return
	}
	m.ensureWakuState()
	rp := m.rightPanel
	sess := m.Session()
	cwd := gmRuntimeCwd(m.runtime)
	if sess != nil && sess.Cwd() != "" {
		cwd = sess.Cwd()
	}
	if cwd == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		curOut, _ := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD").Output()
		curBranch := strings.TrimSpace(string(curOut))

		listOut, err := exec.CommandContext(ctx, "git", "-C", cwd, "branch", "--format=%(refname:short)").Output()
		var branches []string
		if err == nil {
			for _, ln := range strings.Split(string(listOut), "\n") {
				if trimmed := strings.TrimSpace(ln); trimmed != "" {
					branches = append(branches, trimmed)
				}
			}
		}

		rp.mu.Lock()
		rp.branches = branches
		rp.currentBranch = curBranch
		rp.mu.Unlock()

		m.postUI(func() {
			if m.host != nil {
				m.host.RequestRender()
			}
		})
	}()
}

func (m *GuiMode) checkoutGitBranch(branch string) {
	if m == nil || strings.TrimSpace(branch) == "" {
		return
	}
	sess := m.Session()
	cwd := gmRuntimeCwd(m.runtime)
	if sess != nil && sess.Cwd() != "" {
		cwd = sess.Cwd()
	}
	if cwd == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "git", "-C", cwd, "checkout", branch).CombinedOutput()
		if err != nil {
			m.postUI(func() {
				msg := strings.TrimSpace(string(out))
				if msg == "" {
					msg = err.Error()
				}
				m.ShowError("git checkout failed: " + msg)
			})
			return
		}
		m.postUI(func() {
			m.ShowInfo("Branch", "Switched to "+branch)
			m.updateFooterAndTitle()
			m.refreshGitBranchesAsync()
			m.refreshRightPanelChanges()
		})
	}()
}

func wakuLoadGitChangedFiles(cwd, selected string, fallback []wakuChangedFile) ([]wakuChangedFile, bool) {
	if cwd == "" {
		return fallback, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	statusCmd := exec.CommandContext(ctx, "git", "-C", cwd, "status", "--porcelain=v1")
	statusOut, err := statusCmd.Output()
	if err != nil {
		return fallback, false
	}

	numStat := make(map[string][2]int)
	if nsOut, nsErr := exec.CommandContext(ctx, "git", "-C", cwd, "diff", "--numstat", "HEAD").Output(); nsErr == nil {
		wakuParseNumstatInto(string(nsOut), numStat)
	} else if nsOut2, nsErr2 := exec.CommandContext(ctx, "git", "-C", cwd, "diff", "--numstat").Output(); nsErr2 == nil {
		wakuParseNumstatInto(string(nsOut2), numStat)
	}

	var files []wakuChangedFile
	seen := make(map[string]bool)
	for _, ln := range strings.Split(string(statusOut), "\n") {
		if len(ln) < 4 {
			continue
		}
		code := strings.TrimSpace(ln[:2])
		rel := strings.TrimSpace(ln[3:])
		if idx := strings.Index(rel, " -> "); idx >= 0 {
			rel = strings.TrimSpace(rel[idx+4:])
		}
		rel = strings.Trim(rel, `"`)
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true

		st := "M"
		if code == "??" {
			st = "U"
		} else if strings.Contains(code, "A") {
			st = "A"
		} else if strings.Contains(code, "D") {
			st = "D"
		}
		counts := numStat[rel]
		files = append(files, wakuChangedFile{
			path:   rel,
			status: st,
			adds:   counts[0],
			dels:   counts[1],
		})
	}

	// Merge any session fallback patch info and load patch for active file.
	fallbackPatch := make(map[string]string)
	for _, f := range fallback {
		if f.patch != "" {
			fallbackPatch[f.path] = f.patch
		}
	}

	activePath := selected
	if len(files) > 0 {
		found := false
		for _, f := range files {
			if f.path == activePath {
				found = true
				break
			}
		}
		if !found {
			activePath = files[0].path
		}
	}
	for i := range files {
		if files[i].path != activePath {
			continue
		}
		if patch := wakuLoadGitPatchForFile(ctx, cwd, files[i].path, files[i].status); patch != "" {
			files[i].patch = patch
			if files[i].adds == 0 && files[i].dels == 0 {
				files[i].adds, files[i].dels = tvCountDiffStats(patch, "", "")
			}
		} else if p := fallbackPatch[files[i].path]; p != "" {
			files[i].patch = p
		}
	}

	return files, true
}

func wakuParseNumstatInto(out string, dst map[string][2]int) {
	for _, ln := range strings.Split(out, "\n") {
		parts := strings.Split(ln, "\t")
		if len(parts) < 3 {
			continue
		}
		adds, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		dels, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		p := strings.TrimSpace(parts[2])
		if p != "" {
			dst[p] = [2]int{adds, dels}
		}
	}
}

func wakuLoadGitPatchForFile(ctx context.Context, cwd, relPath, status string) string {
	if status == "U" || status == "A" {
		if out, err := exec.CommandContext(ctx, "git", "-C", cwd, "diff", "--cached", "--", relPath).Output(); err == nil && len(out) > 0 {
			return string(out)
		}
		abs := filepath.Join(cwd, relPath)
		raw, err := os.ReadFile(abs)
		if err != nil {
			return ""
		}
		if len(raw) > 256*1024 {
			raw = raw[:256*1024]
		}
		lines := strings.Split(string(raw), "\n")
		var b strings.Builder
		fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", relPath, len(lines))
		for _, l := range lines {
			b.WriteByte('+')
			b.WriteString(l)
			b.WriteByte('\n')
		}
		return b.String()
	}

	if out, err := exec.CommandContext(ctx, "git", "-C", cwd, "diff", "HEAD", "--", relPath).Output(); err == nil && len(out) > 0 {
		return string(out)
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", cwd, "diff", "--", relPath).Output(); err == nil && len(out) > 0 {
		return string(out)
	}
	return ""
}

func (m *GuiMode) renderWakuRightPanel(c *ui.Context, th *gui.GuiTheme) {
	if m == nil {
		return
	}
	m.ensureWakuState()
	rp := m.rightPanel

	rp.mu.Lock()
	width := rp.width
	tab := rp.activeTab
	files := append([]wakuChangedFile(nil), rp.files...)
	selected := rp.selectedPath
	sideBySide := rp.diffSideBySide
	rp.mu.Unlock()

	var (
		closeClicked   bool
		refreshClicked bool
		toggleLayout   bool
		setTab         string
		selectFilePath string
		navigateTreeId string
		openFullTree   bool
	)

	rp.split.view(
		c,
		"waku-right-split",
		th,
		th.BorderStrong,
		width,
		wakuRightPanelMinWidth,
		wakuRightPanelMaxWidth,
		true,
		func(next float32) {
			rp.mu.Lock()
			rp.width = next
			rp.mu.Unlock()
		},
		func() { m.saveLayoutPrefs() },
	)

	panel := ui.Column(c).
		Key("waku-right-panel").
		Width(width).
		Shrink(0).
		FillHeight().
		Background(th.Background)

	panel.Children(func() {
		// Top 48px tab & action header
		h := wakuHeaderHeight(c)
		ui.Row(c).
			FillWidth().
			Height(h).
			Padding(0, 10, 0, 10).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween).
			Gap(6).
			Children(func() {
				// Segmented Changes | Tree pills
				ui.Row(c).
					Background(th.Inset).
					Border(1, th.Border).
					Radius(8).
					Padding(2).
					Gap(2).
					Children(func() {
						changesLbl := "Changes"
						if len(files) > 0 {
							changesLbl = fmt.Sprintf("Changes (%d)", len(files))
						}
						if wakuSegmentTab(c, th, changesLbl, tab == "changes") {
							setTab = "changes"
						}
						if wakuSegmentTab(c, th, "Tree", tab == "tree") {
							setTab = "tree"
						}
					})

				ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(4).Children(func() {
					if tab == "changes" {
						layoutLbl := "Inline Diff"
						if sideBySide {
							layoutLbl = "Split Diff"
						}
						if tvGhostActionButton(c, th, layoutLbl) {
							toggleLayout = true
						}
						if wakuIconButton(c, th, gui.IconChanges, "Refresh Changes", false, th.TextSecondary) {
							refreshClicked = true
						}
					}
					if wakuIconButton(c, th, gui.IconPanelRight, "Toggle Right Panel", false, th.TextSecondary) {
						closeClicked = true
					}
				})
			})
		ui.Box(c).FillWidth().Height(1).Background(th.Border)

		if tab == "changes" {
			ui.Column(c).Fill().Children(func() {
				if len(files) == 0 {
					ui.Column(c).
						Fill().
						AlignItems(ui.Center).
						Justify(ui.Center).
						Gap(8).
						Padding(24).
						Children(func() {
							gui.IconView(c, gui.IconCheck, 22, th.Success)
							ui.Text(c, "Working tree clean").TextColor(th.TextSecondary).FontSize(13).FontWeight(500)
							ui.Text(c, "Edited or unstaged workspace files will appear here.").
								TextColor(th.TextTertiary).FontSize(12).Wrap()
						})
					return
				}

				// File selector list
				listScroll := ui.Scroll(c).FillWidth().MaxHeight(200).Padding(6, 8)
				listScroll.Children(func() {
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						for idx, f := range files {
							file := f
							isSel := file.path == selected
							row := ui.ButtonBase(c).
								Key(fmt.Sprintf("rp-file-%d-%s", idx, file.path)).
								FillWidth().
								Height(28).
								Padding(0, 8, 0, 8).
								Radius(6).
								AlignItems(ui.Center).
								Justify(ui.SpaceBetween).
								Gap(8).
								Cursor(ui.CursorPointer).
								Label(file.path)
							if isSel {
								row.Background(th.SidebarItemBg)
							} else if row.Hovered() {
								row.Background(th.Overlay)
							}
							row.Children(func() {
								ui.Row(c).Shrink(1).AlignItems(ui.Center).Gap(6).Children(func() {
									stCol := th.Warning
									if file.status == "A" || file.status == "U" {
										stCol = th.DiffAdded
									} else if file.status == "D" {
										stCol = th.DiffRemoved
									}
									ui.Text(c, file.status).TextColor(stCol).Font("monospace").FontSize(11).FontWeight(700).SingleLine()
									ui.Text(c, file.path).TextColor(th.Text).Font("monospace").FontSize(11).SingleLine()
								})
								ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(4).Children(func() {
									if file.adds > 0 {
										ui.Textf(c, "+%d", file.adds).TextColor(th.DiffAdded).Font("monospace").FontSize(11).SingleLine()
									}
									if file.dels > 0 {
										ui.Textf(c, "-%d", file.dels).TextColor(th.DiffRemoved).Font("monospace").FontSize(11).SingleLine()
									}
								})
							})
							if row.Clicked() {
								selectFilePath = file.path
							}
						}
					})
				})

				ui.Box(c).FillWidth().Height(1).Background(th.Border)

				// Active file DiffViewer
				var activeFile *wakuChangedFile
				for idx := range files {
					if files[idx].path == selected {
						activeFile = &files[idx]
						break
					}
				}
				if activeFile == nil {
					activeFile = &files[0]
				}

				diffArea := ui.Scroll(c).FillWidth().Grow(1).Padding(8)
				diffArea.Children(func() {
					if activeFile.patch == "" {
						ui.Text(c, "Loading diff for "+activeFile.path+"...").TextColor(th.TextTertiary).FontSize(12)
						return
					}
					dv := rp.ensureDiffViewer(activeFile.path, activeFile.patch, sideBySide, th)
					dv.View(c)
				})
			})
		} else {
			// Session Tree tab (render from thread-safe snapshot)
			rp.mu.Lock()
			hasTree := rp.hasTreeSession
			treeItems := append([]wakuTreeFlatItem(nil), rp.treeItems...)
			leafId := rp.treeLeafID
			rp.mu.Unlock()
			ui.Column(c).Fill().Children(func() {
				if !hasTree {
					ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
						ui.Text(c, "No active session tree").TextColor(th.TextTertiary).FontSize(12)
					})
					return
				}

				treeScroll := ui.Scroll(c).FillWidth().Grow(1).Padding(8)
				treeScroll.Children(func() {
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						if len(treeItems) == 0 {
							ui.Text(c, "Empty session tree").TextColor(th.TextTertiary).FontSize(12).Padding(8, 8, 8, 8)
						}
						for idx, item := range treeItems {
							it := item
							isLeaf := leafId != "" && it.id == leafId
							indent := float32(min(it.depth, 6) * 12)
							row := ui.ButtonBase(c).
								Key(fmt.Sprintf("rp-tree-%d-%s", idx, it.id)).
								FillWidth().
								MinHeight(26).
								Padding(3, 8, 3, 8+indent).
								Radius(6).
								AlignItems(ui.Center).
								Justify(ui.SpaceBetween).
								Gap(6).
								Cursor(ui.CursorPointer)
							if isLeaf {
								row.Background(th.SidebarItemBg)
							} else if row.Hovered() {
								row.Background(th.Overlay)
							}
							row.Children(func() {
								ui.Row(c).Shrink(1).AlignItems(ui.Center).Gap(6).Children(func() {
									dotCol := th.TextGhost
									if isLeaf {
										dotCol = th.Accent
									}
									ui.Box(c).Size(6, 6).Radius(999).Background(dotCol)
									ui.Text(c, it.kind).TextColor(th.TextTertiary).FontSize(10).FontWeight(600).SingleLine()
									ui.Text(c, it.preview).TextColor(th.Text).FontSize(12).SingleLine()
								})
							})
							if row.Clicked() {
								navigateTreeId = it.id
							}
						}
					})
				})

				ui.Box(c).FillWidth().Height(1).Background(th.Border)
				ui.Row(c).FillWidth().Height(36).Padding(0, 10, 0, 10).AlignItems(ui.Center).Justify(ui.End).Children(func() {
					fullBtn := ui.ButtonBase(c).
						Height(24).
						Padding(0, 10, 0, 10).
						Radius(6).
						Background(th.Overlay).
						Border(1, th.BorderStrong).
						AlignItems(ui.Center).
						Cursor(ui.CursorPointer).
						Label("Open Full Tree")
					fullBtn.Children(func() {
						ui.Text(c, "Open Full Tree…").TextColor(th.Text).FontSize(11)
					})
					if fullBtn.Clicked() {
						openFullTree = true
					}
				})
			})
		}
	})

	if closeClicked {
		rp.mu.Lock()
		rp.open = false
		rp.mu.Unlock()
		m.saveLayoutPrefs()
	}
	if refreshClicked {
		m.refreshRightPanelChanges()
	}
	if toggleLayout {
		rp.mu.Lock()
		rp.diffSideBySide = !rp.diffSideBySide
		rp.mu.Unlock()
	}
	if setTab != "" {
		rp.mu.Lock()
		rp.activeTab = setTab
		rp.mu.Unlock()
		m.saveLayoutPrefs()
		if setTab == "changes" {
			m.refreshRightPanelChanges()
		} else if setTab == "tree" {
			m.refreshRightPanelTreeSnapshot()
		}
	}
	if selectFilePath != "" {
		rp.mu.Lock()
		rp.selectedPath = selectFilePath
		rp.mu.Unlock()
		m.refreshRightPanelChanges()
	}
	if navigateTreeId != "" {
		sess := m.Session()
		if sess != nil {
			id := navigateTreeId
			go func() {
				res, err := sess.NavigateTree(m.currentCtx(), id)
				if err != nil {
					m.postUI(func() { m.ShowError("Tree navigation failed: " + err.Error()) })
					return
				}
				if res != nil && res.Cancelled {
					return
				}
				m.postUI(func() {
					m.rebuildTranscript()
					if res != nil && res.EditorText != nil && m.editor != nil {
						m.editor.SetText(*res.EditorText)
					}
					m.updateFooterAndTitle()
				})
			}()
		}
	}
	if openFullTree {
		m.ShowTreeSelector()
	}
}

func (r *wakuRightPanelState) ensureDiffViewer(filename, patch string, sideBySide bool, th *gui.GuiTheme) *gui.DiffViewer {
	sig := fmt.Sprintf("%s:%d:%s", filename, len(patch), msgPrefix(patch, 32))
	layout := editor.DiffUnified
	if sideBySide {
		layout = editor.DiffSplit
	}

	r.mu.Lock()
	dv := r.diffViewer
	needUpdate := dv == nil || r.diffSig != sig
	if dv == nil {
		dv = gui.NewDiffViewer("", "")
		dv.SetShowToolbar(false)
		dv.SetFitContent(true)
		r.diffViewer = dv
	}
	r.mu.Unlock()

	if needUpdate {
		_ = dv.ParseAndSetPatch(patch)
		if filename != "" {
			dv.SetLanguageForFile(filename)
		}
		r.mu.Lock()
		r.diffSig = sig
		r.mu.Unlock()
	}
	dv.SetLayout(layout)
	dv.SetTheme(th)
	return dv
}

func wakuSegmentTab(c *ui.Context, th *gui.GuiTheme, label string, active bool) bool {
	btn := ui.ButtonBase(c).
		Height(24).
		Padding(0, 10, 0, 10).
		Radius(6).
		AlignItems(ui.Center).
		Justify(ui.Center).
		Cursor(ui.CursorPointer).
		Label(label)
	col := th.TextSecondary
	weight := 500
	if active {
		btn.Background(th.Raised)
		col = th.Text
		weight = 600
	} else if btn.Hovered() {
		btn.Background(th.Overlay)
	}
	btn.Children(func() {
		ui.Text(c, label).TextColor(col).FontSize(12).FontWeight(weight).SingleLine()
	})
	return btn.Clicked()
}

type wakuTreeFlatItem struct {
	id      string
	depth   int
	kind    string
	preview string
}

func wakuFlattenTree(nodes []core.SessionTreeNode, depth int) []wakuTreeFlatItem {
	var out []wakuTreeFlatItem
	for _, n := range nodes {
		if n.Entry != nil {
			kind := n.Entry.GetType()
			preview := n.Entry.GetId()
			switch e := n.Entry.(type) {
			case *core.SessionMessageEntry:
				if e.Message != nil {
					switch m := e.Message.(type) {
					case *ai.UserMessage:
						kind = "user"
						txt, _ := gevExtractUserContent(m.Content)
						preview = selTruncateSingleLine(txt, 54)
					case *ai.AssistantMessage:
						kind = "assistant"
						var sb strings.Builder
						for _, blk := range m.Content {
							if tc, ok := blk.(*ai.TextContent); ok && tc != nil && tc.Text != "" {
								sb.WriteString(tc.Text)
								sb.WriteByte(' ')
							}
						}
						preview = selTruncateSingleLine(sb.String(), 54)
						if preview == "" {
							preview = "tool call"
						}
					}
				}
			case *core.CompactionEntry:
				kind = "compact"
				preview = selTruncateSingleLine(e.Summary, 54)
			case *core.BranchSummaryEntry:
				kind = "branch"
				preview = selTruncateSingleLine(e.Summary, 54)
			}
			if preview == "" {
				preview = kind
			}
			out = append(out, wakuTreeFlatItem{
				id:      n.Entry.GetId(),
				depth:   depth,
				kind:    kind,
				preview: preview,
			})
		}
		if len(n.Children) > 0 {
			out = append(out, wakuFlattenTree(n.Children, depth+1)...)
		}
	}
	return out
}
