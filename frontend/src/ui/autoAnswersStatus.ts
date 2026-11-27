import type { AutoAnswersState } from "../bridge/wails";

export const autoAnswersStatus: Record<AutoAnswersState, string> = {
  unavailable: "Недоступны",
  setup: "Требуется настройка",
  paused: "На паузе",
  starting_model: "Запускаю модель",
  starting_capture: "Запускаю захват звука",
  listening: "Слушаю собеседника",
  analyzing: "Собираю вопрос",
  awaiting_continuation: "Жду продолжения вопроса",
  answering: "Готовлю ответ",
  error: "Ошибка автоответов",
};
