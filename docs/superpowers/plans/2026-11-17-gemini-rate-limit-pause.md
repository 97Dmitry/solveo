# Gemini 429: pause and explicit resume

Выбран разрешённый пользователем вариант «Продолжить». HTTP 429 файловой обработки встречи (ASR/summary) ставит операцию на паузу с сохранённым ID/типом/partial, не выдаётся за неверный ключ и не вызывает автоматических повторных POST. Готовые части и промежуточные summary уже сохраняются в архиве.

- [x] Fake HTTP: 429 → typed RateLimitError, Retry-After seconds/date и google.rpc.RetryInfo; дневной лимит распознаётся по QuotaFailure. Прочие ошибки не меняются, vendor bodies не выводятся.
- [x] Manager: пауза отдельно от Error/cleanup Notice. Сохранить исходную операцию/partial/retry_at; не принимать повтор раньше Retry-After; очистка временных файлов остаётся отдельным этапом. Новый запуск/успешное продолжение очищает паузу; State клонирует вложенную структуру.
- [x] UI: причина/время ожидания и «Продолжить» с исходным ID (независимо от выбранной встречи), кнопка активна после срока. Повторные обычные кнопки соответствующей операции также соблюдают cooldown. После перезапуска — существующие checkpoints и обычное явное действие, без автоматической отправки.
- [x] Тесты: 429 на второй части, повтор без ASR первой части, summary partial resume, cooldown/clone, UI исходный ID и отсутствие alert на паузе. Bindings и инструкции обновить.
- [x] make check, affected race suite, read-only review; обновить EXE в Downloads без остановки приложения.

Источники: https://ai.google.dev/gemini-api/docs/rate-limits (RPM/TPM/RPD, project-wide); https://ai.google.dev/gemini-api/docs/troubleshooting (bounded retries for transient errors; автоматический retry в этой правке заменён явным пользовательским продолжением).

Проверка: make check exit 0 (117 → 120 frontend tests), full meeting race suite и targeted 429 tests с -race пройдены. Независимое ревью: Important/Critical нет. Финальная Windows-сборка и checksum доставки проверены; реальные provider requests не выполнялись, архивы пользователя не изменялись.
Доставка: %USERPROFILE%\Downloads\Solveo.exe

Расширение 503: общий typed RetryableError (429/503), прежняя логика checkpoint/resume, подсказка Retry-After/RetryInfo, по умолчанию 15 секунд. Fake HTTP и summary resume-тесты; без автоматического POST replay. Пользователь разрешил ветку/commit/push/PR/merge и публикацию через существующий master CI.
