// Package pioctl speaks pio's newline-delimited control socket protocol.
package pioctl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrUnreachable = errors.New("pio unreachable")

type RequestError struct{ Code, Message string }

func (e *RequestError) Error() string { return e.Code + ": " + e.Message }

type Client struct {
	Socket  string
	Timeout time.Duration
}

func New(workspace string) *Client {
	if workspace == "" {
		workspace = os.Getenv("PIO_ROOT")
	}
	if workspace == "" {
		cwd, _ := os.Getwd()
		workspace = filepath.Join(cwd, ".pio")
	}
	return &Client{Socket: filepath.Join(workspace, "runtime", "control.sock"), Timeout: 2 * time.Second}
}

type Session struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Title  string `json:"title"`
	Active bool   `json:"active"`
}
type Task struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	UpdatedAt      time.Time `json:"updated_at"`
	Attempt        int       `json:"attempt"`
	ReviewRequired bool      `json:"review_required"`
	WaitingFor     string    `json:"waiting_for"`
	Body           string    `json:"body"`
}

// GoalCriterion describes one condition required to complete a goal.
type GoalCriterion struct {
	ID               string `json:"id"`
	Condition        string `json:"condition"`
	EvidenceRequired string `json:"evidence_required"`
}

type Goal struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Status          string          `json:"status"`
	UpdatedAt       time.Time       `json:"updated_at"`
	SuccessCriteria []GoalCriterion `json:"success_criteria"`
	ReviewTrigger   string          `json:"review_trigger"`
	Round           int             `json:"round"`
	RoundTaskIDs    []string        `json:"round_task_ids"`
	Body            string          `json:"body"`
}

func (g *Goal) UnmarshalJSON(data []byte) error {
	type goal Goal
	var wire struct {
		*goal
		SuccessCriteria json.RawMessage `json:"success_criteria"`
	}
	wire.goal = (*goal)(g)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	criteria := bytes.TrimSpace(wire.SuccessCriteria)
	if len(criteria) == 0 || bytes.Equal(criteria, []byte("null")) {
		g.SuccessCriteria = nil
		return nil
	}
	if criteria[0] == '"' {
		var legacy string
		if err := json.Unmarshal(criteria, &legacy); err != nil {
			return err
		}
		if legacy == "" {
			g.SuccessCriteria = nil
		} else {
			g.SuccessCriteria = []GoalCriterion{{Condition: legacy}}
		}
		return nil
	}
	return json.Unmarshal(criteria, &g.SuccessCriteria)
}

type Job struct {
	Number    int        `json:"number"`
	Kind      string     `json:"kind"`
	TaskID    string     `json:"taskId"`
	State     string     `json:"state"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt"`
	Output    []string   `json:"output"`
	Error     string     `json:"error"`
}
type HarnessResult struct {
	Name     string `json:"name"`
	Model    string `json:"model"`
	Previous struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"previous"`
}
type Config struct {
	Harness struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"harness"`
}
type AutoState struct {
	Auto         string `json:"auto"`
	Orchestrator string `json:"orchestrator"`
}
type ChatResult struct {
	OK    bool   `json:"ok"`
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}
type Entry struct {
	At    time.Time `json:"at"`
	Role  string    `json:"role"`
	Text  string    `json:"text"`
	Error string    `json:"error"`
}
type response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *RequestError   `json:"error"`
	Done   bool            `json:"done"`
	Event  json.RawMessage `json:"event"`
}

func (c *Client) call(ctx context.Context, verb string, params any, event func(json.RawMessage)) (json.RawMessage, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer conn.Close()
	request := map[string]any{"id": 1, "verb": verb}
	if params != nil {
		request["params"] = params
	}
	b, _ := json.Marshal(request)
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if verb == "chat.send" {
		// A model turn is quiet longer than any request timeout, and pio always sends a terminal line.
		_ = conn.SetDeadline(time.Time{})
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
	}
	s := bufio.NewScanner(conn)
	for {
		if verb != "chat.send" {
			_ = conn.SetReadDeadline(time.Now().Add(timeout))
		}
		if !s.Scan() {
			break
		}
		var r response
		if err := json.Unmarshal(s.Bytes(), &r); err != nil {
			return nil, err
		}
		if len(r.Event) != 0 {
			if event != nil {
				event(r.Event)
			}
			continue
		}
		if !r.OK {
			if r.Error == nil {
				return nil, &RequestError{Code: "internal", Message: "rejected request"}
			}
			return nil, r.Error
		}
		if verb != "chat.send" || r.Done {
			return r.Result, nil
		}
	}
	if verb == "chat.send" && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return nil, fmt.Errorf("%w: missing response", ErrUnreachable)
}

func (c *Client) Ping(ctx context.Context) error { _, err := c.call(ctx, "ping", nil, nil); return err }
func (c *Client) Status(ctx context.Context) (json.RawMessage, error) {
	return c.call(ctx, "status", nil, nil)
}
func (c *Client) Auto(ctx context.Context) (AutoState, error) {
	b, err := c.Status(ctx)
	var out AutoState
	return out, decode(b, &out, err)
}
func (c *Client) SetAuto(ctx context.Context, enabled bool) (AutoState, error) {
	b, err := c.call(ctx, "auto.set", map[string]bool{"enabled": enabled}, nil)
	var out AutoState
	return out, decode(b, &out, err)
}
func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	b, err := c.call(ctx, "sessions.list", nil, nil)
	var out struct {
		Active   string    `json:"active"`
		Sessions []Session `json:"sessions"`
	}
	if err := decode(b, &out, err); err != nil {
		return nil, err
	}
	for i := range out.Sessions {
		out.Sessions[i].Active = out.Sessions[i].ID == out.Active
	}
	return out.Sessions, nil
}
func (c *Client) Create(ctx context.Context, name string) (Session, error) {
	b, err := c.call(ctx, "sessions.create", map[string]string{"name": name}, nil)
	var out Session
	return out, decode(b, &out, err)
}
func (c *Client) Switch(ctx context.Context, id string) error {
	_, err := c.call(ctx, "sessions.switch", map[string]string{"id": id}, nil)
	return err
}
func (c *Client) GetConfig(ctx context.Context) (Config, error) {
	b, err := c.call(ctx, "config.get", nil, nil)
	var out Config
	return out, decode(b, &out, err)
}
func (c *Client) Tasks(ctx context.Context, status string) ([]Task, error) {
	var params any
	if status != "" {
		params = map[string]string{"status": status}
	}
	b, err := c.call(ctx, "tasks.list", params, nil)
	var out []Task
	return out, decode(b, &out, err)
}
func (c *Client) Goals(ctx context.Context, status string) ([]Goal, error) {
	var params any
	if status != "" {
		params = map[string]string{"status": status}
	}
	b, err := c.call(ctx, "goals.list", params, nil)
	var out []Goal
	return out, decode(b, &out, err)
}
func (c *Client) Jobs(ctx context.Context, recent bool) ([]Job, error) {
	var params any
	if recent {
		params = map[string]bool{"recent": true}
	}
	b, err := c.call(ctx, "jobs.list", params, nil)
	var out []Job
	return out, decode(b, &out, err)
}
func (c *Client) CreateTask(ctx context.Context, title string) (Task, error) {
	b, err := c.call(ctx, "tasks.create", map[string]string{"title": title}, nil)
	var out Task
	return out, decode(b, &out, err)
}
func (c *Client) CreateGoal(ctx context.Context, title string) (Goal, error) {
	b, err := c.call(ctx, "goals.create", map[string]string{"title": title}, nil)
	var out Goal
	return out, decode(b, &out, err)
}
func (c *Client) SetHarness(ctx context.Context, name, model string) (HarnessResult, error) {
	params := map[string]string{"name": name}
	if model != "" {
		params["model"] = model
	}
	b, err := c.call(ctx, "harness.set", params, nil)
	var out HarnessResult
	return out, decode(b, &out, err)
}
func (c *Client) History(ctx context.Context, id string, limit int) (entries []Entry, skipped int, err error) {
	params := map[string]any{"limit": limit}
	if id != "" {
		params["id"] = id
	}
	b, err := c.call(ctx, "sessions.history", params, nil)
	var out struct {
		Entries []Entry `json:"entries"`
		Skipped int     `json:"skipped"`
	}
	if err := decode(b, &out, err); err != nil {
		return nil, 0, err
	}
	return out.Entries, out.Skipped, nil
}
func (c *Client) Send(ctx context.Context, session, text string, event func(json.RawMessage)) (ChatResult, error) {
	b, err := c.call(ctx, "chat.send", map[string]string{"session": session, "text": text}, event)
	var out ChatResult
	if err := decode(b, &out, err); err != nil {
		return out, err
	}
	if !out.OK {
		code := "internal"
		if strings.HasPrefix(out.Error, "Unknown chat session:") {
			code = "not_found"
		}
		return out, &RequestError{Code: code, Message: out.Error}
	}
	return out, nil
}
func decode[T any](b []byte, out *T, err error) error {
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
