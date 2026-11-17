import type { HotkeyAction, Hotkeys } from "../bridge/state";
import { hotkeyDescriptions, hotkeyLabels } from "../settings/hotkeyLabels";
import { Button } from "../ui/Button";

// Groups follow how shortcuts are used during an interview; every action appears exactly once.
const groups: [string, HotkeyAction[]][] = [
  [
    "Собеседование и речь",
    ["send-transcript", "toggle-listening", "audio-window", "send-question", "skip-question"],
  ],
  ["Снимки и вопросы", ["quick-region", "quick-full", "queue-region", "submit"]],
  ["Окно", ["toggle"]],
];
const windowKeys: [string, string][] = [
  ["Enter", "Отправить вопрос из поля ввода"],
  ["Shift+Enter", "Новая строка в поле ввода"],
  ["Ctrl+V", "Вставить изображение из буфера обмена в очередь"],
  [
    "Ctrl+F",
    "Поиск по открытому разговору; Enter и Shift+Enter — следующее и предыдущее совпадение",
  ],
  ["Esc", "Отменить выбор области снимка, закрыть поиск или панель"],
];
const questions = (send: string): [string, string][] => [
  [
    "Как быстрее всего отправить вопрос собеседника?",
    `Нажмите ${send ? send : "сочетание «Отправить новые реплики собеседника» (назначьте его в настройках на вкладке «Клавиши»)"} или кнопку «Новые реплики» на вкладке «Эфир» окна распознавания. В чат уйдут все реплики собеседника после последней отправленной, без промежуточной обработки: ответ начнётся так же быстро, как на обычный вопрос. Модель чата знает, что текст получен из распознавания речи, и учитывает возможные ошибки.`,
  ],
  [
    "Что считается отправленной репликой?",
    "Реплика, вошедшая в принятый вопрос: выбранная вручную, взятая в карточку сборщика или отправленная как новая. Такие реплики помечены «отправлено». Новыми считаются реплики собеседника после последней отправленной; микрофон не отправляется. «Очистить расшифровку» начинает отсчёт заново.",
  ],
  [
    "Что если собеседник ещё говорит?",
    "Фраза, которая распознавалась в момент нажатия, будет дождана (до 6 секунд) и отправлена целиком; речь, начатая после нажатия, войдёт в следующую отправку. Если фраза не закончилась вовремя, Solveo ничего не отправит и покажет ошибку — нажмите ещё раз после паузы.",
  ],
  [
    "Зачем тогда сборщик вопроса?",
    "Сборщик сам следит за речью и готовит аккуратную формулировку из нескольких реплик, но ему нужен отдельный запрос к модели. Сессия сборщика готовится заранее, пока он включён, поэтому анализ не ждёт её создания. Если отправить новые реплики напрямую, карточка, собранная из них, убирается, а следующая строится из новой речи.",
  ],
  [
    "Почему горячая клавиша не работает?",
    "Сочетание может быть занято другой программой: в настройках на вкладке «Клавиши» у такого действия написано «Не работает». Назначьте другое сочетание. Действия без сочетания отмечены здесь как «не назначено».",
  ],
];

export function HelpPanel({
  hotkeys,
  errors,
  onEditHotkeys,
}: {
  hotkeys: Hotkeys;
  errors: Partial<Record<HotkeyAction, string>>;
  onEditHotkeys: () => void;
}) {
  return (
    <div className="help-panel">
      <section aria-labelledby="help-hotkeys">
        <h3 id="help-hotkeys">Глобальные горячие клавиши</h3>
        <p className="form-intro">Работают во всех программах, даже когда окно Solveo скрыто.</p>
        {groups.map(([title, actions]) => (
          <div className="help-group" key={title}>
            <h4>{title}</h4>
            <dl className="help-keys">
              {actions.map((action) => (
                <div className="help-key" key={action}>
                  <dt>
                    {hotkeys[action] ? (
                      <kbd>{hotkeys[action]}</kbd>
                    ) : (
                      <span className="help-unassigned">не назначено</span>
                    )}
                  </dt>
                  <dd>
                    <strong>{hotkeyLabels[action]}</strong>
                    <span>{hotkeyDescriptions[action]}</span>
                    {errors[action] && (
                      <small className="hotkey-error">Не работает: {errors[action]}</small>
                    )}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
        ))}
        <Button onClick={onEditHotkeys}>Изменить сочетания</Button>
      </section>
      <section aria-labelledby="help-window">
        <h3 id="help-window">Клавиши в окне Solveo</h3>
        <dl className="help-keys">
          {windowKeys.map(([key, text]) => (
            <div className="help-key" key={key}>
              <dt>
                <kbd>{key}</kbd>
              </dt>
              <dd>{text}</dd>
            </div>
          ))}
        </dl>
      </section>
      <section aria-labelledby="help-faq">
        <h3 id="help-faq">Частые вопросы</h3>
        {questions(hotkeys["send-transcript"]).map(([question, answer]) => (
          <details className="help-faq" key={question}>
            <summary>{question}</summary>
            <p>{answer}</p>
          </details>
        ))}
      </section>
    </div>
  );
}
