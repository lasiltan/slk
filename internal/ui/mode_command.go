// internal/ui/mode_command.go
//
// Command-mode key handler (Phase 5b).
//
// Command mode is a stub state today: Esc returns to Normal, every
// other key is a no-op. Reserved for a future "vi-style :command"
// prompt; the handler exists so the prompt's entry/exit path is
// already wired through the mode FSM.
package ui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func handleCommandMode(a *App, msg tea.KeyMsg) tea.Cmd {
	if key.Matches(msg, a.keys.Escape) {
		a.SetMode(ModeNormal)
	}
	return nil
}
