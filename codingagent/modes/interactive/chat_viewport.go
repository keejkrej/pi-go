// Ported from packages/coding-agent/src/modes/interactive/chat-viewport.ts (pi v1.5.2).

package interactive

import "github.com/keejkrej/pi-go/tui"

// ChatViewportOptions configures CreateChatViewport.
type ChatViewportOptions struct {
	Document            tui.Component
	PendingMessages     tui.Component
	Status              tui.Component
	Editor              tui.Component
	Footer              tui.Component
	WidgetsAbove        tui.Component
	WidgetsBelow        tui.Component
	Scrollbar           *tui.ScrollViewScrollbar
	ScrollbarTrackStyle func(text string) string
	ScrollbarThumbStyle func(text string) string
}

// ChatViewport holds the fullscreen root VStack and primary transcript ScrollView.
type ChatViewport struct {
	Root       tui.Component
	Transcript *tui.ScrollView
}

func cvIntPtr(v int) *int {
	return &v
}

// CreateChatViewport builds the shared fullscreen transcript and fixed input-dock layout.
func CreateChatViewport(options ChatViewportOptions) *ChatViewport {
	follow := tui.ScrollViewFollowEnd
	primary := true
	overscroll := tui.ScrollLayoutOverscrollChain
	scrollbar := tui.ScrollViewScrollbarAuto
	if options.Scrollbar != nil {
		scrollbar = *options.Scrollbar
	}
	transcript, _ := tui.NewScrollView(options.Document, &tui.ScrollViewOptions{
		Follow:              &follow,
		Primary:             &primary,
		Overscroll:          &overscroll,
		Scrollbar:           &scrollbar,
		ScrollbarTrackStyle: options.ScrollbarTrackStyle,
		ScrollbarThumbStyle: options.ScrollbarThumbStyle,
	})

	dockChildren := []tui.StackChild{
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component: options.PendingMessages,
			StackEntryOptions: tui.StackEntryOptions{
				Shrink:  cvIntPtr(1),
				MinSize: cvIntPtr(0),
			},
		}},
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component: options.Status,
			StackEntryOptions: tui.StackEntryOptions{
				Shrink:  cvIntPtr(1),
				MinSize: cvIntPtr(0),
			},
		}},
	}
	if options.WidgetsAbove != nil {
		dockChildren = append(dockChildren, &tui.StackChildEntry{Entry: tui.StackEntry{
			Component: options.WidgetsAbove,
			StackEntryOptions: tui.StackEntryOptions{
				Shrink:  cvIntPtr(1),
				MinSize: cvIntPtr(0),
			},
		}})
	}
	dockChildren = append(dockChildren, &tui.StackChildEntry{Entry: tui.StackEntry{
		Component: options.Editor,
		StackEntryOptions: tui.StackEntryOptions{
			Shrink:  cvIntPtr(1),
			MinSize: cvIntPtr(3),
		},
	}})
	if options.WidgetsBelow != nil {
		dockChildren = append(dockChildren, &tui.StackChildEntry{Entry: tui.StackEntry{
			Component: options.WidgetsBelow,
			StackEntryOptions: tui.StackEntryOptions{
				Shrink:  cvIntPtr(1),
				MinSize: cvIntPtr(0),
			},
		}})
	}
	dockChildren = append(dockChildren, &tui.StackChildEntry{Entry: tui.StackEntry{
		Component: options.Footer,
		StackEntryOptions: tui.StackEntryOptions{
			Shrink:  cvIntPtr(1),
			MinSize: cvIntPtr(0),
		},
	}})

	dock := tui.NewVStack(dockChildren, nil)
	root := tui.NewVStack([]tui.StackChild{
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component: transcript,
			StackEntryOptions: tui.StackEntryOptions{
				Basis:   &tui.StackLayoutBasisCells{Value: 0},
				Grow:    cvIntPtr(1),
				Shrink:  cvIntPtr(1),
				MinSize: cvIntPtr(1),
			},
		}},
		&tui.StackChildEntry{Entry: tui.StackEntry{
			Component: dock,
			StackEntryOptions: tui.StackEntryOptions{
				Basis:   &tui.StackLayoutBasisAuto{},
				Grow:    cvIntPtr(0),
				Shrink:  cvIntPtr(1),
				MinSize: cvIntPtr(1),
			},
		}},
	}, nil)

	return &ChatViewport{
		Root:       root,
		Transcript: transcript,
	}
}
