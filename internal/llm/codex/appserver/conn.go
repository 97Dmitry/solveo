// Package appserver runs one Codex app-server process over stdio JSON-RPC and
// routes its notifications to the turns waiting for them.
package appserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MaxLine bounds one protocol message; the app-server sends no images back.
	MaxLine = 8 << 20
	// MaxText bounds the accumulated text of one answer item.
	MaxText = 1 << 20

	DefaultStartTimeout = 20 * time.Second
	DefaultCallTimeout  = 30 * time.Second
	DefaultCloseTimeout = 5 * time.Second
)

// interruptTimeout bounds the wait for turn/interrupt confirmation; tests shorten it.
var interruptTimeout = 10 * time.Second

var ErrClosed = errors.New("соединение с Codex app-server закрыто")

// RPCError is a JSON-RPC error returned for one request.
type RPCError struct {
	Method  string
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("Codex app-server %s: %s (код %d)", e.Method, e.Message, e.Code)
}

type Options struct {
	Binary string
	Args   []string
	Dir    string
	Env    []string
	// Configure applies platform subprocess settings, including process-tree cancellation.
	Configure func(*exec.Cmd)
	// Notify receives notifications without a thread, such as account/login/completed.
	// It runs on the reader goroutine and must not block.
	Notify func(method string, params json.RawMessage)
	// ClientName/ClientVersion identify Solveo in initialize.
	ClientName, ClientVersion       string
	StartTimeout, CallTimeout, Wait time.Duration
}

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Conn owns one app-server process: a single stdout reader, serialized stdin
// writes and a table of pending requests. It must not be copied.
//
// A process that stops reading stdin blocks a pipe write forever, and pipes on
// Windows have no write deadlines. Writes therefore run in their own goroutine
// and hold the wsem slot until Write returns; a caller whose context expires
// kills the process, which unblocks the write.
type Conn struct {
	opts   Options
	cmd    *exec.Cmd
	errs   *errorLine
	stdin  io.WriteCloser
	kill   context.CancelFunc
	exited chan struct{}

	wsem chan struct{}

	mu      sync.Mutex
	next    int64
	pending map[int64]chan message
	threads map[string]*subscription
	err     error
	done    chan struct{}
}

// Start launches the process and completes initialize/initialized within StartTimeout.
func Start(ctx context.Context, o Options) (*Conn, error) {
	if o.StartTimeout <= 0 {
		o.StartTimeout = DefaultStartTimeout
	}
	if o.CallTimeout <= 0 {
		o.CallTimeout = DefaultCallTimeout
	}
	if o.Wait <= 0 {
		o.Wait = DefaultCloseTimeout
	}
	// The process outlives the caller's request context; only Close or a fatal error stops it.
	procCtx, kill := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, o.Binary, append([]string{"app-server"}, o.Args...)...)
	cmd.Dir = o.Dir
	cmd.Env = o.Env
	cmd.WaitDelay = 2 * time.Second
	if o.Configure != nil {
		o.Configure(cmd)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		kill()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		kill()
		return nil, err
	}
	// Raw stderr may contain account or local data: only its last "Error:" line is kept.
	errs := &errorLine{}
	cmd.Stderr = errs
	if err = cmd.Start(); err != nil {
		kill()
		return nil, fmt.Errorf("не удалось запустить Codex app-server: %w", err)
	}
	c := &Conn{opts: o, cmd: cmd, errs: errs, stdin: stdin, kill: kill, exited: make(chan struct{}), wsem: make(chan struct{}, 1), pending: map[int64]chan message{}, threads: map[string]*subscription{}, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(c.exited)
	}()
	go c.read(stdout)

	initCtx, cancel := context.WithTimeout(ctx, o.StartTimeout)
	defer cancel()
	name, version := o.ClientName, o.ClientVersion
	if name == "" {
		name, version = "solveo", "0.1.0"
	}
	var init json.RawMessage
	err = c.Call(initCtx, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": name, "title": "Solveo", "version": version},
		"capabilities": map[string]any{"experimentalApi": true},
	}, &init)
	if err == nil {
		err = c.write(initCtx, map[string]any{"method": "initialized", "params": map[string]any{}})
	}
	if err != nil {
		c.Kill()
		return nil, fmt.Errorf("Codex app-server не завершил подключение: %w", c.withReason(err))
	}
	return c, nil
}

// errorLine keeps only the last stderr line starting with "Error:", bounded,
// as the reason a process failed; everything else is dropped.
type errorLine struct {
	mu      sync.Mutex
	partial []byte
	last    string
}

func (e *errorLine) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	data := append(e.partial, p...)
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		e.take(data[:i])
		data = data[i+1:]
	}
	if len(data) > 4096 {
		data = data[len(data)-4096:]
	}
	e.partial = append([]byte(nil), data...)
	return len(p), nil
}

func (e *errorLine) take(line []byte) {
	text := strings.TrimSpace(string(line))
	if !strings.HasPrefix(text, "Error:") {
		return
	}
	if r := []rune(text); len(r) > 400 {
		text = string(r[:400]) + "…"
	}
	e.last = text
}

func (e *errorLine) reason() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.last == "" && len(e.partial) > 0 {
		e.take(e.partial)
	}
	return e.last
}

// withReason adds Codex's own error line once the process has exited.
func (c *Conn) withReason(err error) error {
	select {
	case <-c.exited:
	case <-time.After(time.Second):
	}
	if reason := c.errs.reason(); reason != "" {
		return fmt.Errorf("%w (%s)", err, reason)
	}
	return err
}

// Done is closed once the connection is no longer usable.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err reports why the connection stopped, or nil while it is usable.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) fail(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	pending := c.pending
	c.pending = map[int64]chan message{}
	subs := make([]*subscription, 0, len(c.threads))
	for _, s := range c.threads {
		subs = append(subs, s)
	}
	c.mu.Unlock()
	close(c.done)
	for _, ch := range pending {
		close(ch)
	}
	for _, s := range subs {
		s.wake()
	}
}

func (c *Conn) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), MaxLine)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			// A lost completion would leave turns waiting forever; stop instead.
			c.fail(errors.New("Codex app-server прислал повреждённое сообщение"))
			c.kill()
			return
		}
		switch {
		case len(m.ID) > 0 && m.Method != "":
			// Approvals and other server requests are not supported: Solveo runs
			// read-only with approval "never", so refuse instead of hanging the turn.
			c.refuse(m.ID, m.Method)
		case len(m.ID) > 0:
			id, err := strconv.ParseInt(string(m.ID), 10, 64)
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		default:
			c.route(m.Method, m.Params)
		}
	}
	err := sc.Err()
	switch {
	case errors.Is(err, bufio.ErrTooLong):
		err = errors.New("сообщение Codex app-server больше 8 МиБ")
		c.kill()
	case err == nil:
		err = c.withReason(ErrClosed)
	}
	c.fail(err)
}

func (c *Conn) route(method string, params json.RawMessage) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(params, &p)
	if p.ThreadID == "" {
		if c.opts.Notify != nil {
			c.opts.Notify(method, params)
		}
		return
	}
	c.mu.Lock()
	s := c.threads[p.ThreadID]
	c.mu.Unlock()
	if s != nil {
		s.handle(method, params)
	}
}

// refuse answers a server request without blocking the reader on stdin.
func (c *Conn) refuse(id json.RawMessage, method string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), c.opts.CallTimeout)
		defer cancel()
		_ = c.write(ctx, map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": "Solveo does not support " + method}})
	}()
}

// write sends one message, bounded by ctx. A write that does not finish in time
// may have left a partial line or a stuck pipe, so the connection is killed.
func (c *Conn) write(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case c.wsem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
	result := make(chan error, 1)
	go func() {
		_, err := c.stdin.Write(append(b, '\n'))
		<-c.wsem
		result <- err
	}()
	select {
	case err = <-result:
		if err != nil {
			c.fail(ErrClosed)
			return ErrClosed
		}
		return nil
	case <-ctx.Done():
		c.Kill()
		return fmt.Errorf("Codex app-server не принимает данные: %w", ctx.Err())
	}
}

// Call sends one request and decodes its result into out. Without a deadline in
// ctx it is bounded by CallTimeout.
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.opts.CallTimeout)
		defer cancel()
	}
	ch := make(chan message, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return c.Err()
		}
		if m.Error != nil {
			return &RPCError{Method: method, Code: m.Error.Code, Message: m.Error.Message}
		}
		if out != nil {
			if err := json.Unmarshal(m.Result, out); err != nil {
				return fmt.Errorf("некорректный ответ Codex app-server на %s: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Close closes stdin and waits for the process; after the grace period it
// terminates the process tree.
func (c *Conn) Close(ctx context.Context) error {
	c.fail(ErrClosed)
	timer := time.NewTimer(c.opts.Wait)
	defer timer.Stop()
	// Never wait behind a stuck write: killing the process unblocks it, and
	// exec.Cmd closes the pipe after the process exits.
	select {
	case c.wsem <- struct{}{}:
		_ = c.stdin.Close()
		<-c.wsem
		select {
		case <-c.exited:
			return nil
		case <-timer.C:
		case <-ctx.Done():
		}
	case <-timer.C:
	case <-ctx.Done():
	}
	c.kill()
	select {
	case <-c.exited:
		return nil
	case <-time.After(c.opts.Wait):
		return errors.New("процесс Codex app-server не завершился")
	}
}

// Kill terminates the process tree immediately and fails all waiting requests.
func (c *Conn) Kill() {
	c.fail(ErrClosed)
	c.kill()
}
