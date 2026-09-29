package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pioctl"
)

func TestSproutRendersSkeletonAtSmallAndWideSizes(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}} {
		m := &OS{Settings: config.Global, Width: size[0], Height: size[1], ShowSprout: true, SessionName: "main"}
		out := m.renderSprout()
		for _, want := range []string{"Projects ⌄", "Conversations", "Files", "Workflows", "Search conversations...", "What are you working on?", "Let's cross something off your list.", "Type a message...", "pio: unreachable"} {
			if !strings.Contains(out, want) {
				t.Errorf("%dx%d missing %q", size[0], size[1], want)
			}
		}
		for _, line := range strings.Split(out, "\n") {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("%dx%d overflow: %q", size[0], size[1], line)
			}
		}
	}
}
func TestSproutKeysCycleAndClose(t *testing.T) {
	if got := sproutTabIndex(0, 2, -1); got != 1 {
		t.Fatalf("previous tab = %d", got)
	}
	if got := sproutTabIndex(1, 2, 1); got != 0 {
		t.Fatalf("next tab = %d", got)
	}
	m := &OS{Settings: config.Global, ShowSprout: true}
	m.Sprout.Filter = 3
	m.SproutHandleKey("f")
	if m.Sprout.Filter != 0 {
		t.Fatalf("filter=%d", m.Sprout.Filter)
	}
	m.Sprout.Sessions = []pioctl.Session{{Name: "a"}, {Name: "b"}}
	m.Sprout.Cursor = 0
	m.SproutHandleKey("k")
	if m.Sprout.Cursor != 1 {
		t.Fatalf("cursor=%d", m.Sprout.Cursor)
	}
	m.SproutHandleKey("esc")
	if m.ShowSprout {
		t.Fatal("escape did not restore prior frame")
	}
}
func TestSproutCountsReuseSidebarGroups(t *testing.T) {
	needs, working := sproutAgentCounts([]sidebarAgentEntry{{State: "needs_input"}, {State: "working"}, {State: "done", DoneSeen: true}})
	if needs != 1 || working != 1 {
		t.Fatalf("counts = %d, %d", needs, working)
	}
}
func TestSproutPaletteReachable(t *testing.T) {
	if paletteItemNamed(GetCommandPaletteItems(&config.Global), "Open Sprout").Action == nil {
		t.Fatal("Open Sprout missing")
	}
}
