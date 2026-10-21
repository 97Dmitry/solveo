// Package appservertest is a scripted fake Codex app-server for tests. A test
// binary re-executes itself with EnvFake set and calls Main from TestMain.
//
// Turn behaviour is selected by markers in the first text input:
// [slow] streams until interrupted, [ignore-interrupt] never confirms an
// interrupt, [crash-before]/[crash-after] exit before/after the first text,
// [garbage] writes invalid JSON, [commentary] sends a commentary item first,
// [late] sends events of an older turn first, [split] writes lines in pieces,
// [early] sends notifications before the turn/start response, [fail] fails the
// turn, [big] exceeds the text limit and [stop-reading] stops reading stdin.
package appservertest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	EnvFake  = "SOLVEO_FAKE_APPSERVER"
	EnvStore = "SOLVEO_FAKE_STORE" // directory persisting threads, account and skills across restarts
	EnvLog   = "SOLVEO_FAKE_LOG"   // file receiving one JSON line per request
	// EnvSkills lists skill paths the fake discovers, separated by '|'.
	EnvSkills = "SOLVEO_FAKE_SKILLS"
	// EnvNoAccount starts without a signed-in account.
	EnvNoAccount = "SOLVEO_FAKE_NO_ACCOUNT"
	// EnvLoginEarly sends account/login/completed before the login/start reply.
	EnvLoginEarly = "SOLVEO_FAKE_LOGIN_EARLY"
	// EnvModels names a JSON file with an array of model/list results, one per
	// page; page N is served for cursor "N". A non-array value is returned as an
	// RPC error object.
	EnvModels = "SOLVEO_FAKE_MODELS"
)

type server struct {
	wmu   sync.Mutex
	out   *bufio.Writer
	store string
	log   *os.File
	mu    sync.Mutex
	turns map[string]chan struct{} // turn ID -> interrupt
	seq   int
}

// Main serves stdin/stdout until stdin closes, then exits the process.
func Main() {
	s := &server{out: bufio.NewWriter(os.Stdout), store: os.Getenv(EnvStore), turns: map[string]chan struct{}{}}
	if p := os.Getenv(EnvLog); p != "" {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			s.log = f
			b, _ := json.Marshal(map[string]any{"start": os.Args[1:], "codex_home": os.Getenv("CODEX_HOME")})
			_, _ = f.Write(append(b, '\n'))
		}
	}
	if s.store != "" {
		_ = os.MkdirAll(filepath.Join(s.store, "threads"), 0o700)
		if os.Getenv(EnvNoAccount) == "" && !s.exists("account") {
			s.touch("account")
		}
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if s.log != nil && m.Method != "" {
			_, _ = s.log.Write(append(append([]byte(nil), sc.Bytes()...), '\n'))
		}
		if len(m.ID) == 0 || m.Method == "" {
			continue // notifications and responses to our requests
		}
		s.handle(m.ID, m.Method, m.Params)
	}
	os.Exit(0)
}

func (s *server) exists(name string) bool {
	_, err := os.Stat(filepath.Join(s.store, name))
	return err == nil
}
func (s *server) touch(name string) { _ = os.WriteFile(filepath.Join(s.store, name), nil, 0o600) }

func (s *server) send(v any) {
	b, _ := json.Marshal(v)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
	_ = s.out.Flush()
}
func (s *server) raw(parts ...string) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	for _, p := range parts {
		_, _ = s.out.WriteString(p)
		_ = s.out.Flush()
		time.Sleep(5 * time.Millisecond)
	}
}
func (s *server) reply(id json.RawMessage, result any) {
	s.send(map[string]any{"id": id, "result": result})
}
func (s *server) fail(id json.RawMessage, code int, msg string) {
	s.send(map[string]any{"id": id, "error": map[string]any{"code": code, "message": msg}})
}
func (s *server) note(method string, params any) {
	s.send(map[string]any{"method": method, "params": params})
}

func (s *server) id(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("%s%04d-0000-7000-8000-%012d", prefix, time.Now().UnixNano()%10000, s.seq)
}

func (s *server) skills() []map[string]any {
	list := []map[string]any{}
	for _, p := range strings.Split(os.Getenv(EnvSkills), "|") {
		if p == "" {
			continue
		}
		enabled := !s.exists("disabled-" + filepath.Base(filepath.Dir(p)))
		list = append(list, map[string]any{"name": filepath.Base(filepath.Dir(p)), "path": p, "scope": "user", "enabled": enabled})
	}
	return list
}

func (s *server) handle(id json.RawMessage, method string, params json.RawMessage) {
	var p map[string]any
	_ = json.Unmarshal(params, &p)
	switch method {
	case "initialize":
		s.reply(id, map[string]any{"userAgent": "fake"})
	case "skills/list":
		s.reply(id, map[string]any{"data": []any{map[string]any{"cwd": "work", "errors": []any{}, "skills": s.skills()}}})
	case "skills/config/write":
		path, _ := p["path"].(string)
		s.touch("disabled-" + filepath.Base(filepath.Dir(path)))
		s.reply(id, map[string]any{"effectiveEnabled": false})
	case "plugin/installed":
		s.reply(id, map[string]any{"marketplaces": []any{}, "marketplaceLoadErrors": []any{}})
	case "mcpServerStatus/list":
		s.reply(id, map[string]any{"data": []any{}})
	case "account/read":
		if s.exists("account") {
			s.reply(id, map[string]any{"account": map[string]any{"type": "chatgpt", "email": "secret@example.com"}, "requiresOpenaiAuth": true})
		} else {
			s.reply(id, map[string]any{"account": nil, "requiresOpenaiAuth": true})
		}
	case "account/login/start":
		login := s.id("login")
		if os.Getenv(EnvLoginEarly) != "" {
			s.touch("account")
			s.note("account/login/completed", map[string]any{"loginId": login, "success": true, "error": nil})
			s.reply(id, map[string]any{"type": "chatgptDeviceCode", "loginId": login, "verificationUrl": "https://auth.example.test/device", "userCode": "ABCD-1234"})
			return
		}
		s.reply(id, map[string]any{"type": "chatgptDeviceCode", "loginId": login, "verificationUrl": "https://auth.example.test/device", "userCode": "ABCD-1234"})
		go func() {
			time.Sleep(50 * time.Millisecond)
			if s.exists("login-cancelled") {
				return
			}
			s.touch("account")
			s.note("account/login/completed", map[string]any{"loginId": login, "success": true, "error": nil})
		}()
	case "account/login/cancel":
		s.touch("login-cancelled")
		s.reply(id, map[string]any{"status": "canceled"})
	case "thread/start":
		thread := s.id("01a1")
		if eph, _ := p["ephemeral"].(bool); !eph {
			s.touch(filepath.Join("threads", thread))
		}
		s.reply(id, map[string]any{"thread": map[string]any{"id": thread}, "instructionSources": []any{}, "model": p["model"], "sandbox": map[string]any{"type": "readOnly"}})
	case "thread/resume":
		thread, _ := p["threadId"].(string)
		if thread == "" || strings.ContainsAny(thread, `/\.`) || !s.exists(filepath.Join("threads", thread)) {
			s.fail(id, -32600, "no rollout found for thread id "+thread)
			return
		}
		s.reply(id, map[string]any{"thread": map[string]any{"id": thread}, "instructionSources": []any{}})
	case "model/list":
		s.models(id, p)
	case "thread/unsubscribe":
		s.reply(id, map[string]any{"status": "unsubscribed"})
	case "turn/start":
		s.turn(id, p)
	case "turn/interrupt":
		turn, _ := p["turnId"].(string)
		s.mu.Lock()
		ch := s.turns[turn]
		s.mu.Unlock()
		s.reply(id, map[string]any{})
		if ch != nil {
			close(ch)
		}
	default:
		s.fail(id, -32601, "unsupported "+method)
	}
}

func (s *server) turn(id json.RawMessage, p map[string]any) {
	thread, _ := p["threadId"].(string)
	text := ""
	if input, ok := p["input"].([]any); ok && len(input) > 0 {
		if first, ok := input[0].(map[string]any); ok {
			text, _ = first["text"].(string)
		}
	}
	has := func(marker string) bool { return strings.Contains(text, "["+marker+"]") }
	if has("stop-reading") {
		// A wedged server: stop reading stdin so the client's pipe fills up.
		select {}
	}
	turn := s.id("01b1")
	stop := make(chan struct{})
	s.mu.Lock()
	s.turns[turn] = stop
	s.mu.Unlock()
	item := "msg_" + turn
	delta := func(t string) {
		s.note("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": turn, "itemId": item, "delta": t})
	}
	complete := func(status string, errText any) {
		s.note("turn/completed", map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": status, "items": []any{}, "error": errText}})
	}
	started := func() {
		s.note("turn/started", map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": "inProgress", "items": []any{}}})
	}
	if has("early") {
		started()
		s.note("item/started", map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{"type": "agentMessage", "id": item, "text": "", "phase": "final_answer"}})
		delta("Ранний ")
	}
	s.reply(id, map[string]any{"turn": map[string]any{"id": turn, "status": "inProgress", "items": []any{}}})
	go func() {
		if !has("early") {
			started()
		}
		if has("late") {
			old := "01b1-old-turn"
			s.note("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": old, "itemId": "msg_old", "delta": "СТАРЫЙ"})
			s.note("turn/completed", map[string]any{"threadId": thread, "turn": map[string]any{"id": old, "status": "completed", "items": []any{}}})
		}
		if has("crash-before") {
			fmt.Fprintln(os.Stderr, "token=secret-value")
			fmt.Fprintln(os.Stderr, "Error: failed to initialize state runtime at C:\\share")
			os.Exit(3)
		}
		if has("garbage") {
			s.raw("{not json\n")
			return
		}
		if has("fail") {
			complete("failed", map[string]any{"message": "модель недоступна"})
			return
		}
		if has("commentary") {
			s.note("item/started", map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{"type": "agentMessage", "id": "msg_c", "text": "", "phase": "commentary"}})
			s.note("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": turn, "itemId": "msg_c", "delta": "Думаю вслух"})
			s.note("item/completed", map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{"type": "agentMessage", "id": "msg_c", "text": "Думаю вслух", "phase": "commentary"}})
		}
		if !has("early") {
			s.note("item/started", map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{"type": "agentMessage", "id": item, "text": "", "phase": "final_answer"}})
		}
		if has("big") {
			chunk := strings.Repeat("x", 64<<10)
			for i := 0; i < 20; i++ {
				select {
				case <-stop:
					complete("interrupted", nil)
					return
				default:
				}
				delta(chunk)
			}
			<-stop
			complete("interrupted", nil)
			return
		}
		answer := "Ответ: " + strings.TrimSpace(text)
		if has("early") {
			answer = "Ранний " + answer
			delta(answer[len("Ранний "):])
		} else if has("split") {
			b, _ := json.Marshal(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": thread, "turnId": turn, "itemId": item, "delta": answer}})
			line := string(b) + "\n"
			s.raw(line[:7], line[7:23], line[23:])
		} else {
			half := len(answer) / 2
			for half > 0 && !utf8Start(answer[half]) {
				half--
			}
			delta(answer[:half])
			delta(answer[half:])
		}
		if has("crash-after") {
			time.Sleep(20 * time.Millisecond)
			os.Exit(4)
		}
		if has("slow") || has("ignore-interrupt") {
			for {
				select {
				case <-stop:
					if has("ignore-interrupt") {
						select {}
					}
					complete("interrupted", nil)
					return
				case <-time.After(20 * time.Millisecond):
					delta(".")
				}
			}
		}
		s.note("item/completed", map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{"type": "agentMessage", "id": item, "text": answer, "phase": "final_answer"}})
		complete("completed", nil)
	}()
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func (s *server) models(id json.RawMessage, p map[string]any) {
	b, err := os.ReadFile(os.Getenv(EnvModels))
	if err != nil {
		s.fail(id, -32603, "no catalog")
		return
	}
	var pages []json.RawMessage
	if json.Unmarshal(b, &pages) != nil {
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &e)
		s.fail(id, e.Code, e.Message)
		return
	}
	page := 0
	if cursor, _ := p["cursor"].(string); cursor != "" {
		_, _ = fmt.Sscanf(cursor, "%d", &page)
	}
	if page >= len(pages) {
		s.fail(id, -32602, "bad cursor")
		return
	}
	s.send(map[string]any{"id": id, "result": pages[page]})
}
