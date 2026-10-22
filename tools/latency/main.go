// Command latency runs opt-in real Windows Codex measurements. It is never run by tests.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/llm/codex"
)

func main() {
	home := flag.String("home", "", "Solveo-owned CODEX_HOME (must already have its own keyring login)")
	work := flag.String("work", "", "empty local working directory")
	binary := flag.String("codex", "codex", "Codex binary")
	image := flag.String("image", "", "same PNG for all requests")
	repeats := flag.Int("repeats", 3, "new chats, each followed by one follow-up")
	label := flag.String("label", "baseline", "measurement label")
	prepareIdle := flag.Duration("prepare-idle", 0, "simulate typing time while PrepareChat runs (e.g. 2s); zero disables thread preparation")
	between := flag.Duration("between-chats", 0, "pause between pairs to avoid service rate limits")
	alternate := flag.Bool("alternate-preparation", false, "alternate immediate/prepared chats within one process; requires positive prepare-idle")
	balanced := flag.Bool("balanced-preparation", false, "ABBA order: immediate/prepared/prepared/immediate; requires positive prepare-idle")
	flag.Parse()
	if *home == "" || *work == "" || *image == "" || *repeats < 1 || *prepareIdle < 0 || *between < 0 || ((*alternate || *balanced) && *prepareIdle == 0) || (*alternate && *balanced) {
		panic("home, work and image required")
	}
	b, err := os.ReadFile(*image)
	must(err)
	record(map[string]any{"label": *label, "image_sha256": fmt.Sprintf("%x", sha256.Sum256(b)), "at": time.Now().UTC(), "repeats": *repeats, "prepare_idle_ms": prepareIdle.Milliseconds(), "between_chats_ms": between.Milliseconds(), "alternate": *alternate, "balanced": *balanced, "model": "gpt-6-luna", "reasoning": "low"})
	c := codex.New(*binary, "", 2*time.Minute, *home, *work)
	defer func() { must(c.Close(context.Background())) }()
	start := time.Now()
	must(c.Prepare(context.Background()))
	a, err := c.Account(context.Background())
	must(err)
	if !a.SignedIn {
		panic("Solveo profile is not signed in; login using Solveo, never copy credentials")
	}
	record(map[string]any{"stage": "process_isolation_account", "ms": time.Since(start).Milliseconds()})
	cfg := chat.New().Config
	cfg.Model = "gpt-6-luna"
	cfg.Reasoning = "low"
	for i := 0; i < *repeats; i++ {
		usePreparation := *prepareIdle > 0 && (!*alternate || i%2 == 1)
		if *balanced {
			usePreparation = i%4 == 1 || i%4 == 2
		}
		variant := *label
		if *alternate || *balanced {
			if usePreparation {
				variant += "-prepared"
			} else {
				variant += "-immediate"
			}
		}
		var state json.RawMessage
		localID := chat.ID()
		prepCtx, cancelPreparation := context.WithCancel(context.Background())
		var prepared chan error
		if usePreparation {
			prepared = make(chan error, 1)
			go func() {
				prepared <- c.PrepareChat(prepCtx, llm.ChatPreparation{ChatID: localID, Config: cfg, GlobalInstructions: "Отвечай по-русски. Не используй инструменты."})
			}()
		}
		if *prepareIdle > 0 {
			time.Sleep(*prepareIdle)
		}
		for turn := 0; turn < 2; turn++ {
			q := "Ответь на задание на изображении. Дай только краткий ответ, без инструментов."
			if turn == 1 {
				q = "Кратко объясни предыдущий ответ одним предложением. Не используй инструменты."
			}
			req := llm.Request{ChatID: localID, Config: cfg, ConfigRevision: 1, GlobalInstructions: "Отвечай по-русски. Не используй инструменты.", Turn: chat.Message{Text: q}, State: state, Images: []llm.Image{{ID: "snapshot", Path: *image}}}
			started := time.Now()
			first := time.Duration(0)
			out, e := c.Generate(context.Background(), req, func(ev llm.Event) {
				if ev.Kind == llm.EventAnswerText && strings.TrimSpace(ev.Text) != "" && first == 0 {
					first = time.Since(started)
				}
				if ev.Kind == llm.EventProgress {
					record(map[string]any{"stage": "adapter_timing", "run": i + 1, "turn": turn, "timing": strings.TrimSpace(ev.Text)})
				}
			})
			row := map[string]any{"label": variant, "run": i + 1, "turn": turn, "first_ms": first.Milliseconds(), "completed_ms": time.Since(started).Milliseconds(), "answer": out.Text}
			if e != nil {
				row["error"] = e.Error()
			}
			record(row)
			if turn == 0 {
				cancelPreparation()
				if prepared != nil {
					if err := <-prepared; err != nil && !errors.Is(err, context.Canceled) {
						record(map[string]any{"stage": "preparation_error", "error": err.Error()})
					}
				}
			}
			must(e)
			state = out.State
		}
		if i+1 < *repeats && *between > 0 {
			time.Sleep(*between)
		}
	}
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func record(v any) { b, err := json.Marshal(v); must(err); fmt.Println(string(b)) }
