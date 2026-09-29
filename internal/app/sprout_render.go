package app

import (
	"fmt"
	"image/color"
	"strings"
	"time"
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
	sideW := max(28, w/3)
	if sideW > w-12 {
		sideW = max(12, w/2)
	}
	mainW, bodyRows := max(1, w-sideW-1), max(1, h-3)
	left := m.sproutSidebar(sideW, bodyRows, selectedTabs)
	right := make([]string, bodyRows)
	composer := max(0, bodyRows-2)
	m.sproutMain(right, composer, mainW, pal, style)
	harness := m.Sprout.Config.Harness.Name
	if harness == "" {
		harness = "codex"
	}
	prompt := "Type a message..."
	if m.Sprout.Subnav == 1 {
		prompt = "New task title..."
	} else if m.Sprout.Subnav == 2 {
		prompt = "New goal title..."
	}
	if m.Sprout.Composer != "" {
		prompt = m.Sprout.Composer
	}
	if m.Sprout.Focus == "model" {
		right[composer] = "> model: " + m.Sprout.ModelDraft
	} else if m.Sprout.Focus == "composer" {
		prompt = "> " + prompt
		right[composer] = sproutPair(prompt, "➤", mainW)
	} else {
		right[composer] = sproutPair(prompt, "➤", mainW)
	}
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
	hints := "Esc leave  Tab focus  i compose"
	if m.Sprout.Subnav == 1 {
		hints = "Esc leave  f filter  n new task  h harness  m model"
	} else if m.Sprout.Subnav == 2 {
		hints = "Esc leave  f filter  r recent jobs  n new goal  h harness"
	}
	lines = append(lines, style(pal.FgMute, pal.Canvas).Render(overlay.Truncate(state+"  "+m.SessionName+"  "+hints, w)))
	return strings.Join(lines, "\n")
}

func (m *OS) sproutSidebar(width, rows int, selectedTabs []sessiontree.Node) []string {
	s := &m.Sprout
	left := []string{}
	switch s.Subnav {
	case 1:
		all, open, done := len(s.Tasks), 0, 0
		for _, task := range s.Tasks {
			if task.Status == "todo" || task.Status == "working" || task.Status == "review" || task.Status == "waiting" {
				open++
			}
			if task.Status == "done" || task.Status == "failed" || task.Status == "cancelled" {
				done++
			}
		}
		left = append(left, sproutPair("Files", "+", width), sproutFilters([]string{fmt.Sprintf("All %d", all), fmt.Sprintf("Open %d", open), fmt.Sprintf("Done %d", done)}, s.TaskFilter))
		for i, task := range s.visibleTasks() {
			prefix := "  "
			if i == s.TaskCursor {
				prefix = "> "
			}
			left = append(left, prefix+sproutTaskGlyph(task.Status)+" "+task.Title)
		}
		left = sproutPad(left, rows-1)
		return append(left, sproutCount(len(s.Tasks), "task"))
	case 2:
		all, active, closed := len(s.Goals), 0, 0
		for _, goal := range s.Goals {
			if goal.Status == "proposed" || goal.Status == "planning" || goal.Status == "active" || goal.Status == "review" || goal.Status == "reviewing" || goal.Status == "waiting" {
				active++
			}
			if goal.Status == "done" || goal.Status == "abandoned" {
				closed++
			}
		}
		left = append(left, sproutPair("Workflows", "+", width), sproutFilters([]string{fmt.Sprintf("All %d", all), fmt.Sprintf("Active %d", active), fmt.Sprintf("Closed %d", closed)}, s.GoalFilter))
		for i, goal := range s.visibleGoals() {
			prefix := "  "
			if i == s.GoalCursor {
				prefix = "> "
			}
			left = append(left, prefix+sproutGoalGlyph(goal.Status)+" "+goal.Title)
		}
		left = sproutPad(left, rows-1)
		return append(left, sproutCount(len(s.Goals), "goal"))
	default:
		agents := m.sidebarAgents(selectedTabs)
		needs, working := sproutAgentCounts(agents)
		filters := fmt.Sprintf("All %d  Needs you %d  Working %d  Unread %d", len(s.Sessions), needs, working, len(s.Unread))
		left = append(left, sproutPair("Conversations", "+", width), "Search conversations...", filters, "")
		for _, session := range s.visibleSessions() {
			name := sproutSessionName(session)
			if session.ID == s.Selected {
				name = "> " + name
			} else if session.Active {
				name = "* " + name
			}
			left = append(left, name)
		}
		if len(s.visibleSessions()) == 0 {
			left = append(left, "No conversations yet", "Start something with your agent.")
		}
		left = sproutPad(left, rows-1)
		return append(left, sproutPair(sproutCount(len(s.Sessions), "conversation"), "Show completed", width))
	}
}
func sproutPad(lines []string, n int) []string {
	if len(lines) > n {
		return lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines
}
func sproutFilters(labels []string, active int) string {
	for i := range labels {
		if i == active {
			labels[i] = "[" + labels[i] + "]"
		}
	}
	return strings.Join(labels, "  ")
}
func sproutCount(n int, noun string) string {
	if n != 1 {
		noun += "s"
	}
	return fmt.Sprintf("%d %s", n, noun)
}
func sproutTaskGlyph(status string) string {
	return map[string]string{"todo": "·", "working": "▸", "review": "?", "waiting": "…", "done": "✓", "failed": "✗", "cancelled": "−"}[status]
}
func sproutGoalGlyph(status string) string {
	if status == "review" || status == "reviewing" {
		return "?"
	}
	return map[string]string{"proposed": "·", "planning": "▸", "active": "►", "waiting": "…", "done": "✓", "abandoned": "−"}[status]
}
func (m *OS) sproutMain(right []string, composer, width int, pal overlay.Palette, style func(color.Color, color.Color) lipgloss.Style) {
	s := &m.Sprout
	if s.Focus == "hints" {
		for i, line := range []string{"Sprout keys", "Tab switch subnav", "Conversations: i compose  n new chat  f filter", "[ / ] project  p switcher  j/k scroll  G follow", "Files: f filter  j/k select  n new task", "Workflows: f filter  j/k select  r recent jobs  n new goal", "h harness  m model  ? close hints", "Esc leave"} {
			if i < composer {
				right[i] = line
			}
		}
		return
	}
	if s.Subnav == 1 {
		m.sproutTaskMain(right, composer, width)
	} else if s.Subnav == 2 {
		m.sproutGoalMain(right, composer, width)
	} else {
		header := m.sproutSelectedName()
		if header == "" {
			header = "New conversation"
		}
		right[0] = header
		transcriptRows := max(0, composer-1)
		turns := sproutTurnLines(s.Transcript, width, pal, style)
		if len(turns) == 0 {
			empty := 1 + max(0, (composer-1-2)/2)
			if empty < composer {
				right[empty] = "What are you working on?"
			}
			if empty+1 < composer {
				right[empty+1] = "Let's cross something off your list."
			}
		} else {
			start := s.Scroll
			if s.Follow {
				start, s.Scroll = max(0, len(turns)-transcriptRows), max(0, len(turns)-transcriptRows)
			}
			start = min(max(0, start), max(0, len(turns)-transcriptRows))
			for i, line := range turns[start:min(len(turns), start+transcriptRows)] {
				right[1+i] = line
			}
		}
	}
	if s.Focus == "picker" {
		lines := []string{"Harness", "  codex", "  claude-code", "  agent-zero (not implemented)"}
		lines[s.Picker+1] = "> " + strings.TrimPrefix(lines[s.Picker+1], "  ")
		start := max(0, composer-len(lines))
		for i, line := range lines {
			if start+i < composer {
				right[start+i] = line
			}
		}
	}
}
func (m *OS) sproutTaskMain(right []string, composer, width int) {
	tasks := m.Sprout.visibleTasks()
	if len(tasks) == 0 {
		right[0] = "No task selected"
		return
	}
	task := tasks[m.Sprout.TaskCursor%len(tasks)]
	right[0] = task.Title
	parts := []string{task.Status, fmt.Sprintf("attempt %d", task.Attempt)}
	if task.ReviewRequired {
		parts = append(parts, "review required")
	}
	if task.WaitingFor != "" {
		parts = append(parts, "waiting for "+task.WaitingFor)
	}
	if !task.UpdatedAt.IsZero() {
		parts = append(parts, "updated "+task.UpdatedAt.Local().Format("15:04"))
	}
	if composer > 1 {
		right[1] = strings.Join(parts, " · ")
	}
	body := task.Body
	if body == "" {
		body = "(no body)"
	}
	for i, line := range strings.Split(ansi.Wrap(sproutText(body), max(1, width), ""), "\n") {
		if i+2 < composer {
			right[i+2] = line
		}
	}
}
func (m *OS) sproutGoalMain(right []string, composer, width int) {
	goals := m.Sprout.visibleGoals()
	if len(goals) == 0 {
		right[0] = "No goal selected"
		return
	}
	goal := goals[m.Sprout.GoalCursor%len(goals)]
	right[0] = goal.Title
	parts := []string{goal.Status, fmt.Sprintf("round %d", goal.Round)}
	if goal.ReviewTrigger != "" {
		parts = append(parts, goal.ReviewTrigger)
	}
	if !goal.UpdatedAt.IsZero() {
		parts = append(parts, "updated "+goal.UpdatedAt.Local().Format("15:04"))
	}
	if composer > 1 {
		right[1] = strings.Join(parts, " · ")
	}
	row := 2
	if goal.SuccessCriteria != "" && row < composer {
		right[row], row = "criteria: "+goal.SuccessCriteria, row+1
	}
	body := goal.Body
	if body == "" {
		body = "(no body)"
	}
	for _, line := range strings.Split(ansi.Wrap(sproutText(body), max(1, width), ""), "\n") {
		if row >= composer {
			break
		}
		right[row], row = line, row+1
	}
	jobs := []string{"Jobs (live)"}
	if m.Sprout.RecentJobs {
		jobs[0] = "Jobs (recent 20)"
	}
	if len(m.Sprout.Jobs) == 0 {
		jobs = append(jobs, "no live jobs — r shows recent")
	} else {
		for i, job := range m.Sprout.Jobs {
			if i == 5 {
				break
			}
			jobs = append(jobs, sproutJobLine(job))
		}
	}
	start := max(row, composer-len(jobs))
	for i, line := range jobs {
		if start+i < composer {
			right[start+i] = line
		}
	}
}
func sproutJobLine(job pioctl.Job) string {
	line := fmt.Sprintf("#%d %s %s %s", job.Number, job.Kind, job.TaskID, job.State)
	if job.Error != "" {
		return line + " " + job.Error
	}
	end := job.EndedAt
	if end == nil {
		now := time.Now()
		end = &now
	}
	return line + " " + sproutElapsed(end.Sub(job.StartedAt))
}
func sproutElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Round(time.Minute).Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Round(time.Hour).Hours()))
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
