package app

import (
	"context"
	"encoding/json"
	"image/color"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pioctl"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

type fakeSproutClient struct {
	sessions                                                      []pioctl.Session
	sessionsByCall                                                [][]pioctl.Session
	history                                                       map[string][]pioctl.Entry
	switched                                                      []string
	created                                                       pioctl.Session
	send                                                          pioctl.ChatResult
	sendErr                                                       error
	events                                                        []json.RawMessage
	tasks                                                         []pioctl.Task
	tasksByCall                                                   [][]pioctl.Task
	goals                                                         []pioctl.Goal
	jobs                                                          []pioctl.Job
	config                                                        pioctl.Config
	auto                                                          pioctl.AutoState
	task                                                          pioctl.Task
	goal                                                          pioctl.Goal
	harness                                                       pioctl.HarnessResult
	tasksErr, goalsErr, jobsErr, taskErr, goalErr, harnessErr     error
	autoErr, setAutoErr                                           error
	setHarness                                                    [][2]string
	setAuto                                                       []bool
	sends                                                         [][2]string
	taskTitles, goalTitles                                        []string
	sessionsCalls, configCalls, tasksCalls, goalsCalls, jobsCalls int
}

func (f *fakeSproutClient) Sessions(context.Context) ([]pioctl.Session, error) {
	f.sessionsCalls++
	if i := f.sessionsCalls - 1; i < len(f.sessionsByCall) {
		return f.sessionsByCall[i], nil
	}
	return f.sessions, nil
}
func (f *fakeSproutClient) Create(context.Context, string) (pioctl.Session, error) {
	return f.created, nil
}
func (f *fakeSproutClient) Switch(_ context.Context, id string) error {
	f.switched = append(f.switched, id)
	return nil
}
func (f *fakeSproutClient) GetConfig(context.Context) (pioctl.Config, error) {
	f.configCalls++
	return f.config, nil
}
func (f *fakeSproutClient) Auto(context.Context) (pioctl.AutoState, error) { return f.auto, f.autoErr }
func (f *fakeSproutClient) SetAuto(_ context.Context, enabled bool) (pioctl.AutoState, error) {
	f.setAuto = append(f.setAuto, enabled)
	return f.auto, f.setAutoErr
}
func (f *fakeSproutClient) Tasks(context.Context, string) ([]pioctl.Task, error) {
	f.tasksCalls++
	if i := f.tasksCalls - 1; i < len(f.tasksByCall) {
		return f.tasksByCall[i], f.tasksErr
	}
	return f.tasks, f.tasksErr
}
func (f *fakeSproutClient) Goals(context.Context, string) ([]pioctl.Goal, error) {
	f.goalsCalls++
	return f.goals, f.goalsErr
}
func (f *fakeSproutClient) Jobs(context.Context, bool) ([]pioctl.Job, error) {
	f.jobsCalls++
	return f.jobs, f.jobsErr
}
func (f *fakeSproutClient) CreateTask(_ context.Context, title string) (pioctl.Task, error) {
	f.taskTitles = append(f.taskTitles, title)
	return f.task, f.taskErr
}
func (f *fakeSproutClient) CreateGoal(_ context.Context, title string) (pioctl.Goal, error) {
	f.goalTitles = append(f.goalTitles, title)
	return f.goal, f.goalErr
}
func (f *fakeSproutClient) SetHarness(_ context.Context, name, model string) (pioctl.HarnessResult, error) {
	f.setHarness = append(f.setHarness, [2]string{name, model})
	return f.harness, f.harnessErr
}
func (f *fakeSproutClient) History(_ context.Context, id string, _ int) ([]pioctl.Entry, int, error) {
	return f.history[id], 0, nil
}
func (f *fakeSproutClient) Send(_ context.Context, session, text string, event func(json.RawMessage)) (pioctl.ChatResult, error) {
	f.sends = append(f.sends, [2]string{session, text})
	for _, e := range f.events {
		event(e)
	}
	return f.send, f.sendErr
}

func sproutOS(client *fakeSproutClient) *OS {
	return &OS{Settings: config.Global, Width: 120, Height: 40, ShowSprout: true, Sprout: SproutState{client: client, Sessions: client.sessions, Tasks: client.tasks, Goals: client.goals, Jobs: client.jobs, Config: client.config, Focus: "sidebar", Follow: true, liveTurn: -1, pendingUser: -1}}
}
func driveSprout(t *testing.T, m *OS, cmd tea.Cmd) {
	t.Helper()
	var drive func(tea.Cmd)
	drive = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, child := range msg {
				drive(child)
			}
		case SproutTickMsg:
			// A tick starts the next periodic cycle; callers drive it explicitly.
		default:
			drive(m.handleSproutMsg(msg))
		}
	}
	drive(cmd)
}

func sproutBatch(t *testing.T, cmd tea.Cmd) tea.BatchMsg {
	t.Helper()
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("refresh result = %T, want tea.BatchMsg", msg)
	}
	return batch
}

func requireSproutTick(t *testing.T, m *OS, cmd tea.Cmd) {
	t.Helper()
	msg := cmd()
	tick, ok := msg.(SproutTickMsg)
	if !ok || tick.Gen != m.Sprout.gen {
		t.Fatalf("tick = %#v, want SproutTickMsg{Gen: %d}", msg, m.Sprout.gen)
	}
}

func TestSproutFirstRefreshSelectsAndRearmsTick(t *testing.T) {
	fake := &fakeSproutClient{sessions: []pioctl.Session{{ID: "active", Active: true}}, history: map[string][]pioctl.Entry{"active": nil}}
	m := sproutOS(fake)
	m.Sprout.gen = 7
	batch := sproutBatch(t, m.handleSproutMsg(SproutRefreshMsg{Sessions: fake.sessions, Gen: 7}))
	if got := m.Sprout.Selected; got != "active" {
		t.Fatalf("selected = %q", got)
	}
	ticks := 0
	for _, cmd := range batch {
		switch msg := cmd().(type) {
		case SproutHistoryMsg:
			if follow := m.handleSproutMsg(msg); follow != nil {
				t.Fatalf("history follow-up = %T", follow())
			}
		case SproutTickMsg:
			if msg.Gen != m.Sprout.gen {
				t.Fatalf("tick generation = %d, want %d", msg.Gen, m.Sprout.gen)
			}
			ticks++
		default:
			t.Fatalf("batch message = %T", msg)
		}
	}
	if ticks != 1 {
		t.Fatalf("ticks = %d, want 1", ticks)
	}
}

func TestSproutRefreshChainFollowsSessionsWithoutInput(t *testing.T) {
	fake := &fakeSproutClient{
		sessionsByCall: [][]pioctl.Session{
			{{ID: "default", Active: true}, {ID: "first-turn-probe"}},
			{{ID: "default"}, {ID: "first-turn-probe"}, {ID: "new-chat", Active: true}},
		},
		history: map[string][]pioctl.Entry{"default": nil},
	}
	m := sproutOS(fake)
	batch := sproutBatch(t, m.handleSproutMsg(m.OpenSprout()()))
	var tick SproutTickMsg
	for _, cmd := range batch {
		switch msg := cmd().(type) {
		case SproutHistoryMsg:
			if follow := m.handleSproutMsg(msg); follow != nil {
				t.Fatalf("history follow-up = %T", follow())
			}
		case SproutTickMsg:
			tick = msg
		default:
			t.Fatalf("batch message = %T", msg)
		}
	}
	if tick.Gen != m.Sprout.gen {
		t.Fatalf("tick generation = %d, want %d", tick.Gen, m.Sprout.gen)
	}
	refresh := m.handleSproutMsg(tick)
	rearm := m.handleSproutMsg(refresh())
	if got := len(m.Sprout.Sessions); got != 3 {
		t.Fatalf("sessions = %d, want 3", got)
	}
	if !m.Sprout.Sessions[2].Active {
		t.Fatalf("active sessions = %+v", m.Sprout.Sessions)
	}
	requireSproutTick(t, m, rearm)
}

func TestSproutBoardRefreshChainUpdatesTaskWithoutInput(t *testing.T) {
	fake := &fakeSproutClient{tasksByCall: [][]pioctl.Task{{{ID: "task", Status: "todo"}}, {{ID: "task", Status: "done"}}}}
	m := sproutOS(fake)
	m.Sprout.gen = 1
	driveSprout(t, m, m.SproutHandleKey("tab"))
	if got := m.Sprout.Tasks[0].Status; got != "todo" {
		t.Fatalf("initial status = %q", got)
	}
	batch := sproutBatch(t, m.handleSproutMsg(SproutTickMsg{Gen: 1}))
	var rearm tea.Cmd
	for _, cmd := range batch {
		switch msg := cmd().(type) {
		case SproutRefreshMsg:
			rearm = m.handleSproutMsg(msg)
		case SproutBoardMsg:
			if follow := m.handleSproutMsg(msg); follow != nil {
				t.Fatalf("board follow-up = %T", follow())
			}
		default:
			t.Fatalf("batch message = %T", msg)
		}
	}
	if got := m.Sprout.Tasks[0].Status; got != "done" {
		t.Fatalf("refreshed status = %q", got)
	}
	requireSproutTick(t, m, rearm)
}

func TestSproutReopenIgnoresOldTick(t *testing.T) {
	fake := &fakeSproutClient{sessions: []pioctl.Session{{ID: "active", Active: true}}, history: map[string][]pioctl.Entry{"active": nil}}
	m := sproutOS(fake)
	m.Sprout.gen = 5
	m.CloseSprout()
	open := m.OpenSprout()
	if got := m.handleSproutMsg(SproutTickMsg{Gen: 5}); got != nil {
		t.Fatalf("old tick follow-up = %T", got())
	}
	batch := sproutBatch(t, m.handleSproutMsg(open()))
	ticks := 0
	for _, cmd := range batch {
		switch msg := cmd().(type) {
		case SproutHistoryMsg:
			m.handleSproutMsg(msg)
		case SproutTickMsg:
			if msg.Gen != m.Sprout.gen {
				t.Fatalf("tick generation = %d, want %d", msg.Gen, m.Sprout.gen)
			}
			ticks++
		default:
			t.Fatalf("batch message = %T", msg)
		}
	}
	if ticks != 1 {
		t.Fatalf("ticks = %d, want 1", ticks)
	}
}

func sproutType(m *OS, text string) {
	for _, r := range text {
		msg := tea.KeyPressMsg{Code: r, Text: string(r)}
		if r == ' ' {
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		}
		m.SproutHandleKey(msg.String())
	}
}

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

func TestSproutSelectLoadsHistoryAndDistinctRoles(t *testing.T) {
	fake := &fakeSproutClient{sessions: []pioctl.Session{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}}, history: map[string][]pioctl.Entry{"b": {{Role: "user", Text: "question"}, {Role: "assistant", Text: "answer"}}}}
	m := sproutOS(fake)
	m.Sprout.Cursor = 1
	driveSprout(t, m, m.SproutHandleKey("enter"))
	if m.Sprout.Selected != "b" || len(fake.switched) != 1 || fake.switched[0] != "b" {
		t.Fatalf("selected=%q switched=%v", m.Sprout.Selected, fake.switched)
	}
	if got := m.renderSprout(); !strings.Contains(got, "you › question") || !strings.Contains(got, "assistant › answer") || !strings.Contains(got, "Beta") {
		t.Fatalf("history not rendered:\n%s", got)
	}
}

func TestSproutOpenAndNewLoadTheirSessionHistory(t *testing.T) {
	fake := &fakeSproutClient{sessions: []pioctl.Session{{ID: "active", Name: "Active", Active: true}}, created: pioctl.Session{ID: "new", Name: "New"}, history: map[string][]pioctl.Entry{"active": {{Role: "assistant", Text: "loaded"}}, "new": {{Role: "assistant", Text: "fresh"}}}}
	m := sproutOS(fake)
	driveSprout(t, m, m.OpenSprout())
	if m.Sprout.Selected != "active" || m.Sprout.Transcript[0].Text != "loaded" {
		t.Fatalf("open selected=%q transcript=%+v", m.Sprout.Selected, m.Sprout.Transcript)
	}
	driveSprout(t, m, m.SproutHandleKey("n"))
	if m.Sprout.Selected != "new" || m.Sprout.Transcript[0].Text != "fresh" {
		t.Fatalf("new selected=%q transcript=%+v", m.Sprout.Selected, m.Sprout.Transcript)
	}
}

func TestSproutComposerSwallowsNavigationAndEscapesInStages(t *testing.T) {
	m := sproutOS(&fakeSproutClient{})
	m.Sprout.Focus, m.Sprout.Filter, m.Sprout.Tab = "composer", 2, 1
	for _, key := range []string{"]", "f", "n", "p", "é"} {
		m.SproutHandleKey(key)
	}
	if m.Sprout.Composer != "]fnpé" || m.Sprout.Filter != 2 || m.Sprout.Tab != 1 {
		t.Fatalf("composer=%q filter=%d tab=%d", m.Sprout.Composer, m.Sprout.Filter, m.Sprout.Tab)
	}
	m.SproutHandleKey("esc")
	if !m.ShowSprout || m.Sprout.Focus != "sidebar" || m.Sprout.Composer != "]fnpé" {
		t.Fatalf("first escape show=%t focus=%q composer=%q", m.ShowSprout, m.Sprout.Focus, m.Sprout.Composer)
	}
	m.SproutHandleKey("esc")
	if m.ShowSprout {
		t.Fatal("sidebar escape did not leave Sprout")
	}
}

func TestSproutSendStreamsOneAssistantAndErrorTurn(t *testing.T) {
	fake := &fakeSproutClient{send: pioctl.ChatResult{OK: true, Text: "abc"}, events: []json.RawMessage{json.RawMessage(`{"type":"delta","text":"a"}`), json.RawMessage(`{"type":"delta","text":"b"}`), json.RawMessage(`{"type":"delta","text":"c"}`)}}
	m := sproutOS(fake)
	m.Sprout.Selected, m.Sprout.Focus, m.Sprout.Composer = "a", "composer", "hello"
	driveSprout(t, m, m.SproutHandleKey("enter"))
	if m.Sprout.Composer != "" || len(m.Sprout.Transcript) != 2 || m.Sprout.Transcript[0].Text != "hello" || m.Sprout.Transcript[1].Text != "abc" {
		t.Fatalf("transcript=%+v composer=%q", m.Sprout.Transcript, m.Sprout.Composer)
	}
	fake.events, fake.send = nil, pioctl.ChatResult{OK: false, Error: "model failed"}
	m.Sprout.Composer = "again"
	driveSprout(t, m, m.SproutHandleKey("enter"))
	if got := m.Sprout.Transcript[len(m.Sprout.Transcript)-1]; got.Error != "model failed" {
		t.Fatalf("error turn=%+v", got)
	}
}

func TestSproutTextEntryUsesRealKeyMessages(t *testing.T) {
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	if got := space.String(); got != "space" {
		t.Fatalf("space key string = %q", got)
	}

	t.Run("composer", func(t *testing.T) {
		fake := &fakeSproutClient{send: pioctl.ChatResult{OK: true}}
		m := sproutOS(fake)
		m.Sprout.Selected, m.Sprout.Focus = "session-7", "composer"
		sproutType(m, "What is 7 times 6?")
		driveSprout(t, m, m.SproutHandleKey((tea.KeyPressMsg{Code: tea.KeyEnter}).String()))
		if got, want := fake.sends, [][2]string{{"session-7", "What is 7 times 6?"}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("Send calls = %#v, want %#v", got, want)
		}
	})

	t.Run("task and goal", func(t *testing.T) {
		fake := &fakeSproutClient{}
		m := sproutOS(fake)
		m.Sprout.Subnav, m.Sprout.Focus = 1, "composer"
		sproutType(m, "Tidy the README")
		driveSprout(t, m, m.SproutHandleKey((tea.KeyPressMsg{Code: tea.KeyEnter}).String()))
		if got, want := fake.taskTitles, []string{"Tidy the README"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("CreateTask titles = %#v, want %#v", got, want)
		}
		m.Sprout.Subnav, m.Sprout.Focus = 2, "composer"
		sproutType(m, "Ship the docs")
		driveSprout(t, m, m.SproutHandleKey((tea.KeyPressMsg{Code: tea.KeyEnter}).String()))
		if got, want := fake.goalTitles, []string{"Ship the docs"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("CreateGoal titles = %#v, want %#v", got, want)
		}
	})

	t.Run("model", func(t *testing.T) {
		fake := &fakeSproutClient{harness: pioctl.HarnessResult{Name: "codex", Model: "model-x"}}
		m := sproutOS(fake)
		m.Sprout.Config.Harness.Name = "codex"
		m.SproutHandleKey("m")
		sproutType(m, "model-x")
		driveSprout(t, m, m.SproutHandleKey((tea.KeyPressMsg{Code: tea.KeyEnter}).String()))
		if got, want := fake.setHarness, [][2]string{{"codex", "model-x"}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("SetHarness calls = %#v, want %#v", got, want)
		}
		m.Sprout.Config.Harness.Model = ""
		m.SproutHandleKey("m")
		sproutType(m, "model x")
		if got := m.Sprout.ModelDraft; got != "model x" {
			t.Fatalf("model draft = %q, want space retained", got)
		}
	})
}

func TestSproutDropsStaleReplyAndMarksOtherSessionUnread(t *testing.T) {
	m := sproutOS(&fakeSproutClient{history: map[string][]pioctl.Entry{"b": nil}})
	m.Sprout.Selected, m.Sprout.gen, m.Sprout.stream = "a", 1, 1
	m.selectSproutSession("b") // switching increments the generation before the old terminal arrives.
	m.handleSproutMsg(SproutStreamMsg{Session: "a", Gen: 1, Stream: 1, Result: pioctl.ChatResult{OK: true, Text: "old"}})
	if len(m.Sprout.Transcript) != 0 || !m.Sprout.Unread["a"] {
		t.Fatalf("transcript=%+v unread=%v", m.Sprout.Transcript, m.Sprout.Unread)
	}
	m.Sprout.Sessions = []pioctl.Session{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}}
	m.Sprout.Filter = 3
	if got := m.renderSprout(); !strings.Contains(got, "Alpha") || strings.Contains(got, "No conversations yet") {
		t.Fatalf("unread filter did not list unread session:\n%s", got)
	}
	driveSprout(t, m, m.selectSproutSession("a"))
	if m.Sprout.Unread["a"] {
		t.Fatal("viewing session did not clear unread")
	}
}

func TestSproutRejectAndUnreachableKeepTranscriptSafe(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		err                      error
		wantStatus, wantComposer string
	}{
		{"rejected", &pioctl.RequestError{Code: "not_found", Message: "gone"}, "pio: not_found: gone", "hello"},
		{"unreachable", pioctl.ErrUnreachable, "pio: unreachable", "hello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sproutOS(&fakeSproutClient{sendErr: tc.err})
			m.Sprout.Selected, m.Sprout.Focus, m.Sprout.Composer = "a", "composer", "hello"
			driveSprout(t, m, m.SproutHandleKey("enter"))
			if len(m.Sprout.Transcript) != 0 || m.Sprout.Status != tc.wantStatus || m.Sprout.Composer != tc.wantComposer {
				t.Fatalf("transcript=%+v status=%q composer=%q", m.Sprout.Transcript, m.Sprout.Status, m.Sprout.Composer)
			}
			if tc.name == "unreachable" {
				m.SproutHandleKey("!")
				if m.Sprout.Composer != "hello!" {
					t.Fatalf("composer=%q", m.Sprout.Composer)
				}
			}
		})
	}
}

func TestSproutTranscriptFramesNoOverflow(t *testing.T) {
	entries := make([]pioctl.Entry, 6)
	for i := range entries {
		entries[i] = pioctl.Entry{Role: "assistant", Text: "long reply wraps safely across this narrow transcript pane"}
		if i%2 == 0 {
			entries[i].Role = "user"
		}
	}
	for _, size := range [][2]int{{120, 40}, {80, 24}} {
		m := sproutOS(&fakeSproutClient{})
		m.Width, m.Height = size[0], size[1]
		m.Sprout.Selected, m.Sprout.Transcript, m.Sprout.Composer = "a", entries, "half typed"
		for _, line := range strings.Split(m.renderSprout(), "\n") {
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
	m.Sprout.Tasks = []pioctl.Task{{ID: "a"}, {ID: "b"}}
	m.SproutHandleKey("tab")
	if m.Sprout.Subnav != 1 {
		t.Fatalf("subnav=%d", m.Sprout.Subnav)
	}
	m.Sprout.Cursor = 0
	m.SproutHandleKey("k")
	if m.Sprout.TaskCursor != 1 {
		t.Fatalf("task cursor=%d", m.Sprout.TaskCursor)
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

func TestSproutBoardFiltersAndRender(t *testing.T) {
	fake := &fakeSproutClient{tasks: []pioctl.Task{{ID: "done", Title: "Done", Status: "done", UpdatedAt: time.Now(), Body: "done body"}, {ID: "working", Title: "Working", Status: "working", UpdatedAt: time.Now(), Attempt: 2, ReviewRequired: true, Body: "working body"}, {ID: "todo", Title: "Todo", Status: "todo", UpdatedAt: time.Now()}}, goals: []pioctl.Goal{{ID: "old", Title: "Old", Status: "abandoned"}, {ID: "live", Title: "Live", Status: "active", SuccessCriteria: []pioctl.GoalCriterion{{Condition: "ship"}}, Body: "goal body"}}}
	m := sproutOS(fake)
	driveSprout(t, m, m.SproutHandleKey("tab"))
	m.Sprout.TaskCursor = 1
	if got := m.renderSprout(); !strings.Contains(got, "✓ Done") || !strings.Contains(got, "working body") || !strings.Contains(got, "[All 3]  Open 2  Done 1") {
		t.Fatalf("files board:\n%s", got)
	}
	m.SproutHandleKey("f")
	if len(m.Sprout.visibleTasks()) != 2 {
		t.Fatalf("open=%d", len(m.Sprout.visibleTasks()))
	}
	m.SproutHandleKey("f")
	if len(m.Sprout.visibleTasks()) != 1 {
		t.Fatalf("done=%d", len(m.Sprout.visibleTasks()))
	}
	driveSprout(t, m, m.SproutHandleKey("tab"))
	m.SproutHandleKey("f")
	if len(m.Sprout.visibleGoals()) != 1 {
		t.Fatalf("active=%d", len(m.Sprout.visibleGoals()))
	}
	m.SproutHandleKey("f")
	if len(m.Sprout.visibleGoals()) != 1 {
		t.Fatalf("closed=%d", len(m.Sprout.visibleGoals()))
	}
}

func TestSproutGoalCriteriaRender(t *testing.T) {
	criteria := []pioctl.GoalCriterion{
		{ID: "welcome-file-exact-content", Condition: "A regular WELCOME.md file exists at the workspace root; its complete content is exactly welcome to pio with at most one terminal newline."},
		{Condition: "The workspace contains no extra welcome files, and each visible path stays inside the narrow workflows pane without overflowing its frame."},
	}
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		m := sproutOS(&fakeSproutClient{goals: []pioctl.Goal{{ID: "welcome", Title: "Welcome", Status: "active", SuccessCriteria: criteria, Body: "body"}}})
		m.Width, m.Height, m.Sprout.Subnav = size[0], size[1], 2
		frame := ansi.Strip(m.renderSprout())
		lines := strings.Split(frame, "\n")
		if len(lines) != size[1] {
			t.Fatalf("%dx%d lines=%d", size[0], size[1], len(lines))
		}
		for _, line := range lines {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("%dx%d overflow %q", size[0], size[1], line)
			}
		}
		flat := strings.Join(strings.Fields(frame), " ")
		if !strings.Contains(flat, "criteria:") {
			t.Fatalf("%dx%d missing criteria:\n%s", size[0], size[1], frame)
		}
		for _, criterion := range criteria {
			text := criterion.Condition
			if criterion.ID != "" {
				text = "[" + criterion.ID + "] " + text
			}
			if !strings.Contains(flat, strings.Join(strings.Fields(text), " ")) {
				t.Fatalf("%dx%d missing criterion %q:\n%s", size[0], size[1], text, frame)
			}
		}
	}

	m := sproutOS(&fakeSproutClient{goals: []pioctl.Goal{{ID: "empty", Title: "Empty", Status: "active", Body: "body"}}})
	m.Sprout.Subnav = 2
	if frame := ansi.Strip(m.renderSprout()); strings.Contains(frame, "criteria:") {
		t.Fatalf("empty criteria block:\n%s", frame)
	}
}

func TestSproutBoardTickRefreshesAndRearmsOnce(t *testing.T) {
	fake := &fakeSproutClient{sessions: []pioctl.Session{{ID: "a"}}, tasks: []pioctl.Task{{ID: "t"}}, goals: []pioctl.Goal{{ID: "g"}}, jobs: []pioctl.Job{{Number: 1}}}
	m := sproutOS(fake)
	m.Sprout.gen, m.Sprout.Subnav = 1, 1
	batch, ok := m.handleSproutMsg(SproutTickMsg{Gen: 1})().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("batch=%T len=%d", batch, len(batch))
	}
	ticks := 0
	for _, cmd := range batch {
		switch msg := cmd().(type) {
		case SproutRefreshMsg:
			if rearm := m.handleSproutMsg(msg); rearm != nil {
				if _, ok := rearm().(SproutTickMsg); !ok {
					t.Fatalf("rearm=%T", rearm())
				}
				ticks++
			}
		case SproutBoardMsg:
			m.handleSproutMsg(msg)
		default:
			t.Fatalf("message=%T", msg)
		}
	}
	if fake.sessionsCalls != 1 || fake.configCalls != 1 || fake.tasksCalls != 1 || fake.goalsCalls != 1 || fake.jobsCalls != 1 || ticks != 1 {
		t.Fatalf("calls sessions=%d config=%d tasks=%d goals=%d jobs=%d ticks=%d", fake.sessionsCalls, fake.configCalls, fake.tasksCalls, fake.goalsCalls, fake.jobsCalls, ticks)
	}
}

func TestSproutCreatesTaskAndGoal(t *testing.T) {
	fake := &fakeSproutClient{task: pioctl.Task{ID: "made", Title: "Made", Status: "todo"}, goal: pioctl.Goal{ID: "goal", Title: "Goal", Status: "proposed"}}
	m := sproutOS(fake)
	m.Sprout.Subnav, m.Sprout.Focus, m.Sprout.Composer = 1, "composer", "Made"
	driveSprout(t, m, m.SproutHandleKey("enter"))
	if len(m.Sprout.Tasks) != 1 || m.Sprout.Status != "task created: made" || m.Sprout.Composer != "" || m.Sprout.TaskCursor != 0 {
		t.Fatalf("tasks=%+v state=%+v", m.Sprout.Tasks, m.Sprout)
	}
	m.Sprout.Subnav, m.Sprout.Focus, m.Sprout.Composer = 2, "composer", "Goal"
	driveSprout(t, m, m.SproutHandleKey("enter"))
	if len(m.Sprout.Goals) != 1 || m.Sprout.Status != "goal created: goal" || m.Sprout.Composer != "" || m.Sprout.GoalCursor != 0 {
		t.Fatalf("goals=%+v state=%+v", m.Sprout.Goals, m.Sprout)
	}
	fake.taskErr = &pioctl.RequestError{Code: "invalid_params", Message: "title required"}
	m.Sprout.Subnav, m.Sprout.Focus, m.Sprout.Composer = 1, "composer", "keep"
	driveSprout(t, m, m.SproutHandleKey("enter"))
	if m.Sprout.Composer != "keep" || m.Sprout.Status != "title required" {
		t.Fatalf("composer=%q status=%q", m.Sprout.Composer, m.Sprout.Status)
	}
}

func TestSproutHarnessPickerAndModel(t *testing.T) {
	fake := &fakeSproutClient{}
	fake.harness.Name, fake.harness.Model, fake.harness.Previous.Name = "claude-code", "gpt-5.6-terra", "codex"
	m := sproutOS(fake)
	m.Sprout.Config.Harness.Name, m.Sprout.Config.Harness.Model = "codex", "old"
	m.SproutHandleKey("h")
	m.SproutHandleKey("j")
	driveSprout(t, m, m.SproutHandleKey("enter"))
	if got := fake.setHarness[0]; got != [2]string{"claude-code", ""} || m.Sprout.Config.Harness.Name != "claude-code" {
		t.Fatalf("calls=%v config=%+v", fake.setHarness, m.Sprout.Config)
	}
	old := m.Sprout.Config.Harness.Name
	fake.harnessErr = &pioctl.RequestError{Code: "busy", Message: "Cannot change harness: 1 live turn, 0 live jobs"}
	m.SproutHandleKey("h")
	driveSprout(t, m, m.SproutHandleKey("enter"))
	if m.Sprout.Config.Harness.Name != old || m.Sprout.Status != "Cannot change harness: 1 live turn, 0 live jobs" {
		t.Fatalf("config=%+v status=%q", m.Sprout.Config, m.Sprout.Status)
	}
	fake.harnessErr = nil
	fake.harness.Name, fake.harness.Model = "claude-code", "opus-5"
	m.Sprout.Config.Harness.Model = ""
	m.SproutHandleKey("m")
	for _, key := range strings.Split("opus-5", "") {
		m.SproutHandleKey(key)
	}
	driveSprout(t, m, m.SproutHandleKey("enter"))
	if got := fake.setHarness[len(fake.setHarness)-1]; got != [2]string{"claude-code", "opus-5"} {
		t.Fatalf("model call=%v", got)
	}
	m.Sprout.Config.Harness.Model = "original"
	m.SproutHandleKey("m")
	m.SproutHandleKey("x")
	m.SproutHandleKey("esc")
	if m.Sprout.Config.Harness.Model != "original" {
		t.Fatalf("model=%q", m.Sprout.Config.Harness.Model)
	}
}

func TestSproutStaleBoardCreateHarnessAndHints(t *testing.T) {
	m := sproutOS(&fakeSproutClient{})
	m.Sprout.gen = 7
	m.CloseSprout()
	m.handleSproutMsg(SproutBoardMsg{Gen: 7, Tasks: []pioctl.Task{{ID: "bad"}}})
	m.handleSproutMsg(SproutCreatedMsg{Gen: 7, Subnav: 1, Task: pioctl.Task{ID: "bad"}})
	m.handleSproutMsg(SproutHarnessMsg{Gen: 7, Result: pioctl.HarnessResult{Name: "bad"}})
	if len(m.Sprout.Tasks) != 0 || m.Sprout.Config.Harness.Name != "" {
		t.Fatalf("stale applied: %+v", m.Sprout)
	}
	m.ShowSprout, m.Sprout.Focus = true, "sidebar"
	m.SproutHandleKey("?")
	if got := m.renderSprout(); !strings.Contains(got, "Sprout keys") || !strings.Contains(got, "h harness") {
		t.Fatalf("hints:\n%s", got)
	}
	m.SproutHandleKey("esc")
	if m.Sprout.Focus != "sidebar" {
		t.Fatalf("focus=%q", m.Sprout.Focus)
	}
}

func TestSproutFilesComposerSwallowsKeysAndFrames(t *testing.T) {
	fake := &fakeSproutClient{tasks: []pioctl.Task{{Title: "Task", Status: "working", Body: "body"}}, goals: []pioctl.Goal{{Title: "Goal", Status: "active", Body: "body"}}, jobs: []pioctl.Job{{Number: 3, Kind: "implement", TaskID: "task", State: "running", StartedAt: time.Now()}}}
	for _, subnav := range []int{1, 2} {
		for _, size := range [][2]int{{80, 24}, {120, 40}} {
			m := sproutOS(fake)
			m.Width, m.Height, m.Sprout.Subnav = size[0], size[1], subnav
			out := m.renderSprout()
			if len(strings.Split(out, "\n")) != size[1] {
				t.Fatalf("%d %dx%d lines=%d", subnav, size[0], size[1], len(strings.Split(out, "\n")))
			}
			for _, line := range strings.Split(out, "\n") {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("%d %dx%d overflow %q", subnav, size[0], size[1], line)
				}
			}
		}
	}
	m := sproutOS(fake)
	m.Sprout.Subnav, m.Sprout.Focus = 1, "composer"
	for _, key := range []string{"h", "m", "r", "f"} {
		m.SproutHandleKey(key)
	}
	if m.Sprout.Composer != "hmrf" {
		t.Fatalf("composer=%q", m.Sprout.Composer)
	}
	m.Sprout.Focus, m.Sprout.Picker = "picker", 1
	if got := m.renderSprout(); !strings.Contains(got, "> claude-code") || !strings.Contains(got, "body") {
		t.Fatalf("picker:\n%s", got)
	}
}

func TestSproutFrameDump(t *testing.T) {
	if os.Getenv("SPROUT_DUMP") != "1" {
		t.Skip("set SPROUT_DUMP=1")
	}
	now := time.Now()
	model := "gpt-5.6-terra"
	fake := &fakeSproutClient{
		sessions: []pioctl.Session{{ID: "a", Name: "Alpha", Active: true}, {ID: "b", Name: "Beta"}},
		tasks:    []pioctl.Task{{ID: "todo", Title: "Write frame dump", Status: "todo", Body: "## Progress\n- Added frame rows\n\n- Reviewed picker", UpdatedAt: now}, {ID: "working", Title: "Refresh board", Status: "working", Body: "Working body", UpdatedAt: now}, {ID: "done", Title: "Done task", Status: "done", UpdatedAt: now}},
		goals:    []pioctl.Goal{{ID: "ship", Title: "Ship Sprout", Status: "active", SuccessCriteria: []pioctl.GoalCriterion{{ID: "checks", Condition: "All checks green"}, {Condition: "Workflows frame stays inside its pane"}}, Body: "Goal body", UpdatedAt: now}, {ID: "old", Title: "Old goal", Status: "abandoned", UpdatedAt: now}},
		jobs:     []pioctl.Job{{Number: 1, Kind: "implement", TaskID: "working", State: "running", StartedAt: now}, {Number: 2, Kind: "review", TaskID: "done", State: "error", Error: "failed", StartedAt: now, EndedAt: &now}},
	}
	fake.config.Harness.Name, fake.config.Harness.Model = "codex", model
	views := []struct {
		name  string
		apply func(*OS)
	}{
		{"CONVERSATIONS", func(m *OS) {
			m.Sprout.Selected = "a"
			m.Sprout.Transcript = []pioctl.Entry{{Role: "user", Text: "hello"}, {Role: "assistant", Text: "first reply line\nsecond reply line\nthird reply line"}}
		}},
		{"FILES", func(m *OS) { m.Sprout.Subnav = 1 }},
		{"WORKFLOWS", func(m *OS) { m.Sprout.Subnav = 2 }},
		{"PICKER", func(m *OS) { m.Sprout.Subnav, m.Sprout.Focus, m.Sprout.Picker = 1, "picker", 1 }},
		{"MODEL", func(m *OS) { m.Sprout.Subnav, m.Sprout.Focus, m.Sprout.ModelDraft = 1, "model", model }},
		{"HINTS", func(m *OS) { m.Sprout.Focus = "hints" }},
	}
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		for _, auto := range []string{"on", "off"} {
			for _, view := range views {
				m := sproutOS(fake)
				m.Width, m.Height = size[0], size[1]
				m.Sprout.Auto = pioctl.AutoState{Auto: auto, Orchestrator: "running"}
				view.apply(m)
				frame := ansi.Strip(m.renderSprout())
				lines := strings.Split(frame, "\n")
				if len(lines) != size[1] {
					t.Fatalf("%s %dx%d lines=%d", view.name, size[0], size[1], len(lines))
				}
				for _, line := range lines {
					if lipgloss.Width(line) > size[0] {
						t.Fatalf("%s %dx%d overflow %q", view.name, size[0], size[1], line)
					}
				}
				if !strings.Contains(frame, "auto "+auto) {
					t.Fatalf("%s %dx%d missing auto %s:\n%s", view.name, size[0], size[1], auto, frame)
				}
				if !strings.Contains(frame, "a auto") {
					t.Fatalf("%s %dx%d missing auto hint:\n%s", view.name, size[0], size[1], frame)
				}
				t.Logf("--- %s %s %dx%d ---\n%s", view.name, auto, size[0], size[1], frame)
			}
		}
	}
}

func TestSproutNoticesExpireButLiveStatusPersists(t *testing.T) {
	start := time.Date(2026, time.October, 6, 0, 0, 0, 0, time.UTC)
	m := sproutOS(&fakeSproutClient{})
	m.Sprout.gen = 1
	m.handleSproutMsg(SproutCreatedMsg{Gen: 1, Subnav: 1, Task: pioctl.Task{ID: "made"}})
	if m.Sprout.Status == "" {
		t.Fatal("task-created notice missing")
	}
	m.Sprout.StatusAt = start
	m.handleSproutMsg(SproutTickMsg{Gen: 1, At: start.Add(4 * time.Second)})
	if m.Sprout.Status == "" {
		t.Fatal("notice cleared before 6 seconds")
	}
	m.handleSproutMsg(SproutTickMsg{Gen: 1, At: start.Add(6 * time.Second)})
	if m.Sprout.Status != "" {
		t.Fatalf("notice = %q after 6 seconds, want cleared", m.Sprout.Status)
	}

	m.Sprout.Status = "thinking…"
	m.Sprout.StatusAt = time.Time{}
	m.handleSproutMsg(SproutTickMsg{Gen: 1, At: start.Add(12 * time.Second)})
	if m.Sprout.Status != "thinking…" {
		t.Fatalf("live status = %q, want thinking", m.Sprout.Status)
	}

	m.Sprout.StatusAt = start
	m.Sprout.Focus, m.Sprout.Selected, m.Sprout.Composer = "composer", "a", "message"
	m.SproutHandleKey("enter")
	m.handleSproutMsg(SproutTickMsg{Gen: 1, At: start.Add(12 * time.Second)})
	if m.Sprout.Status != "thinking…" {
		t.Fatalf("send status = %q, want thinking", m.Sprout.Status)
	}
}

func TestSproutSuccessfulRefreshClearsUnreachableNotice(t *testing.T) {
	m := sproutOS(&fakeSproutClient{sessions: []pioctl.Session{{ID: "a"}}})
	m.Sprout.gen = 1
	m.Sprout.Status = "pio: unreachable"
	m.handleSproutMsg(SproutRefreshMsg{Gen: 1, Sessions: m.Sprout.Sessions})
	if m.Sprout.Status != "" {
		t.Fatalf("status = %q after successful refresh, want cleared", m.Sprout.Status)
	}
}

func TestSproutAutoRefreshToggleAndNotice(t *testing.T) {
	fake := &fakeSproutClient{sessions: []pioctl.Session{{ID: "a"}}, auto: pioctl.AutoState{Auto: "off", Orchestrator: "stopped"}}
	m := sproutOS(fake)
	m.Sprout.gen = 1
	driveSprout(t, m, m.sproutRefreshCmd())
	if got := m.Sprout.Auto; got != fake.auto {
		t.Fatalf("auto after refresh = %+v, want %+v", got, fake.auto)
	}
	fake.auto = pioctl.AutoState{Auto: "on", Orchestrator: "running"}
	msg := m.SproutHandleKey("a")()
	if follow := m.handleSproutMsg(msg); follow != nil {
		t.Fatalf("auto toggle follow-up = %T, want nil", follow())
	}
	if got := fake.setAuto; !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("SetAuto calls = %v, want [true]", got)
	}
	if got := m.Sprout.Auto; got != fake.auto {
		t.Fatalf("auto after toggle = %+v, want %+v", got, fake.auto)
	}
	if got := m.Sprout.Status; got != "auto: on" {
		t.Fatalf("notice = %q, want auto: on", got)
	}
	if !m.Sprout.StatusAt.IsZero() {
		m.handleSproutMsg(SproutTickMsg{Gen: 1, At: m.Sprout.StatusAt.Add(sproutNoticeDuration)})
	}
	if got := m.Sprout.Status; got != "" {
		t.Fatalf("expired notice = %q", got)
	}
	fake.auto = pioctl.AutoState{Auto: "off", Orchestrator: "stopped"}
	driveSprout(t, m, m.SproutHandleKey("a"))
	if got := fake.setAuto; !reflect.DeepEqual(got, []bool{true, false}) {
		t.Fatalf("SetAuto calls = %v, want [true false]", got)
	}
	fake.auto, fake.autoErr = pioctl.AutoState{Auto: "on", Orchestrator: "running"}, &pioctl.RequestError{Code: "internal", Message: "status unavailable"}
	driveSprout(t, m, m.sproutRefreshCmd())
	if got := m.Sprout.Auto.Auto; got != "off" || !m.Sprout.Connected {
		t.Fatalf("auto failure state=%+v connected=%t", m.Sprout.Auto, m.Sprout.Connected)
	}
}

func TestSproutAutoRejectComposerAndStaleReply(t *testing.T) {
	fake := &fakeSproutClient{auto: pioctl.AutoState{Auto: "off", Orchestrator: "running"}}
	m := sproutOS(fake)
	m.Sprout.gen = 1
	m.Sprout.Auto = fake.auto
	fake.setAutoErr = &pioctl.RequestError{Code: "invalid_params", Message: "enabled must be a boolean"}
	driveSprout(t, m, m.SproutHandleKey("a"))
	if got := m.Sprout.Auto.Auto; got != "off" {
		t.Fatalf("auto after rejected set = %q", got)
	}
	if got := m.Sprout.Status; got != "enabled must be a boolean" {
		t.Fatalf("rejection notice = %q", got)
	}
	m.Sprout.Focus = "composer"
	m.SproutHandleKey("a")
	if got := m.Sprout.Composer; got != "a" || len(fake.setAuto) != 1 {
		t.Fatalf("composer=%q calls=%v", got, fake.setAuto)
	}
	m.Sprout.Focus, m.Sprout.Status = "sidebar", ""
	m.Sprout.gen++
	m.handleSproutMsg(SproutAutoMsg{State: pioctl.AutoState{Auto: "on"}, Gen: 1})
	if got := m.Sprout.Auto.Auto; got != "off" || m.Sprout.Status != "" {
		t.Fatalf("stale auto applied: %+v status=%q", m.Sprout.Auto, m.Sprout.Status)
	}
}

func TestSproutMultilineTextKeepsRowsAndWidths(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		m := sproutOS(&fakeSproutClient{})
		m.Width, m.Height, m.Sprout.Subnav = size[0], size[1], 1
		m.Sprout.Tasks = []pioctl.Task{{ID: "task", Title: "Task", Body: "## Progress\n- Added a longer line that wraps at narrow pane width\n\n- Reviewed it"}}
		frame := ansi.Strip(m.renderSprout())
		for _, want := range []string{"## Progress", "- Added a longer", "- Reviewed it"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("%dx%d missing row %q:\n%s", size[0], size[1], want, frame)
			}
		}
		if got := len(strings.Split(frame, "\n")); got != size[1] {
			t.Fatalf("%dx%d rows = %d", size[0], size[1], got)
		}
		for _, line := range strings.Split(frame, "\n") {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("%dx%d overflow: %q", size[0], size[1], line)
			}
		}
	}

	turns := sproutTurnLines([]pioctl.Entry{{Role: "assistant", Text: "one\ntwo\nthree"}}, 40, theme.UI(), func(fg, bg color.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(fg).Background(bg)
	})
	if got, want := ansi.Strip(strings.Join(turns, "\n")), "assistant › one\n"+strings.Repeat(" ", lipgloss.Width("assistant › "))+"two\n"+strings.Repeat(" ", lipgloss.Width("assistant › "))+"three"; got != want {
		t.Fatalf("multiline transcript = %q, want %q", got, want)
	}
}

func TestSproutSelectionFollowsIDsAcrossRefreshFilterAndCreation(t *testing.T) {
	m := sproutOS(&fakeSproutClient{})
	m.Sprout.gen, m.Sprout.Subnav = 1, 1
	m.Sprout.Tasks = []pioctl.Task{{ID: "a", Title: "A", Status: "todo", Body: "body A"}, {ID: "b", Title: "B", Status: "working", Body: "body B"}, {ID: "c", Title: "C", Status: "done", Body: "body C"}}
	m.Sprout.TaskCursor = 1
	m.Sprout.TaskSelected = "b"
	m.handleSproutMsg(SproutBoardMsg{Gen: 1, Tasks: []pioctl.Task{{ID: "c", Title: "C", Status: "done", Body: "body C"}, {ID: "a", Title: "A", Status: "todo", Body: "body A"}, {ID: "b", Title: "B", Status: "working", Body: "body B"}}})
	if m.Sprout.TaskCursor != 2 || !strings.Contains(ansi.Strip(m.renderSprout()), "body B") {
		t.Fatalf("task selection = id %q cursor %d", m.Sprout.TaskSelected, m.Sprout.TaskCursor)
	}
	m.SproutHandleKey("k")
	if m.Sprout.TaskSelected != "a" {
		t.Fatalf("previous task selection = %q", m.Sprout.TaskSelected)
	}
	m.SproutHandleKey("j")
	if m.Sprout.TaskSelected != "b" {
		t.Fatalf("next task selection = %q", m.Sprout.TaskSelected)
	}
	m.handleSproutMsg(SproutBoardMsg{Gen: 1, Tasks: []pioctl.Task{{ID: "c", Title: "C", Status: "done"}, {ID: "a", Title: "A", Status: "todo"}}})
	if m.Sprout.TaskCursor != 1 || m.Sprout.TaskSelected != "a" {
		t.Fatalf("removed task selection = id %q cursor %d", m.Sprout.TaskSelected, m.Sprout.TaskCursor)
	}
	m.handleSproutMsg(SproutCreatedMsg{Gen: 1, Subnav: 1, Task: pioctl.Task{ID: "new", Title: "New", Status: "todo"}})
	if m.Sprout.TaskSelected != "new" {
		t.Fatalf("created task selection = %q", m.Sprout.TaskSelected)
	}

	m.Sprout.Subnav = 2
	m.Sprout.Goals = []pioctl.Goal{{ID: "a", Title: "A", Status: "active", Body: "body A"}, {ID: "b", Title: "B", Status: "active", Body: "body B"}, {ID: "c", Title: "C", Status: "done", Body: "body C"}}
	m.Sprout.GoalCursor, m.Sprout.GoalSelected = 1, "b"
	m.handleSproutMsg(SproutBoardMsg{Gen: 1, Goals: []pioctl.Goal{{ID: "c", Title: "C", Status: "done", Body: "body C"}, {ID: "a", Title: "A", Status: "active", Body: "body A"}, {ID: "b", Title: "B", Status: "active", Body: "body B"}}})
	if m.Sprout.GoalCursor != 2 || m.Sprout.GoalSelected != "b" || !strings.Contains(ansi.Strip(m.renderSprout()), "body B") {
		t.Fatalf("goal selection = id %q cursor %d", m.Sprout.GoalSelected, m.Sprout.GoalCursor)
	}
	m.SproutHandleKey("k")
	if m.Sprout.GoalSelected != "a" {
		t.Fatalf("previous goal selection = %q", m.Sprout.GoalSelected)
	}
	m.SproutHandleKey("j")
	if m.Sprout.GoalSelected != "b" {
		t.Fatalf("next goal selection = %q", m.Sprout.GoalSelected)
	}
	m.handleSproutMsg(SproutCreatedMsg{Gen: 1, Subnav: 2, Goal: pioctl.Goal{ID: "new-goal", Title: "New", Status: "proposed"}})
	if m.Sprout.GoalSelected != "new-goal" {
		t.Fatalf("created goal selection = %q", m.Sprout.GoalSelected)
	}
	m.handleSproutMsg(SproutBoardMsg{Gen: 1, Goals: []pioctl.Goal{{ID: "c", Title: "C", Status: "done"}, {ID: "a", Title: "A", Status: "active"}}})
	if m.Sprout.GoalCursor != 1 || m.Sprout.GoalSelected != "a" {
		t.Fatalf("removed goal selection = id %q cursor %d", m.Sprout.GoalSelected, m.Sprout.GoalCursor)
	}
}

func TestSproutConversationCursorFollowsID(t *testing.T) {
	m := sproutOS(&fakeSproutClient{})
	m.Sprout.gen = 1
	m.Sprout.Sessions = []pioctl.Session{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	m.Sprout.Cursor, m.Sprout.CursorID = 1, "b"
	m.handleSproutMsg(SproutRefreshMsg{Gen: 1, Sessions: []pioctl.Session{{ID: "new", Name: "New"}, {ID: "a", Name: "A"}, {ID: "b", Name: "B"}}})
	if m.Sprout.Cursor != 2 || m.Sprout.CursorID != "b" {
		t.Fatalf("conversation cursor = id %q index %d", m.Sprout.CursorID, m.Sprout.Cursor)
	}
	m.Sprout.Filter = 2
	m.Sprout.Unread = map[string]bool{"a": true}
	m.SproutHandleKey("f")
	if m.Sprout.CursorID != "a" || m.Sprout.Cursor != 0 {
		t.Fatalf("filtered cursor = id %q index %d", m.Sprout.CursorID, m.Sprout.Cursor)
	}
}
