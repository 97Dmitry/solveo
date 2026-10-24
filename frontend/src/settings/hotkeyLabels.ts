import type { HotkeyAction } from "../bridge/state";

// Labels match internal/hotkeys, which names actions in conflict errors.
export const hotkeyLabels: Record<HotkeyAction, string> = {
  "send-transcript": "Отправить новые реплики собеседника",
  "send-question": "Отправить собранный вопрос",
  "skip-question": "Пропустить собранный вопрос",
  "toggle-listening": "Слушать / остановить",
  "audio-window": "Показать окно распознавания",
  "quick-region": "Снимок области и ответ",
  "queue-region": "Добавить область в очередь",
  "quick-full": "Снимок всего экрана и ответ",
  submit: "Отправить вопрос и очередь",
  toggle: "Показать / скрыть окно",
};

// What each global shortcut does; shown in the help panel.
export const hotkeyDescriptions: Record<HotkeyAction, string> = {
  "send-transcript":
    "Сразу отправляет в чат все реплики собеседника после последней отправленной, без сборщика вопроса. Если собеседник ещё договаривает, Solveo дождётся конца фразы (до 6 с).",
  "send-question": "Отправляет готовую карточку сборщика вопроса.",
  "skip-question": "Убирает карточку сборщика: следующая соберётся из новой речи.",
  "toggle-listening": "Начинает или останавливает запись звука, когда распознавание подготовлено.",
  "audio-window": "Открывает окно распознавания речи.",
  "quick-region": "Выберите область экрана: снимок сразу уйдёт в чат, текст и очередь останутся.",
  "queue-region": "Добавляет снимок области в очередь к следующему вопросу.",
  "quick-full": "Снимок всего экрана сразу уходит в чат.",
  submit: "Отправляет текст из поля ввода вместе с очередью снимков.",
  toggle: "Скрывает или показывает главное окно.",
};
