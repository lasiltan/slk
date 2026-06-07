package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/gammons/slk/internal/ui/messages"
)

func TestR_OpensReactionsViewFromMessage(t *testing.T) {
	app := NewApp()
	app.activeChannelID = "C123"
	app.focusedPanel = PanelMessages
	app.SetUserNames(map[string]string{"U1": "alice", "U2": "bob"})
	app.messagepane.SetMessages([]messages.MessageItem{
		{TS: "1700000001.000200", UserName: "alice", Text: "hello",
			Reactions: []messages.ReactionItem{
				{Emoji: "thumbsup", Count: 2, UserIDs: []string{"U1", "U2"}},
				{Emoji: "tada", Count: 1, UserIDs: []string{"U2"}},
			},
		},
	})

	cmd := app.handleNormalMode(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if cmd != nil {
		t.Fatalf("R expected no follow-up cmd, got %T", cmd())
	}
	if app.mode != ModeReactionsView {
		t.Fatalf("mode after R = %v, want ModeReactionsView", app.mode)
	}
	if !app.reactionsView.IsVisible() {
		t.Fatal("reactionsView should be visible after R")
	}
	tabs := app.reactionsView.Tabs()
	if len(tabs) != 2 {
		t.Fatalf("tabs = %d, want 2", len(tabs))
	}
	if tabs[0].Emoji != "thumbsup" || tabs[1].Emoji != "tada" {
		t.Fatalf("tab order = [%q, %q], want [thumbsup, tada]", tabs[0].Emoji, tabs[1].Emoji)
	}
	if got := tabs[0].Users; len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Fatalf("tab[0].Users = %v, want [alice, bob]", got)
	}
}

func TestR_OnMessageWithNoReactionsIsNoop(t *testing.T) {
	app := NewApp()
	app.activeChannelID = "C123"
	app.focusedPanel = PanelMessages
	app.messagepane.SetMessages([]messages.MessageItem{
		{TS: "1700000001.000200", UserName: "alice", Text: "hello"},
	})

	app.handleNormalMode(tea.KeyPressMsg{Code: 'R', Text: "R"})

	if app.mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal", app.mode)
	}
	if app.reactionsView.IsVisible() {
		t.Fatal("reactionsView should not be visible when message has no reactions")
	}
}

func TestR_UnknownUserIDFallsBackToRawID(t *testing.T) {
	app := NewApp()
	app.activeChannelID = "C123"
	app.focusedPanel = PanelMessages
	// userNames empty — every UserID resolves to itself.
	app.messagepane.SetMessages([]messages.MessageItem{
		{TS: "1700000001.000200", UserName: "alice", Text: "hi",
			Reactions: []messages.ReactionItem{
				{Emoji: "wave", Count: 1, UserIDs: []string{"U_UNKNOWN"}},
			},
		},
	})

	app.handleNormalMode(tea.KeyPressMsg{Code: 'R', Text: "R"})

	if !app.reactionsView.IsVisible() {
		t.Fatal("reactionsView should be visible")
	}
	tabs := app.reactionsView.Tabs()
	if len(tabs) != 1 || len(tabs[0].Users) != 1 || tabs[0].Users[0] != "U_UNKNOWN" {
		t.Fatalf("tabs[0].Users = %v, want [U_UNKNOWN]", tabs[0].Users)
	}
}

func TestR_EscClosesReactionsView(t *testing.T) {
	app := NewApp()
	app.activeChannelID = "C123"
	app.focusedPanel = PanelMessages
	app.messagepane.SetMessages([]messages.MessageItem{
		{TS: "1700000001.000200", UserName: "alice", Text: "hi",
			Reactions: []messages.ReactionItem{
				{Emoji: "wave", Count: 1, UserIDs: []string{"U1"}},
			},
		},
	})
	app.handleNormalMode(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if !app.reactionsView.IsVisible() {
		t.Fatal("setup: should be visible")
	}

	// Esc routes through dispatchModeKey because we're now in ModeReactionsView.
	dispatchModeKey(app, tea.KeyPressMsg{Code: tea.KeyEscape})

	if app.reactionsView.IsVisible() {
		t.Fatal("Esc should close the overlay")
	}
	if app.mode != ModeNormal {
		t.Fatalf("mode after Esc = %v, want ModeNormal", app.mode)
	}
}
