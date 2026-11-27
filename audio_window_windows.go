//go:build windows && !bindings

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	core "github.com/97Dmitry/solveo/internal/app"
	"github.com/97Dmitry/solveo/internal/audiowindow"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/diagnostics"
	native "github.com/97Dmitry/solveo/internal/platform/windows"
)

type detachedWindow struct {
	flag, title, class, profile string
	width, height               int
	minWidth, minHeight         int
}

var detachedAudio = detachedWindow{
	flag: "--audio-window", title: "Аудио", class: "SolveoAudio", profile: "AudioWebView2",
	width: 460, height: 680, minWidth: 380, minHeight: 360,
}

var detachedAnswers = detachedWindow{
	flag: "--answers-window", title: "Автоответы", class: "SolveoAnswers", profile: "AnswersWebView2",
	width: 520, height: 680, minWidth: 380, minHeight: 360,
}

type audioProcess struct {
	mu     sync.Mutex
	mode   detachedWindow
	server *audiowindow.Server
	cmd    *exec.Cmd
	done   chan struct{}
	closed bool
}

func (p *audioProcess) Show(x, y int, parent uintptr) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("Приложение завершает работу")
	}
	if p.cmd != nil {
		p.server.Show()
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	config := p.server.Config()
	config.ParentWindow = uint64(parent)
	config.ParentPID = uint32(os.Getpid())
	config.X = x
	config.Y = y
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, p.mode.flag)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Env = audiowindow.ChildEnvironment(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	// A Go panic of the child ends up on its stderr; keep the end for the session log.
	stderr := &diagnostics.Tail{Limit: 16 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		diagLog.Error("detached window failed to start", "window", p.mode.class, "error", err)
		return fmt.Errorf("не удалось открыть окно «%s»", p.mode.title)
	}
	diagLog.Info("detached window started", "window", p.mode.class, "pid", cmd.Process.Pid)
	p.cmd = cmd
	p.done = make(chan struct{})
	done := p.done
	go func() {
		if err := cmd.Wait(); err != nil {
			diagLog.Error("detached window exited", "window", p.mode.class, "pid", cmd.Process.Pid, "error", err, "stderr", stderr.String())
		} else {
			diagLog.Info("detached window exited", "window", p.mode.class, "pid", cmd.Process.Pid)
		}
		p.mu.Lock()
		if p.cmd == cmd {
			p.cmd = nil
		}
		p.mu.Unlock()
		close(done)
	}()
	return nil
}
func (p *audioProcess) Close() {
	p.mu.Lock()
	p.closed = true
	cmd, done := p.cmd, p.done
	p.mu.Unlock()
	p.server.Close()
	if cmd != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
}
func (p *audioProcess) showBeside(window *desktopWindow) error {
	window.mu.Lock()
	ctx, parent := window.ctx, window.hwnd
	window.mu.Unlock()
	if ctx == nil {
		return errors.New("Основное окно ещё не готово")
	}
	if p.mode.flag == detachedAnswers.flag {
		bounds, err := native.WindowBounds(parent)
		if err != nil {
			return err
		}
		return p.Show(bounds.Min.X-p.mode.width-8, bounds.Min.Y, 0)
	}
	x, y := wruntime.WindowGetPosition(ctx)
	width, _ := wruntime.WindowGetSize(ctx)
	return p.Show(x+width+8, y, parent)
}

func configureDetachedWindows(controller *core.Controller, window *desktopWindow, app *App) (audioUI, answersUI *audioProcess, err error) {
	var server *audiowindow.Server
	view := func() audiowindow.View {
		s := controller.AudioWindowTarget()
		return audiowindow.View{Meetings: controller.MeetingState(), Question: s.Question, Pinned: s.Pinned, Audio: controller.AudioSnapshot(), ChatID: s.ChatID, ChatTitle: s.Title, CanSend: s.CanSend, Disabled: s.Disabled, Theme: s.Theme, UIScale: s.UIScale, Dictionary: s.Dictionary, SendHotkey: s.SendHotkey, AutoAnswersState: controller.AutoAnswersState()}
	}
	execute := func(ctx context.Context, cmd audiowindow.Command) error {
		switch cmd.Name {
		case "showAnswers":
			return answersUI.showBeside(window)
		case "meeting":
			if cmd.Meeting == nil {
				return errors.New("Не задана команда встречи")
			}
			return controller.MeetingCommand(*cmd.Meeting)
		case "question":
			if cmd.Question == nil {
				return errors.New("Не задана команда сборщика")
			}
			return controller.QuestionCommand(*cmd.Question)
		case "refresh":
			return controller.RefreshAudio()
		case "prepare":
			return controller.PrepareAudio()
		case "startModel":
			return controller.StartAudioModel()
		case "stopModel":
			return controller.StopAudioModel()
		case "startListening":
			return controller.StartListening()
		case "stopListening":
			return controller.StopListening()
		case "clear":
			return controller.ClearTranscript()
		case "deleteChatHistory":
			return controller.DeleteChatAudioHistory(cmd.ChatID)
		case "selectProvider":
			return controller.SetAudioProvider(cmd.Provider)
		case "setKey":
			return controller.SetSTTKey(cmd.Provider, cmd.Key)
		case "deleteKey":
			return controller.DeleteSTTKey(cmd.Provider)
		case "setDictionary":
			return controller.SetContextPhrases(cmd.Phrases)
		case "diagnostic":
			if cmd.Diagnostic == nil {
				return errors.New("Не задана диагностика")
			}
			d := *cmd.Diagnostic
			d.Source = "audio"
			return controller.ReportRendererError(d)
		case "send":
			_, err := controller.SubmitTranscript(core.SubmitTranscriptInput{ChatID: cmd.ChatID, RequestID: chat.ID(), SegmentIDs: cmd.IDs})
			if err == nil {
				window.Show()
			}
			return err
		case "sendUnsent":
			err := controller.SendUnsentTranscript(cmd.ChatID)
			if err == nil {
				window.Show()
			}
			return err
		case "insert":
			return requestMainInsert(ctx, server, controller, window, cmd.ChatID, cmd.Text)
		default:
			return errors.New("Неизвестная команда аудио")
		}
	}
	server, err = audiowindow.New(view, execute, controller)
	if err != nil {
		return nil, nil, err
	}
	audioUI = &audioProcess{mode: detachedAudio, server: server}
	answersUI, err = configureAnswersWindow(controller, window, server, func() error {
		server.SelectSettings()
		return audioUI.showBeside(window)
	})
	if err != nil {
		server.Close()
		return nil, nil, err
	}
	app.showAudio = func() error { return audioUI.showBeside(window) }
	app.audioInsertResult = server.CompleteInsert
	return audioUI, answersUI, nil
}

func runAudioWindow(mode detachedWindow) error {
	// The mode has been consumed; Wails dev must not parse it as its own flag.
	os.Args = os.Args[:1]
	var config audiowindow.Config
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 4096)).Decode(&config); err != nil {
		return fmt.Errorf("не удалось прочитать канал окна «%s»", mode.title)
	}
	client, err := audiowindow.NewClient(config)
	if err != nil {
		return err
	}
	if _, err := client.State(); err != nil {
		return err
	}
	embedded, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return err
	}
	var mu sync.Mutex
	var runtimeCtx context.Context
	var childHWND uintptr
	initialized := false
	var lastTheme string
	var lastPinned bool
	var haveSettings bool
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	var closePolicy audiowindow.ClosePolicy
	quitChild := func(ctx context.Context) { closePolicy.BeginQuit(); wruntime.Quit(ctx) }

	var dock *audioDock
	bridge := &AudioWindowApp{client: client}
	assetOptions := &assetserver.Options{Assets: audiowindow.Assets(embedded), Handler: client.AudioHandler()}
	if mode.flag == detachedAnswers.flag {
		assetOptions = &assetserver.Options{Assets: audiowindow.AnswersAssets(embedded)}
		bridge.setPinned = func(pinned bool) error {
			mu.Lock()
			hwnd := childHWND
			mu.Unlock()
			if hwnd == 0 {
				return errors.New("Окно автоответов ещё не готово")
			}
			return native.PinDetachedWindow(hwnd, pinned)
		}
	} else {
		dock = &audioDock{parent: uintptr(config.ParentWindow), parentPID: config.ParentPID}
		bridge.dockLeft, bridge.float, bridge.layout = dock.attach, dock.detach, dock.layout
	}
	bridge.changed = func(v audiowindow.View) {
		mu.Lock()
		ctx := runtimeCtx
		themeChanged := !haveSettings || lastTheme != v.Theme
		pinChanged := !haveSettings || lastPinned != v.Pinned
		lastTheme, lastPinned, haveSettings = v.Theme, v.Pinned, true
		mu.Unlock()
		if ctx == nil {
			return
		}
		if themeChanged {
			if v.Theme == "light" {
				wruntime.WindowSetLightTheme(ctx)
			} else {
				wruntime.WindowSetDarkTheme(ctx)
			}
		}
		if dock != nil && pinChanged {
			wruntime.WindowSetAlwaysOnTop(ctx, v.Pinned)
		}
	}

	bridge.show = func() {
		mu.Lock()
		ctx := runtimeCtx
		ok := initialized
		mu.Unlock()
		if ctx != nil && ok {
			if dock != nil {
				dock.manualShow()
			}
			wruntime.WindowUnminimise(ctx)
			wruntime.WindowShow(ctx)
		}
	}
	bridge.lost = func() {
		mu.Lock()
		ctx := runtimeCtx
		mu.Unlock()
		if ctx != nil {
			quitChild(ctx)
		}
	}
	bridge.ready = func() error {
		mu.Lock()
		defer mu.Unlock()
		if !initialized {
			return fmt.Errorf("окно «%s» ещё не готово", mode.title)
		}
		return nil
	}
	return wails.Run(&options.App{
		Title: "Solveo — " + mode.title, Width: mode.width, Height: mode.height, MinWidth: mode.minWidth, MinHeight: mode.minHeight, StartHidden: true, HideWindowOnClose: false,
		Windows:     &windows.Options{ContentProtection: true, WindowClassName: mode.class, WebviewUserDataPath: filepath.Join(os.Getenv("LOCALAPPDATA"), "Solveo", mode.profile), Theme: windows.Dark},
		AssetServer: assetOptions,
		Bind:        []interface{}{bridge},
		OnStartup: func(ctx context.Context) {
			mu.Lock()
			runtimeCtx = ctx
			mu.Unlock()
			go func() {
				timer := time.NewTimer(200 * time.Millisecond)
				defer timer.Stop()
				for {
					select {
					case <-watchCtx.Done():
						return
					case <-timer.C:
						version, err := client.ShowVersion()
						if err != nil {
							quitChild(ctx)
							return
						}
						bridge.reveal(version)
						interval := 200 * time.Millisecond
						if dock != nil {
							switch dock.follow() {
							case audiowindow.DockHide:
								wruntime.WindowHide(ctx)
							case audiowindow.DockShow:
								wruntime.WindowUnminimise(ctx)
								wruntime.WindowShow(ctx)
							case audiowindow.DockKeep:
							}
							if attached, _ := dock.layout(); attached {
								interval = 33 * time.Millisecond
							}
						}
						timer.Reset(interval)
					}
				}
			}()
		},
		OnShutdown: func(context.Context) { stopWatch() },
		OnBeforeClose: func(ctx context.Context) bool {
			return closePolicy.BeforeClose(func() {
				if dock != nil {
					dock.manualHide()
				}
				wruntime.WindowHide(ctx)
			})
		},
		OnDomReady: func(ctx context.Context) {
			hwnd, err := native.FindMainWindow(mode.class, uint32(os.Getpid()))
			if err == nil {
				err = native.EnsureExcluded(hwnd)
			}
			if err == nil {
				err = native.TrayOnly(hwnd)
			}
			if err == nil {
				err = native.PlaceAudioWindow(hwnd, config.X, config.Y, mode.width, mode.height)
			}
			if err != nil {
				startupError(fmt.Errorf("не удалось безопасно подготовить окно «%s»: %w", mode.title, err))
				quitChild(ctx)
				return
			}
			if dock != nil {
				dock.mu.Lock()
				dock.hwnd = hwnd
				dock.mu.Unlock()
			}
			mu.Lock()
			childHWND = hwnd
			initialized = true
			mu.Unlock()
		},
	})
}
