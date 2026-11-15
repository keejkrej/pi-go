package gui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
)

// renderWakuComposerZone renders the centered MaxWidth(760) bottom composer zone:
// above-editor extension widgets, optional docked queue tray, 13px floating Waku
// composer card (chromeless gui.Editor + bottom inline model/thinking/attach/send-stop
// controls), 28px workspace footer with Context Usage gauge + popover, and below-editor widgets.
func (m *GuiMode) renderWakuComposerZone(c *ui.Context, th *gui.GuiTheme) {
	if m == nil {
		return
	}
	m.ensureWakuState()

	sess := m.Session()
	st := m.buildSafeFooterState(sess)

	m.mu.Lock()
	steering := append([]string(nil), m.steeringQueue...)
	followUp := append([]string(nil), m.followUpQueue...)
	widgets := append([]gmWidgetItem(nil), m.widgets...)
	bashRunning := m.bashRunning
	ctxPopoverOpen := m.contextPopoverOpen
	branchPopoverOpen := m.branchPopoverOpen
	m.mu.Unlock()

	if m.editor != nil && !m.editor.Chromeless() {
		m.editor.SetChromeless(true)
	}

	hasQueue := len(steering) > 0 || len(followUp) > 0
	isBusy := st.IsStreaming || st.IsCompacting || st.IsRetrying || bashRunning

	var (
		clearQueueClicked bool
		openModelClicked  bool
		openThinkClicked  bool
		attachClicked     bool
		stopClicked       bool
		sendClicked       bool
		followUpClicked   bool
		compactClicked    bool
		openChanges       bool
		switchBranch      string
	)

	outer := ui.Column(c).
		Key("waku-composer-outer").
		FillWidth().
		AlignItems(ui.Center)

	outer.Children(func() {
		col := ui.Column(c).
			FillWidth().
			MaxWidth(wakuContentMaxWidth).
			Padding(2, 20, 8, 20).
			Gap(0)

		col.Children(func() {
			// Above-editor extension widgets
			if wakuHasWidgets(widgets, core.WidgetPlacementAboveEditor) {
				ui.Column(c).Key("waku-widgets-above").FillWidth().Gap(4).Margin(0, 0, 6, 0).Children(func() {
					gmRenderWidgets(c, th, widgets, core.WidgetPlacementAboveEditor)
				})
			}

			// Docked queue tray directly attached above composer when messages are queued
			if hasQueue {
				tray := ui.Column(c).
					Key("waku-queue-tray").
					FillWidth().
					Background(th.Raised).
					Border(1, th.BorderStrong).
					Radius(12, 12, 0, 0).
					Padding(7, 12).
					Gap(4)
				tray.Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
							gui.IconView(c, gui.IconQueue, 13, th.Accent)
							if len(steering) > 0 {
								ui.Box(c).Background(th.Inset).Border(1, th.Border).Radius(5).Padding(1, 6).Children(func() {
									ui.Textf(c, "Steering (%d)", len(steering)).TextColor(th.Text).FontSize(11).FontWeight(500)
								})
							}
							if len(followUp) > 0 {
								ui.Box(c).Background(th.Inset).Border(1, th.Border).Radius(5).Padding(1, 6).Children(func() {
									ui.Textf(c, "Follow-up (%d)", len(followUp)).TextColor(th.Text).FontSize(11).FontWeight(500)
								})
							}
						})
						clrBtn := ui.ButtonBase(c).
							Height(22).
							Padding(0, 8, 0, 8).
							Radius(6).
							AlignItems(ui.Center).
							Cursor(ui.CursorPointer).
							Label("Clear Queue")
						if clrBtn.Hovered() {
							clrBtn.Background(th.Overlay)
						}
						clrBtn.Children(func() {
							ui.Text(c, "Clear Queue").TextColor(th.TextSecondary).FontSize(11).SingleLine()
						})
						if clrBtn.Clicked() {
							clearQueueClicked = true
						}
					})
					for idx, text := range steering {
						ui.Text(c, "→ [steer] "+msgSingleLinePreview(text, 92)).
							Key(fmt.Sprintf("q-steer-%d", idx)).
							TextColor(th.TextSecondary).
							FontSize(12).
							SingleLine()
					}
					for idx, text := range followUp {
						ui.Text(c, "↳ [follow-up] "+msgSingleLinePreview(text, 92)).
							Key(fmt.Sprintf("q-follow-%d", idx)).
							TextColor(th.TextSecondary).
							FontSize(12).
							SingleLine()
					}
				})
			}

			// Floating 13px Waku Composer Card
			borderCol := th.Border
			if m.editor != nil && m.editor.BashMode() {
				borderCol = th.Warning.Alpha(0.75)
			}
			card := ui.Column(c).
				Key("waku-composer-card").
				FillWidth().
				Background(th.ComposerBg).
				Border(1, borderCol).
				Shadow(0, 4, 18, 0, ui.RGBA(0, 0, 0, 0.14)).
				Padding(10, 12, 8, 12).
				Gap(6)
			if hasQueue {
				card.Radius(0, 0, wakuComposerRadius, wakuComposerRadius)
			} else {
				card.Radius(wakuComposerRadius)
			}

			card.Children(func() {
				if m.editor != nil {
					m.editor.View(c)
				}

				// Bottom inline controls bar
				ui.Row(c).
					FillWidth().
					Height(28).
					AlignItems(ui.Center).
					Justify(ui.SpaceBetween).
					Gap(8).
					Children(func() {
						// Left model / thinking / attach / bash indicator pills
						ui.Row(c).Shrink(1).AlignItems(ui.Center).Gap(4).Children(func() {
							modelLbl := st.ModelId
							if modelLbl == "" {
								modelLbl = "Select model"
							}
							modelBtn := ui.ButtonBase(c).
								Key("composer-model-chip").
								Height(24).
								Padding(0, 8, 0, 8).
								Radius(999).
								AlignItems(ui.Center).
								Gap(5).
								Cursor(ui.CursorPointer).
								Label("Select Model Chip")
							if modelBtn.Hovered() {
								modelBtn.Background(th.Overlay)
							}
							modelBtn.Children(func() {
								gui.IconView(c, gui.IconPiLogo, 12, th.Accent)
								ui.Text(c, modelLbl).TextColor(th.TextSecondary).FontSize(12).SingleLine()
								gui.IconView(c, gui.IconChevronDown, 10, th.TextTertiary)
							})
							if modelBtn.Clicked() {
								openModelClicked = true
							}

							thinkLbl := string(st.ThinkingLevel)
							if thinkLbl == "" {
								thinkLbl = "off"
							}
							thinkBtn := ui.ButtonBase(c).
								Key("composer-thinking-chip").
								Height(24).
								Padding(0, 8, 0, 8).
								Radius(999).
								AlignItems(ui.Center).
								Gap(5).
								Cursor(ui.CursorPointer).
								Label("Select Thinking Chip")
							if thinkBtn.Hovered() {
								thinkBtn.Background(th.Overlay)
							}
							thinkBtn.Children(func() {
								gui.IconView(c, gui.IconSparkle, 12, th.TextTertiary)
								ui.Text(c, thinkLbl).TextColor(th.TextSecondary).FontSize(12).SingleLine()
								gui.IconView(c, gui.IconChevronDown, 10, th.TextTertiary)
							})
							if thinkBtn.Clicked() {
								openThinkClicked = true
							}

							attBtn := ui.ButtonBase(c).
								Key("composer-attach-btn").
								Size(24, 24).
								Radius(999).
								AlignItems(ui.Center).
								Justify(ui.Center).
								Cursor(ui.CursorPointer).
								Label("Attach clipboard image")
							if attBtn.Hovered() {
								attBtn.Background(th.Overlay)
							}
							attBtn.Children(func() {
								gui.IconView(c, gui.IconPlus, 13, th.TextTertiary)
							})
							if attBtn.Clicked() {
								attachClicked = true
							}

							if m.editor != nil && m.editor.BashMode() {
								modeTxt := "! bash"
								if m.editor.BashExcludedFromContext() {
									modeTxt = "!! bash"
								}
								ui.Box(c).
									Background(th.Warning.Alpha(0.15)).
									Border(1, th.Warning.Alpha(0.5)).
									Radius(999).
									Padding(2, 8).
									Children(func() {
										ui.Text(c, modeTxt).TextColor(th.Warning).FontSize(11).FontWeight(600).SingleLine()
									})
							}
						})

						// Right circular Send / Queue / Stop buttons
						hasDraft := m.editor != nil && (strings.TrimSpace(m.editor.Text()) != "" || len(m.editor.Attachments()) > 0)
						ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Children(func() {
							if isBusy {
								if hasDraft {
									qBtn := ui.ButtonBase(c).
										Key("composer-queue-btn").
										Size(26, 26).
										Radius(999).
										Background(th.Inverse).
										AlignItems(ui.Center).
										Justify(ui.Center).
										Cursor(ui.CursorPointer).
										Label("Queue Follow-up")
									qBtn.Children(func() {
										gui.IconView(c, gui.IconArrowUp, 13, th.OnInverse)
									})
									if qBtn.Clicked() {
										followUpClicked = true
									}
								}

								stopBtn := ui.ButtonBase(c).
									Key("composer-stop-btn").
									Size(26, 26).
									Radius(999).
									Background(th.OverlayStrong).
									AlignItems(ui.Center).
									Justify(ui.Center).
									Cursor(ui.CursorPointer).
									Label("Stop")
								stopBtn.Children(func() {
									gui.IconView(c, gui.IconStop, 10, th.Text)
								})
								if stopBtn.Clicked() {
									stopClicked = true
								}
							} else {
								sendBg := th.OverlayStrong
								sendFg := th.TextTertiary
								if hasDraft {
									sendBg = th.Inverse
									sendFg = th.OnInverse
								}
								sendBtn := ui.ButtonBase(c).
									Key("composer-send-btn").
									Size(26, 26).
									Radius(999).
									Background(sendBg).
									AlignItems(ui.Center).
									Justify(ui.Center).
									Cursor(ui.CursorPointer).
									Label("Send")
								sendBtn.Children(func() {
									gui.IconView(c, gui.IconArrowUp, 13, sendFg)
								})
								if sendBtn.Clicked() {
									sendClicked = true
								}
							}
						})
					})
			})

			// 28px Waku Workspace Footer directly underneath composer card
			ui.Row(c).
				Key("waku-workspace-footer").
				FillWidth().
				Height(wakuWorkspaceFooterHeight).
				Padding(4, 10, 8, 10).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Gap(8).
				Children(func() {
					// Left workspace folder, environment, git branch switcher, and extension statuses
					ui.Row(c).Shrink(1).AlignItems(ui.Center).Gap(10).Children(func() {
						folderName := filepath.Base(st.Cwd)
						if folderName == "" || folderName == "." || folderName == "/" {
							if st.Cwd != "" {
								folderName = utils.ShortenHomePath(st.Cwd)
							} else {
								folderName = "workspace"
							}
						}
						ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
							gui.IconView(c, gui.IconFolder, 12, th.TextTertiary)
							ui.Text(c, folderName).TextColor(th.TextSecondary).FontSize(12).SingleLine()
						})

						ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
							gui.IconView(c, gui.IconLaptop, 12, th.TextTertiary)
							ui.Text(c, "Local").TextColor(th.TextSecondary).FontSize(12).SingleLine()
						})

						if st.GitBranch != nil && strings.TrimSpace(*st.GitBranch) != "" {
							brAnchor := ui.ButtonBase(c).
								Key("footer-git-branch").
								Height(20).
								Padding(0, 6, 0, 6).
								Radius(6).
								AlignItems(ui.Center).
								Gap(4).
								Cursor(ui.CursorPointer).
								Label("Switch Git Branch")
							if brAnchor.Hovered() {
								brAnchor.Background(th.Overlay)
							}
							brAnchor.Children(func() {
								gui.IconView(c, gui.IconGitBranch, 12, th.TextTertiary)
								ui.Text(c, *st.GitBranch).TextColor(th.TextSecondary).FontSize(12).SingleLine()
							})
							if brAnchor.Clicked() {
								branchPopoverOpen = !branchPopoverOpen
								if branchPopoverOpen {
									m.refreshGitBranchesAsync()
								}
							}
							ui.PopoverBase(c, brAnchor, &branchPopoverOpen, func(panel ui.Element) {
								panel.Width(240).
									Background(th.Raised).
									Border(1, th.BorderStrong).
									Radius(10).
									Shadow(0, 10, 26, 0, ui.RGBA(0, 0, 0, 0.32)).
									Padding(8)
								panel.Children(func() {
									branches, curBr := m.rightPanel.gitBranchesSnapshot()
									ui.Column(c).FillWidth().Gap(4).Children(func() {
										ui.Text(c, "Branches").TextColor(th.TextTertiary).FontSize(11).FontWeight(600).Padding(0, 4, 2, 4)
										if len(branches) == 0 {
											ui.Text(c, "No local branches found").TextColor(th.TextTertiary).FontSize(12).Padding(4, 4, 4, 4)
										}
										for bIdx, br := range branches {
											brName := br
											isCur := brName == curBr || (curBr == "" && st.GitBranch != nil && brName == *st.GitBranch)
											bBtn := ui.ButtonBase(c).
												Key(fmt.Sprintf("git-br-%d-%s", bIdx, brName)).
												FillWidth().
												Height(26).
												Padding(0, 8, 0, 8).
												Radius(6).
												AlignItems(ui.Center).
												Justify(ui.SpaceBetween).
												Cursor(ui.CursorPointer)
											if isCur || bBtn.Hovered() {
												bBtn.Background(th.Overlay)
											}
											bBtn.Children(func() {
												ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
													gui.IconView(c, gui.IconGitBranch, 12, th.TextSecondary)
													ui.Text(c, brName).TextColor(th.Text).FontSize(12).SingleLine()
												})
												if isCur {
													gui.IconView(c, gui.IconCheck, 12, th.Accent)
												}
											})
											if bBtn.Clicked() && !isCur {
												switchBranch = brName
												branchPopoverOpen = false
											}
										}
										ui.Box(c).FillWidth().Height(1).Background(th.Border).Margin(2, 0, 2, 0)
										chgBtn := ui.ButtonBase(c).
											FillWidth().
											Height(26).
											Padding(0, 8, 0, 8).
											Radius(6).
											AlignItems(ui.Center).
											Gap(6).
											Cursor(ui.CursorPointer)
										if chgBtn.Hovered() {
											chgBtn.Background(th.Overlay)
										}
										chgBtn.Children(func() {
											gui.IconView(c, gui.IconChanges, 12, th.Accent)
											ui.Text(c, "Open Changes Panel").TextColor(th.Text).FontSize(12).SingleLine()
										})
										if chgBtn.Clicked() {
											openChanges = true
											branchPopoverOpen = false
										}
									})
								})
							})
						}

						if len(st.ExtensionStatuses) > 0 {
							keys := make([]string, 0, len(st.ExtensionStatuses))
							for k := range st.ExtensionStatuses {
								keys = append(keys, k)
							}
							sort.Strings(keys)
							for idx, k := range keys {
								val := strings.TrimSpace(st.ExtensionStatuses[k])
								if val != "" {
									ui.Text(c, val).
										Key(fmt.Sprintf("ext-st-%d-%s", idx, k)).
										TextColor(th.TextTertiary).
										FontSize(11).
										SingleLine()
								}
							}
						}
					})

					// Right cost + circular Context Usage gauge opening Context Window popover
					ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(8).Children(func() {
						if st.TotalCost > 0 {
							ui.Text(c, FormatCost(st.TotalCost)).TextColor(th.TextTertiary).FontSize(11).SingleLine()
						}

						ctxAnchor := ui.ButtonBase(c).
							Key("footer-context-gauge").
							Height(22).
							Padding(0, 6, 0, 6).
							Radius(999).
							AlignItems(ui.Center).
							Gap(5).
							Cursor(ui.CursorPointer).
							Label("Context Window Usage")
						if ctxPopoverOpen || ctxAnchor.Hovered() {
							ctxAnchor.Background(th.Overlay)
						}
						ctxAnchor.Children(func() {
							gui.RenderContextGauge(c, st.ContextPercent, st.HasContextPercent, 14, 2.0, th)
							if st.HasContextPercent {
								ui.Textf(c, "%.0f%%", st.ContextPercent).TextColor(th.TextSecondary).FontSize(11).SingleLine()
							}
						})
						if ctxAnchor.Clicked() {
							ctxPopoverOpen = !ctxPopoverOpen
						}

						ui.PopoverBase(c, ctxAnchor, &ctxPopoverOpen, func(panel ui.Element) {
							panel.Width(320).
								Background(th.Raised).
								Border(1, th.BorderStrong).
								Radius(12).
								Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.36)).
								Padding(12, 14)
							panel.Children(func() {
								ui.Column(c).FillWidth().Gap(10).Children(func() {
									ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
										ui.Text(c, "Context Window").TextColor(th.Text).FontSize(13).FontWeight(600)
										if st.ContextWindow > 0 && st.HasContextTokens {
											ui.Textf(c, "%.1f%% of %s", st.ContextPercent, FormatTokens(st.ContextWindow)).
												TextColor(th.TextSecondary).FontSize(12)
										} else if st.ContextWindow > 0 {
											ui.Textf(c, "%s max", FormatTokens(st.ContextWindow)).
												TextColor(th.TextSecondary).FontSize(12)
										}
									})

									// Horizontal bar inside 292px track
									const trackW float32 = 290
									pct := st.ContextPercent
									if pct < 0 {
										pct = 0
									}
									if pct > 100 {
										pct = 100
									}
									fillW := trackW * float32(pct/100.0)
									barCol := th.Gauge
									if pct >= 90 {
										barCol = th.Error
									} else if pct >= 70 {
										barCol = th.Warning
									}
									ui.Row(c).Width(trackW).Height(6).Radius(999).Background(th.Inset).Children(func() {
										if fillW > 0 {
											ui.Box(c).Width(fillW).Height(6).Radius(999).Background(barCol)
										}
									})

									ui.Column(c).FillWidth().Gap(4).Children(func() {
										wakuPopoverStatRow(c, th, "Context tokens", wakuOptTokens(st.HasContextTokens, st.ContextTokens))
										wakuPopoverStatRow(c, th, "Session input", FormatTokens(st.InputTokens))
										wakuPopoverStatRow(c, th, "Session output", FormatTokens(st.OutputTokens))
										if st.CacheReadTokens > 0 || st.CacheWriteTokens > 0 {
											wakuPopoverStatRow(c, th, "Cache read / write",
												fmt.Sprintf("%s / %s", FormatTokens(st.CacheReadTokens), FormatTokens(st.CacheWriteTokens)))
										}
										wakuPopoverStatRow(c, th, "Session cost", FormatCost(st.TotalCost))
										acStatus := "off"
										if st.AutoCompactEnabled {
											acStatus = "auto"
										}
										wakuPopoverStatRow(c, th, "Auto-compact", acStatus)
									})

									if !isBusy && sess != nil {
										ui.Box(c).FillWidth().Height(1).Background(th.Border)
										cmpBtn := ui.ButtonBase(c).
											FillWidth().
											Height(28).
											Radius(7).
											Background(th.Overlay).
											Border(1, th.BorderStrong).
											AlignItems(ui.Center).
											Justify(ui.Center).
											Cursor(ui.CursorPointer).
											Label("Compact Now")
										if cmpBtn.Hovered() {
											cmpBtn.Background(th.OverlayStrong)
										}
										cmpBtn.Children(func() {
											ui.Text(c, "Compact Now").TextColor(th.Text).FontSize(12).FontWeight(500)
										})
										if cmpBtn.Clicked() {
											compactClicked = true
											ctxPopoverOpen = false
										}
									}
								})
							})
						})
					})
				})

			// Below-editor extension widgets
			if wakuHasWidgets(widgets, core.WidgetPlacementBelowEditor) {
				ui.Column(c).Key("waku-widgets-below").FillWidth().Gap(4).Margin(6, 0, 0, 0).Children(func() {
					gmRenderWidgets(c, th, widgets, core.WidgetPlacementBelowEditor)
				})
			}
		})
	})

	m.mu.Lock()
	m.contextPopoverOpen = ctxPopoverOpen
	m.branchPopoverOpen = branchPopoverOpen
	m.mu.Unlock()

	if clearQueueClicked {
		m.restoreQueuedMessages()
	}
	if openModelClicked {
		m.ShowModelSelector()
	}
	if openThinkClicked {
		m.ShowThinkingSelector()
	}
	if attachClicked {
		m.pasteImageFromClipboard()
	}
	if stopClicked {
		m.handleEscapeAbort()
	}
	if followUpClicked && m.editor != nil {
		m.handleEditorSubmit(gui.EditorSubmitEvent{
			Text:               m.editor.Text(),
			Attachments:        m.editor.Attachments(),
			BashMode:           m.editor.BashMode(),
			ExcludeFromContext: m.editor.BashExcludedFromContext(),
		}, true)
	}
	if sendClicked && m.editor != nil {
		m.handleEditorSubmit(gui.EditorSubmitEvent{
			Text:               m.editor.Text(),
			Attachments:        m.editor.Attachments(),
			BashMode:           m.editor.BashMode(),
			ExcludeFromContext: m.editor.BashExcludedFromContext(),
		}, false)
	}
	if compactClicked {
		go m.runCompactCommand(m.currentCtx(), "")
	}
	if openChanges {
		m.ensureWakuState()
		m.rightPanel.mu.Lock()
		m.rightPanel.open = true
		m.rightPanel.activeTab = "changes"
		m.rightPanel.mu.Unlock()
		m.saveLayoutPrefs()
		m.refreshRightPanelChanges()
	}
	if switchBranch != "" {
		m.checkoutGitBranch(switchBranch)
	}
}

func wakuHasWidgets(widgets []gmWidgetItem, placement core.WidgetPlacement) bool {
	for _, w := range widgets {
		if w.placement == placement {
			return true
		}
	}
	return false
}

func wakuOptTokens(has bool, n int) string {
	if !has {
		return "—"
	}
	return FormatTokens(n)
}

func wakuPopoverStatRow(c *ui.Context, th *gui.GuiTheme, label, value string) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
		ui.Text(c, label).TextColor(th.TextSecondary).FontSize(12)
		ui.Text(c, value).TextColor(th.Text).Font("monospace").FontSize(12)
	})
}
