// Package pioctl speaks pio's newline-delimited control socket protocol.
package pioctl

import (
	"bufio"
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
type Config struct {
	Harness struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"harness"`
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
	s := bufio.NewScanner(conn)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
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
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return nil, fmt.Errorf("%w: missing response", ErrUnreachable)
}

func (c *Client) Ping(ctx context.Context) error { _, err := c.call(ctx, "ping", nil, nil); return err }
func (c *Client) Status(ctx context.Context) (json.RawMessage, error) {
	return c.call(ctx, "status", nil, nil)
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
