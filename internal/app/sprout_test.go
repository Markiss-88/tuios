package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pioctl"
)

type fakeSproutClient struct {
	sessions []pioctl.Session
	history  map[string][]pioctl.Entry
	switched []string
	created  pioctl.Session
	send     pioctl.ChatResult
	sendErr  error
	events   []json.RawMessage
}

func (f *fakeSproutClient) Sessions(context.Context) ([]pioctl.Session, error) {
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
	return pioctl.Config{}, nil
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
	return &OS{Settings: config.Global, Width: 120, Height: 40, ShowSprout: true, Sprout: SproutState{client: client, Sessions: client.sessions, Focus: "sidebar", Follow: true, liveTurn: -1, pendingUser: -1}}
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
	m.Sprout.Sessions = []pioctl.Session{{ID: "a"}, {ID: "b"}}
	m.SproutHandleKey("tab")
	if m.Sprout.Subnav != 1 {
		t.Fatalf("subnav=%d", m.Sprout.Subnav)
	}
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
