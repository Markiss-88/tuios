package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/pioctl"
)

// sproutClient is the control-socket surface Sprout uses: session discovery and
// selection, transcript loading, and streamed sends. It keeps UI tests local.
type sproutClient interface {
	Sessions(context.Context) ([]pioctl.Session, error)
	Create(context.Context, string) (pioctl.Session, error)
	Switch(context.Context, string) error
	GetConfig(context.Context) (pioctl.Config, error)
	Tasks(context.Context, string) ([]pioctl.Task, error)
	Goals(context.Context, string) ([]pioctl.Goal, error)
	Jobs(context.Context, bool) ([]pioctl.Job, error)
	CreateTask(context.Context, string) (pioctl.Task, error)
	CreateGoal(context.Context, string) (pioctl.Goal, error)
	SetHarness(context.Context, string, string) (pioctl.HarnessResult, error)
	History(context.Context, string, int) ([]pioctl.Entry, int, error)
	Send(context.Context, string, string, func(json.RawMessage)) (pioctl.ChatResult, error)
}

type SproutState struct {
	Tab, Subnav, Filter, Cursor int
	TaskFilter, GoalFilter      int
	TaskCursor, GoalCursor      int
	CursorID                    string
	TaskSelected, GoalSelected  string
	RecentJobs                  bool
	Picker                      int
	ModelDraft                  string
	Focus, Selected             string
	Sessions                    []pioctl.Session
	Tasks                       []pioctl.Task
	Goals                       []pioctl.Goal
	Jobs                        []pioctl.Job
	Config                      pioctl.Config
	Transcript                  []pioctl.Entry
	Composer, Status            string
	StatusAt                    time.Time
	Scroll                      int
	Follow                      bool
	Unread                      map[string]bool
	Connected, Sending          bool
	client                      sproutClient
	gen, stream                 uint64
	liveTurn, pendingUser       int
	pendingText                 string
}

type SproutRefreshMsg struct {
	Sessions []pioctl.Session
	Config   pioctl.Config
	Err      error
	Gen      uint64
}
type SproutHistoryMsg struct {
	Session string
	Entries []pioctl.Entry
	Skipped int
	Err     error
	Gen     uint64
}
type SproutCreateMsg struct {
	Session pioctl.Session
	Err     error
	Gen     uint64
}
type SproutBoardMsg struct {
	Tasks []pioctl.Task
	Goals []pioctl.Goal
	Jobs  []pioctl.Job
	Err   error
	Gen   uint64
}
type SproutCreatedMsg struct {
	Subnav int
	Task   pioctl.Task
	Goal   pioctl.Goal
	Err    error
	Gen    uint64
}
type SproutHarnessMsg struct {
	Result pioctl.HarnessResult
	Err    error
	Gen    uint64
}
type SproutStreamMsg struct {
	Session     string
	Type        string
	Text        string
	Result      pioctl.ChatResult
	Err         error
	Gen, Stream uint64
	ch          <-chan SproutStreamMsg
}
type SproutTickMsg struct {
	Gen uint64
	At  time.Time
}

const sproutNoticeDuration = 6 * time.Second

func sproutTabIndex(current, count, delta int) int {
	if count == 0 {
		return 0
	}
	return (current + delta%count + count) % count
}

func (s *SproutState) visibleSessions() []pioctl.Session {
	if s.Filter != 3 {
		return s.Sessions
	}
	var sessions []pioctl.Session
	for _, session := range s.Sessions {
		if s.Unread[session.ID] {
			sessions = append(sessions, session)
		}
	}
	return sessions
}
func (s *SproutState) visibleTasks() []pioctl.Task {
	var out []pioctl.Task
	for _, task := range s.Tasks {
		open := task.Status == "todo" || task.Status == "working" || task.Status == "review" || task.Status == "waiting"
		done := task.Status == "done" || task.Status == "failed" || task.Status == "cancelled"
		if s.TaskFilter == 0 || (s.TaskFilter == 1 && open) || (s.TaskFilter == 2 && done) {
			out = append(out, task)
		}
	}
	return out
}
func (s *SproutState) visibleGoals() []pioctl.Goal {
	var out []pioctl.Goal
	for _, goal := range s.Goals {
		active := goal.Status == "proposed" || goal.Status == "planning" || goal.Status == "active" || goal.Status == "review" || goal.Status == "reviewing" || goal.Status == "waiting"
		closed := goal.Status == "done" || goal.Status == "abandoned"
		if s.GoalFilter == 0 || (s.GoalFilter == 1 && active) || (s.GoalFilter == 2 && closed) {
			out = append(out, goal)
		}
	}
	return out
}

func (s *SproutState) syncSessionCursor() {
	sessions := s.visibleSessions()
	if len(sessions) == 0 {
		s.Cursor, s.CursorID = 0, ""
		return
	}
	for i, session := range sessions {
		if session.ID == s.CursorID {
			s.Cursor = i
			return
		}
	}
	s.Cursor = min(max(0, s.Cursor), len(sessions)-1)
	s.CursorID = sessions[s.Cursor].ID
}

func (s *SproutState) syncTaskCursor() {
	tasks := s.visibleTasks()
	if len(tasks) == 0 {
		s.TaskCursor, s.TaskSelected = 0, ""
		return
	}
	for i, task := range tasks {
		if task.ID == s.TaskSelected {
			s.TaskCursor = i
			return
		}
	}
	s.TaskCursor = min(max(0, s.TaskCursor), len(tasks)-1)
	s.TaskSelected = tasks[s.TaskCursor].ID
}

func (s *SproutState) syncGoalCursor() {
	goals := s.visibleGoals()
	if len(goals) == 0 {
		s.GoalCursor, s.GoalSelected = 0, ""
		return
	}
	for i, goal := range goals {
		if goal.ID == s.GoalSelected {
			s.GoalCursor = i
			return
		}
	}
	s.GoalCursor = min(max(0, s.GoalCursor), len(goals)-1)
	s.GoalSelected = goals[s.GoalCursor].ID
}

func (s *SproutState) setNotice(status string) {
	s.Status, s.StatusAt = status, time.Now()
}

func (s *SproutState) clearStatus() { s.Status, s.StatusAt = "", time.Time{} }

func sproutPrintable(key string) bool {
	r, size := utf8.DecodeRuneInString(key)
	return size == len(key) && unicode.IsPrint(r)
}

func (m *OS) OpenSprout() tea.Cmd {
	m.ShowSprout = true
	m.Sprout.gen++
	m.Sprout.Focus, m.Sprout.Follow = "sidebar", true
	m.Sprout.Selected, m.Sprout.Transcript = "", nil
	if m.Sprout.client == nil {
		m.Sprout.client = pioctl.New("")
	}
	return m.sproutRefreshCmd()
}
func (m *OS) CloseSprout() { m.ShowSprout = false; m.Sprout.gen++ }

func (m *OS) sproutRefreshCmd() tea.Cmd {
	gen, client := m.Sprout.gen, m.Sprout.client
	return func() tea.Msg {
		if client == nil {
			return SproutRefreshMsg{Err: pioctl.ErrUnreachable, Gen: gen}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sessions, err := client.Sessions(ctx)
		if err != nil {
			return SproutRefreshMsg{Err: err, Gen: gen}
		}
		cfg, _ := client.GetConfig(ctx)
		return SproutRefreshMsg{Sessions: sessions, Config: cfg, Gen: gen}
	}
}

func (m *OS) sproutSelectCmd(id string) tea.Cmd {
	gen, client := m.Sprout.gen, m.Sprout.client
	return func() tea.Msg {
		if client == nil {
			return SproutHistoryMsg{Session: id, Err: pioctl.ErrUnreachable, Gen: gen}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Switch(ctx, id); err != nil {
			return SproutHistoryMsg{Session: id, Err: err, Gen: gen}
		}
		entries, skipped, err := client.History(ctx, id, 200)
		return SproutHistoryMsg{Session: id, Entries: entries, Skipped: skipped, Err: err, Gen: gen}
	}
}

func (m *OS) sproutCreateCmd() tea.Cmd {
	gen, client := m.Sprout.gen, m.Sprout.client
	return func() tea.Msg {
		if client == nil {
			return SproutCreateMsg{Err: pioctl.ErrUnreachable, Gen: gen}
		}
		session, err := client.Create(context.Background(), "")
		return SproutCreateMsg{Session: session, Err: err, Gen: gen}
	}
}
func (m *OS) sproutBoardCmd() tea.Cmd {
	gen, client, recent := m.Sprout.gen, m.Sprout.client, m.Sprout.RecentJobs
	return func() tea.Msg {
		if client == nil {
			return SproutBoardMsg{Err: pioctl.ErrUnreachable, Gen: gen}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		tasks, err := client.Tasks(ctx, "")
		if err != nil {
			return SproutBoardMsg{Err: err, Gen: gen}
		}
		goals, err := client.Goals(ctx, "")
		if err != nil {
			return SproutBoardMsg{Err: err, Gen: gen}
		}
		jobs, err := client.Jobs(ctx, recent)
		return SproutBoardMsg{Tasks: tasks, Goals: goals, Jobs: jobs, Err: err, Gen: gen}
	}
}
func (m *OS) sproutCreatedCmd(subnav int, title string) tea.Cmd {
	gen, client := m.Sprout.gen, m.Sprout.client
	return func() tea.Msg {
		if client == nil {
			return SproutCreatedMsg{Subnav: subnav, Err: pioctl.ErrUnreachable, Gen: gen}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if subnav == 1 {
			task, err := client.CreateTask(ctx, title)
			return SproutCreatedMsg{Subnav: subnav, Task: task, Err: err, Gen: gen}
		}
		goal, err := client.CreateGoal(ctx, title)
		return SproutCreatedMsg{Subnav: subnav, Goal: goal, Err: err, Gen: gen}
	}
}
func (m *OS) sproutHarnessCmd(name, model string) tea.Cmd {
	gen, client := m.Sprout.gen, m.Sprout.client
	return func() tea.Msg {
		if client == nil {
			return SproutHarnessMsg{Err: pioctl.ErrUnreachable, Gen: gen}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		result, err := client.SetHarness(ctx, name, model)
		return SproutHarnessMsg{Result: result, Err: err, Gen: gen}
	}
}
func (m *OS) sproutTick() tea.Cmd {
	gen := m.Sprout.gen
	return tea.Tick(2*time.Second, func(at time.Time) tea.Msg { return SproutTickMsg{Gen: gen, At: at} })
}

func (m *OS) selectSproutSession(id string) tea.Cmd {
	s := &m.Sprout
	s.gen++
	s.Selected, s.Transcript, s.Scroll, s.Follow = id, nil, 0, true
	s.Focus = "main"
	s.clearStatus()
	s.Sending, s.liveTurn, s.pendingUser, s.pendingText = false, -1, -1, ""
	if s.Unread != nil {
		delete(s.Unread, id)
	}
	s.CursorID = id
	s.syncSessionCursor()
	return m.sproutSelectCmd(id)
}

func (m *OS) sproutStartSendCmd() tea.Cmd {
	s := &m.Sprout
	s.stream++
	stream, gen, session, text, client := s.stream, s.gen, s.Selected, s.pendingText, s.client
	ch := make(chan SproutStreamMsg)
	return func() tea.Msg {
		go func() {
			if client == nil {
				ch <- SproutStreamMsg{Session: session, Err: pioctl.ErrUnreachable, Gen: gen, Stream: stream, ch: ch}
				return
			}
			result, err := client.Send(context.Background(), session, text, func(raw json.RawMessage) {
				var event struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal(raw, &event) == nil && (event.Type == "delta" || event.Type == "status") {
					ch <- SproutStreamMsg{Session: session, Type: event.Type, Text: event.Text, Gen: gen, Stream: stream, ch: ch}
				}
			})
			ch <- SproutStreamMsg{Session: session, Result: result, Err: err, Gen: gen, Stream: stream, ch: ch}
		}()
		return <-ch
	}
}
func sproutNextStreamCmd(ch <-chan SproutStreamMsg) tea.Cmd { return func() tea.Msg { return <-ch } }

func (m *OS) handleSproutMsg(msg tea.Msg) tea.Cmd {
	s := &m.Sprout
	switch x := msg.(type) {
	case SproutRefreshMsg:
		if x.Gen != s.gen || !m.ShowSprout {
			return nil
		}
		m.renderSkipped, s.Connected = false, x.Err == nil
		if x.Err != nil {
			return m.sproutTick()
		}
		s.Sessions, s.Config = x.Sessions, x.Config
		s.syncSessionCursor()
		if s.Status == "pio: unreachable" {
			s.clearStatus()
		}
		if s.Selected == "" {
			for _, session := range s.Sessions {
				if session.Active {
					return tea.Batch(m.selectSproutSession(session.ID), m.sproutTick())
				}
			}
		}
		return m.sproutTick()
	case SproutHistoryMsg:
		if x.Gen != s.gen || !m.ShowSprout || x.Session != s.Selected {
			return nil
		}
		m.renderSkipped = false
		if x.Err != nil {
			s.setNotice("pio: " + x.Err.Error())
			return nil
		}
		s.Transcript, s.Scroll, s.Follow = x.Entries, 0, true
		s.clearStatus()
	case SproutCreateMsg:
		if x.Gen != s.gen || !m.ShowSprout {
			return nil
		}
		if x.Err != nil {
			s.setNotice("pio: " + x.Err.Error())
			return nil
		}
		return tea.Batch(m.selectSproutSession(x.Session.ID), m.sproutTick())
	case SproutBoardMsg:
		if x.Gen != s.gen || !m.ShowSprout {
			return nil
		}
		if x.Err != nil {
			s.setNotice(sproutError(x.Err))
			return nil
		}
		s.Tasks, s.Goals, s.Jobs, s.Connected = x.Tasks, x.Goals, x.Jobs, true
		s.syncTaskCursor()
		s.syncGoalCursor()
	case SproutCreatedMsg:
		if x.Gen != s.gen || !m.ShowSprout {
			return nil
		}
		if x.Err != nil {
			s.setNotice(sproutError(x.Err))
			return nil
		}
		if x.Subnav == 1 {
			s.Tasks = append(s.Tasks, x.Task)
			s.TaskFilter, s.TaskSelected = 0, x.Task.ID
			s.syncTaskCursor()
			s.setNotice("task created: " + x.Task.ID)
		} else {
			s.Goals = append(s.Goals, x.Goal)
			s.GoalFilter, s.GoalSelected = 0, x.Goal.ID
			s.syncGoalCursor()
			s.setNotice("goal created: " + x.Goal.ID)
		}
		s.Composer = ""
	case SproutHarnessMsg:
		if x.Gen != s.gen || !m.ShowSprout {
			return nil
		}
		if x.Err != nil {
			s.setNotice(sproutError(x.Err))
			s.Focus = "sidebar"
			return nil
		}
		s.Config.Harness.Name, s.Config.Harness.Model = x.Result.Name, x.Result.Model
		s.setNotice("harness: " + x.Result.Name + " " + x.Result.Model)
		s.Focus = "sidebar"
	case SproutStreamMsg:
		terminal := x.Type == ""
		// WHY: a command may outlive its pane; generation keeps its stream off a new chat.
		stale := x.Gen != s.gen || !m.ShowSprout || x.Stream != s.stream
		if stale || x.Session != s.Selected {
			if terminal && x.Err == nil && x.Session != s.Selected {
				if s.Unread == nil {
					s.Unread = make(map[string]bool)
				}
				s.Unread[x.Session] = true
			}
			if !terminal {
				return sproutNextStreamCmd(x.ch)
			}
			return nil
		}
		m.renderSkipped = false
		switch x.Type {
		case "delta":
			if s.liveTurn < 0 {
				s.Transcript = append(s.Transcript, pioctl.Entry{Role: "assistant"})
				s.liveTurn = len(s.Transcript) - 1
			}
			s.Transcript[s.liveTurn].Text += x.Text
		case "status":
			s.Status, s.StatusAt = x.Text, time.Time{}
		default:
			liveTurn := s.liveTurn
			s.Sending, s.liveTurn = false, -1
			if x.Err != nil {
				if s.pendingUser >= 0 && s.pendingUser < len(s.Transcript) {
					s.Transcript = append(s.Transcript[:s.pendingUser], s.Transcript[s.pendingUser+1:]...)
				}
				s.Composer = s.pendingText
				if errors.Is(x.Err, pioctl.ErrUnreachable) {
					s.setNotice("pio: unreachable")
				} else {
					s.setNotice("pio: " + x.Err.Error())
				}
				s.pendingUser, s.pendingText = -1, ""
				return nil
			}
			if !x.Result.OK {
				s.Transcript = append(s.Transcript, pioctl.Entry{Role: "assistant", Error: x.Result.Error})
			} else if liveTurn < 0 {
				s.Transcript = append(s.Transcript, pioctl.Entry{Role: "assistant", Text: x.Result.Text})
			}
			s.clearStatus()
			s.pendingUser, s.pendingText = -1, ""
		}
		if !terminal {
			return sproutNextStreamCmd(x.ch)
		}
	case SproutTickMsg:
		if x.Gen == s.gen && m.ShowSprout {
			m.renderSkipped = false
			at := x.At
			if at.IsZero() {
				at = time.Now()
			}
			if !s.StatusAt.IsZero() && !at.Before(s.StatusAt.Add(sproutNoticeDuration)) {
				s.clearStatus()
			}
			if s.Subnav != 0 {
				return tea.Batch(m.sproutRefreshCmd(), m.sproutBoardCmd())
			}
			return m.sproutRefreshCmd()
		}
	}
	return nil
}

func sproutError(err error) string {
	if errors.Is(err, pioctl.ErrUnreachable) {
		return "pio: unreachable"
	}
	var requestErr *pioctl.RequestError
	if errors.As(err, &requestErr) {
		return requestErr.Message
	}
	return "pio: " + err.Error()
}

func (m *OS) SproutConnected() bool { return m.Sprout.Connected }
func (m *OS) SproutHandleKey(key string) tea.Cmd {
	s := &m.Sprout
	if s.Focus == "" {
		s.Focus = "sidebar"
	}
	if key == "space" && (s.Focus == "composer" || s.Focus == "model") {
		key = " "
	}
	if s.Focus == "composer" {
		switch key {
		case "esc":
			// WHY: one Esc from a text box must not discard the text by leaving Sprout.
			s.Focus = "sidebar"
		case "backspace":
			if len(s.Composer) > 0 {
				_, size := utf8.DecodeLastRuneInString(s.Composer)
				s.Composer = s.Composer[:len(s.Composer)-size]
			}
		case "enter":
			if s.Composer == "" {
				return nil
			}
			if s.Subnav == 1 {
				return m.sproutCreatedCmd(1, s.Composer)
			}
			if s.Subnav == 2 {
				return m.sproutCreatedCmd(2, s.Composer)
			}
			if !s.Sending {
				s.Transcript = append(s.Transcript, pioctl.Entry{Role: "user", Text: s.Composer})
				s.Status, s.StatusAt, s.Sending, s.liveTurn, s.Follow = "thinking…", time.Time{}, true, -1, true
				s.pendingUser, s.pendingText = len(s.Transcript)-1, s.Composer
				s.Composer = ""
				return m.sproutStartSendCmd()
			}
		default:
			if sproutPrintable(key) {
				s.Composer += key
			}
		}
		return nil
	}
	if s.Focus == "model" {
		switch key {
		case "esc":
			s.ModelDraft, s.Focus = "", "sidebar"
		case "backspace":
			if len(s.ModelDraft) > 0 {
				_, size := utf8.DecodeLastRuneInString(s.ModelDraft)
				s.ModelDraft = s.ModelDraft[:len(s.ModelDraft)-size]
			}
		case "enter":
			if s.ModelDraft == "" {
				s.setNotice("model cannot be empty")
				return nil
			}
			return m.sproutHarnessCmd(s.Config.Harness.Name, s.ModelDraft)
		default:
			if sproutPrintable(key) {
				s.ModelDraft += key
			}
		}
		return nil
	}
	if s.Focus == "picker" {
		const harnesses = 3
		switch key {
		case "esc":
			s.Focus = "sidebar"
		case "j", "down":
			s.Picker = sproutTabIndex(s.Picker, harnesses, 1)
		case "k", "up":
			s.Picker = sproutTabIndex(s.Picker, harnesses, -1)
		case "enter":
			// WHY: pio owns accepted harness names and their implementation status.
			return m.sproutHarnessCmd([]string{"codex", "claude-code", "agent-zero"}[s.Picker], "")
		}
		return nil
	}
	if s.Focus == "hints" {
		if key == "?" || key == "esc" {
			s.Focus = "sidebar"
		}
		return nil
	}
	switch key {
	case "esc":
		m.NoteAction("sprout_close")
		m.CloseSprout()
	case "]", "[":
		tabs := m.sessionSwitcherItems()
		if len(tabs) > 0 {
			delta := 1
			if key == "[" {
				delta = -1
			}
			s.Tab = sproutTabIndex(s.Tab, len(tabs), delta)
			m.OpenSessionNode(tabs[s.Tab])
		}
	case "tab":
		s.Subnav = (s.Subnav + 1) % 3
		if s.Subnav != 0 {
			return m.sproutBoardCmd()
		}
	case "i":
		s.Focus = "composer"
	case "f":
		switch s.Subnav {
		case 0:
			s.Filter = (s.Filter + 1) % 4
			s.syncSessionCursor()
		case 1:
			s.TaskFilter = (s.TaskFilter + 1) % 3
			s.syncTaskCursor()
		case 2:
			s.GoalFilter = (s.GoalFilter + 1) % 3
			s.syncGoalCursor()
		}
	case "j", "down":
		if s.Subnav == 1 {
			if tasks := s.visibleTasks(); len(tasks) > 0 {
				s.TaskCursor = (s.TaskCursor + 1) % len(tasks)
				s.TaskSelected = tasks[s.TaskCursor].ID
			}
		} else if s.Subnav == 2 {
			if goals := s.visibleGoals(); len(goals) > 0 {
				s.GoalCursor = (s.GoalCursor + 1) % len(goals)
				s.GoalSelected = goals[s.GoalCursor].ID
			}
		} else if s.Focus == "main" {
			s.Scroll++
			s.Follow = false
		} else if sessions := s.visibleSessions(); len(sessions) > 0 {
			s.Cursor = (s.Cursor + 1) % len(sessions)
			s.CursorID = sessions[s.Cursor].ID
		}
	case "k", "up":
		if s.Subnav == 1 {
			if tasks := s.visibleTasks(); len(tasks) > 0 {
				s.TaskCursor = (s.TaskCursor + len(tasks) - 1) % len(tasks)
				s.TaskSelected = tasks[s.TaskCursor].ID
			}
		} else if s.Subnav == 2 {
			if goals := s.visibleGoals(); len(goals) > 0 {
				s.GoalCursor = (s.GoalCursor + len(goals) - 1) % len(goals)
				s.GoalSelected = goals[s.GoalCursor].ID
			}
		} else if s.Focus == "main" {
			if s.Scroll > 0 {
				s.Scroll--
			}
			s.Follow = false
		} else if sessions := s.visibleSessions(); len(sessions) > 0 {
			s.Cursor = (s.Cursor + len(sessions) - 1) % len(sessions)
			s.CursorID = sessions[s.Cursor].ID
		}
	case "pgup":
		if s.Focus == "main" {
			s.Scroll -= 8
			if s.Scroll < 0 {
				s.Scroll = 0
			}
			s.Follow = false
		}
	case "pgdown":
		if s.Focus == "main" {
			s.Scroll += 8
			s.Follow = false
		}
	case "G":
		if s.Focus == "main" {
			s.Follow = true
		}
	case "enter":
		if sessions := s.visibleSessions(); s.Focus == "sidebar" && s.Cursor < len(sessions) {
			return tea.Batch(m.selectSproutSession(sessions[s.Cursor].ID), m.sproutTick())
		}
	case "n":
		if s.Subnav == 0 {
			return m.sproutCreateCmd()
		}
		s.Focus = "composer"
	case "r":
		if s.Subnav == 2 {
			s.RecentJobs = !s.RecentJobs
			return m.sproutBoardCmd()
		}
	case "h":
		s.Focus, s.Picker = "picker", 0
		for i, name := range []string{"codex", "claude-code", "agent-zero"} {
			if s.Config.Harness.Name == name {
				s.Picker = i
			}
		}
	case "m":
		s.Focus, s.ModelDraft = "model", s.Config.Harness.Model
	case "p":
		m.OpenSessionSwitcher()
	case "?":
		s.Focus = "hints"
	}
	return nil
}

// SproutPaste appends one-line paste text to the focused Sprout editor.
func (m *OS) SproutPaste(text string) {
	s := &m.Sprout
	var b strings.Builder
	newline := false
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r':
			if !newline {
				b.WriteByte(' ')
			}
			newline = true
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
			newline = false
		}
	}
	if s.Focus == "composer" {
		s.Composer += b.String()
	} else if s.Focus == "model" {
		s.ModelDraft += b.String()
	}
}
