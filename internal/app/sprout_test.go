package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pioctl"
	"github.com/charmbracelet/x/ansi"
)

type fakeSproutClient struct {
	sessions                                                      []pioctl.Session
	history                                                       map[string][]pioctl.Entry
	switched                                                      []string
	created                                                       pioctl.Session
	send                                                          pioctl.ChatResult
	sendErr                                                       error
	events                                                        []json.RawMessage
	tasks                                                         []pioctl.Task
	goals                                                         []pioctl.Goal
	jobs                                                          []pioctl.Job
	config                                                        pioctl.Config
	task                                                          pioctl.Task
	goal                                                          pioctl.Goal
	harness                                                       pioctl.HarnessResult
	tasksErr, goalsErr, jobsErr, taskErr, goalErr, harnessErr     error
	setHarness                                                    [][2]string
	sessionsCalls, configCalls, tasksCalls, goalsCalls, jobsCalls int
}

func (f *fakeSproutClient) Sessions(context.Context) ([]pioctl.Session, error) {
	f.sessionsCalls++
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
func (f *fakeSproutClient) Tasks(context.Context, string) ([]pioctl.Task, error) {
	f.tasksCalls++
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
func (f *fakeSproutClient) CreateTask(context.Context, string) (pioctl.Task, error) {
	return f.task, f.taskErr
}
func (f *fakeSproutClient) CreateGoal(context.Context, string) (pioctl.Goal, error) {
	return f.goal, f.goalErr
}
func (f *fakeSproutClient) SetHarness(_ context.Context, name, model string) (pioctl.HarnessResult, error) {
	f.setHarness = append(f.setHarness, [2]string{name, model})
	return f.harness, f.harnessErr
}
func (f *fakeSproutClient) History(_ context.Context, id string, _ int) ([]pioctl.Entry, int, error) {
	return f.history[id], 0, nil
}
func (f *fakeSproutClient) Send(_ context.Context, _ string, _ string, event func(json.RawMessage)) (pioctl.ChatResult, error) {
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
	for cmd != nil {
		cmd = m.handleSproutMsg(cmd())
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
		{"rejected", &pioctl.RequestError{Code: "not_found", Message: "gone"}, "pio: not_found: gone", ""},
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
	fake := &fakeSproutClient{tasks: []pioctl.Task{{ID: "done", Title: "Done", Status: "done", UpdatedAt: time.Now(), Body: "done body"}, {ID: "working", Title: "Working", Status: "working", UpdatedAt: time.Now(), Attempt: 2, ReviewRequired: true, Body: "working body"}, {ID: "todo", Title: "Todo", Status: "todo", UpdatedAt: time.Now()}}, goals: []pioctl.Goal{{ID: "old", Title: "Old", Status: "abandoned"}, {ID: "live", Title: "Live", Status: "active", SuccessCriteria: "ship", Body: "goal body"}}}
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
		tasks:    []pioctl.Task{{ID: "todo", Title: "Write frame dump", Status: "todo", Body: "Task body stays visible under the picker.", UpdatedAt: now}, {ID: "working", Title: "Refresh board", Status: "working", Body: "Working body", UpdatedAt: now}, {ID: "done", Title: "Done task", Status: "done", UpdatedAt: now}},
		goals:    []pioctl.Goal{{ID: "ship", Title: "Ship Sprout", Status: "active", SuccessCriteria: "All checks green", Body: "Goal body", UpdatedAt: now}, {ID: "old", Title: "Old goal", Status: "abandoned", UpdatedAt: now}},
		jobs:     []pioctl.Job{{Number: 1, Kind: "implement", TaskID: "working", State: "running", StartedAt: now}, {Number: 2, Kind: "review", TaskID: "done", State: "error", Error: "failed", StartedAt: now, EndedAt: &now}},
	}
	fake.config.Harness.Name, fake.config.Harness.Model = "codex", model
	views := []struct {
		name  string
		apply func(*OS)
	}{
		{"CONVERSATIONS", func(m *OS) {
			m.Sprout.Selected = "a"
			m.Sprout.Transcript = []pioctl.Entry{{Role: "user", Text: "hello"}, {Role: "assistant", Text: "world"}}
		}},
		{"FILES", func(m *OS) { m.Sprout.Subnav = 1 }},
		{"WORKFLOWS", func(m *OS) { m.Sprout.Subnav = 2 }},
		{"PICKER", func(m *OS) { m.Sprout.Subnav, m.Sprout.Focus, m.Sprout.Picker = 1, "picker", 1 }},
		{"MODEL", func(m *OS) { m.Sprout.Subnav, m.Sprout.Focus, m.Sprout.ModelDraft = 1, "model", model }},
		{"HINTS", func(m *OS) { m.Sprout.Focus = "hints" }},
	}
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		for _, view := range views {
			m := sproutOS(fake)
			m.Width, m.Height = size[0], size[1]
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
			t.Logf("--- %s %dx%d ---\n%s", view.name, size[0], size[1], frame)
		}
	}
}
