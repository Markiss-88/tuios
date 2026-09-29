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
	"strings"
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

func fixture(t *testing.T) *Client {
	t.Helper()
	return fakeRequest(t, func(q map[string]any) []string {
		name := fixtureName(q)
		b, err := os.ReadFile(filepath.Join("testdata", "wire", name+".jsonl"))
		if err != nil {
			t.Fatalf("fixture %s: %v", name, err)
		}
		return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	})
}
func fixtureName(q map[string]any) string {
	verb, _ := q["verb"].(string)
	params, _ := q["params"].(map[string]any)
	switch verb {
	case "status", "sessions.switch":
		return "ping"
	case "tasks.list":
		if params["status"] == "working" {
			return "tasks.list.working"
		}
	case "jobs.list":
		if params["recent"] == true {
			return "jobs.list.recent"
		}
		return "jobs.list.live"
	case "chat.send":
		if params["session"] == "nope" {
			return "chat.send.bad-session"
		}
	case "harness.set":
		switch params["name"] {
		case "claude-code":
			return "harness.set.ok"
		case "agent-zero":
			return "harness.set.internal"
		default:
			return "harness.set.busy"
		}
	}
	if verb == "bad.verb" {
		return "bad-verb"
	}
	return verb
}
func fakeRequest(t *testing.T, reply func(map[string]any) []string) *Client {
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
				for _, line := range reply(q) {
					c.Write([]byte(line + "\n"))
				}
			}()
		}
	}()
	return &Client{Socket: path, Timeout: time.Second}
}

func TestVerbsAndStream(t *testing.T) {
	c := fixture(t)
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Sessions(ctx); err != nil || len(got) != 2 || !got[0].Active {
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
	if got, err := c.Send(ctx, "default", "hi", func(json.RawMessage) { events++ }); err != nil || !got.OK || got.Text != "echo: hello" || events != 3 {
		t.Fatalf("send: %+v %v events=%d", got, err, events)
	}
}
func TestRejectedMalformedAndUnreachable(t *testing.T) {
	rejected := fixture(t)
	if _, err := rejected.call(context.Background(), "bad.verb", nil, nil); err == nil {
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
	c := fixture(t)
	entries, skipped, err := c.History(context.Background(), "", 200)
	if err != nil || skipped != 0 || len(entries) != 2 || entries[0].Text == "" || entries[0].At.IsZero() {
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

func TestRealWireDecodes(t *testing.T) {
	c := fixture(t)
	ctx := context.Background()
	tasks, err := c.Tasks(ctx, "")
	if err != nil || len(tasks) != 3 || tasks[0].Status != "done" || tasks[1].Status != "working" || tasks[2].Status != "todo" {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	recent, err := c.Jobs(ctx, true)
	if err != nil || len(recent) != 3 || recent[0].State != "interrupted" || recent[1].State != "error" || recent[2].State != "final" || recent[0].EndedAt == nil || recent[1].EndedAt == nil || recent[2].EndedAt == nil || len(recent[0].Output) != 2 || recent[1].Error != "copy review failed" {
		t.Fatalf("recent=%+v err=%v", recent, err)
	}
	live, err := c.Jobs(ctx, false)
	if err != nil || len(live) != 1 || live[0].State != "running" || live[0].EndedAt != nil {
		t.Fatalf("live=%+v err=%v", live, err)
	}
	ok, err := c.SetHarness(ctx, "claude-code", "")
	if err != nil || ok.Previous.Name != "codex" {
		t.Fatalf("ok=%+v err=%v", ok, err)
	}
	for _, tc := range []struct{ name, want string }{{"codex", "busy"}, {"agent-zero", "internal"}} {
		_, err := c.SetHarness(ctx, tc.name, "")
		var requestErr *RequestError
		if !errors.As(err, &requestErr) || requestErr.Code != tc.want {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.want == "busy" && requestErr.Message != "Cannot change harness: 0 live turns, 1 live job" {
			t.Fatalf("busy message=%q", requestErr.Message)
		}
	}
	created, err := c.CreateTask(ctx, "Made from socket")
	if err != nil || created.ID != "made-from-socket" {
		t.Fatalf("task=%+v err=%v", created, err)
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
	tasks, err := c.Tasks(ctx, "")
	if err != nil || len(tasks) != 3 {
		t.Fatalf("tasks=%d err=%v", len(tasks), err)
	}
	working, err := c.Tasks(ctx, "working")
	if err != nil || len(working) != 1 || working[0].Status != "working" {
		t.Fatalf("working=%+v err=%v", working, err)
	}
	goals, err := c.Goals(ctx, "")
	if err != nil || len(goals) != 2 {
		t.Fatalf("goals=%d err=%v", len(goals), err)
	}
	jobs, err := c.Jobs(ctx, true)
	if err != nil || len(jobs) != 3 {
		t.Fatalf("jobs=%d err=%v", len(jobs), err)
	}
	if _, err := c.CreateTask(ctx, "tuios live task"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateGoal(ctx, "tuios live goal"); err != nil {
		t.Fatal(err)
	}
	var requestErr *RequestError
	if os.Getenv("PIO_LIVE_JOB") != "1" {
		set, err := c.SetHarness(ctx, "claude-code", "")
		if err != nil || set.Previous.Name != "codex" {
			t.Fatalf("set=%+v err=%v", set, err)
		}
		if _, err := c.SetHarness(ctx, "codex", ""); err != nil {
			t.Fatal(err)
		}
		_, err = c.SetHarness(ctx, "agent-zero", "")
		if !errors.As(err, &requestErr) || requestErr.Code != "internal" {
			t.Fatalf("agent-zero: %v", err)
		}
		slow := make(chan struct {
			result ChatResult
			err    error
		}, 1)
		go func() {
			result, err := c.Send(context.Background(), created.ID, "slow please", nil)
			slow <- struct {
				result ChatResult
				err    error
			}{result, err}
		}()
		time.Sleep(150 * time.Millisecond)
		_, err = c.SetHarness(ctx, "claude-code", "")
		if !errors.As(err, &requestErr) || requestErr.Code != "busy" {
			t.Fatalf("busy by turn: %v", err)
		}
		terminal := <-slow
		if terminal.err != nil || !terminal.result.OK || terminal.result.Text != "echo: slow please" {
			t.Fatalf("slow terminal=%+v", terminal)
		}
	}
	if os.Getenv("PIO_LIVE_JOB") == "1" {
		t.Run("busy by job", func(t *testing.T) {
			_, err := c.SetHarness(ctx, "claude-code", "")
			var requestErr *RequestError
			if !errors.As(err, &requestErr) || requestErr.Code != "busy" || requestErr.Message != "Cannot change harness: 0 live turns, 1 live job" {
				t.Fatalf("busy by job: %v", err)
			}
		})
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
	requestErr = nil
	if !errors.As(err, &requestErr) || requestErr.Code != "not_found" || events != 0 {
		t.Fatalf("missing session: err=%v events=%d", err, events)
	}
	started := time.Now()
	_, err = New(filepath.Join(t.TempDir(), "unreachable")).Status(context.Background())
	if !errors.Is(err, ErrUnreachable) || time.Since(started) >= 3*time.Second {
		t.Fatalf("unreachable: err=%v elapsed=%s", err, time.Since(started))
	}
}
