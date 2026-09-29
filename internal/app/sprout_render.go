package app

import (
	"fmt"
	"image/color"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/pioctl"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
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
	unread := len(m.Sprout.Unread)
	filters := fmt.Sprintf("All %d  Needs you %d  Working %d  Unread %d", len(m.Sprout.Sessions), needs, working, unread)
	sideW := max(28, w/3)
	if sideW > w-12 {
		sideW = max(12, w/2)
	}
	mainW, bodyRows := max(1, w-sideW-1), max(1, h-3)
	left := []string{sproutPair("Conversations", "+", sideW), "Search conversations...", filters, ""}
	sessions := m.Sprout.visibleSessions()
	if len(sessions) == 0 {
		left = append(left, "No conversations yet", "Start something with your agent.")
	} else {
		for _, session := range sessions {
			name := sproutSessionName(session)
			if session.ID == m.Sprout.Selected {
				name = "> " + name
			} else if session.Active {
				name = "* " + name
			}
			left = append(left, name)
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
	header := m.sproutSelectedName()
	if header == "" {
		header = "New conversation"
	}
	right := make([]string, bodyRows)
	right[0] = header
	composer := max(0, bodyRows-2)
	transcriptRows := max(0, composer-1)
	turns := sproutTurnLines(m.Sprout.Transcript, mainW, pal, style)
	if len(turns) == 0 {
		empty := 1 + max(0, (composer-1-2)/2)
		if empty < composer {
			right[empty] = "What are you working on?"
		}
		if empty+1 < composer {
			right[empty+1] = "Let's cross something off your list."
		}
	} else {
		start := m.Sprout.Scroll
		if m.Sprout.Follow {
			start = max(0, len(turns)-transcriptRows)
			m.Sprout.Scroll = start
		}
		start = min(max(0, start), max(0, len(turns)-transcriptRows))
		for i, line := range turns[start:min(len(turns), start+transcriptRows)] {
			right[1+i] = line
		}
	}
	harness := m.Sprout.Config.Harness.Name
	if harness == "" {
		harness = "codex"
	}
	prompt := "Type a message..."
	if m.Sprout.Composer != "" {
		prompt = m.Sprout.Composer
	}
	if m.Sprout.Focus == "composer" {
		prompt = "> " + prompt
	}
	right[composer] = sproutPair(prompt, "➤", mainW)
	if composer+1 < len(right) {
		status := m.Sprout.Status
		if status == "" {
			status = harness + "  " + m.Sprout.Config.Harness.Model
		}
		right[composer+1] = status
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
	if m.Sprout.Status != "" {
		state = m.Sprout.Status
	}
	lines = append(lines, style(pal.FgMute, pal.Canvas).Render(overlay.Truncate(state+"  "+m.SessionName+"  Esc leave  Tab focus  i compose", w)))
	return strings.Join(lines, "\n")
}

func sproutTurnLines(entries []pioctl.Entry, width int, pal overlay.Palette, style func(color.Color, color.Color) lipgloss.Style) []string {
	var lines []string
	for _, entry := range entries {
		prefix, fg, text := "assistant › ", pal.Fg, entry.Text
		if entry.Role == "user" {
			prefix, fg = "you › ", pal.Accent
		}
		if entry.Error != "" {
			prefix, fg, text = "error › ", pal.Warn, entry.Error
		}
		wrapped := strings.Split(ansi.Wrap(sproutText(text), max(1, width-lipgloss.Width(prefix)), ""), "\n")
		for i, line := range wrapped {
			lead := ""
			if i == 0 {
				lead = prefix
			}
			lines = append(lines, style(fg, pal.Surface).Render(lead+line))
		}
	}
	return lines
}

func sproutText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
}
func sproutSessionName(session pioctl.Session) string {
	if session.Name != "" {
		return session.Name
	}
	if session.Title != "" {
		return session.Title
	}
	return session.ID
}
func (m *OS) sproutSelectedName() string {
	for _, session := range m.Sprout.Sessions {
		if session.ID == m.Sprout.Selected {
			return sproutSessionName(session)
		}
	}
	return m.Sprout.Selected
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
