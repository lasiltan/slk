// internal/ui/mode_reactions_view.go
//
// Reactions-view mode key handler.
//
// Forwards normalised keys to the read-only reactions list overlay.
// Closing the overlay (esc / q) returns to ModeNormal. No async work,
// no optimistic updates: the overlay is a snapshot taken at open time.
package ui

import (
	tea "charm.land/bubbletea/v2"
)

func handleReactionsViewMode(a *App, msg tea.KeyMsg) tea.Cmd {
	keyStr := msg.String()

	switch msg.Key().Code {
	case tea.KeyEscape:
		keyStr = "esc"
	case tea.KeyTab:
		keyStr = "tab"
	case tea.KeyUp:
		keyStr = "up"
	case tea.KeyDown:
		keyStr = "down"
	case tea.KeyLeft:
		keyStr = "left"
	case tea.KeyRight:
		keyStr = "right"
	}

	closed := a.reactionsView.HandleKey(keyStr)
	if closed {
		a.SetMode(ModeNormal)
	}
	return nil
}
