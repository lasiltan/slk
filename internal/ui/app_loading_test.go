// internal/ui/app_loading_test.go
//
// Phase 0 characterization tests for the multi-workspace bootstrap
// overlay: SetLoadingWorkspaces, MarkWorkspaceReady, MarkWorkspaceFailed,
// checkLoadingDone, and the connecting/ready/failed state transitions.
// Pins behavior for the future workspaceBootstrap extraction.
package ui

import (
	"strings"
	"testing"
)

func TestSetLoadingWorkspacesSeedsConnectingEntries(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme", "globex", "initech"})

	if !a.bootstrap.IsLoading() {
		t.Fatal("expected loading=true after SetLoadingWorkspaces")
	}
	if len(a.bootstrap.states) != 3 {
		t.Fatalf("len(loadingStates): want 3, got %d", len(a.bootstrap.states))
	}
	wantNames := []string{"acme", "globex", "initech"}
	for i, want := range wantNames {
		if a.bootstrap.states[i].TeamName != want {
			t.Errorf("loadingStates[%d].TeamName: want %q, got %q",
				i, want, a.bootstrap.states[i].TeamName)
		}
		if a.bootstrap.states[i].Status != "connecting" {
			t.Errorf("loadingStates[%d].Status: want %q, got %q",
				i, "connecting", a.bootstrap.states[i].Status)
		}
	}
}

func TestSetLoadingWorkspacesReplacesPriorState(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme"})
	a.SetLoadingWorkspaces([]string{"globex", "initech"})

	if len(a.bootstrap.states) != 2 {
		t.Errorf("want 2 entries after replace, got %d", len(a.bootstrap.states))
	}
	if a.bootstrap.states[0].TeamName != "globex" {
		t.Errorf("first entry: want globex, got %q", a.bootstrap.states[0].TeamName)
	}
}

func TestMarkWorkspaceReadyFlipsEntryStatus(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme", "globex"})

	a.bootstrap.MarkReady("globex")

	if a.bootstrap.states[1].Status != "ready" {
		t.Errorf("globex status: want %q, got %q",
			"ready", a.bootstrap.states[1].Status)
	}
	// acme remains connecting.
	if a.bootstrap.states[0].Status != "connecting" {
		t.Errorf("acme should stay connecting; got %q", a.bootstrap.states[0].Status)
	}
}

func TestMarkWorkspaceFailedFlipsEntryStatus(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme", "globex"})

	a.bootstrap.MarkFailed("acme")

	if a.bootstrap.states[0].Status != "failed" {
		t.Errorf("acme status: want %q, got %q",
			"failed", a.bootstrap.states[0].Status)
	}
}

func TestMarkWorkspaceReadyForUnknownNameIsNoop(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme"})

	// Unknown name should not panic, should not change any entries,
	// and (because there's no ready entry and acme is still connecting)
	// must leave loading=true.
	a.bootstrap.MarkReady("ghost")

	if a.bootstrap.states[0].Status != "connecting" {
		t.Errorf("acme should still be connecting; got %q", a.bootstrap.states[0].Status)
	}
	if !a.bootstrap.IsLoading() {
		t.Error("loading should remain true; no workspace is actually ready")
	}
}

func TestCheckLoadingDoneDismissesOnFirstReady(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme", "globex", "initech"})

	// One ready, two still connecting → overlay dismisses immediately
	// (other workspaces continue connecting in the background).
	a.bootstrap.MarkReady("globex")

	if a.bootstrap.IsLoading() {
		t.Error("loading should flip to false as soon as one workspace is ready")
	}
}

func TestCheckLoadingDoneStaysWhileAllConnecting(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme", "globex"})

	// No marks yet — still loading.
	a.bootstrap.checkDone()
	if !a.bootstrap.IsLoading() {
		t.Error("loading should remain true while all are still connecting")
	}
}

func TestCheckLoadingDoneDismissesWhenAllFailed(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme", "globex"})

	a.bootstrap.MarkFailed("acme")
	if !a.bootstrap.IsLoading() {
		t.Error("loading should remain true while globex is still connecting")
	}

	a.bootstrap.MarkFailed("globex")
	if a.bootstrap.IsLoading() {
		t.Error("loading should be false once all workspaces failed")
	}
}

func TestCheckLoadingDoneStaysWhileSomeFailedSomeConnecting(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme", "globex", "initech"})

	a.bootstrap.MarkFailed("acme")
	a.bootstrap.MarkFailed("globex")
	// initech is still connecting → not done yet.

	if !a.bootstrap.IsLoading() {
		t.Error("loading should remain true while at least one is still connecting")
	}
}

// renderLoadingOverlay is a render path. We don't pin exact lipgloss
// output (it's terminal-mode-sensitive), but we DO pin that it mentions
// every workspace by name and reflects their status transitions.
func TestRenderLoadingOverlayMentionsAllWorkspaces(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme", "globex"})

	out := a.bootstrap.Render(80, 24, "·")
	for _, name := range []string{"acme", "globex"} {
		if !strings.Contains(out, name) {
			t.Errorf("overlay missing workspace name %q\noutput:\n%s", name, out)
		}
	}
	// In the all-connecting state, neither "failed" marker nor a tick
	// glyph is in the output (the latter is hard to assert exactly,
	// but "(failed)" is clearly absent).
	if strings.Contains(out, "(failed)") {
		t.Error("overlay should not show (failed) when no workspace has failed")
	}
}

func TestRenderLoadingOverlayShowsFailedMarker(t *testing.T) {
	a := NewApp()
	a.SetLoadingWorkspaces([]string{"acme"})
	a.bootstrap.MarkFailed("acme")

	out := a.bootstrap.Render(80, 24, "·")
	if !strings.Contains(out, "acme") {
		t.Errorf("overlay missing workspace name; got:\n%s", out)
	}
	if !strings.Contains(out, "(failed)") {
		t.Errorf("overlay should label failed workspace; got:\n%s", out)
	}
}
