package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) renderSprout() string {
	pal := theme.UI()
	w, h := max(m.Width, 1), max(m.Height, 1)
	style := func(fg, bg color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(bg) }
	tabs := m.sessionSwitcherItems()
	var names []string
	for i, t := range tabs {
		label := t.Title
		if label == "" {
			label = t.ID
		}
		badge := 0
		for _, a := range m.sidebarAgents([]sessiontree.Node{t}) {
			if sidebarAgentGroup(a.State, a.DoneSeen) == sidebarGroupNeedsYou {
				badge++
			}
		}
		if badge > 0 {
			label += fmt.Sprintf(" (%d)", badge)
		}
		if i == m.Sprout.Tab {
			label = "[" + label + "]"
		}
		names = append(names, label)
	}
	top := overlay.Truncate(strings.Join(names, "  ")+"  Projects ⌄", w)
	sub := "[Conversations]  Files  Workflows"
	if m.Sprout.Subnav == 1 {
		sub = "Conversations  [Files]  Workflows"
	}
	if m.Sprout.Subnav == 2 {
		sub = "Conversations  Files  [Workflows]"
	}
	selectedTabs := tabs
	if len(tabs) > 0 {
		m.Sprout.Tab %= len(tabs)
		selectedTabs = tabs[m.Sprout.Tab : m.Sprout.Tab+1]
	}
	agents := m.sidebarAgents(selectedTabs)
	needs, working := sproutAgentCounts(agents)
	unread := 0
	for _, it := range m.Inbox.Items {
		if inboxNeedsYou(it) {
			unread++
		}
	}
	filters := fmt.Sprintf("All %d  Needs you %d  Working %d  Unread %d", len(m.Sprout.Sessions), needs, working, unread)
	sideW := max(28, w/3)
	if sideW > w-12 {
		sideW = max(12, w/2)
	}
	mainW := max(1, w-sideW-1)
	bodyRows := max(1, h-3)
	left := []string{sproutPair("Conversations", "+", sideW), "Search conversations...", filters, ""}
	if len(m.Sprout.Sessions) == 0 {
		left = append(left, "No conversations yet", "Start something with your agent.")
	} else {
		for i, s := range m.Sprout.Sessions {
			n := s.Name
			if n == "" {
				n = s.Title
			}
			if i == m.Sprout.Cursor {
				n = "> " + n
			}
			left = append(left, n)
		}
	}
	if len(left) > bodyRows-1 {
		left = left[:bodyRows-1]
	}
	for len(left) < bodyRows-1 {
		left = append(left, "")
	}
	count := fmt.Sprintf("%d conversations", len(m.Sprout.Sessions))
	if lipgloss.Width(count)+lipgloss.Width("Show completed")+1 > sideW {
		count = fmt.Sprintf("%d chats", len(m.Sprout.Sessions))
	}
	left = append(left, sproutPair(count, "Show completed", sideW))
	header := m.Sprout.Selected
	if header == "" {
		header = "New conversation"
	}
	right := make([]string, bodyRows)
	right[0] = header
	composer := max(0, bodyRows-2)
	empty := 1 + max(0, (composer-1-2)/2)
	if empty < composer {
		right[empty] = "What are you working on?"
	}
	if empty+1 < composer {
		right[empty+1] = "Let's cross something off your list."
	}
	harness := m.Sprout.Config.Harness.Name
	if harness == "" {
		harness = "codex"
	}
	right[composer] = sproutPair("Type a message...", "➤", mainW)
	if composer+1 < len(right) {
		right[composer+1] = harness + "  " + m.Sprout.Config.Harness.Model
	}
	lines := []string{style(pal.Fg, pal.Canvas).Render(top), style(pal.FgDim, pal.Panel).Render(sub)}
	for i := 0; i < max(len(left), len(right)); i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines = append(lines, style(pal.FgDim, pal.Panel).Width(sideW).Render(overlay.Truncate(l, sideW))+" "+style(pal.Fg, pal.Surface).Width(mainW).Render(overlay.Truncate(r, mainW)))
	}
	state := "pio: unreachable"
	if m.Sprout.Connected {
		state = "pio: connected"
	}
	lines = append(lines, style(pal.FgMute, pal.Canvas).Render(overlay.Truncate(state+"  "+m.SessionName+"  Esc leave  ]/[ tabs  Tab nav  f filter  ? hints", w)))
	return strings.Join(lines, "\n")
}

func sproutPair(left, right string, width int) string {
	space := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	return overlay.Truncate(left+strings.Repeat(" ", space)+right, width)
}

func sproutAgentCounts(agents []sidebarAgentEntry) (needs, working int) {
	for _, a := range agents {
		switch sidebarAgentGroup(a.State, a.DoneSeen) {
		case sidebarGroupNeedsYou:
			needs++
		case sidebarGroupWorking:
			working++
		}
	}
	return
}
