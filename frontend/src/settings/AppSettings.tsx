import { useEffect, useState } from "react";
import {
  errorText,
  settingsSchema,
  globalInstructionsSchema,
  type Account as AccountState,
  type AppSettings as Config,
  type Hotkeys,
  type Snapshot,
} from "../bridge/state";
import { Button } from "../ui/Button";
import { HotkeySettings } from "./HotkeySettings";
export type ConnectionActions = {
  loadAccount: () => Promise<AccountState>;
  onLogin: () => void;
  onCancelLogin: () => void;
  onOpenLink: (url: string) => void;
};
export type HotkeyBridge = {
  loadDefaults: () => Promise<Hotkeys>;
  suspend: (on: boolean) => Promise<void>;
};
export function AppSettings({
  snapshot,
  busy = false,
  hotkeyActions,
  connection,
  onSettings,
  onCheck,
  onLog,
  onRetrySave,
  onDiscard,
  onExit,
}: {
  snapshot: Snapshot;
  busy?: boolean;
  hotkeyActions: HotkeyBridge;
  connection: ConnectionActions;
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
      <GlobalInstructions settings={snapshot.settings} onSettings={onSettings} />
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
        {snapshot.provider.label} с отдельным профилем Solveo: ваши настройки Codex, плагины и
        skills не используются, поэтому нужен собственный вход. Проверка выполнит отдельный короткий
        запрос и не изменит чат.
      </p>
      <Account login={snapshot.login} actions={connection} />
      <div className="button-row">
        <Button disabled={!snapshot.controls.can_submit} onClick={onCheck}>
          Проверить соединение
        </Button>
        <Button onClick={onLog}>Журнал запроса</Button>
      </div>
      <div className="form-divider" />
      <h3>Горячие клавиши</h3>
      <HotkeySettings
        hotkeys={snapshot.settings.hotkeys}
        errors={snapshot.hotkey_errors}
        disabled={busy}
        onSave={(hotkeys) => onSettings({ ...snapshot.settings, hotkeys })}
        loadDefaults={hotkeyActions.loadDefaults}
        suspend={hotkeyActions.suspend}
      />
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

// Account shows the sign-in state of Solveo's own Codex profile and runs the
// device-code login. Go owns the login state.
function Account({ login, actions }: { login: Snapshot["login"]; actions: ConnectionActions }) {
  const [account, setAccount] = useState<{ key: string; value: AccountState | string } | null>(
    null,
  );
  // Re-read the account whenever the login outcome changes.
  const key = `${login.state}:${login.id}`;
  const load = actions.loadAccount;
  useEffect(() => {
    let live = true;
    load().then(
      (value) => {
        if (live) setAccount({ key, value });
      },
      (error: unknown) => {
        if (live) setAccount({ key, value: errorText(error) });
      },
    );
    return () => {
      live = false;
    };
  }, [key, load]);
  const status = account?.key === key ? account.value : "";
  return (
    <>
      <div className="connection-account" role="status">
        {status === ""
          ? "Проверяю вход…"
          : typeof status === "string"
            ? status
            : status.signed_in
              ? `Вход выполнен${status.kind ? ` (${status.kind})` : ""}`
              : "Вход не выполнен"}
      </div>
      {login.state === "pending" && (
        <div className="login-prompt">
          <p>Откройте страницу входа и введите код:</p>
          <p className="login-code">{login.code}</p>
          <div className="button-row">
            <Button className="primary" onClick={() => actions.onOpenLink(login.url)}>
              Открыть страницу входа
            </Button>
            <Button onClick={actions.onCancelLogin}>Отменить вход</Button>
          </div>
        </div>
      )}
      {login.state === "failed" && (
        <div className="login-prompt">
          <p>{login.error || "Вход не выполнен"}</p>
          <Button onClick={actions.onCancelLogin}>Закрыть</Button>
        </div>
      )}
      {login.state !== "pending" && typeof status !== "string" && !status.signed_in && (
        <Button className="primary" onClick={actions.onLogin}>
          Войти в Codex
        </Button>
      )}
    </>
  );
}

function GlobalInstructions({
  settings,
  onSettings,
}: {
  settings: Config;
  onSettings: (config: Config) => void;
}) {
  const [draft, setDraft] = useState({
    base: settings.global_instructions,
    text: settings.global_instructions,
  });
  if (draft.base !== settings.global_instructions) {
    setDraft({
      base: settings.global_instructions,
      text: draft.text === draft.base ? settings.global_instructions : draft.text,
    });
  }
  const text = draft.text;
  const parsed = globalInstructionsSchema.safeParse(text);
  return (
    <>
      <label>
        Общие инструкции
        <textarea
          rows={6}
          value={text}
          aria-describedby="global-instructions-help"
          onChange={(event) => setDraft({ ...draft, text: event.target.value })}
        />
      </label>
      <p id="global-instructions-help" className="form-intro">
        Применяется к новым чатам
      </p>
      <p className="form-intro">
        Общие сведения о вас и пожелания к ответам. Преднастройка чата дополняет их и имеет
        приоритет. До 16384 символов.
      </p>
      {!parsed.success && <p role="alert">Не более 16384 символов Unicode</p>}
      <Button
        disabled={!parsed.success || text === settings.global_instructions}
        onClick={() => {
          const value = globalInstructionsSchema.safeParse(text);
          if (value.success) onSettings({ ...settings, global_instructions: value.data });
        }}
      >
        Сохранить общие инструкции
      </Button>
      <div className="form-divider" />
    </>
  );
}
