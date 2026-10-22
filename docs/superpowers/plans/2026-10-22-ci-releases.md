# CI and releases implementation plan

**Цель:** сохранить существующие четыре проверки CI и добавить автоматические сборочные релизы Windows x64 из проверенного артефакта.

**Архитектура:** существующий `.github/workflows/ci.yml`, отдельный job публикации после `go-tests`, `checks`, `windows`, `build`. PR и manual dispatch проверяют проект; только push в `master` публикует `build-N`. Нумерация сборочная, независимо от productVersion Wails. Повторный запуск продолжает draft или пропускает опубликованный релиз. Публикация сериализована; устаревший commit master пропускается.

**Ограничения:** сохранить имена обязательных jobs; read-only permissions кроме release job; SHA-pinned actions; без реальных LLM-запросов, подписания, installer, push и публикации из текущей сессии. Сборка не подтверждает Windows UI/capture acceptance.

- [x] Добавить checksum и передать точный artifact ID из build в release. Публиковать EXE и SHA256SUMS.txt без повторной сборки.
- [x] Добавить `.github/scripts/publish-release.sh`: checksum verification, проверка актуальности master, draft/upload/publish, безопасный rerun без замены опубликованных файлов.
- [x] Сохранить отмену устаревших PR/manual runs; не прерывать master workflow во время публикации. Сериализовать release jobs.
- [x] Обновить `.github/AGENTS.md` и README: скачивание, checksum, unsigned build, разделение CI и ручной Windows acceptance.
- [x] Проверить actionlint, shell syntax, сценарии публикации с fake gh (без сетевых мутаций), `make check` и `git diff --check`.

## Проверка результата

- `actionlint` и `bash -n .github/scripts/publish-release.sh`: успешно.
- `python3 .github/scripts/test_publish_release.py`: 8 тестов, включая checksum mismatch, устаревший commit, ошибку API, незавершённую загрузку, повтор draft и запрет замены опубликованного релиза. Тесты включены в CI.
- `make check`: успешно; frontend checks/tests, Go tests/lint, Windows vet и Wails cross-build.
- `go test -race ./internal/...`: успешно.
- `git diff --check`: успешно.
- Push, удалённый запуск Actions и настоящая публикация не выполнялись. Windows runtime acceptance остаётся отдельной ручной проверкой.

Решение при ревью: включить event_name в workflow concurrency group, чтобы manual dispatch не мог отменить публикацию master. Публикация остаётся в исходном workflow, поэтому отдельный привилегированный workflow_run и дополнительная граница доверия не нужны.
