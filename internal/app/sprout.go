package app

import (
	"context"
	"encoding/json"
	"errors"
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
	History(context.Context, string, int) ([]pioctl.Entry, int, error)
	Send(context.Context, string, string, func(json.RawMessage)) (pioctl.ChatResult, error)
}

type SproutState struct {
	Tab, Subnav, Filter, Cursor int
	Focus, Selected             string
	Sessions                    []pioctl.Session
	Config                      pioctl.Config
	Transcript                  []pioctl.Entry
	Composer, Status            string
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
type SproutStreamMsg struct {
	Session     string
	Type        string
	Text        string
	Result      pioctl.ChatResult
	Err         error
	Gen, Stream uint64
	ch          <-chan SproutStreamMsg
}
type SproutTickMsg struct{ Gen uint64 }

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
func (m *OS) sproutTick() tea.Cmd {
	gen := m.Sprout.gen
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return SproutTickMsg{Gen: gen} })
}

func (m *OS) selectSproutSession(id string) tea.Cmd {
	s := &m.Sprout
	s.gen++
	s.Selected, s.Transcript, s.Scroll, s.Follow = id, nil, 0, true
	s.Focus = "main"
	s.Status, s.Sending, s.liveTurn, s.pendingUser, s.pendingText = "", false, -1, -1, ""
	if s.Unread != nil {
		delete(s.Unread, id)
	}
	return m.sproutSelectCmd(id)
}

func (m *OS) sproutStartSendCmd() tea.Cmd {
	s := &m.Sprout
	s.stream++
	stream, gen, session, text, client := s.stream, s.gen, s.Selected, s.Composer, s.client
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
		if s.Selected == "" {
			for _, session := range s.Sessions {
				if session.Active {
					return m.selectSproutSession(session.ID)
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
			s.Status = "pio: " + x.Err.Error()
			return nil
		}
		s.Transcript, s.Scroll, s.Follow, s.Status = x.Entries, 0, true, ""
	case SproutCreateMsg:
		if x.Gen != s.gen || !m.ShowSprout {
			return nil
		}
		if x.Err != nil {
			s.Status = "pio: " + x.Err.Error()
			return nil
		}
		return m.selectSproutSession(x.Session.ID)
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
			s.Status = x.Text
		default:
			liveTurn := s.liveTurn
			s.Sending, s.liveTurn = false, -1
			if x.Err != nil {
				if s.pendingUser >= 0 && s.pendingUser < len(s.Transcript) {
					s.Transcript = append(s.Transcript[:s.pendingUser], s.Transcript[s.pendingUser+1:]...)
				}
				if errors.Is(x.Err, pioctl.ErrUnreachable) {
					s.Status = "pio: unreachable"
					s.Composer = s.pendingText
				} else {
					s.Status = "pio: " + x.Err.Error()
				}
				s.pendingUser, s.pendingText = -1, ""
				return nil
			}
			if !x.Result.OK {
				s.Transcript = append(s.Transcript, pioctl.Entry{Role: "assistant", Error: x.Result.Error})
			} else if liveTurn < 0 {
				s.Transcript = append(s.Transcript, pioctl.Entry{Role: "assistant", Text: x.Result.Text})
			}
			s.Status, s.pendingUser, s.pendingText = "", -1, ""
		}
		if !terminal {
			return sproutNextStreamCmd(x.ch)
		}
	case SproutTickMsg:
		if x.Gen == s.gen && m.ShowSprout {
			m.renderSkipped = false
			return m.sproutRefreshCmd()
		}
	}
	return nil
}

func (m *OS) SproutConnected() bool { return m.Sprout.Connected }
func (m *OS) SproutHandleKey(key string) tea.Cmd {
	s := &m.Sprout
	if s.Focus == "" {
		s.Focus = "sidebar"
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
			if s.Composer != "" && !s.Sending {
				s.Transcript = append(s.Transcript, pioctl.Entry{Role: "user", Text: s.Composer})
				s.Status, s.Sending, s.liveTurn, s.Follow = "thinking…", true, -1, true
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
	case "i":
		s.Focus = "composer"
	case "f":
		if s.Focus == "sidebar" {
			s.Filter = (s.Filter + 1) % 4
			s.Cursor = 0
		}
	case "j", "down":
		if s.Focus == "main" {
			s.Scroll++
			s.Follow = false
		} else if sessions := s.visibleSessions(); len(sessions) > 0 {
			s.Cursor = (s.Cursor + 1) % len(sessions)
		}
	case "k", "up":
		if s.Focus == "main" {
			if s.Scroll > 0 {
				s.Scroll--
			}
			s.Follow = false
		} else if sessions := s.visibleSessions(); len(sessions) > 0 {
			s.Cursor = (s.Cursor + len(sessions) - 1) % len(sessions)
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
			return m.selectSproutSession(sessions[s.Cursor].ID)
		}
	case "n":
		return m.sproutCreateCmd()
	case "p":
		m.OpenSessionSwitcher()
	case "?":
		if s.Focus == "hints" {
			s.Focus = "sidebar"
		} else {
			s.Focus = "hints"
		}
	}
	return nil
}
