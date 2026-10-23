import { useEffect, useRef, useState } from "react";
import type { ChatConfig, Profile, ProfileInput, Snapshot } from "../bridge/state";
import { errorText, profileInputSchema } from "../bridge/state";
import { Button } from "../ui/Button";

export type ProfileActions = {
  save: (input: ProfileInput) => Promise<boolean>;
  remove: (id: string, revision: number) => Promise<boolean>;
  reload: () => Promise<boolean>;
  create: (id: string, revision: number) => Promise<boolean>;
};
export function ProfileControls({
  snapshot,
  config,
  dirty,
  navigationDirty,
  disabled,
  actions,
  onLoad,
  onClose,
  canUseConfig,
  onBusy,
}: {
  snapshot: Snapshot;
  config: ChatConfig;
  dirty: boolean;
  navigationDirty: boolean;
  disabled: boolean;
  actions: ProfileActions;
  onLoad: (config: ChatConfig) => void;
  onClose: () => void;
  canUseConfig: (config: ChatConfig) => boolean;
  onBusy: (busy: boolean) => void;
}) {
  const [selectedID, setSelectedID] = useState("");
  const [selectedRevision, setSelectedRevision] = useState(0);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [confirmation, setConfirmation] = useState<{
    kind: "load" | "delete" | "new";
    profile: Profile;
  } | null>(null);
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const selected = snapshot.profiles.find((p) => p.id === selectedID);
  const blocked = disabled || busy;
  const libraryBlocked = blocked || snapshot.profiles_error !== "";
  const run = async (action: () => Promise<boolean>, success: string, close = false) => {
    if (blocked) return;
    setBusy(true);
    onBusy(true);
    setError("");
    setNotice("");
    try {
      const ok = await action();
      if (!mounted.current) return;
      if (!ok)
        setError("Действие с профилем не выполнено. Проверьте сообщение об ошибке приложения.");
      else if (close) onClose();
      else setNotice(success);
    } catch (cause) {
      if (mounted.current) setError(errorText(cause));
    } finally {
      if (mounted.current) {
        setBusy(false);
        onBusy(false);
      }
    }
  };
  const save = (replace: boolean) => {
    const input = profileInputSchema.safeParse({
      id: replace ? selected?.id : "",
      name,
      config,
      expected_revision: replace ? selectedRevision : 0,
    });
    if (!input.success) {
      setError("Проверьте название профиля (1–160 символов) и поля подготовки (до 1 МиБ каждое).");
      return;
    }
    void run(
      async () => {
        const ok = await actions.save(input.data);
        if (ok && mounted.current) {
          if (replace) setSelectedRevision(input.data.expected_revision + 1);
          else {
            setSelectedID("");
            setSelectedRevision(0);
          }
        }
        return ok;
      },
      replace
        ? "Профиль обновлён. Созданные чаты не изменены."
        : "Профиль сохранён. Настройки текущего чата не изменены.",
    );
  };
  const loadProfile = (profile: Profile) => {
    setSelectedID(profile.id);
    setSelectedRevision(profile.revision);
    setName(profile.name);
    onLoad({ ...profile.config });
    setNotice("Профиль загружен в форму. Сохраните преднастройку, чтобы применить к чату.");
  };
  const selectProfile = (id: string) => {
    if (libraryBlocked) return;
    setError("");
    setNotice("");
    setConfirmation(null);
    const profile = snapshot.profiles.find((p) => p.id === id);
    if (!profile) {
      setSelectedID("");
      setSelectedRevision(0);
      setName("");
      return;
    }
    if (dirty && JSON.stringify(config) !== JSON.stringify(profile.config)) {
      setConfirmation({ kind: "load", profile: { ...profile, config: { ...profile.config } } });
    } else {
      loadProfile(profile);
    }
  };
  const request = (kind: "delete" | "new") => {
    if (!selected || libraryBlocked) return;
    if (kind === "new" && !navigationDirty) {
      void run(() => actions.create(selected.id, selectedRevision), "", true);
      return;
    }
    setConfirmation({
      kind,
      profile: { ...selected, revision: selectedRevision, config: { ...selected.config } },
    });
  };
  const confirm = () => {
    if (!confirmation || libraryBlocked) return;
    const { kind, profile } = confirmation;
    setConfirmation(null);
    if (kind === "load") {
      loadProfile(profile);
    } else if (kind === "delete") {
      void run(
        () => actions.remove(profile.id, profile.revision),
        "Профиль удалён. Созданные чаты не изменены.",
      );
    } else {
      void run(() => actions.create(profile.id, profile.revision), "", true);
    }
  };
  return (
    <section className="profile-controls" aria-label="Профили подготовки интервью">
      {snapshot.profiles_error && <p role="alert">{snapshot.profiles_error}</p>}
      <div className="profile-row">
        <label>
          Профиль подготовки
          <select
            value={
              confirmation?.kind === "load" ? confirmation.profile.id : selected ? selectedID : ""
            }
            disabled={libraryBlocked}
            onChange={(e) => selectProfile(e.target.value)}
          >
            <option value="">
              {snapshot.profiles.length ? "Выберите профиль" : "Профилей пока нет"}
            </option>
            {snapshot.profiles.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} · {new Date(p.updated_at).toLocaleString("ru-RU")}
              </option>
            ))}
          </select>
        </label>
        <Button
          disabled={
            libraryBlocked || !selected || !canUseConfig(selected.config) || confirmation !== null
          }
          title="Создать новый чат с сохранённой версией профиля"
          onClick={() => request("new")}
        >
          Новый чат из профиля
        </Button>
      </div>
      {selected && !canUseConfig(selected.config) && (
        <p role="alert">
          Модель или reasoning профиля недоступны. Исправьте параметры в форме и обновите профиль.
        </p>
      )}
      <details className="profile-manage">
        <summary>Сохранить или изменить профиль</summary>
        <p className="form-intro">
          Профиль хранит поля подготовки ниже. Новый чат получает его копию и текущие общие
          инструкции; изменение профиля не меняет созданные чаты.
        </p>
        <label>
          Название профиля
          <input value={name} disabled={blocked} onChange={(e) => setName(e.target.value)} />
        </label>
        <div className="button-row">
          <Button disabled={libraryBlocked || confirmation !== null} onClick={() => save(false)}>
            Сохранить как профиль
          </Button>
          <Button
            disabled={libraryBlocked || !selected || confirmation !== null}
            onClick={() => save(true)}
          >
            Обновить профиль
          </Button>
          <Button
            className="danger"
            disabled={libraryBlocked || !selected || confirmation !== null}
            onClick={() => request("delete")}
          >
            Удалить профиль
          </Button>
          <Button
            disabled={blocked || confirmation !== null}
            onClick={() => {
              void run(actions.reload, "Профили перечитаны.");
            }}
          >
            Перечитать профили
          </Button>
        </div>
      </details>
      {confirmation && (
        <div
          role="group"
          className="profile-confirm"
          aria-label="Подтверждение действия с профилем"
        >
          <p>
            {confirmation.kind === "delete"
              ? `Удалить профиль «${confirmation.profile.name}»? Созданные чаты сохранятся.`
              : confirmation.kind === "load"
                ? "Заменить несохранённые поля формы настройками профиля?"
                : "Создать чат из сохранённого профиля? Несохранённые изменения формы не попадут в новый чат."}
          </p>
          <div className="form-actions">
            <Button
              className={confirmation.kind === "delete" ? "danger" : "primary"}
              disabled={
                blocked ||
                (confirmation.kind === "new" && !canUseConfig(confirmation.profile.config))
              }
              onClick={confirm}
            >
              {confirmation.kind === "delete"
                ? "Подтвердить удаление"
                : confirmation.kind === "load"
                  ? "Заменить поля"
                  : "Создать из сохранённого профиля"}
            </Button>
            <Button disabled={blocked} onClick={() => setConfirmation(null)}>
              Оставить форму
            </Button>
          </div>
        </div>
      )}
      {busy && <p role="status">Сохраняю изменения профилей…</p>}
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
    </section>
  );
}
