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
	"github.com/keejkrej/pi-go/codingagent/utils"
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

type wakuGitWorktree struct {
	Path   string
	Branch string
	IsMain bool
}

type wakuRightPanelState struct {
	mu                 sync.Mutex
	open               bool
	width              float32
	activeTab          string // "changes", "files", "terminal", "tree"
	openTabs           []string
	addSurfaceOpen     bool
	files              []wakuChangedFile
	selectedPath       string
	diffViewer         *gui.DiffViewer
	diffSig            string
	diffSideBySide     bool
	loading            bool
	gitCwd             string
	isGitRepo          bool
	isLinkedWorktree   bool
	mainRepoRoot       string
	worktrees          []wakuGitWorktree
	branches           []string
	currentBranch      string
	browseCwd          string
	browseFiles        []string
	selectedBrowseFile string
	previewPath        string
	previewContent     string
	termOutput         string
	termCmd            string
	treeItems          []wakuTreeFlatItem
	treeLeafID         string
	hasTreeSession     bool
	split              wakuSplitDragTracker
	refreshSeq         atomic.Uint64
}

func newWakuRightPanelState(prefs *wakuLayoutPrefs) *wakuRightPanelState {
	rp := &wakuRightPanelState{
		open:       false,
		width:      wakuRightPanelDefaultWidth,
		activeTab:  "changes",
		openTabs:   []string{"changes", "files", "terminal", "tree"},
		termOutput: "$ pwd\nworkspace ready\n",
	}
	if prefs != nil {
		rp.open = prefs.RightPanelOpen
		if prefs.RightPanelWidth >= wakuRightPanelMinWidth && prefs.RightPanelWidth <= wakuRightPanelMaxWidth {
			rp.width = prefs.RightPanelWidth
		}
		switch prefs.RightPanelTab {
		case "changes", "files", "terminal", "tree":
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

func (r *wakuRightPanelState) snapshotChangedTotals() (cwd string, adds, dels, count int) {
	if r == nil {
		return "", 0, 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cwd = r.gitCwd
	count = len(r.files)
	for _, f := range r.files {
		adds += f.adds
		dels += f.dels
	}
	return cwd, adds, dels, count
}

func (r *wakuRightPanelState) gitWorkspaceSnapshot(cwd string) (
	loaded, isGitRepo, isLinkedWorktree bool,
	mainRepoRoot, currentBranch string,
	worktrees []wakuGitWorktree,
	branches []string,
) {
	if r == nil {
		return false, false, false, "", "", nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cwd == "" || r.gitCwd == "" || wakuResolvePath(r.gitCwd) != wakuResolvePath(cwd) {
		return false, false, false, "", "", nil, nil
	}
	return true, r.isGitRepo, r.isLinkedWorktree, r.mainRepoRoot, r.currentBranch,
		append([]wakuGitWorktree(nil), r.worktrees...), append([]string(nil), r.branches...)
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

func wakuDetectLinkedWorktreeQuick(cwd string) (isLinked bool, mainRepoRoot, worktreeName string) {
	dir := strings.TrimSpace(cwd)
	if dir == "" {
		return false, "", ""
	}
	dir = filepath.Clean(dir)
	for depth := 0; depth < 6; depth++ {
		dotGit := filepath.Join(dir, ".git")
		info, err := os.Lstat(dotGit)
		if err == nil {
			if info.IsDir() {
				return false, "", ""
			}
			if info.Mode().IsRegular() {
				raw, rErr := os.ReadFile(dotGit)
				if rErr == nil {
					for _, ln := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
						ln = strings.TrimSpace(ln)
						if strings.HasPrefix(strings.ToLower(ln), "gitdir:") {
							gd := strings.TrimSpace(ln[len("gitdir:"):])
							if gd == "" {
								break
							}
							if !filepath.IsAbs(gd) {
								gd = filepath.Join(dir, gd)
							}
							gd = filepath.Clean(gd)
							if filepath.Base(filepath.Dir(gd)) == "worktrees" {
								commonGit := filepath.Dir(filepath.Dir(gd))
								root := filepath.Dir(commonGit)
								if st, sErr := os.Stat(root); sErr == nil && st.IsDir() {
									return true, root, filepath.Base(dir)
								}
							}
							break
						}
					}
				}
			}
			return false, "", ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false, "", ""
}

func wakuParseGitWorktreeListPorcelain(raw string) []wakuGitWorktree {
	var out []wakuGitWorktree
	blocks := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n\n")
	for _, blk := range blocks {
		blk = strings.TrimSpace(blk)
		if blk == "" {
			continue
		}
		var wt wakuGitWorktree
		for _, ln := range strings.Split(blk, "\n") {
			ln = strings.TrimSpace(ln)
			switch {
			case strings.HasPrefix(ln, "worktree "):
				wt.Path = filepath.Clean(strings.TrimSpace(strings.TrimPrefix(ln, "worktree ")))
			case strings.HasPrefix(ln, "branch "):
				br := strings.TrimSpace(strings.TrimPrefix(ln, "branch "))
				br = strings.TrimPrefix(br, "refs/heads/")
				wt.Branch = br
			}
		}
		if wt.Path != "" {
			wt.IsMain = len(out) == 0
			out = append(out, wt)
		}
	}
	return out
}

func wakuSanitizePathSegment(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	cleaned := strings.Trim(b.String(), "-")
	if cleaned == "" {
		return "workspace"
	}
	return cleaned
}

func (m *GuiMode) activeWorkspaceCwd() string {
	if m == nil {
		return ""
	}
	sess := m.Session()
	cwd := gmRuntimeCwd(m.runtime)
	if sess != nil && sess.Cwd() != "" {
		cwd = sess.Cwd()
	}
	return cwd
}

func (m *GuiMode) refreshGitBranchesSync(cwd string) {
	if m == nil || strings.TrimSpace(cwd) == "" {
		return
	}
	m.ensureWakuState()
	rp := m.rightPanel

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	topOut, topErr := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if topErr != nil {
		rp.mu.Lock()
		rp.gitCwd = cwd
		rp.isGitRepo = false
		rp.isLinkedWorktree = false
		rp.mainRepoRoot = ""
		rp.worktrees = nil
		rp.branches = nil
		rp.currentBranch = ""
		rp.mu.Unlock()
		return
	}
	toplevel := filepath.Clean(strings.TrimSpace(string(topOut)))

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

	wtOut, wtErr := exec.CommandContext(ctx, "git", "-C", cwd, "worktree", "list", "--porcelain").Output()
	var worktrees []wakuGitWorktree
	if wtErr == nil {
		worktrees = wakuParseGitWorktreeListPorcelain(string(wtOut))
	}

	mainRoot := toplevel
	if len(worktrees) > 0 && worktrees[0].Path != "" {
		mainRoot = worktrees[0].Path
	}
	isLinked := mainRoot != "" && wakuResolvePath(toplevel) != wakuResolvePath(mainRoot)
	if !isLinked && wtErr != nil {
		if qLinked, qMain, _ := wakuDetectLinkedWorktreeQuick(toplevel); qLinked && qMain != "" {
			isLinked = true
			mainRoot = qMain
		}
	}

	rp.mu.Lock()
	rp.gitCwd = cwd
	rp.isGitRepo = true
	rp.isLinkedWorktree = isLinked
	rp.mainRepoRoot = mainRoot
	rp.worktrees = worktrees
	rp.branches = branches
	rp.currentBranch = curBranch
	rp.mu.Unlock()
}

func (m *GuiMode) refreshGitBranchesAsync() {
	if m == nil {
		return
	}
	m.ensureWakuState()
	cwd := m.activeWorkspaceCwd()
	if cwd == "" {
		return
	}

	go func() {
		m.refreshGitBranchesSync(cwd)
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
	cwd := m.activeWorkspaceCwd()
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
			if fp := m.footerProviderSnapshot(); fp != nil {
				fp.RefreshGitBranch()
			}
			m.ShowInfo("Branch", "Switched to "+branch)
			m.updateFooterAndTitle()
			m.refreshGitBranchesAsync()
			m.refreshRightPanelChanges()
		})
	}()
}

func (m *GuiMode) createAndCheckoutGitBranch(branchName string) {
	if m == nil {
		return
	}
	branchName = strings.TrimSpace(branchName)
	if branchName == "" || strings.ContainsAny(branchName, " \t\r\n") {
		m.ShowError("Invalid branch name")
		return
	}
	cwd := m.activeWorkspaceCwd()
	if cwd == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "git", "-C", cwd, "checkout", "-b", branchName).CombinedOutput()
		if err != nil {
			m.postUI(func() {
				msg := strings.TrimSpace(string(out))
				if msg == "" {
					msg = err.Error()
				}
				m.ShowError("git checkout -b failed: " + msg)
			})
			return
		}
		m.postUI(func() {
			if fp := m.footerProviderSnapshot(); fp != nil {
				fp.RefreshGitBranch()
			}
			m.ShowInfo("Branch", "Created and switched to "+branchName)
			m.updateFooterAndTitle()
			m.refreshGitBranchesAsync()
			m.refreshRightPanelChanges()
		})
	}()
}

func (m *GuiMode) createAndSwitchGitWorktree() {
	if m == nil {
		return
	}
	cwd := m.activeWorkspaceCwd()
	if cwd == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		topOut, topErr := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
		if topErr != nil {
			m.postUI(func() {
				m.ShowError("Cannot create worktree outside a git repository")
			})
			return
		}
		mainRoot := filepath.Clean(strings.TrimSpace(string(topOut)))
		if wtOut, wtErr := exec.CommandContext(ctx, "git", "-C", cwd, "worktree", "list", "--porcelain").Output(); wtErr == nil {
			if list := wakuParseGitWorktreeListPorcelain(string(wtOut)); len(list) > 0 && list[0].Path != "" {
				mainRoot = list[0].Path
			}
		}

		wtBaseDir := filepath.Join(utils.GetAgentDir(), "worktrees", wakuSanitizePathSegment(filepath.Base(mainRoot)))
		if mkErr := os.MkdirAll(wtBaseDir, 0o755); mkErr != nil {
			m.postUI(func() {
				m.ShowError("Failed to prepare worktrees directory: " + mkErr.Error())
			})
			return
		}

		stamp := time.Now().Format("0102-150405")
		var wtName, wtPath string
		for idx := 0; idx < 100; idx++ {
			candidate := "wt-" + stamp
			if idx > 0 {
				candidate = fmt.Sprintf("wt-%s-%d", stamp, idx)
			}
			candPath := filepath.Join(wtBaseDir, candidate)
			if _, statErr := os.Stat(candPath); statErr == nil {
				continue
			}
			if exec.CommandContext(ctx, "git", "-C", mainRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+candidate).Run() == nil {
				continue
			}
			wtName = candidate
			wtPath = candPath
			break
		}
		if wtName == "" {
			m.postUI(func() {
				m.ShowError("Failed to allocate unique worktree name")
			})
			return
		}

		out, err := exec.CommandContext(ctx, "git", "-C", mainRoot, "worktree", "add", "-b", wtName, wtPath).CombinedOutput()
		if err != nil {
			m.postUI(func() {
				msg := strings.TrimSpace(string(out))
				if msg == "" {
					msg = err.Error()
				}
				m.ShowError("git worktree add failed: " + msg)
			})
			return
		}
		m.postUI(func() {
			m.ShowInfo("Worktree", "Created worktree "+wtName)
			m.switchProjectDirectory(wtPath)
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
	openTabs := append([]string(nil), rp.openTabs...)
	if len(openTabs) == 0 {
		openTabs = []string{"changes", "files", "terminal", "tree"}
		rp.openTabs = append([]string(nil), openTabs...)
	}
	addSurfaceOpen := rp.addSurfaceOpen
	files := append([]wakuChangedFile(nil), rp.files...)
	selected := rp.selectedPath
	sideBySide := rp.diffSideBySide
	browseFiles := append([]string(nil), rp.browseFiles...)
	browseCwd := rp.browseCwd
	selectedBrowse := rp.selectedBrowseFile
	previewPath := rp.previewPath
	previewContent := rp.previewContent
	termOut := rp.termOutput
	rp.mu.Unlock()

	wsCwd := gmRuntimeCwd(m.runtime)
	if sess := m.Session(); sess != nil && sess.Cwd() != "" {
		wsCwd = sess.Cwd()
	}
	if tab == "files" && wsCwd != "" && (wakuResolvePath(browseCwd) != wakuResolvePath(wsCwd) || len(browseFiles) == 0) {
		listed := rpListWorkspaceFiles(wsCwd)
		firstPath := selectedBrowse
		if firstPath == "" && len(listed) > 0 {
			firstPath = listed[0]
		}
		prevTxt := ""
		if firstPath != "" {
			prevTxt = rpReadFilePreview(wsCwd, firstPath)
		}
		rp.mu.Lock()
		rp.browseCwd = wsCwd
		rp.browseFiles = listed
		if rp.selectedBrowseFile == "" {
			rp.selectedBrowseFile = firstPath
		}
		rp.previewPath = firstPath
		rp.previewContent = prevTxt
		browseFiles = listed
		selectedBrowse = rp.selectedBrowseFile
		previewPath = firstPath
		previewContent = prevTxt
		rp.mu.Unlock()
	}

	var (
		closeClicked   bool
		openCommitDlg  bool
		refreshClicked bool
		toggleLayout   bool
		setTab         string
		closeTab       string
		addTab         string
		selectFilePath string
		selectBrowse   string
		runTermCmd     string
		navigateTreeId string
		openFullTree   bool
	)

	panel := ui.Column(c).
		Key("waku-right-panel").
		Width(width).
		Shrink(0).
		FillHeight().
		Background(th.Background).
		Role(ui.RoleRegion).
		Label("Right Panel")

	panel.Children(func() {
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
		// Top 48px tab & action header (exact Waku right_panel.rs padding [0, 14, 0, 10])
		h := wakuHeaderHeight(c)
		ui.Row(c).
			FillWidth().
			Height(h).
			Padding(0, 14, 0, 10).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween).
			Gap(6).
			Role(ui.RoleRegion).
			Label("Right Panel Header").
			Children(func() {
				// 28px closable surface tabs row
				compactTabs := len(openTabs) >= 4
				tabListGap := float32(4)
				if compactTabs {
					tabListGap = 2
				}
				ui.Row(c).
					Grow(1).
					Shrink(1).
					MinWidth(0).
					Height(28).
					AlignItems(ui.Center).
					Gap(tabListGap).
					Clip().
					Role(ui.RoleTabList).
					Label("Right Panel Tabs").
					Children(func() {
						for _, tName := range openTabs {
							tID := tName
							tLabel := rpTabDisplayName(tID)
							clicked, closed := rpRenderSurfaceTabWithCompact(c, th, tID, tLabel, tab == tID, true, compactTabs)
							if closed {
								closeTab = tID
							} else if clicked {
								setTab = tID
							}
						}
					})

				ui.Row(c).Shrink(0).Padding(0, 0, 0, 12).AlignItems(ui.Center).Gap(6).Children(func() {
					addBtn := ui.ButtonBase(c).
						Key("rp-add-surface-btn").
						Size(26, 26).
						Radius(6).
						AlignItems(ui.Center).
						Justify(ui.Center).
						Cursor(ui.CursorPointer).
						Label("Add Right Panel Surface")
					if addSurfaceOpen || addBtn.Hovered() {
						addBtn.Background(th.Overlay)
					}
					addBtn.Children(func() {
						gui.IconView(c, gui.IconPlus, 14, th.TextSecondary)
					})
					if addBtn.Clicked() {
						addSurfaceOpen = !addSurfaceOpen
					}

					ui.PopoverBase(c, addBtn, &addSurfaceOpen, func(popPanel ui.Element) {
						popPanel.AttachTo(addBtn, ui.AnchorBottomEnd, ui.AnchorTopEnd).
							Margin(4, 0, 0, 0).
							MinWidth(160).
							Background(th.Raised).
							Border(1, th.BorderStrong).
							Radius(9).
							Padding(4).
							Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.34)).
							Role(ui.RoleMenu).
							Label("Add Right Panel Surface Menu").
							Children(func() {
								ui.Column(c).FillWidth().Gap(2).Children(func() {
									for _, tID := range []string{"changes", "files", "terminal", "tree"} {
										idCopy := tID
										lbl := rpTabDisplayName(idCopy)
										if wakuSidebarOptionRow(c, th, lbl, tab == idCopy) {
											addTab = idCopy
											addSurfaceOpen = false
										}
									}
								})
							})
					})

					if wakuIconButton(c, th, gui.IconPanelRight, "Toggle Right Panel", false, th.TextSecondary) {
						closeClicked = true
					}
				})
			})

		ui.Column(c).
			Fill().
			MinWidth(0).
			MinHeight(0).
			Role(ui.RoleRegion).
			Label("Right Panel Body").
			Children(func() {
				switch tab {
				case "changes":
					// Top changes toolbar with 26px Commit action button
					ui.Row(c).
						FillWidth().
						Padding(8, 16, 4, 16).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween).
						Gap(8).
						Children(func() {
							ui.Row(c).Shrink(1).AlignItems(ui.Center).Gap(6).Children(func() {
								countLbl := "No unstaged changes"
								if len(files) > 0 {
									countLbl = fmt.Sprintf("%d %s changed", len(files), wakuPlural(len(files), "file", "files"))
								}
								ui.Text(c, countLbl).TextColor(th.TextSecondary).FontSize(12).SingleLine()
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
							})
							commitBtn := ui.ButtonBase(c).
								Key("rp-changes-commit-btn").
								Height(26).
								Padding(0, 10, 0, 10).
								Radius(6).
								Background(th.Overlay).
								Border(1, th.BorderStrong).
								AlignItems(ui.Center).
								Justify(ui.Center).
								Gap(6).
								Cursor(ui.CursorPointer).
								Label("Commit")
							if commitBtn.Hovered() {
								commitBtn.Background(th.OverlayStrong)
							}
							commitBtn.Children(func() {
								gui.IconView(c, gui.IconGitBranch, 12, th.Text)
								ui.Text(c, "Commit").TextColor(th.Text).FontSize(12).FontWeight(500).SingleLine()
							})
							if commitBtn.Clicked() {
								openCommitDlg = true
							}
						})

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

				case "files":
					ui.Row(c).Fill().MinWidth(0).Gap(0).Children(func() {
						// 184px left file tree pane
						ui.Scroll(c).
							Key("rp-files-tree").
							Width(184).
							Shrink(0).
							FillHeight().
							Padding(6).
							Role(ui.RoleList).
							Label("Workspace Files Tree").
							Children(func() {
								ui.Column(c).FillWidth().Gap(2).Children(func() {
									if len(browseFiles) == 0 {
										ui.Text(c, "Empty workspace").TextColor(th.TextTertiary).FontSize(12).Padding(6)
										return
									}
									for idx, rel := range browseFiles {
										p := rel
										isSel := p == selectedBrowse
										btn := ui.ButtonBase(c).
											Key(fmt.Sprintf("rp-browse-%d-%s", idx, p)).
											FillWidth().
											Height(26).
											Padding(0, 8, 0, 8).
											Radius(6).
											AlignItems(ui.Center).
											Justify(ui.Start).
											Gap(6).
											Cursor(ui.CursorPointer).
											Label(p)
										if isSel {
											btn.Background(th.SidebarItemBg)
										} else if btn.Hovered() {
											btn.Background(th.Overlay)
										}
										btn.Children(func() {
											gui.IconView(c, gui.IconFile, 12, th.TextSecondary)
											ui.Text(c, p).TextColor(th.Text).FontSize(12).SingleLine().Shrink(1)
										})
										if btn.Clicked() {
											selectBrowse = p
										}
									}
								})
							})

						ui.Box(c).Width(1).FillHeight().Background(th.Border)

						// File preview pane
						ui.Scroll(c).FillHeight().Grow(1).MinWidth(0).Padding(12).Children(func() {
							ui.Column(c).FillWidth().Gap(6).Children(func() {
								if previewPath == "" {
									ui.Text(c, "Select a file to preview").TextColor(th.TextTertiary).FontSize(12)
									return
								}
								ui.Text(c, previewPath).TextColor(th.TextSecondary).Font("monospace").FontSize(11.5).FontWeight(600).SingleLine()
								ui.Text(c, previewContent).TextColor(th.Text).Font("monospace").FontSize(12).Wrap().Selectable()
							})
						})
					})

				case "terminal":
					ui.Column(c).
						Key("rp-terminal-surface").
						Fill().
						Background(th.TerminalBg).
						Padding(10, 12, 10, 12).
						Gap(4).
						Role(ui.RoleRegion).
						Label("Terminal Surface").
						Children(func() {
							ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
								ui.Text(c, termOut).TextColor(th.Text).Font("monospace").FontSize(12).Wrap().Selectable()
							})
							ui.Box(c).FillWidth().Height(1).Background(th.Border)
							var cmdIn ui.Element
							ui.Row(c).FillWidth().Height(30).AlignItems(ui.Center).Gap(6).Children(func() {
								ui.Text(c, "$").TextColor(th.Accent).Font("monospace").FontSize(12).FontWeight(600)
								cmdIn = ui.TextInputBase(c, &rp.termCmd).
									Grow(1).
									Shrink(1).
									MinWidth(0).
									Font("monospace").
									FontSize(12).
									TextColor(th.Text).
									Placeholder("Run shell command…").
									Label("Terminal Command Input")
							})
							cmdIn.OnTextKey(func(k ui.TextKey) bool {
								if k.Key == ui.KeyEnter && k.Mods == 0 {
									cmd := strings.TrimSpace(k.Text())
									if cmd != "" {
										runTermCmd = cmd
										k.SetText("")
									}
									return true
								}
								return false
							})
						})

				default:
					// Session Tree tab (render from thread-safe snapshot)
					rp.mu.Lock()
					hasTree := rp.hasTreeSession
					treeItems := append([]wakuTreeFlatItem(nil), rp.treeItems...)
					leafId := rp.treeLeafID
					rp.mu.Unlock()
					ui.Column(c).Fill().Children(func() {
						ui.Scroll(c).
							Key("rp-tree-body").
							Fill().
							Padding(8).
							Role(ui.RoleList).
							Label("Session Tree Body").
							Children(func() {
								if !hasTree {
									ui.Column(c).Fill().Padding(24).AlignItems(ui.Center).Justify(ui.Center).Gap(10).Children(func() {
										ui.Text(c, "No active session tree").TextColor(th.TextTertiary).FontSize(12)
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
									return
								}
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
									ui.Row(c).FillWidth().Padding(8, 2, 4, 2).AlignItems(ui.Center).Justify(ui.End).Children(func() {
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
							})
					})
				}
			})
	})

	rp.mu.Lock()
	rp.addSurfaceOpen = addSurfaceOpen
	rp.mu.Unlock()

	if closeClicked {
		rp.mu.Lock()
		rp.open = false
		rp.mu.Unlock()
		m.saveLayoutPrefs()
	}
	if closeTab != "" {
		rp.mu.Lock()
		nextTabs := make([]string, 0, len(rp.openTabs))
		for _, tID := range rp.openTabs {
			if tID != closeTab {
				nextTabs = append(nextTabs, tID)
			}
		}
		if len(nextTabs) == 0 {
			rp.open = false
		} else {
			rp.openTabs = nextTabs
			if rp.activeTab == closeTab {
				rp.activeTab = nextTabs[0]
			}
		}
		rp.mu.Unlock()
		m.saveLayoutPrefs()
	}
	if addTab != "" {
		rp.mu.Lock()
		found := false
		for _, tID := range rp.openTabs {
			if tID == addTab {
				found = true
				break
			}
		}
		if !found {
			rp.openTabs = append(rp.openTabs, addTab)
		}
		rp.activeTab = addTab
		rp.mu.Unlock()
		m.saveLayoutPrefs()
		if addTab == "changes" {
			m.refreshRightPanelChanges()
		} else if addTab == "tree" {
			m.refreshRightPanelTreeSnapshot()
		}
	}
	if refreshClicked {
		m.refreshRightPanelChanges()
	}
	if toggleLayout {
		rp.mu.Lock()
		rp.diffSideBySide = !rp.diffSideBySide
		rp.mu.Unlock()
	}
	if openCommitDlg {
		m.ShowCommitDialog()
	}
	if selectBrowse != "" {
		prevTxt := rpReadFilePreview(wsCwd, selectBrowse)
		rp.mu.Lock()
		rp.selectedBrowseFile = selectBrowse
		rp.previewPath = selectBrowse
		rp.previewContent = prevTxt
		rp.mu.Unlock()
	}
	if runTermCmd != "" && wsCwd != "" {
		go m.runRightPanelTerminalCommand(wsCwd, runTermCmd)
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

func rpTabDisplayName(tabID string) string {
	switch tabID {
	case "changes":
		return "Changes"
	case "files":
		return "Files"
	case "terminal":
		return "Terminal"
	case "tree":
		return "Tree"
	default:
		return tabID
	}
}

func rpTabIcon(tabID string) *ui.SVG {
	switch tabID {
	case "changes":
		return gui.IconFileDiff
	case "files":
		return gui.IconFolder
	case "terminal":
		return gui.IconTerminal
	case "tree":
		return gui.IconGitBranch
	default:
		return gui.IconFile
	}
}

func rpRenderSurfaceTab(
	c *ui.Context,
	th *gui.GuiTheme,
	tabID, label string,
	active, closable bool,
) (clicked, closed bool) {
	return rpRenderSurfaceTabWithCompact(c, th, tabID, label, active, closable, false)
}

func rpRenderSurfaceTabWithCompact(
	c *ui.Context,
	th *gui.GuiTheme,
	tabID, label string,
	active, closable, compact bool,
) (clicked, closed bool) {
	padX := float32(8)
	gap := float32(6)
	if compact {
		padX = 5
		gap = 4
	}
	btn := ui.ButtonBase(c).
		Key("rp-tab-"+tabID).
		Height(28).
		Shrink(0).
		Padding(0, padX, 0, padX).
		Radius(7).
		AlignItems(ui.Center).
		Gap(gap).
		Cursor(ui.CursorPointer).
		Role(ui.RoleTab).
		Selected(active).
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
		gui.IconView(c, rpTabIcon(tabID), 13, col)
		ui.Text(c, label).TextColor(col).FontSize(12).FontWeight(weight).SingleLine()
		if closable {
			closeBtn := ui.ButtonBase(c).
				Key("rp-tab-close-"+tabID).
				Size(16, 16).
				Radius(4).
				AlignItems(ui.Center).
				Justify(ui.Center).
				Cursor(ui.CursorPointer).
				Label("Close " + label + " tab")
			if closeBtn.Hovered() {
				closeBtn.Background(th.OverlayStrong)
			}
			closeBtn.Children(func() {
				gui.IconView(c, gui.IconClose, 10, th.TextTertiary)
			})
			if closeBtn.Clicked() {
				closed = true
			}
		}
	})
	if btn.Clicked() && !closed {
		clicked = true
	}
	return clicked, closed
}

func rpListWorkspaceFiles(cwd string) []string {
	entries, err := os.ReadDir(cwd)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			out = append(out, name+"/")
		} else {
			out = append(out, name)
		}
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func rpReadFilePreview(cwd, relPath string) string {
	if strings.HasSuffix(relPath, "/") {
		sub := strings.TrimSuffix(relPath, "/")
		subEntries, err := os.ReadDir(filepath.Join(cwd, sub))
		if err != nil {
			return err.Error()
		}
		var sb strings.Builder
		for _, se := range subEntries {
			sb.WriteString(se.Name())
			if se.IsDir() {
				sb.WriteByte('/')
			}
			sb.WriteByte('\n')
		}
		return sb.String()
	}
	clean := filepath.Clean(relPath)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(cwd, clean))
	if err != nil {
		return err.Error()
	}
	if len(data) > 16384 {
		data = data[:16384]
	}
	return string(data)
}

func (m *GuiMode) runRightPanelTerminalCommand(cwd, command string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil && text == "" {
		text = err.Error() + "\n"
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	m.rightPanel.mu.Lock()
	m.rightPanel.termOutput += "$ " + command + "\n" + text
	if len(m.rightPanel.termOutput) > 16384 {
		m.rightPanel.termOutput = m.rightPanel.termOutput[len(m.rightPanel.termOutput)-16384:]
	}
	m.rightPanel.mu.Unlock()
	if m.host != nil {
		m.host.RequestRender()
	}
}

func rpGitCommit(cwd, message string, includeUnstaged bool) error {
	if strings.TrimSpace(cwd) == "" {
		return fmt.Errorf("no active repository path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if includeUnstaged {
		if out, err := exec.CommandContext(ctx, "git", "-C", cwd, "add", "-A").CombinedOutput(); err != nil {
			return fmt.Errorf("git add failed: %s", strings.TrimSpace(string(out)))
		}
	}
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "commit", "-m", message).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commit failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func rpGitPush(cwd string) error {
	if strings.TrimSpace(cwd) == "" {
		return fmt.Errorf("no active repository path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "push").CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
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
