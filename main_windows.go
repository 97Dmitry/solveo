//go:build windows && !bindings

package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	core "github.com/97Dmitry/solveo/internal/app"
	"github.com/97Dmitry/solveo/internal/audio"
	"github.com/97Dmitry/solveo/internal/capture"
	"github.com/97Dmitry/solveo/internal/config"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/instance"
	"github.com/97Dmitry/solveo/internal/llm/codex"
	"github.com/97Dmitry/solveo/internal/platform"
	native "github.com/97Dmitry/solveo/internal/platform/windows"
	"github.com/97Dmitry/solveo/internal/profiles"
	"github.com/97Dmitry/solveo/internal/secrets"
	"github.com/97Dmitry/solveo/internal/settings"
	"github.com/97Dmitry/solveo/internal/stt"
	"github.com/97Dmitry/solveo/internal/stt/grok"
	"github.com/97Dmitry/solveo/internal/stt/qwen"
	"github.com/97Dmitry/solveo/internal/workstate"
)

type screenCapture struct{}

func (screenCapture) SavePrimary(p string) error                   { return capture.SavePrimary(p) }
func (screenCapture) SaveRegion(p string, r image.Rectangle) error { return capture.SaveRegion(p, r) }

type desktopWindow struct {
	mu    sync.Mutex
	ctx   context.Context
	hwnd  uintptr
	ready bool
}

func (w *desktopWindow) SetAlwaysOnTop(v bool) {
	w.mu.Lock()
	ctx := w.ctx
	w.mu.Unlock()
	if ctx != nil {
		wruntime.WindowSetAlwaysOnTop(ctx, v)
	}
}
func (w *desktopWindow) Show() {
	w.mu.Lock()
	ctx, ready := w.ctx, w.ready
	w.mu.Unlock()
	if ready && ctx != nil {
		wruntime.WindowUnminimise(ctx)
		wruntime.WindowShow(ctx)
	}
}
func (w *desktopWindow) Hide() {
	w.mu.Lock()
	ctx := w.ctx
	w.mu.Unlock()
	if ctx != nil {
		wruntime.WindowHide(ctx)
	}
}
func (w *desktopWindow) Toggle() {
	w.mu.Lock()
	hwnd := w.hwnd
	w.mu.Unlock()
	if hwnd == 0 {
		return
	}
	visible, _, _ := syscall.NewLazyDLL("user32.dll").NewProc("IsWindowVisible").Call(hwnd)
	if visible != 0 {
		w.Hide()
	} else {
		w.Show()
	}
}
func main() {
	if err := run(); err != nil {
		startupError(err)
	}
}
func startupError(err error) {
	log.Print(err)
	text, _ := syscall.UTF16PtrFromString(err.Error())
	title, _ := syscall.UTF16PtrFromString("Solveo")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
func run() error {
	flags, e := config.Parse(os.Args[1:])
	if e != nil {
		return e
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	dir := filepath.Join(filepath.Dir(exe), "solveo-data")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	dir, e = filepath.EvalSymlinks(dir)
	if e != nil {
		return e
	}
	release, e := instance.Acquire(dir)
	if e != nil {
		return e
	}
	defer release()
	store, e := history.Open(dir)
	if e != nil {
		return e
	}
	// Drafts, queued captures and answer checkpoints survive restarts beside the archive.
	work, e := workstate.Open(filepath.Join(dir, "work"))
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host := &native.Host{}
	window := &desktopWindow{}
	events := make(chan platform.Event, 128)
	if e = host.Start(ctx, func(ev platform.Event) {
		select {
		case events <- ev:
		default:
			log.Printf("native event queue full: %s", ev.Kind)
		}
	}); e != nil {
		return e
	}
	defer func() { _ = host.Close() }()
	// Codex keeps Solveo's own state, login and sessions beside the archive.
	provider := codex.New(flags.Binary, flags.Prompt, flags.Timeout, filepath.Join(dir, "codex-home"), filepath.Join(dir, "codex-work"))
	// Local speech recognition: Docker Desktop's CLI drives the solveo-stt container; weights stay beside the archive.
	keys := secrets.NewCredentialManager()
	speech := core.AudioDeps{
		Providers: []stt.Provider{
			qwen.New(filepath.Join(dir, "models"), os.Getenv),
			grok.New(func() (string, bool, error) { return keys.Get(stt.KeyName("grok")) }),
		},
		Capturer: audio.NewCapturer(),
		Secrets:  keys,
	}
	controller, e := core.New(core.Dependencies{
		Store:         store,
		SettingsStore: settings.Open(dir),
		ProfilesStore: profiles.New(dir),
		Provider:      provider,
		Capturer:      screenCapture{},
		NativeHost:    host,
		Window:        window,
		Work:          work,
		Audio:         speech,
		Emit: func(name string, data interface{}) {
			window.mu.Lock()
			runtimeCtx := window.ctx
			window.mu.Unlock()
			if runtimeCtx != nil {
				wruntime.EventsEmit(runtimeCtx, name, data)
			}
		},
	})
	if e != nil {
		return e
	}
	app := &App{controller: controller}
	app.exit = func() error {
		window.mu.Lock()
		runtimeCtx := window.ctx
		window.mu.Unlock()
		if runtimeCtx == nil {
			return errors.New("application is still starting")
		}
		exitCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := controller.Close(exitCtx); err != nil {
			return err
		}
		wruntime.Quit(runtimeCtx)
		return nil
	}

	var stateMu sync.Mutex
	domReady, rendererReady := false, false
	shown := false
	var readyErr error
	showWhenReady := func() error {
		stateMu.Lock()
		defer stateMu.Unlock()
		if readyErr != nil {
			return readyErr
		}
		if domReady && rendererReady && !shown {
			shown = true
			window.mu.Lock()
			window.ready = true
			window.mu.Unlock()
			window.SetAlwaysOnTop(controller.Snapshot().Settings.AlwaysOnTop)
			window.Show()
		}
		return nil
	}
	app.ready = func() error { stateMu.Lock(); rendererReady = true; stateMu.Unlock(); return showWhenReady() }
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-events:
				if ev.Kind == "exit" {
					if err := app.exit(); err != nil {
						controller.ReportError(err)
						window.Show()
					}
					continue
				}

				controller.HandleNativeEvent(ev)
			}
		}
	}()
	profile := filepath.Join(os.Getenv("LOCALAPPDATA"), "Solveo", "WebView2")
	if os.Getenv("LOCALAPPDATA") == "" {
		return errors.New("LOCALAPPDATA is unavailable")
	}
	theme := windows.Dark
	if controller.Snapshot().Settings.Theme == "light" {
		theme = windows.Light
	}
	_ = host.SetTheme(theme == windows.Dark)
	return wails.Run(&options.App{Title: "Solveo", Width: 740, Height: 680, MinWidth: 520, MinHeight: 400, StartHidden: true, HideWindowOnClose: true,
		AssetServer: &assetserver.Options{Assets: assets, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, err := controller.ResolveImageURL(r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, p)
		})},
		Bind: []interface{}{app}, Windows: &windows.Options{ContentProtection: true, WindowClassName: "SolveoMain", WebviewUserDataPath: profile, Theme: theme},
		OnStartup: func(ctx context.Context) { window.mu.Lock(); window.ctx = ctx; window.mu.Unlock() },
		OnDomReady: func(ctx context.Context) {
			window.mu.Lock()
			window.ctx = ctx
			window.mu.Unlock()
			hwnd, err := native.FindMainWindow("SolveoMain", uint32(os.Getpid()))
			if err == nil {
				err = native.EnsureExcluded(hwnd)
			}
			if err == nil {
				// Solveo lives in the tray: no taskbar button and no Alt+Tab entry.
				err = native.TrayOnly(hwnd)
			}
			stateMu.Lock()
			readyErr = err
			domReady = err == nil
			stateMu.Unlock()
			if err != nil {
				startupError(fmt.Errorf("initialize main window: %w", err))
				wruntime.Quit(ctx)
				return
			}
			window.mu.Lock()
			window.hwnd = hwnd
			window.mu.Unlock()
			_ = showWhenReady()
		},
		OnBeforeClose: func(ctx context.Context) bool {
			snap := controller.Snapshot()
			if snap.Controls.CanDiscard {
				controller.ReportError(core.ErrSavePending)
				window.Show()
				return true
			}
			return false
		},
		OnShutdown: func(context.Context) {
			cancel()
			shutdownCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			_ = controller.Close(shutdownCtx)
			// The process is ending even if Close refused: never leave app-server running.
			_ = provider.Close(shutdownCtx)
			_ = host.Close()
		},
	})
}
