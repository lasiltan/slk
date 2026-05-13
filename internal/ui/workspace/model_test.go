package workspace

import (
	"strings"
	"testing"
)

func TestWorkspaceRailView(t *testing.T) {
	m := New([]WorkspaceItem{
		{ID: "T1", Name: "Acme Corp", Initials: "AC", HasUnread: false},
		{ID: "T2", Name: "Beta Inc", Initials: "BI", HasUnread: true},
	}, 0)

	view := m.View(20) // 20 rows height
	if !strings.Contains(view, "AC") {
		t.Error("expected 'AC' in view")
	}
	if !strings.Contains(view, "BI") {
		t.Error("expected 'BI' in view")
	}
}

func TestWorkspaceRailSelect(t *testing.T) {
	m := New([]WorkspaceItem{
		{ID: "T1", Name: "Acme", Initials: "AC"},
		{ID: "T2", Name: "Beta", Initials: "BE"},
	}, 0)

	if m.SelectedID() != "T1" {
		t.Error("expected T1 selected initially")
	}

	m.Select(1)
	if m.SelectedID() != "T2" {
		t.Error("expected T2 selected after Select(1)")
	}
}

// TestClickAt asserts ClickAt's mapping from rail-local y to workspace
// item using the rail's View() layout: row 0 is the top padding, row
// 1 is item 0, row 2 is the gap between items, row 3 is item 1, and
// so on (Padding(1,0) above and "\n\n"-joined item rows).
func TestClickAt(t *testing.T) {
	m := New([]WorkspaceItem{
		{ID: "T1", Name: "Acme", Initials: "AC"},
		{ID: "T2", Name: "Beta", Initials: "BE"},
		{ID: "T3", Name: "Gamma", Initials: "GA"},
	}, 0)

	cases := []struct {
		name   string
		y      int
		wantID string
		wantOK bool
	}{
		{"top padding", 0, "", false},
		{"first item", 1, "T1", true},
		{"gap between items 0 and 1", 2, "", false},
		{"second item", 3, "T2", true},
		{"gap between items 1 and 2", 4, "", false},
		{"third item", 5, "T3", true},
		{"below last item", 6, "", false},
		{"well below content", 99, "", false},
		{"negative y", -1, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := m.ClickAt(tc.y)
			if ok != tc.wantOK {
				t.Fatalf("ClickAt(%d) ok=%v want %v", tc.y, ok, tc.wantOK)
			}
			if got.ID != tc.wantID {
				t.Errorf("ClickAt(%d) ID=%q want %q", tc.y, got.ID, tc.wantID)
			}
		})
	}
}

func TestClickAt_EmptyRail(t *testing.T) {
	m := New(nil, 0)
	if _, ok := m.ClickAt(1); ok {
		t.Error("ClickAt on empty rail must return ok=false")
	}
}
