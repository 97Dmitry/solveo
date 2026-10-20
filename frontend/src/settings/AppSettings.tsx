import { useState } from "react";
import { settingsSchema, type AppSettings as Config, type Snapshot } from "../bridge/state";
import { Button } from "../ui/Button";
export function AppSettings({
  snapshot,
  onSettings,
  onCheck,
  onLog,
  onRetrySave,
  onDiscard,
  onExit,
}: {
  snapshot: Snapshot;
  onSettings: (config: Config) => void;
  onCheck: () => void;
  onLog: () => void;
  onRetrySave: () => void;
  onDiscard: () => void;
  onExit: () => void;
}) {
  const [fontSize, setFontSize] = useState(snapshot.settings.font_size);
  return (
    <div className="settings-form">
      <label>
        Тема
        <select
          value={snapshot.settings.theme}
          onChange={(e) => {
            const value = e.target.value;
            if (value === "dark" || value === "light")
              onSettings({ ...snapshot.settings, theme: value });
          }}
        >
          <option value="dark">Тёмная</option>
          <option value="light">Светлая</option>
        </select>
      </label>
      <label>
        Масштаб интерфейса
        <select
          aria-label="Масштаб интерфейса"
          value={snapshot.settings.ui_scale}
          onChange={(event) => {
            const parsed = settingsSchema.shape.ui_scale.safeParse(Number(event.target.value));
            if (parsed.success) onSettings({ ...snapshot.settings, ui_scale: parsed.data });
          }}
        >
          {Array.from({ length: 15 }, (_, index) => 80 + index * 5).map((scale) => (
            <option key={scale} value={scale}>
              {scale}%{scale === 100 ? " — обычный" : ""}
            </option>
          ))}
          {snapshot.settings.ui_scale % 5 !== 0 && (
            <option value={snapshot.settings.ui_scale}>{snapshot.settings.ui_scale}%</option>
          )}
        </select>
      </label>
      <p className="form-intro">
        Масштаб меняет основной интерфейс: кнопки, панели, поля и текст. Настройки сохраняются
        автоматически в solveo-data/settings.json рядом с чатами и снимками.
      </p>
      <label>
        Размер текста <span className="range-value">{fontSize} px</span>
        <input
          type="range"
          min={14}
          max={30}
          value={fontSize}
          onChange={(e) => setFontSize(Number(e.target.value))}
          onPointerUp={() => onSettings({ ...snapshot.settings, font_size: fontSize })}
          onKeyUp={() => onSettings({ ...snapshot.settings, font_size: fontSize })}
          onBlur={() => {
            if (fontSize !== snapshot.settings.font_size)
              onSettings({ ...snapshot.settings, font_size: fontSize });
          }}
        />
      </label>
      <label className="switch-row">
        <span>Поверх всех окон</span>
        <input
          type="checkbox"
          checked={snapshot.settings.always_on_top}
          onChange={(e) => onSettings({ ...snapshot.settings, always_on_top: e.target.checked })}
        />
      </label>
      <div className="form-divider" />
      <h3>Подключение</h3>
      <p className="form-intro">
        {snapshot.provider.label}. Проверка выполнит отдельный короткий запрос и не изменит чат.
      </p>
      <div className="button-row">
        <Button disabled={!snapshot.controls.can_submit} onClick={onCheck}>
          Проверить соединение
        </Button>
        <Button onClick={onLog}>Журнал запроса</Button>
      </div>
      <div className="form-divider" />
      <h3>Горячие клавиши</h3>
      <dl className="shortcuts">
        <dt>Ctrl+Alt+S</dt>
        <dd>Снимок области и ответ</dd>
        <dt>Ctrl+Alt+A</dt>
        <dd>Добавить область в очередь</dd>
        <dt>Ctrl+Alt+F</dt>
        <dd>Снимок всего экрана и ответ</dd>
        <dt>Ctrl+Alt+Enter</dt>
        <dd>Отправить вопрос и очередь</dd>
        <dt>Ctrl+Alt+O</dt>
        <dd>Показать / скрыть окно</dd>
      </dl>
      <Button onClick={onExit}>Выйти из Solveo</Button>
      {snapshot.controls.can_discard && (
        <div className="recovery">
          <h3>Не удалось сохранить результат</h3>
          <p>
            Ответ остаётся в приложении. Повторите сохранение или явно начните новый чат без
            сохранения.
          </p>
          <Button className="primary" onClick={onRetrySave}>
            Повторить сохранение
          </Button>
          <Button className="danger" onClick={onDiscard}>
            Новый без сохранения
          </Button>
        </div>
      )}
    </div>
  );
}
