# История аудиосеансов

Авторизация: пользователь согласовал отдельные аудиосеансы и попросил сохранять
историю для разбора ошибок. Храним только текст, без записи звука.

Каждый StartListening создаёт отдельный сеанс (UTC, provider ID/label,
словарь, источники/устройства). Сохраняются только непустые окончательные
реплики с source stream/track, временем появления и final, ошибки источников
и отправки выбранных IDs в чат с ChatID/OperationID. Это не ground truth:
время — наблюдение приложения, не акустические timestamps. Новые слоты
stream→session замораживаются: поздний final старого потока не попадёт в новый
сеанс/провайдер. Очистка экрана не удаляет архив.

Хранение: solveo-data/audio-history/<32hex ID>.json, version 1, os.Root,
атомарная замена storage.Replace. Coalescing 1 с через существующий persister;
I/O никогда под audio.mu или c.mu. Подтверждённые final не зависят от лимита
300 отображаемых реплик. Корректный Close ждёт запись, ошибка видна в UI и
не выдаётся за успех; после аварии может отсутствовать последняя секунда.
Partial не сохраняется как окончательный. При выходе сначала остановить
захват/flush потоков, затем drain событий и финальная запись истории.

Файл содержит компактный summary перед полным session: список последних
200 файлов читает только заголовки. До 10000 final / 32 MiB на сеанс;
превышение и ошибки явно показываются, архив не обрезается молча. Старые
повреждённые/новые версии не перезаписывать. Никаких API-ключей, PCM или
provider state в документах.

UI: История в отдельном окне аудио; выбор сеанса, readonly список, страницы
по 300 реплик, provider/дата/ошибки/связанные отправки. Кнопки STT остаются
доступными при просмотре архива. Архив не загружается в live список и не
отправляется автоматически. Можно удалить выбранный завершённый сеанс с
подтверждением; текущий сеанс удалить нельзя. После удаления поздние события
не воссоздают файл.

- [x] Store + bounds/version/path/roundtrip/summary/delete tests.
- [x] Controller lifecycle + async persistence/retry/late finals/clear/eviction/
  submissions/close/restart tests.
- [x] IPC history list/detail/delete + read-only frontend picker/pagination tests.
- [x] Docs, независимое ревью, race и make check, Windows EXE.

Не входит: аудиозаписи, автоматический анализ качества или отправка вопросов,
восстановление уже потерянных RAM-транскриптов. Без коммита/push/PR.


Итог: отдельные version-1 файлы с summary/session, явные list/load/delete в
аудиоокне, readonly страницы 300, ссылки на отправки и диагностические ошибки.
Максимальный файл 32 MiB, предохранительный бюджет данных session 16 MiB;
грязные документы ограничены общим бюджетом 64 MiB с видимым предупреждением.
Saved completed docs освобождаются до кэша 4 / 32 MiB. Late events используют
Load вне audio.mu, writeMu сериализует Load/install с Save/Delete, pins держат
документ до окончания изменения. Индекс сегментов живёт только с UI300;
удалённые IDs — tombstones, не воскресают после late event/reload.

Ревью: исправлены count-limit, RAM retention и pin/reload гонки. Добавлены
регрессии отправок/ошибок при 10000 final, нескольких выгруженных сеансов,
поздних final, последней фразы на shutdown Flush, отказа диска/повтора Close.
Повторное узкое ревью pin/lock-order — без новых замечаний.

Проверено: go test -race ./internal/app ./internal/audiohistory
./internal/audiowindow — PASS; make check — PASS (97 frontend tests,
40 worker tests, Go suite, host+Windows lint, Windows vet/build). После
последнего улучшения preview отправленных фраз: frontend check + 20 audio
UI tests — PASS, make build — PASS, go test -race -tags bindings . — PASS.
Нативная проверка сохранения/перезапуска в Windows ещё требуется, чеклист
в docs/windows-acceptance.md. Реальные пользовательские данные не менялись.
