package pioctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fake(t *testing.T, reply func(string) []string) *Client {
	t.Helper()
	path := filepath.Join("/tmp", fmt.Sprintf("tuios-pioctl-%d.sock", time.Now().UnixNano()))
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close(); os.Remove(path) })
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				var q map[string]any
				json.NewDecoder(c).Decode(&q)
				for _, line := range reply(q["verb"].(string)) {
					c.Write([]byte(line + "\n"))
				}
			}()
		}
	}()
	return &Client{Socket: path, Timeout: time.Second}
}

func TestVerbsAndStream(t *testing.T) {
	c := fake(t, func(verb string) []string {
		switch verb {
		case "sessions.list":
			return []string{`{"id":2,"ok":true,"result":{"active":"default","sessions":[{"id":"default","name":"Default","created_at":"2026-09-29T08:00:00.000Z","last_used_at":"2026-09-29T08:00:00.000Z"}]}}`}
		case "sessions.create":
			return []string{`{"ok":true,"result":{"id":"a"}}`}
		case "config.get":
			return []string{`{"ok":true,"result":{"harness":{"name":"codex","model":"x"}}}`}
		case "chat.send":
			return []string{`{"event":{"type":"delta"}}`, `{"ok":true,"done":true,"result":{"ok":true,"text":"reply"}}`}
		default:
			return []string{`{"ok":true,"result":{}}`}
		}
	})
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Sessions(ctx); err != nil || len(got) != 1 || !got[0].Active {
		t.Fatalf("sessions: %v %v", got, err)
	}
	if _, err := c.Create(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if err := c.Switch(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if got, err := c.GetConfig(ctx); err != nil || got.Harness.Name != "codex" {
		t.Fatalf("config: %+v %v", got, err)
	}
	events := 0
	if got, err := c.Send(ctx, "a", "hi", func(json.RawMessage) { events++ }); err != nil || !got.OK || got.Text != "reply" || events != 1 {
		t.Fatalf("send: %+v %v events=%d", got, err, events)
	}
}
func TestRejectedMalformedAndUnreachable(t *testing.T) {
	rejected := fake(t, func(string) []string { return []string{`{"ok":false,"error":{"code":"bad_request","message":"no"}}`} })
	if _, err := rejected.Status(context.Background()); err == nil {
		t.Fatal("rejection accepted")
	}
	bad := fake(t, func(string) []string { return []string{"not json"} })
	if _, err := bad.Status(context.Background()); err == nil {
		t.Fatal("malformed accepted")
	}
	_, err := (&Client{Socket: filepath.Join(t.TempDir(), "none"), Timeout: time.Millisecond}).Status(context.Background())
	if err == nil {
		t.Fatal("unreachable accepted")
	}
}

func TestHistory(t *testing.T) {
	c := fake(t, func(verb string) []string {
		if verb == "sessions.history" {
			return []string{`{"ok":true,"result":{"id":"default","entries":[{"at":"2026-09-29T08:00:00Z","role":"user","text":"hi"}],"skipped":0}}`}
		}
		return []string{`{"ok":true,"result":{}}`}
	})
	entries, skipped, err := c.History(context.Background(), "", 200)
	if err != nil || skipped != 0 || len(entries) != 1 || entries[0].Text != "hi" || entries[0].At.IsZero() {
		t.Fatalf("history: entries=%+v skipped=%d err=%v", entries, skipped, err)
	}
}

func TestHistoryRejectsUnknownAndBadLimit(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		id          string
		limit       int
		code        string
	}{
		{"unknown", `{"ok":false,"error":{"code":"not_found","message":"Unknown chat session: gone"}}`, "gone", 200, "not_found"},
		{"bad limit", `{"ok":false,"error":{"code":"invalid_params","message":"limit must be an integer from 1 to 2000"}}`, "", 0, "invalid_params"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fake(t, func(string) []string { return []string{tc.reply} })
			_, _, err := c.History(context.Background(), tc.id, tc.limit)
			var requestErr *RequestError
			if !errors.As(err, &requestErr) || requestErr.Code != tc.code {
				t.Fatalf("err=%v, want %s", err, tc.code)
			}
		})
	}
}

// TestLiveAgainstRealPio protects the client from fake-only wire assumptions.
// Run it against a running pio with:
// PIO_LIVE_ROOT=/path/to/pio-workspace go test ./internal/pioctl -run TestLiveAgainstRealPio -count=1
func TestLiveAgainstRealPio(t *testing.T) {
	root := os.Getenv("PIO_LIVE_ROOT")
	if root == "" {
		t.Skip("set PIO_LIVE_ROOT to a running pio workspace")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := New(root)
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := c.call(ctx, "status", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := c.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	active := false
	for _, session := range sessions {
		active = active || session.Active
	}
	if !active {
		t.Fatal("sessions did not derive an active session")
	}
	created, err := c.Create(ctx, fmt.Sprintf("tuios-live-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Switch(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetConfig(ctx); err != nil {
		t.Fatal(err)
	}
	config, err := c.call(ctx, "config.get", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(status, []byte("AAHfake")) || bytes.Contains(config, []byte("AAHfake")) {
		t.Fatal("pio secret marker crossed the control socket")
	}
	events := 0
	result, err := c.Send(ctx, created.ID, "Reply with one word: pong", func(json.RawMessage) { events++ })
	if err != nil || !result.OK || events < 1 {
		t.Fatalf("send: result=%+v err=%v events=%d", result, err, events)
	}
	entries, _, err := c.History(ctx, created.ID, 200)
	if err != nil || len(entries) < 2 || entries[len(entries)-2].Role != "user" || entries[len(entries)-1].Role != "assistant" {
		t.Fatalf("history after send: entries=%+v err=%v", entries, err)
	}
	events = 0
	_, err = c.Send(ctx, "no-such-session", "hello", func(json.RawMessage) { events++ })
	var requestErr *RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != "not_found" || events != 0 {
		t.Fatalf("missing session: err=%v events=%d", err, events)
	}
	started := time.Now()
	_, err = New(filepath.Join(t.TempDir(), "unreachable")).Status(context.Background())
	if !errors.Is(err, ErrUnreachable) || time.Since(started) >= 3*time.Second {
		t.Fatalf("unreachable: err=%v elapsed=%s", err, time.Since(started))
	}
}
