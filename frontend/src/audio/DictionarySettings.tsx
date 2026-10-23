import { useState } from "react";
import type { AudioState } from "../bridge/state";
import { Button } from "../ui/Button";

const maxPhrases = 256;
const maxRunes = 128;
const split = (text: string) =>
  text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");

// DictionarySettings edits the recognition dictionary, one term per line. Go trims, deduplicates,
// limits and persists it; the list applies at the next listening start. Unsaved text survives
// polling unless the saved dictionary changes while the field is untouched.
export function DictionarySettings({
  dictionary,
  audio,
  disabled,
  onSave,
}: {
  dictionary: string[];
  audio: AudioState;
  disabled: boolean;
  onSave: (phrases: string[]) => void;
}) {
  const saved = dictionary.join("\n");
  const [draft, setDraft] = useState({ base: saved, text: saved });
  if (draft.base !== saved)
    setDraft({ base: saved, text: draft.text === draft.base ? saved : draft.text });
  const phrases = split(draft.text);
  const tooMany = phrases.length > maxPhrases;
  const tooLong = phrases.find((phrase) => Array.from(phrase).length > maxRunes);
  const changed = phrases.join("\n") !== saved;
  const limits = audio.provider.max_phrases > 0 ? audio.provider : null;
  return (
    <section className="settings-section dictionary" aria-label="Словарь терминов">
      <h2>Словарь терминов</h2>
      <p className="audio-note" id="dictionary-help">
        По одному термину на строку: названия технологий, библиотек и аббревиатуры, которые нужно
        писать точно. Общий для всех чатов.
      </p>
      <textarea
        aria-label="Словарь терминов для распознавания"
        aria-describedby="dictionary-help"
        placeholder={"Kafka\ngRPC\nPostgreSQL"}
        spellCheck={false}
        value={draft.text}
        onChange={(event) => setDraft({ ...draft, text: event.target.value })}
      />
      <p className="audio-note">
        Терминов: {phrases.length}
        {limits &&
          ` · ${limits.label}: до ${limits.max_phrases} по ${limits.max_phrase_runes} символов`}
      </p>
      {tooMany && <p role="alert">Не более {maxPhrases} терминов</p>}
      {tooLong && (
        <p role="alert">
          Термин длиннее {maxRunes} символов: «{Array.from(tooLong).slice(0, 24).join("")}…»
        </p>
      )}
      <div className="audio-controls">
        <Button
          className="primary"
          disabled={disabled || tooMany || tooLong !== undefined || !changed}
          onClick={() => onSave(phrases)}
        >
          Сохранить словарь
        </Button>
        <Button
          disabled={draft.text === saved}
          onClick={() => setDraft({ base: saved, text: saved })}
        >
          Отменить
        </Button>
      </div>
      <p className="audio-note">
        {audio.listening
          ? "Идёт прослушивание: изменения применятся после следующего запуска."
          : "Применяется при следующем запуске прослушивания."}
      </p>
    </section>
  );
}
