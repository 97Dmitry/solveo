//go:build windows && !bindings

package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/go-webview2/webviewloader"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"golang.org/x/sys/windows"

	core "github.com/97Dmitry/solveo/internal/app"
	"github.com/97Dmitry/solveo/internal/diagnostics"
)

// sessionDiagnostics adapts the session log to the controller; archives go to
// solveo-data/exports beside chat exports.
type sessionDiagnostics struct {
	session *diagnostics.Session
	exports string
}

func (d sessionDiagnostics) LogsDir() string { return d.session.Dir() }
func (d sessionDiagnostics) Archive(summary string) (string, error) {
	return d.session.Archive(d.exports, environmentSummary()+summary)
}
func (d sessionDiagnostics) PreviousCrash() string { return d.session.PreviousCrash }

// diagnosticsDeps returns nil, not a typed nil, when the log could not be opened.
func diagnosticsDeps(session *diagnostics.Session, dataDir string) core.Diagnostics {
	if session == nil {
		return nil
	}
	return sessionDiagnostics{session: session, exports: filepath.Join(dataDir, "exports")}
}

// openDiagnostics starts the session log in solveo-data/logs. A log that cannot
// be opened must not prevent startup: records are then discarded.
func openDiagnostics(dataDir string) (*diagnostics.Session, *slog.Logger) {
	session, err := diagnostics.Open(diagnostics.Options{Dir: filepath.Join(dataDir, "logs"), CrashOutput: true})
	if err != nil {
		return nil, slog.New(slog.DiscardHandler)
	}
	attrs := []any{"pid", os.Getpid(), "args", flagNames(os.Args[1:])}
	for _, a := range diagnostics.BuildAttrs() {
		attrs = append(attrs, a)
	}
	attrs = append(attrs, "windows", windowsVersion(), "webview2", webView2Version())
	if session.PreviousCrash != "" {
		attrs = append(attrs, "previous_crash", session.PreviousCrash)
	}
	if session.PreviousUnclean {
		attrs = append(attrs, "previous_unclean_exit", true)
	}
	session.Logger.Info("session start", attrs...)
	return session, session.Logger
}

// flagNames keeps option names only: values such as an instruction file path stay out of the log.
func flagNames(args []string) string {
	names := []string{}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			name, _, _ := strings.Cut(a, "=")
			names = append(names, name)
		}
	}
	return strings.Join(names, " ")
}

func windowsVersion() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

func webView2Version() string {
	v, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err != nil {
		return "unavailable: " + err.Error()
	}
	return v
}

func environmentSummary() string {
	var b strings.Builder
	for _, a := range diagnostics.BuildAttrs() {
		fmt.Fprintf(&b, "%s=%s\n", a.Key, a.Value.String())
	}
	fmt.Fprintf(&b, "windows=%s\nwebview2=%s\n", windowsVersion(), webView2Version())
	return b.String()
}

// startupStep logs how long each startup stage took.
type startupStep struct {
	log  *slog.Logger
	last time.Time
}

func (s *startupStep) done(name string) {
	now := time.Now()
	s.log.Info("startup step", "step", name, "duration", now.Sub(s.last).Round(time.Millisecond).String())
	s.last = now
}

// wailsLogger forwards Wails runtime messages to the session log.
type wailsLogger struct {
	log     *slog.Logger
	session *diagnostics.Session
}

func (l wailsLogger) Print(m string)   { l.log.Info("wails", "message", m) }
func (l wailsLogger) Trace(m string)   { l.log.Debug("wails", "message", m) }
func (l wailsLogger) Debug(m string)   { l.log.Debug("wails", "message", m) }
func (l wailsLogger) Info(m string)    { l.log.Info("wails", "message", m) }
func (l wailsLogger) Warning(m string) { l.log.Warn("wails", "message", m) }
func (l wailsLogger) Error(m string)   { l.log.Error("wails", "message", m) }

// Fatal is followed by os.Exit in Wails: write the record before that.
func (l wailsLogger) Fatal(m string) {
	l.log.Error("wails fatal", "message", m)
	if l.session != nil {
		l.session.Flush(time.Second)
	}
}

var _ logger.Logger = wailsLogger{}
