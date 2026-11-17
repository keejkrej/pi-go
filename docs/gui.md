# Desktop GUI Architecture (`--gui`)

`pi-go` provides a native GPU-accelerated desktop interface launched via `pi --gui` or `pi --mode gui` (or programmatically via `codingagent.MainOptions{DefaultGuiMode: true}`), built in pure Go (`CGO_ENABLED=0`) on top of `github.com/egoist/mygo`.

## 1. Two-Layer Design

The GUI is strictly split into an agent-agnostic Layer-0 toolkit and a coding-agent application mode:

1. **Layer-0 Toolkit ([`gui/`](../gui/))**:
   - Completely decoupled from `agent`, `ai`, and `codingagent` packages (enforced in [`gui/doc.go`](../gui/doc.go)).
   - Core controller [`gui.GUI`](../gui/gui.go) wraps `mygo.Application` / `mygo.Window`, an overlay stack (`OverlayOptions`, `OverlayHandle`), [`ProgramStatus`](../gui/gui.go) title/taskbar progress badges, and [`DesktopHooks`](../gui/gui.go) for clipboard, file dialogs, message dialogs, and OS notifications.
   - Reusable UI primitives:
     - [`Editor`](../gui/components_editor.go) — multi-line editor with undo stack, kill ring, attachments, and submit events
     - [`CombinedAutocompleteProvider`](../gui/autocomplete.go) & `AutocompletePopup` — `@file` and `/command` completion
     - [`MarkdownView`](../gui/components_markdown.go), [`DiffViewer`](../gui/components_diff.go), [`CodeViewer`](../gui/components_diff.go), [`ScrollView`](../gui/components_scroll_view.go)
     - [`SelectList`](../gui/components_lists.go), [`SettingsList`](../gui/components_lists.go), [`SearchBar`](../gui/components_lists.go), [`Loader`](../gui/components_lists.go), [`CancellableLoader`](../gui/components_lists.go), [`CollapsibleCard`](../gui/components_lists.go)
     - [`GuiTheme`](../gui/theme.go), color math ([`colors.go`](../gui/colors.go)), vector icons ([`icons.go`](../gui/icons.go)), and [`KeybindingsManager`](../gui/keybindings.go)

2. **Coding Agent Mode ([`codingagent/modes/gui/`](../codingagent/modes/gui/))**:
   - [`GuiMode`](../codingagent/modes/gui/gui_mode.go) (`NewGuiMode`, `RunGuiMode`) wires an `*core.AgentSessionRuntime` into a three-pane desktop workspace:
     - **Left sidebar** ([`sidebar.go`](../codingagent/modes/gui/sidebar.go)): session list with filtering, date grouping, sorting, pinning, and persisted layout preferences.
     - **Center workspace** ([`messages.go`](../codingagent/modes/gui/messages.go), [`transcript_groups.go`](../codingagent/modes/gui/transcript_groups.go), [`tool_views.go`](../codingagent/modes/gui/tool_views.go), [`composer.go`](../codingagent/modes/gui/composer.go), [`footer.go`](../codingagent/modes/gui/footer.go)): scrollable transcript with turn/activity grouping, tool execution cards, chromeless composer, interactive model/reasoning/footer pickers, and modal selectors ([`selectors.go`](../codingagent/modes/gui/selectors.go)).
     - **Right panel & settings** ([`right_panel.go`](../codingagent/modes/gui/right_panel.go), [`settings_page.go`](../codingagent/modes/gui/settings_page.go), [`slash_commands.go`](../codingagent/modes/gui/slash_commands.go), [`theme_adapter.go`](../codingagent/modes/gui/theme_adapter.go), [`window_chrome.go`](../codingagent/modes/gui/window_chrome.go)): workspace git/diff context panel, settings view, custom window chrome, and Pi theme-to-`GuiTheme` mapping.

## 2. Threading & Immediate-Mode Rules

Because `github.com/egoist/mygo/ui` is immediate-mode:

- **Never retain `ui.Element`**: elements created during a build pass expire after that frame. Persist `ui.Handle`, `ui.ListState`, `ui.ScrollState`, `ui.TextBuffer`, `editor.State`, `editor.DiffState`, and `gui` component structs across frames.
- **Marshal background updates**: all cross-goroutine state changes that schedule a UI update must go through `GUI.Post` or `GUI.RequestRender` (which dispatch onto the `mygo.Window` main loop or run synchronously in headless `ui.NewTester` sessions).
- **No locks across UI callbacks**: never hold a mutex while calling `Window.Update` or invoking user/session callbacks.

## 3. Headless Testing

Both layers run without a live window server in tests:

- `gui` tests use `github.com/egoist/mygo/ui.NewTester` and `DesktopHooks` stubs.
- `codingagent/modes/gui` tests construct `NewGuiMode(runtime, &GuiModeOptions{SkipWindow: true, ...})` with faux providers.
- Optional parity helpers in [`waku_composer_spec_test.go`](../codingagent/modes/gui/waku_composer_spec_test.go) and [`waku_parity_test.go`](../codingagent/modes/gui/waku_parity_test.go) stay inactive unless `PI_WAKU_SNAPSHOT_DIR` or `WAKU_REPO_DIR` is explicitly provided.
