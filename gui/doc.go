// Package gui provides a Layer-L0 native GPU graphical user interface toolkit
// built on github.com/egoist/mygo (mygo, ui, ui/markdown, ui/editor). It serves
// as the desktop GUI counterpart to package tui while remaining completely
// decoupled from pi's agent, model, and session layers.
//
// Threading and lifecycle rules:
//   - Immediate-mode ui.Element values expire after each build pass and must
//     never be retained in application state or globals. Persist ui.Handle,
//     ui.ListState, ui.ScrollState, ui.TextBuffer, editor.State, editor.DiffState,
//     and gui component structs instead.
//   - Cross-goroutine UI updates MUST go through GUI.Post or GUI.RequestRender,
//     which hop onto the main UI thread via *mygo.Window.Update / Invalidate when
//     attached to a window, or execute safely in headless ui.NewTester sessions
//     when no *mygo.Window is present.
//   - Never hold locks across callbacks or Window.Update calls.
//   - Pure Go only (CGO_ENABLED=0); never import "C" or call reflect Method
//     lookup helpers.
package gui
