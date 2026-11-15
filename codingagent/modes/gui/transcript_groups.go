package gui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
)

type wakuModFileStat struct {
	path string
	adds int
	dels int
}

// renderWakuTranscript renders the centered 760px Waku transcript stream inside the
// ScrollView, including empty state, collapsible "Worked for ..." turn clusters,
// changed-files review cards, live working pulse, and notices.
func (m *GuiMode) renderWakuTranscript(c *ui.Context, th *gui.GuiTheme) {
	if m == nil {
		return
	}
	m.ensureWakuState()

	m.mu.Lock()
	entries := append([]gmTranscriptEntry(nil), m.entries...)
	hdrBanner := m.headerBanner
	ftrBanner := m.footerBanner
	turnStarted := m.turnStartedAt
	bashRunning := m.bashRunning
	turnDurs := make(map[string]time.Duration, len(m.turnDurations))
	for k, v := range m.turnDurations {
		turnDurs[k] = v
	}
	collapsedTurns := make(map[string]bool, len(m.collapsedTurns))
	for k, v := range m.collapsedTurns {
		collapsedTurns[k] = v
	}
	m.mu.Unlock()

	sess := m.Session()
	cwd := gmRuntimeCwd(m.runtime)
	if sess != nil && sess.Cwd() != "" {
		cwd = sess.Cwd()
	}
	isStreaming := sess != nil && (sess.IsStreaming() || sess.IsCompacting() || sess.IsRetrying())
	sbWorking, sbWorkingMsg, sbCompacting, sbRetrying, sbRetryAttempt, sbRetryMax, sbRetryDelay, sbRetryErr, sbNotice, sbNoticeErr := m.statusBanner.StateSnapshot()
	isBusy := isStreaming || bashRunning || sbWorking || sbCompacting || sbRetrying

	var toggleTurnKey string
	var openReviewFile string
	var launchStarterPrompt string
	var openSessionPicker bool

	wrap := ui.Column(c).FillWidth().AlignItems(ui.Center)
	wrap.Children(func() {
		col := ui.Column(c).
			FillWidth().
			MaxWidth(760).
			Padding(16, 24, 14, 24).
			Gap(14)

		col.Children(func() {
			// Compact header / extension warnings (if any)
			if hdrBanner != "" {
				ui.Text(c, hdrBanner).TextColor(th.TextTertiary).FontSize(12).Wrap()
			}
			var convEntries []gmTranscriptEntry
			for _, e := range entries {
				if e.kind == "header" {
					if ha, ok := e.card.(*HeaderAnnouncement); ok && ha != nil && ha.HasActiveAlerts() {
						ui.Box(c).Key("waku-hdr-alerts").FillWidth().Children(func() {
							ha.ViewCompactAlerts(c)
						})
					}
					continue
				}
				convEntries = append(convEntries, e)
			}

			// Show Waku Empty State when conversation has no turns yet and nothing is running.
			if len(convEntries) == 0 && !isBusy {
				prompt, resume := renderWakuEmptyState(c, th, cwd)
				if prompt != "" {
					launchStarterPrompt = prompt
				}
				if resume {
					openSessionPicker = true
				}
			}

			// Partition conversation entries into user/standalone cards and assistant+tool turn clusters.
			i := 0
			for i < len(convEntries) {
				cur := convEntries[i]
				if cur.kind != "assistant" && cur.kind != "tool" {
					e := cur
					if e.card != nil {
						ui.Box(c).Key("entry-" + e.id).FillWidth().Children(func() {
							e.card.View(c)
						})
					}
					i++
					continue
				}

				// Maximal run of assistant / tool entries forming one agent turn.
				j := i
				for j < len(convEntries) && (convEntries[j].kind == "assistant" || convEntries[j].kind == "tool") {
					j++
				}
				turnEntries := convEntries[i:j]
				isLastTurn := j == len(convEntries)
				turnActive := isLastTurn && isBusy
				turnKey := turnEntries[0].id

				// Find final prose-bearing assistant entry (kept borderless below the tool cluster).
				finalAsstIdx := -1
				for k := len(turnEntries) - 1; k >= 0; k-- {
					if turnEntries[k].kind != "assistant" {
						continue
					}
					if ac, ok := turnEntries[k].card.(*AssistantMessageCard); ok && ac != nil {
						if ac.HasProseText() || (turnActive && k == len(turnEntries)-1) {
							finalAsstIdx = k
							break
						}
					}
				}

				var activity []gmTranscriptEntry
				var modFiles []wakuModFileStat
				modFileIdx := make(map[string]int)
				var toolCount int
				var summedToolDur time.Duration
				var anyToolPending bool

				for k, te := range turnEntries {
					if k == finalAsstIdx {
						continue
					}
					activity = append(activity, te)
					if te.kind == "tool" {
						toolCount++
						if tc, ok := te.card.(*ToolExecutionCard); ok && tc != nil {
							if tc.IsPending() {
								anyToolPending = true
							}
							summedToolDur += tc.Duration()
							if fp, adds, dels := tc.ModifiedFileStats(); fp != "" {
								if idx, exists := modFileIdx[fp]; exists {
									modFiles[idx].adds += adds
									modFiles[idx].dels += dels
								} else {
									modFileIdx[fp] = len(modFiles)
									modFiles = append(modFiles, wakuModFileStat{path: fp, adds: adds, dels: dels})
								}
							}
						}
					}
				}

				turnCol := ui.Column(c).Key("turn-" + turnKey).FillWidth().Gap(8)
				turnCol.Children(func() {
					if len(activity) > 0 {
						elapsed := turnDurs[turnKey]
						if elapsed <= 0 {
							elapsed = summedToolDur
						}
						if elapsed <= 0 && turnActive && !turnStarted.IsZero() {
							elapsed = time.Since(turnStarted)
						}

						var headerLbl string
						if turnActive || anyToolPending {
							if elapsed >= time.Second {
								headerLbl = fmt.Sprintf("Working for %s", FormatDuration(elapsed))
							} else {
								headerLbl = "Working"
							}
							if toolCount > 0 {
								headerLbl = fmt.Sprintf("%s · %d %s", headerLbl, toolCount, wakuPlural(toolCount, "tool", "tools"))
							}
						} else {
							if elapsed > 0 {
								headerLbl = fmt.Sprintf("Worked for %s", FormatDuration(elapsed))
							} else if toolCount > 0 {
								headerLbl = fmt.Sprintf("Worked · %d %s", toolCount, wakuPlural(toolCount, "step", "steps"))
							} else {
								headerLbl = "Worked for a moment"
							}
						}

						isCollapsed := collapsedTurns[turnKey]
						hdrBtn := ui.ButtonBase(c).
							Key("turn-toggle-" + turnKey).
							FillWidth().
							Height(24).
							AlignItems(ui.Center).
							Gap(8).
							Cursor(ui.CursorPointer).
							Label(headerLbl)
						hdrBtn.Children(func() {
							ui.Box(c).Width(18).Height(1).Background(th.Border)
							chevSvg := gui.IconChevronDown
							if isCollapsed {
								chevSvg = gui.IconChevronRight
							}
							gui.IconView(c, chevSvg, 12, th.TextTertiary)
							lblCol := th.TextTertiary
							if hdrBtn.Hovered() {
								lblCol = th.TextSecondary
							}
							ui.Text(c, headerLbl).TextColor(lblCol).FontSize(12).SingleLine()
							ui.Box(c).Grow(1).Height(1).Background(th.Border)
						})
						if hdrBtn.Clicked() {
							toggleTurnKey = turnKey
						}

						if !isCollapsed {
							ui.Row(c).
								Key("turn-body-"+turnKey).
								FillWidth().
								Padding(2, 0, 2, 6).
								Gap(10).
								Children(func() {
									ui.Box(c).Width(1).FillHeight().Background(th.BorderStrong)
									ui.Column(c).Grow(1).Shrink(1).Gap(6).Children(func() {
										for aIdx, act := range activity {
											if act.card != nil {
												actEntry := act
												ui.Box(c).Key(fmt.Sprintf("act-%s-%d", actEntry.id, aIdx)).FillWidth().Children(func() {
													actEntry.card.View(c)
												})
											}
										}
									})
								})
						}
					}

					if finalAsstIdx >= 0 && turnEntries[finalAsstIdx].card != nil {
						finalEntry := turnEntries[finalAsstIdx]
						ui.Box(c).Key("final-" + finalEntry.id).FillWidth().Children(func() {
							finalEntry.card.View(c)
						})
					}

					if !turnActive && len(modFiles) > 0 {
						if picked := renderWakuChangedFilesCard(c, th, turnKey, modFiles); picked != "" {
							openReviewFile = picked
						}
					}
				})

				i = j
			}

			// Live Waku working pulse row when streaming/compacting/retrying/running bash.
			if isBusy {
				var liveMsg string
				switch {
				case sbRetrying:
					liveMsg = fmt.Sprintf("Retrying (%d/%d in %s)", sbRetryAttempt, sbRetryMax, FormatDuration(sbRetryDelay))
					if sbRetryErr != "" {
						liveMsg += ": " + selTruncateSingleLine(sbRetryErr, 72)
					}
				case sbCompacting:
					liveMsg = "Compacting context..."
				case strings.TrimSpace(sbWorkingMsg) != "":
					liveMsg = sbWorkingMsg
				case !turnStarted.IsZero():
					d := time.Since(turnStarted)
					if d >= time.Second {
						liveMsg = fmt.Sprintf("Working for %s...", FormatDuration(d))
					} else {
						liveMsg = "Working..."
					}
				default:
					liveMsg = "Working..."
				}

				ui.Row(c).
					Key("waku-live-working").
					FillWidth().
					Padding(4, 2).
					AlignItems(ui.Center).
					Gap(8).
					Children(func() {
						spinnerCol := th.Accent
						if sbRetrying {
							spinnerCol = th.Warning
						}
						ui.Spinner(c).TextColor(spinnerCol).Label("Working")
						ui.Text(c, liveMsg).TextColor(th.TextSecondary).FontSize(12).SingleLine()
					})
			}

			// Non-working custom notice from StatusBanner (e.g. model fallback or Ctrl+C hint).
			if sbNotice != "" {
				nBorder := th.BorderStrong
				nIconCol := th.Accent
				nTextCol := th.TextSecondary
				nSvg := gui.IconInfo
				if sbNoticeErr {
					nBorder = th.Error.Alpha(0.6)
					nIconCol = th.Error
					nTextCol = th.Error
					nSvg = gui.IconAlert
				} else if strings.Contains(strings.ToLower(sbNotice), "fallback") || strings.Contains(strings.ToLower(sbNotice), "warn") {
					nBorder = th.Warning.Alpha(0.55)
					nIconCol = th.Warning
					nTextCol = th.Warning
					nSvg = gui.IconAlert
				}
				ui.Row(c).
					Key("waku-status-notice").
					FillWidth().
					Background(th.Raised).
					Border(1, nBorder).
					Radius(8).
					Padding(7, 11).
					AlignItems(ui.Center).
					Gap(8).
					Children(func() {
						gui.IconView(c, nSvg, 13, nIconCol)
						ui.Text(c, sbNotice).TextColor(nTextCol).FontSize(12).Wrap()
					})
			}

			if ftrBanner != "" {
				ui.Text(c, ftrBanner).TextColor(th.TextTertiary).FontSize(11).Wrap()
			}
		})
	})

	if toggleTurnKey != "" {
		m.mu.Lock()
		if m.collapsedTurns == nil {
			m.collapsedTurns = make(map[string]bool)
		}
		m.collapsedTurns[toggleTurnKey] = !m.collapsedTurns[toggleTurnKey]
		m.mu.Unlock()
		if m.host != nil {
			m.host.RequestRender()
		}
	}
	if openReviewFile != "" {
		m.openRightPanelFileDiff(openReviewFile)
	}
	if openSessionPicker {
		m.ShowSessionSelector()
	}
	if launchStarterPrompt != "" {
		go m.dispatchTextOrPrompt(m.currentCtx(), launchStarterPrompt, nil, false)
	}
}

func renderWakuEmptyState(c *ui.Context, th *gui.GuiTheme, cwd string) (selectedPrompt string, openResume bool) {
	projName := filepath.Base(cwd)
	if projName == "" || projName == "." || projName == "/" {
		projName = "pi"
	}
	shortCwd := ""
	if cwd != "" {
		shortCwd = utils.ShortenHomePath(cwd)
	}

	box := ui.Column(c).
		Key("waku-empty-state").
		FillWidth().
		Padding(44, 16, 24, 16).
		AlignItems(ui.Center).
		Gap(14)

	box.Children(func() {
		ui.Box(c).
			Size(48, 48).
			Radius(13).
			Background(th.Raised).
			Border(1, th.BorderStrong).
			AlignItems(ui.Center).
			Justify(ui.Center).
			Children(func() {
				gui.IconView(c, gui.IconPiLogo, 24, th.Accent)
			})

		ui.Column(c).AlignItems(ui.Center).Gap(4).Children(func() {
			ui.Text(c, projName).TextColor(th.Text).FontSize(20).FontWeight(600)
			if shortCwd != "" {
				ui.Text(c, shortCwd).TextColor(th.TextTertiary).Font("monospace").FontSize(11)
			}
			ui.Text(c, "Ask anything, @ to mention files, / for commands").TextColor(th.TextSecondary).FontSize(13)
		})

		ui.Row(c).Wrap().Justify(ui.Center).Gap(8).Margin(4, 0, 0, 0).Children(func() {
			starters := []struct {
				label  string
				prompt string
				resume bool
			}{
				{label: "Review changes", prompt: "Inspect the current working tree changes and summarize any issues or improvements."},
				{label: "Explain codebase", prompt: "Summarize the project structure and key entry points in this workspace."},
				{label: "Resume session", resume: true},
			}
			for i, st := range starters {
				item := st
				chip := ui.ButtonBase(c).
					Key(fmt.Sprintf("empty-starter-%d", i)).
					Height(30).
					Padding(0, 12, 0, 12).
					Radius(999).
					Background(th.Raised).
					Border(1, th.Border).
					AlignItems(ui.Center).
					Gap(6).
					Cursor(ui.CursorPointer).
					Label(item.label)
				if chip.Hovered() {
					chip.Background(th.Overlay).Border(1, th.BorderStrong)
				}
				chip.Children(func() {
					svg := gui.IconSparkle
					if item.resume {
						svg = gui.IconSearch
					} else if i == 0 {
						svg = gui.IconChanges
					}
					gui.IconView(c, svg, 13, th.TextSecondary)
					ui.Text(c, item.label).TextColor(th.TextSecondary).FontSize(12).SingleLine()
				})
				if chip.Clicked() {
					if item.resume {
						openResume = true
					} else {
						selectedPrompt = item.prompt
					}
				}
			}
		})
	})

	return selectedPrompt, openResume
}

func renderWakuChangedFilesCard(c *ui.Context, th *gui.GuiTheme, turnKey string, files []wakuModFileStat) string {
	var reviewTarget string
	totalAdds, totalDels := 0, 0
	for _, f := range files {
		totalAdds += f.adds
		totalDels += f.dels
	}

	card := ui.Row(c).
		Key("changed-files-"+turnKey).
		FillWidth().
		Background(th.Raised).
		Border(1, th.Border).
		Radius(10).
		Padding(8, 12).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Gap(10)

	card.Children(func() {
		ui.Row(c).Shrink(1).Grow(1).AlignItems(ui.Center).Wrap().Gap(8).Children(func() {
			gui.IconView(c, gui.IconFileDiff, 14, th.Accent)
			ui.Textf(c, "%d %s modified", len(files), wakuPlural(len(files), "file", "files")).
				TextColor(th.Text).FontSize(12).FontWeight(600).SingleLine()
			if totalAdds > 0 {
				ui.Textf(c, "+%d", totalAdds).TextColor(th.DiffAdded).Font("monospace").FontSize(11).SingleLine()
			}
			if totalDels > 0 {
				ui.Textf(c, "-%d", totalDels).TextColor(th.DiffRemoved).Font("monospace").FontSize(11).SingleLine()
			}
			limit := min(len(files), 3)
			for idx := 0; idx < limit; idx++ {
				f := files[idx]
				pill := ui.ButtonBase(c).
					Key(fmt.Sprintf("cf-%s-%d-%s", turnKey, idx, f.path)).
					Height(22).
					Padding(0, 7, 0, 7).
					Radius(6).
					Background(th.Inset).
					Border(1, th.Border).
					AlignItems(ui.Center).
					Gap(4).
					Cursor(ui.CursorPointer)
				if pill.Hovered() {
					pill.Border(1, th.BorderStrong)
				}
				pill.Children(func() {
					ui.Text(c, filepath.Base(f.path)).TextColor(th.TextSecondary).Font("monospace").FontSize(11).SingleLine()
				})
				if pill.Clicked() {
					reviewTarget = f.path
				}
			}
		})

		revBtn := ui.ButtonBase(c).
			Height(24).
			Padding(0, 10, 0, 10).
			Radius(7).
			Background(th.Overlay).
			Border(1, th.BorderStrong).
			AlignItems(ui.Center).
			Gap(5).
			Cursor(ui.CursorPointer).
			Label("Review changes")
		if revBtn.Hovered() {
			revBtn.Background(th.OverlayStrong)
		}
		revBtn.Children(func() {
			gui.IconView(c, gui.IconChanges, 12, th.Accent)
			ui.Text(c, "Review changes").TextColor(th.Text).FontSize(11).FontWeight(500).SingleLine()
		})
		if revBtn.Clicked() {
			reviewTarget = files[0].path
		}
	})

	return reviewTarget
}

func wakuPlural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
