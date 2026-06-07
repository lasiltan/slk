package styles

import (
	"fmt"
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestUserColorDeterministic(t *testing.T) {
	got1 := UserColor("U123ABC")
	got2 := UserColor("U123ABC")
	if got1 != got2 {
		t.Fatalf("UserColor not deterministic: %v vs %v", got1, got2)
	}
}

func TestUserColorEmptyFallsBackToPrimary(t *testing.T) {
	if UserColor("") != Primary {
		t.Fatalf("empty userID should return Primary")
	}
}

func TestUserColorAdaptsToThemeBackground(t *testing.T) {
	orig := Background
	t.Cleanup(func() {
		Background = orig
		resetUserColorCache()
	})

	Background = lipgloss.Color("#101020")
	resetUserColorCache()
	dark := luminanceOf(UserColor("Usame"))

	Background = lipgloss.Color("#F0F0F0")
	resetUserColorCache()
	light := luminanceOf(UserColor("Usame"))

	if !(dark > light) {
		t.Fatalf("expected lighter foreground on dark bg (%.3f) vs light bg (%.3f)", dark, light)
	}
}

func luminanceOf(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
}

func TestUserColorDistribution(t *testing.T) {
	const n = 5000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		c := UserColor(fmt.Sprintf("U%07d", i))
		r, g, b, _ := c.RGBA()
		key := fmt.Sprintf("%04x%04x%04x", r, g, b)
		seen[key] = struct{}{}
	}
	if len(seen) < 1000 {
		t.Fatalf("expected ≥1000 distinct colors across %d IDs, got %d", n, len(seen))
	}
}
