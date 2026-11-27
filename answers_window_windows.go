//go:build windows && !bindings

package main

import (
	"context"
	"errors"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	core "github.com/97Dmitry/solveo/internal/app"
	"github.com/97Dmitry/solveo/internal/audiowindow"
)

func configureAnswersWindow(controller *core.Controller, window *desktopWindow, insertServer *audiowindow.Server, openAudioSettings func() error) (*audioProcess, error) {
	view := func() audiowindow.View {
		theme, uiScale := controller.AutoAnswersTheme()
		answers := controller.AutoAnswersSnapshot()
		return audiowindow.View{Theme: theme, UIScale: uiScale, Answers: &answers}
	}
	execute := func(ctx context.Context, cmd audiowindow.Command) error {
		if cmd.Name == "openAudioSettings" {
			return openAudioSettings()
		}
		if cmd.Name == "diagnostic" {
			if cmd.Diagnostic == nil {
				return errors.New("Не задана диагностика")
			}
			d := *cmd.Diagnostic
			d.Source = "answers"
			return controller.ReportRendererError(d)
		}
		cardID := ""
		if len(cmd.IDs) == 1 {
			cardID = cmd.IDs[0]
		}
		if cmd.Name != "insertAnswer" {
			return controller.AutoAnswersCommand(cmd.Name, cmd.ChatID, cardID, cmd.AnswerRevision)
		}
		if cmd.ChatID == "" || cardID == "" {
			return errors.New("Не выбран вопрос для вставки")
		}
		// Resolve the target before loading the question, which may read an archive.
		target := controller.AudioWindowTarget().ChatID
		detail, err := controller.AutoAnswerDetail(cmd.ChatID, cardID)
		if err != nil {
			return err
		}
		return requestMainInsert(ctx, insertServer, controller, window, target, detail.Question)
	}
	server, err := audiowindow.NewAnswers(view, execute, controller)
	if err != nil {
		return nil, err
	}
	return &audioProcess{mode: detachedAnswers, server: server}, nil
}

func requestMainInsert(ctx context.Context, server *audiowindow.Server, controller *core.Controller, window *desktopWindow, chatID, text string) error {
	if chatID != controller.AudioWindowTarget().ChatID {
		return errors.New("Активный чат изменился; повторите вставку")
	}
	err := server.RequestInsert(ctx, chatID, text, func(v audiowindow.Insert) {
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
}
