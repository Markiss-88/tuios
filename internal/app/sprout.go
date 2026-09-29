package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/pioctl"
)

type SproutState struct {
	Tab, Subnav, Filter, Cursor int
	Focus, Selected             string
	Sessions                    []pioctl.Session
	Config                      pioctl.Config
	Connected                   bool
	client                      *pioctl.Client
	gen                         uint64
}
type SproutRefreshMsg struct {
	Sessions []pioctl.Session
	Config   pioctl.Config
	Err      error
	Gen      uint64
}
type SproutTickMsg struct{ Gen uint64 }

func sproutTabIndex(current, count, delta int) int {
	if count == 0 {
		return 0
	}
	return (current + delta%count + count) % count
}

func (m *OS) OpenSprout() tea.Cmd {
	m.ShowSprout = true
	m.Sprout.client = pioctl.New("")
	return m.sproutRefreshCmd()
}
func (m *OS) CloseSprout() { m.ShowSprout = false; m.Sprout.gen++ }
func (m *OS) sproutRefreshCmd() tea.Cmd {
	gen := m.Sprout.gen
	client := m.Sprout.client
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
func (m *OS) sproutTick() tea.Cmd {
	gen := m.Sprout.gen
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return SproutTickMsg{Gen: gen} })
}
func (m *OS) handleSproutMsg(msg tea.Msg) tea.Cmd {
	switch x := msg.(type) {
	case SproutRefreshMsg:
		if x.Gen != m.Sprout.gen || !m.ShowSprout {
			return nil
		}
		m.renderSkipped = false
		m.Sprout.Connected = x.Err == nil
		if x.Err == nil {
			m.Sprout.Sessions = x.Sessions
			m.Sprout.Config = x.Config
		}
		return m.sproutTick()
	case SproutTickMsg:
		if x.Gen == m.Sprout.gen && m.ShowSprout {
			m.renderSkipped = false
			return m.sproutRefreshCmd()
		}
	}
	return nil
}
func (m *OS) SproutConnected() bool { return m.Sprout.Connected }
func (m *OS) SproutHandleKey(key string) tea.Cmd {
	s := &m.Sprout
	switch key {
	case "esc":
		m.NoteAction("sprout_close")
		m.CloseSprout()
	case "]":
		tabs := m.sessionSwitcherItems()
		if len(tabs) > 0 {
			s.Tab = sproutTabIndex(s.Tab, len(tabs), 1)
			m.OpenSessionNode(tabs[s.Tab])
		}
	case "[":
		tabs := m.sessionSwitcherItems()
		if len(tabs) > 0 {
			s.Tab = sproutTabIndex(s.Tab, len(tabs), -1)
			m.OpenSessionNode(tabs[s.Tab])
		}
	case "tab":
		s.Subnav = (s.Subnav + 1) % 3
	case "f":
		s.Filter = (s.Filter + 1) % 4
		s.Cursor = 0
	case "j", "down":
		if len(s.Sessions) > 0 {
			s.Cursor = (s.Cursor + 1) % len(s.Sessions)
		}
	case "k", "up":
		if len(s.Sessions) > 0 {
			s.Cursor = (s.Cursor + len(s.Sessions) - 1) % len(s.Sessions)
		}
	case "enter":
		if s.Cursor < len(s.Sessions) {
			s.Selected = s.Sessions[s.Cursor].Name
			if s.Selected == "" {
				s.Selected = s.Sessions[s.Cursor].Title
			}
		}
	case "n":
		if s.client != nil {
			return func() tea.Msg {
				_, err := s.client.Create(context.Background(), "")
				if err != nil {
					return SproutRefreshMsg{Err: err, Gen: s.gen}
				}
				return SproutTickMsg{Gen: s.gen}
			}
		}
	case "p":
		m.OpenSessionSwitcher()
	case "?":
		if s.Focus == "hints" {
			s.Focus = ""
		} else {
			s.Focus = "hints"
		}
	}
	return nil
}
