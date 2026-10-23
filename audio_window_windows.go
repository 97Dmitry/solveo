//go:build windows && !bindings

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
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
	native "github.com/97Dmitry/solveo/internal/platform/windows"
)

type audioProcess struct {
	mu     sync.Mutex
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
	cmd := exec.Command(exe, "--audio-window")
	cmd.Stdin = bytes.NewReader(data)
	cmd.Env = audiowindow.ChildEnvironment(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return errors.New("Не удалось открыть окно аудио")
	}
	p.cmd = cmd
	p.done = make(chan struct{})
	done := p.done
	go func() {
		_ = cmd.Wait()
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
func configureAudioWindow(controller *core.Controller, window *desktopWindow, app *App) (*audioProcess, error) {
	var server *audiowindow.Server
	view := func() audiowindow.View {
		s := controller.AudioWindowTarget()
		return audiowindow.View{Pinned: s.Pinned, Audio: controller.AudioSnapshot(), ChatID: s.ChatID, ChatTitle: s.Title, CanSend: s.CanSend, Disabled: s.Disabled, Theme: s.Theme, UIScale: s.UIScale}
	}
	execute := func(ctx context.Context, cmd audiowindow.Command) error {
		switch cmd.Name {
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
		case "send":
			_, err := controller.SubmitTranscript(core.SubmitTranscriptInput{ChatID: cmd.ChatID, RequestID: chat.ID(), SegmentIDs: cmd.IDs})
			if err == nil {
				window.Show()
			}
			return err
		case "insert":
			if cmd.ChatID != controller.AudioWindowTarget().ChatID {
				return errors.New("Активный чат изменился; повторите вставку")
			}
			err := server.RequestInsert(ctx, cmd.ChatID, cmd.Text, func(v audiowindow.Insert) {
				window.mu.Lock()
				runtimeCtx := window.ctx
				window.mu.Unlock()
				if runtimeCtx != nil {
					wruntime.EventsEmit(runtimeCtx, "solveo:audio-insert", v)
				}
			})
			if err == nil {
				window.Show()
			}
			return err
		default:
			return errors.New("Неизвестная команда аудио")
		}
	}
	var err error
	server, err = audiowindow.New(view, execute, controller)
	if err != nil {
		return nil, err
	}
	p := &audioProcess{server: server}
	app.showAudio = func() error {
		window.mu.Lock()
		ctx := window.ctx
		parent := window.hwnd
		window.mu.Unlock()
		if ctx == nil {
			return errors.New("Основное окно ещё не готово")
		}
		x, y := wruntime.WindowGetPosition(ctx)
		width, _ := wruntime.WindowGetSize(ctx)
		return p.Show(x+width+8, y, parent)
	}
	app.audioInsertResult = server.CompleteInsert
	return p, nil
}

func runAudioWindow() error {
	// The mode has been consumed; Wails dev must not parse it as its own flag.
	os.Args = os.Args[:1]
	var config audiowindow.Config
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 4096)).Decode(&config); err != nil {
		return errors.New("Не удалось прочитать канал окна аудио")
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
	initialized := false
	var lastTheme string
	var lastPinned bool
	var haveSettings bool
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	var closePolicy audiowindow.ClosePolicy
	quitChild := func(ctx context.Context) { closePolicy.BeginQuit(); wruntime.Quit(ctx) }

	dock := &audioDock{parent: uintptr(config.ParentWindow), parentPID: config.ParentPID}
	bridge := &AudioWindowApp{client: client, dockLeft: dock.attach, float: dock.detach, layout: dock.layout}
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
		if pinChanged {
			wruntime.WindowSetAlwaysOnTop(ctx, v.Pinned)
		}
	}

	bridge.show = func() {
		mu.Lock()
		ctx := runtimeCtx
		ok := initialized
		mu.Unlock()
		if ctx != nil && ok {
			dock.manualShow()
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
			return errors.New("Окно аудио ещё не готово")
		}
		return nil
	}
	return wails.Run(&options.App{
		Title: "Solveo — Аудио", Width: 460, Height: 680, MinWidth: 380, MinHeight: 360, StartHidden: true, HideWindowOnClose: false,
		Windows:     &windows.Options{ContentProtection: true, WindowClassName: "SolveoAudio", WebviewUserDataPath: filepath.Join(os.Getenv("LOCALAPPDATA"), "Solveo", "AudioWebView2"), Theme: windows.Dark},
		AssetServer: &assetserver.Options{Assets: audiowindow.Assets(embedded), Handler: http.NotFoundHandler()},
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
						switch dock.follow() {
						case audiowindow.DockHide:
							wruntime.WindowHide(ctx)
						case audiowindow.DockShow:
							wruntime.WindowUnminimise(ctx)
							wruntime.WindowShow(ctx)
						case audiowindow.DockKeep:
						}
						attached, _ := dock.layout()
						interval := 200 * time.Millisecond
						if attached {
							interval = 33 * time.Millisecond
						}
						timer.Reset(interval)
					}
				}
			}()
		},
		OnShutdown: func(context.Context) { stopWatch() },
		OnBeforeClose: func(ctx context.Context) bool {
			return closePolicy.BeforeClose(func() { dock.manualHide(); wruntime.WindowHide(ctx) })
		},
		OnDomReady: func(ctx context.Context) {
			hwnd, err := native.FindMainWindow("SolveoAudio", uint32(os.Getpid()))
			if err == nil {
				err = native.EnsureExcluded(hwnd)
			}
			if err == nil {
				err = native.TrayOnly(hwnd)
			}
			if err == nil {
				err = native.PlaceAudioWindow(hwnd, config.X, config.Y, 460, 680)
			}
			if err != nil {
				startupError(errors.New("Не удалось безопасно подготовить окно аудио"))
				quitChild(ctx)
				return
			}
			dock.mu.Lock()
			dock.hwnd = hwnd
			dock.mu.Unlock()
			mu.Lock()
			initialized = true
			mu.Unlock()
		},
	})
}
