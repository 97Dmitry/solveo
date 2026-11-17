package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/llm/codex/appserver"
)

// isolationArgs keep the app-server independent of the user's Codex profile.
// A separate CODEX_HOME removes user config, MCP, AGENTS.md and sessions, but
// after login the account syncs remote plugins whose skills and MCP reach the
// prompt; those features are disabled here. Built-in Codex instructions and the
// machine config layer cannot be disabled.
func isolationArgs() []string {
	args := []string{
		"-c", `model_provider="openai"`,
		"-c", "project_doc_max_bytes=0",
		"-c", "include_apps_instructions=false",
		"-c", `cli_auth_credentials_store="keyring"`,
	}
	for _, f := range []string{"plugins", "remote_plugin", "apps", "tool_suggest", "recommended_plugins", "image_generation", "browser_use", "computer_use", "in_app_browser", "goals", "daemon_auto_start"} {
		args = append(args, "--disable", f)
	}
	return args
}

// Analysis restrictions belong to the process: a thread/start config entry named
// "features" replaces the whole table, undoing unrelated startup disables.
func (a *appServer) isolationArgs() []string {
	args := isolationArgs()
	if !a.analysis {
		return args
	}
	for _, f := range []string{"shell_tool", "unified_exec", "view_image", "multi_agent", "sleep_tool", "code_mode", "code_mode_host", "code_mode_only", "request_permissions_tool", "token_budget", "deferred_executor", "send_message_to_user_async", "current_time_reminder", "hooks", "workspace_dependencies", "skill_search"} {
		args = append(args, "--disable", f)
	}
	return append(args, "-c", `web_search="disabled"`, "-c", "tools.view_image=false", "-c", "tools.update_plan.enabled=false", "-c", "tools.experimental_request_user_input.enabled=false")
}

// appServer is the single app-server connection shared by a Client and all its
// copies. Its mutex and connection are reached only through this pointer.
type appServer struct {
	binary, home, work string
	analysis           bool
	start              func(context.Context, appserver.Options) (*appserver.Conn, error)
	// log receives content-free process lifecycle records; nil discards them.
	log *slog.Logger

	mu                sync.Mutex
	conn              *appserver.Conn
	starting          chan struct{}
	closed            bool
	prepared          *preparedThread
	lastSubmittedChat string
	foreground        int
	preparationGate   chan struct{}
	loaded            map[string]bool // threads loaded in the current process
	login             *pendingLogin
	// unclaimed keeps login completions that arrived before StartLogin
	// registered their login ID ("" for a completion without an ID).
	unclaimed map[string]error
	// fresh reports a process start to the first request after it, even when
	// Prepare or an account check started the process.
	fresh *startInfo
}

type pendingLogin struct {
	id   string
	once sync.Once
	done func(error)
}

func (p *pendingLogin) finish(err error) {
	p.once.Do(func() {
		if p.done != nil {
			p.done(err)
		}
	})
}

type startInfo struct {
	cold          bool
	launched, rdy time.Duration
}

// connection returns the ready connection, starting the process once if needed.
// Concurrent callers wait for the same start; a failed or lost connection is
// started again on the next call, never resubmitting earlier work.
func (a *appServer) connection(ctx context.Context) (*appserver.Conn, error) {
	for {
		a.mu.Lock()
		if a.closed {
			a.mu.Unlock()
			return nil, errors.New("приложение завершается")
		}
		if a.conn != nil {
			select {
			case <-a.conn.Done():
				a.conn = nil
			default:
				conn := a.conn
				a.mu.Unlock()
				return conn, nil
			}
		}
		if wait := a.starting; wait != nil {
			a.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		wait := make(chan struct{})
		a.starting = wait
		a.mu.Unlock()

		conn, info, err := a.launch(ctx)
		a.mu.Lock()
		a.starting = nil
		close(wait)
		if err == nil && a.closed {
			a.mu.Unlock()
			conn.Kill()
			return nil, errors.New("приложение завершается")
		}
		if err == nil {
			a.conn = conn
			a.loaded = map[string]bool{}
			a.fresh = &info
			go a.watch(conn)
		}
		a.mu.Unlock()
		return conn, err
	}
}

func (a *appServer) launch(ctx context.Context) (*appserver.Conn, startInfo, error) {
	began := time.Now()
	conn, info, err := a.launchProcess(ctx, began)
	if err != nil {
		a.logger().Error("codex app-server failed to start", "purpose", a.purpose(), "error", err, "duration", time.Since(began).Round(time.Millisecond).String())
	} else {
		a.logger().Info("codex app-server started", "purpose", a.purpose(), "launch", info.launched.Round(time.Millisecond).String(), "ready", info.rdy.Round(time.Millisecond).String())
	}
	return conn, info, err
}

func (a *appServer) purpose() string {
	if a.analysis {
		return "analysis"
	}
	return "main"
}

func (a *appServer) logger() *slog.Logger {
	if a.log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return a.log
}

func (a *appServer) launchProcess(ctx context.Context, began time.Time) (*appserver.Conn, startInfo, error) {
	// Codex keeps SQLite state in its home and cannot open it on a network path.
	if err := localDisk(a.home); err != nil {
		return nil, startInfo{}, err
	}
	for _, dir := range []string{a.home, a.work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, startInfo{}, fmt.Errorf("папка подключения Codex: %w", err)
		}
	}
	conn, err := a.start(ctx, appserver.Options{
		Binary:    a.binary,
		Args:      a.isolationArgs(),
		Dir:       a.work,
		Env:       append(os.Environ(), "CODEX_HOME="+a.home),
		Configure: configureSubprocess,
		Notify:    a.notify,
	})
	if err != nil {
		return nil, startInfo{}, err
	}
	launched := time.Since(began)
	if err = a.isolate(ctx, conn); err != nil {
		conn.Kill()
		return nil, startInfo{}, err
	}
	// The first account read of a process takes seconds; doing it here moves
	// that cost into background preparation instead of the first answer.
	if err = conn.Call(ctx, "account/read", map[string]any{"refreshToken": false}, nil); err != nil {
		conn.Kill()
		return nil, startInfo{}, fmt.Errorf("не удалось проверить вход в Codex: %w", err)
	}
	return conn, startInfo{cold: true, launched: launched, rdy: time.Since(began)}, nil
}

// isolate disables every discovered skill and verifies the result. skills/list
// does not show plugin skills; plugins are disabled by isolationArgs instead.
func (a *appServer) isolate(ctx context.Context, conn *appserver.Conn) error {
	type listing struct {
		Data []struct {
			Skills []struct {
				Name    string `json:"name"`
				Path    string `json:"path"`
				Enabled bool   `json:"enabled"`
			} `json:"skills"`
		} `json:"data"`
	}
	list := func() (listing, error) {
		var l listing
		err := conn.Call(ctx, "skills/list", map[string]any{"cwds": []string{a.work}, "forceReload": true}, &l)
		return l, err
	}
	l, err := list()
	if err != nil {
		return fmt.Errorf("не удалось проверить skills Codex: %w", err)
	}
	changed := false
	for _, entry := range l.Data {
		for _, s := range entry.Skills {
			if s.Enabled {
				if err = conn.Call(ctx, "skills/config/write", map[string]any{"path": s.Path, "enabled": false}, nil); err != nil {
					return fmt.Errorf("не удалось отключить skill Codex %q: %w", s.Name, err)
				}
				changed = true
			}
		}
	}
	if changed {
		if l, err = list(); err != nil {
			return fmt.Errorf("не удалось проверить skills Codex: %w", err)
		}
	}
	for _, entry := range l.Data {
		for _, s := range entry.Skills {
			if s.Enabled {
				return fmt.Errorf("skill Codex %q не отключился; подключение не изолировано", s.Name)
			}
		}
	}
	var plugins struct {
		Marketplaces []struct {
			Plugins []json.RawMessage `json:"plugins"`
		} `json:"marketplaces"`
	}
	if err = conn.Call(ctx, "plugin/installed", map[string]any{}, &plugins); err != nil {
		return fmt.Errorf("не удалось проверить плагины Codex: %w", err)
	}
	for _, m := range plugins.Marketplaces {
		if len(m.Plugins) > 0 {
			return errors.New("в профиле Codex Solveo доступны плагины; подключение не изолировано")
		}
	}
	return nil
}

// watch forgets a lost connection and fails a pending login bound to it.
func (a *appServer) watch(conn *appserver.Conn) {
	<-conn.Done()
	// Close and Kill fail the connection with ErrClosed itself; an exit adds its reason.
	if err := conn.Err(); err == appserver.ErrClosed { //nolint:errorlint // identity marks an intentional close
		a.logger().Info("codex app-server closed", "purpose", a.purpose())
	} else {
		a.logger().Error("codex app-server stopped", "purpose", a.purpose(), "error", err)
	}
	a.mu.Lock()
	login := a.login
	if a.conn == conn {
		a.conn = nil
		a.login = nil
	} else {
		login = nil
	}
	a.mu.Unlock()
	if login != nil {
		login.finish(errors.New("соединение с Codex прервалось до завершения входа"))
	}
}

func (a *appServer) notify(method string, params json.RawMessage) {
	if method != "account/login/completed" {
		return
	}
	var p struct {
		LoginID *string `json:"loginId"`
		Success bool    `json:"success"`
		Error   *string `json:"error"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	var result error
	if !p.Success {
		msg := "вход в Codex не выполнен"
		if p.Error != nil && *p.Error != "" {
			msg += ": " + *p.Error
		}
		result = errors.New(msg)
	}
	id := ""
	if p.LoginID != nil {
		id = *p.LoginID
	}
	a.mu.Lock()
	login := a.login
	if p.Success && a.prepared != nil {
		a.prepared.cancel()
	}
	if login == nil || (id != "" && id != login.id) {
		// The completion can arrive before StartLogin registers the login.
		if a.unclaimed == nil || len(a.unclaimed) >= 8 {
			a.unclaimed = map[string]error{}
		}
		a.unclaimed[id] = result
		a.mu.Unlock()
		return
	}
	a.login = nil
	a.mu.Unlock()
	login.finish(result)
}

func (a *appServer) markLoaded(conn *appserver.Conn, thread string) {
	a.mu.Lock()
	if a.conn == conn {
		a.loaded[thread] = true
	}
	a.mu.Unlock()
}

func (a *appServer) isLoaded(conn *appserver.Conn, thread string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn == conn && a.loaded[thread]
}

func (a *appServer) close(ctx context.Context) error {
	a.mu.Lock()
	a.closed = true
	if a.prepared != nil {
		a.prepared.cancel()
	}
	conn := a.conn
	login := a.login
	a.conn, a.login = nil, nil
	a.mu.Unlock()
	if login != nil {
		login.finish(context.Canceled)
	}
	if conn == nil {
		return nil
	}
	return conn.Close(ctx)
}

func (c *Client) Account(ctx context.Context) (llm.Account, error) {
	conn, err := c.app.connection(ctx)
	if err != nil {
		return llm.Account{}, err
	}
	var r struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err = conn.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &r); err != nil {
		return llm.Account{}, err
	}
	if r.Account == nil {
		return llm.Account{}, nil
	}
	// Only the account kind leaves the adapter; e-mail and plan stay inside Codex.
	return llm.Account{SignedIn: true, Kind: r.Account.Type}, nil
}

func (c *Client) StartLogin(ctx context.Context, done func(error)) (llm.LoginPrompt, error) {
	conn, err := c.app.connection(ctx)
	if err != nil {
		return llm.LoginPrompt{}, err
	}
	var r struct {
		Type            string `json:"type"`
		LoginID         string `json:"loginId"`
		VerificationURL string `json:"verificationUrl"`
		UserCode        string `json:"userCode"`
	}
	if err = conn.Call(ctx, "account/login/start", map[string]any{"type": "chatgptDeviceCode"}, &r); err != nil {
		return llm.LoginPrompt{}, err
	}
	if r.Type != "chatgptDeviceCode" || r.LoginID == "" || r.UserCode == "" || !strings.HasPrefix(r.VerificationURL, "https://") {
		return llm.LoginPrompt{}, errors.New("Codex вернул некорректные данные для входа")
	}
	next := &pendingLogin{id: r.LoginID, done: func(err error) {
		if err == nil {
			c.analysis.resetConnection()
		}
		if done != nil {
			done(err)
		}
	}}
	c.app.mu.Lock()
	previous := c.app.login
	result, finished := c.app.unclaimed[r.LoginID]
	if !finished {
		result, finished = c.app.unclaimed[""]
	}
	c.app.unclaimed = nil
	if finished {
		c.app.login = nil
	} else {
		c.app.login = next
	}
	c.app.mu.Unlock()
	if previous != nil {
		previous.finish(context.Canceled)
	}
	if finished {
		// done may therefore run before StartLogin returns; see llm.Connection.
		next.finish(result)
	}
	return llm.LoginPrompt{ID: r.LoginID, URL: r.VerificationURL, Code: r.UserCode}, nil
}

func (c *Client) CancelLogin(ctx context.Context, id string) error {
	c.app.mu.Lock()
	login := c.app.login
	conn := c.app.conn
	if login == nil || login.id != id {
		c.app.mu.Unlock()
		return nil
	}
	c.app.login = nil
	c.app.mu.Unlock()
	login.finish(context.Canceled)
	if conn == nil {
		return nil
	}
	return conn.Call(ctx, "account/login/cancel", map[string]any{"loginId": id}, nil)
}

// Prepare starts the connection in the background without any model turn.
func (c *Client) Prepare(ctx context.Context) error {
	_, err := c.app.connection(ctx)
	return err
}

func (c *Client) Close(ctx context.Context) error {
	return errors.Join(c.app.close(ctx), c.analysis.close(ctx))
}

func threadParams(cwd string, cfg chat.Config) map[string]any {
	p := map[string]any{
		"cwd":            cwd,
		"modelProvider":  "openai",
		"sandbox":        "read-only",
		"approvalPolicy": "never",
	}
	if cfg.Model != "" {
		p["model"] = cfg.Model
	}
	if cfg.Reasoning != "" {
		// Matching the thread's effort lets Codex reuse its connection warm-up.
		p["config"] = map[string]any{"model_reasoning_effort": cfg.Reasoning}
	}
	return p
}

// turnSpec describes one request. An ephemeral one-off session neither
// continues nor returns chat state.
type turnSpec struct {
	chatID           string
	instructions     string
	instructionsHash string
	state            sessionState
	purpose          string
	config           chat.Config
	threadID         string
	images           []string
	prompt           string
	ephemeral        bool
	timeout          time.Duration
	analysis         bool
	outputSchema     json.RawMessage
}

// run sends one request through the persistent connection.
func (c *Client) run(ctx context.Context, spec turnSpec, emit func(llm.Event)) (llm.Result, error) {
	// Reserve foreground ownership before a late preparation can publish a slot.
	c.app.mu.Lock()
	c.app.foreground++
	c.app.mu.Unlock()
	defer func() { c.app.mu.Lock(); c.app.foreground--; c.app.mu.Unlock() }()
	timeout, threadID, ephemeral := spec.timeout, spec.threadID, spec.ephemeral
	applied, pending := spec.state.InstructionsHash, spec.state.PendingInstructionsHash
	newThread := threadID == ""
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	progress := func(s string) {
		if emit != nil {
			emit(llm.Event{Kind: llm.EventProgress, Text: s})
		}
	}
	keep := func() llm.Result {
		if threadID == "" || ephemeral {
			return llm.Result{}
		}
		state, _ := json.Marshal(sessionState{Version: 2, Transport: transportAppServer, ThreadID: threadID, InstructionsHash: applied, PendingInstructionsHash: pending})
		return llm.Result{State: state}
	}

	conn, err := c.app.connection(ctx)
	if err != nil {
		return keep(), fmt.Errorf("подключение Codex недоступно: %w", err)
	}
	var preparedID string
	key := preparationKey{}
	if newThread && !ephemeral && spec.chatID != "" {
		key = preparationKey{chatID: spec.chatID, instructions: spec.instructionsHash, model: spec.config.Model, reasoning: spec.config.Reasoning}
	}
	if spec.analysis {
		key = analysisKey(spec.config)
	}
	preparedID, err = c.app.takePrepared(ctx, conn, key)
	if err != nil {
		return keep(), err
	}
	// A stale cleanup may have retired this process. No turn has been sent yet.
	if conn.Err() != nil {
		preparedID = ""
		conn, err = c.app.connection(ctx)
		if err != nil {
			return keep(), fmt.Errorf("подключение Codex недоступно: %w", err)
		}
	}
	// Ownership transfers at claim, including early cancellation/account errors.
	// The already archived question can retain this exact thread and pending hash.
	// An ephemeral (analysis) claim is released after its single turn below.
	if preparedID != "" && !ephemeral {
		threadID = preparedID
		pending = spec.instructionsHash
		c.app.markLoaded(conn, threadID)
	}
	var info startInfo
	c.app.mu.Lock()
	if c.app.fresh != nil && c.app.conn == conn {
		info, c.app.fresh = *c.app.fresh, nil
	}
	c.app.mu.Unlock()
	accountStart := time.Now()
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err = conn.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
		return keep(), fmt.Errorf("не удалось проверить вход в Codex: %w", err)
	}
	if account.Account == nil {
		return keep(), errors.New("войдите в Codex в настройках подключения: Solveo использует собственный вход")
	}
	accountReady := time.Since(accountStart)

	threadStart := time.Now()
	preparedThreadUsed := preparedID != ""
	params := threadParams(c.app.work, spec.config)
	if spec.analysis {
		params = c.app.analysisThreadParams(spec.config)
	}

	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	switch {
	case threadID != "" && c.app.isLoaded(conn, threadID):
	case threadID != "":
		params["threadId"] = threadID
		params["excludeTurns"] = true
		if err = conn.Call(ctx, "thread/resume", params, &thread); err != nil {
			var rpc *appserver.RPCError
			if errors.As(err, &rpc) && strings.Contains(rpc.Message, "no rollout found") {
				return keep(), errors.New("сессия Codex этого чата не найдена; продолжить её нельзя, начните новый чат")
			}
			return keep(), fmt.Errorf("не удалось продолжить сессию Codex: %w", err)
		}
		if thread.Thread.ID != threadID {
			return keep(), fmt.Errorf("%w: Codex продолжил другую сессию, ответ отброшен", llm.ErrOutcomeUnknown)
		}
		c.app.markLoaded(conn, threadID)
	default:
		threadID = preparedID
		preparedThreadUsed = threadID != ""
		if threadID == "" {
			params["ephemeral"] = ephemeral
			if spec.instructions != "" {
				params["developerInstructions"] = spec.instructions
			}
			if err = conn.Call(ctx, "thread/start", params, &thread); err != nil {
				return llm.Result{}, fmt.Errorf("не удалось начать сессию Codex: %w", err)
			}
			if !validThreadID(thread.Thread.ID) {
				return llm.Result{}, errors.New("Codex вернул некорректный ID сессии")
			}
			threadID = thread.Thread.ID
		}
		// Codex defers model-visible initial context until the first turn.
		// A thread/start reply alone is not durable instruction confirmation.
		pending = spec.instructionsHash
		if ephemeral {
			// The one-off approach thread would otherwise stay loaded until exit.
			defer unsubscribe(conn, threadID)
		} else {
			c.app.markLoaded(conn, threadID)
		}
	}
	threadReady := time.Since(threadStart)

	prompt := spec.prompt
	updating := !newThread && spec.instructionsHash != "" && (applied != spec.instructionsHash || pending != "")
	if updating {
		prompt = "Обновление инструкций Solveo. Следующие пожелания заменяют прежнюю преднастройку и действуют для этого и следующих вопросов.\n" + spec.instructions + "\nКонец обновления инструкций.\n" + prompt
		pending = spec.instructionsHash
	}
	input := []map[string]any{{"type": "text", "text": prompt}}
	for _, p := range spec.images {
		input = append(input, map[string]any{"type": "localImage", "path": p})
	}
	turn := map[string]any{"input": input}
	if spec.outputSchema != nil {
		turn["outputSchema"] = spec.outputSchema
	}
	if spec.config.Model != "" {
		turn["model"] = spec.config.Model
	}
	if spec.config.Reasoning != "" {
		turn["effort"] = spec.config.Reasoning
	}
	result, err := conn.RunTurn(ctx, threadID, turn, func(text string) {
		if emit != nil {
			emit(llm.Event{Kind: llm.EventAnswerText, Text: text})
		}
	})
	progress(timingLine(spec.purpose, info, accountReady, threadReady, preparedThreadUsed, result.Timing))
	if (newThread || updating) && err == nil && result.Status == appserver.StatusCompleted && strings.TrimSpace(result.Text) != "" {
		applied, pending = spec.instructionsHash, ""
	}
	state := keep()
	switch {
	case errors.Is(err, llm.ErrOutcomeUnknown):
		return state, err
	case errors.Is(err, context.DeadlineExceeded):
		return state, fmt.Errorf("Codex не ответил за %s", timeout)
	case errors.Is(err, context.Canceled):
		return state, fmt.Errorf("ответ отменён: %w", err)
	case err != nil:
		return state, err
	}
	switch result.Status {
	case appserver.StatusCompleted:
	case appserver.StatusFailed:
		msg := strings.TrimSpace(result.Error)
		if msg == "" {
			msg = "без описания"
		}
		return state, fmt.Errorf("ошибка Codex: %s", msg)
	case appserver.StatusInterrupted:
		return state, errors.New("Codex прервал ответ")
	default:
		return state, fmt.Errorf("%w: Codex завершил ответ с состоянием %q", llm.ErrOutcomeUnknown, result.Status)
	}
	if strings.TrimSpace(result.Text) == "" {
		return state, fmt.Errorf("%w: Codex вернул пустой ответ", llm.ErrOutcomeUnknown)
	}
	state.Text = result.Text
	return state, nil
}

// unsubscribe releases a finished one-off thread; failures only leave it loaded.
func unsubscribe(conn *appserver.Conn, threadID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Call(ctx, "thread/unsubscribe", map[string]any{"threadId": threadID}, nil)
}

// timingLine is a content-free measurement record for the native log.
func timingLine(purpose string, info startInfo, account, thread time.Duration, prepared bool, t appserver.Timing) string {
	if purpose == "" {
		purpose = "chat"
	}
	ms := func(d time.Duration) string {
		if d <= 0 {
			return "-"
		}
		return fmt.Sprintf("%d", d.Milliseconds())
	}
	process := "warm"
	if info.cold {
		process = fmt.Sprintf("cold launched_ms=%s ready_ms=%s", ms(info.launched), ms(info.rdy))
	}
	return fmt.Sprintf("Codex timing: purpose=%s process=%s account_ms=%s thread_ms=%s prepared_thread=%t first_text_ms=%s first_readable_ms=%s completed_ms=%s\n",
		purpose, process, ms(account), ms(thread), prepared, ms(t.FirstText), ms(t.FirstReadable), ms(t.Completed))
}

// A successful credential change invalidates the auxiliary process auth cache.
func (a *appServer) resetConnection() {
	a.mu.Lock()
	conn := a.conn
	a.conn = nil
	a.loaded = nil
	a.mu.Unlock()
	if conn != nil {
		conn.Kill()
	}
}
