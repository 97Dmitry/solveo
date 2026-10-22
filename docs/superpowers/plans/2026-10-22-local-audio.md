# Этап 9: локальное распознавание аудио — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Этот документ подготовлен для обсуждения; он не разрешает начинать реализацию, делать commit/push или публиковать сборку. Отдельные агенты могут независимо рецензировать документ по запросу пользователя.

**Статус:** 2026-10-23 — этап 9.0 в работе: инструмент `tools/audio-bench/` готов, быстрый отсев (30 записей, один говорящий) выполнен, решения зафиксированы в «Решения 9.0» (§3). Код приложения не изменён.

**Goal:** получать вопрос из системного звука и/или микрофона, распознавать его локально с сохранением русско-английской речи, давать проверить текст и отправлять через существующий механизм чата.

**Architecture:** захват и нормализация PCM в Go → отдельный STT-провайдер → transcript и карточки в Go. Один STT-worker сохраняет модель загруженной, исполняет VAD/endpointing по фиксированной конфигурации и ведёт отдельное stream state на каждый track; аудио не занимает слот LLM. Карточка вопроса отделена от обычного черновика; после подтверждения используется общий pipeline сохранения, вызова провайдера и recovery.

**Tech Stack:** существующие Go 1.27.1 / Wails v2 / React / TypeScript / pnpm; WASAPI shared-mode. По окончательному выбору пользователя первым реализуется Qwen3-ASR-1.7B без квантования, BF16 + CUDA, официальный streaming API qwen-asr/vLLM в локальном Docker-worker. `stt.Provider` сохраняется; Nemotron проверен на отсеве и отклонён для mixed RU/EN («Решения 9.0»). У пользователя есть драйвер, WSL2 и Docker. Корректность mixed RU/EN и IT-терминов приоритетнее минимальной задержки; hardware/model/runtime gates 9.0 остаются обязательными.

**Spec:** [roadmap, §9](../../roadmap/2026-10-21-core-features.md#9-аудио--отдельный-поздний-этап), [архитектура, §9.2](../../specs/solveo-design.md#92-будущий-аудиоввод). Этот план уточняет этап, но не меняет остальные пункты roadmap.

## 1. Проблема, ограничения и решения

Требование пользователя: локальное распознавание, включая русские и английские слова в одном предложении. Качество важнее выбора конкретного бренда модели. «Понимает всё» недостижимо как гарантия: аббревиатуры, неизвестные идентификаторы, акцент, шум и наложение речи дают ошибки даже у больших моделей. Многоязычность и лидерство на монолингвальном benchmark не доказывают качество code-switching.

Целевой ПК по данным пользователя: AMD Ryzen 7 6800H, NVIDIA GeForce RTX 3070 Ti Laptop GPU, 32 ГБ RAM, 8 ГБ VRAM. Основной кандидат ускорения для benchmark — CUDA; совместимость конкретного runtime с драйвером ещё нужно проверить. Версия Windows, драйвер, GPU power limit и режим питания ноутбука пока неизвестны. Указанное пользователем «6 ГГц» не интерпретировано и не используется для расчётов. Все бюджеты ниже — предложения для ревью, а не измеренные свойства этого ПК.

WSL2, Docker и NVIDIA-драйвер уже установлены по сообщению пользователя. Проверка плана не включает их установку, запуск контейнеров или загрузку весов. При CUDA совместимый драйвер нужен в любом варианте; CUDA Toolkit/компилятор нужны машине сборки native runtime, а не обязательно конечному пользователю.

### Global Constraints

- Windows x64; разработка в текущем checkout под WSL2 Arch. Linux-тесты и cross-build не подтверждают Windows capture/runtime.
- Go владеет бизнес-состоянием. Wails UI thread и native host thread не выполняют захват и inference. Для COM-аудиоклиента отдельный корректно инициализированный locked OS thread; HWND этому worker не нужен.
- STT не является методом `llm.Provider`; не использовать Codex для транскрибации и не запускать фоновые Codex-запросы для выделения вопроса.
- Локальность относится к аудио и STT: нет облачного fallback, удалённых STT API и отправки звука. Подтверждённый текст отправляется выбранному LLM-провайдеру по обычным правилам Solveo; это не полностью офлайн-помощник.
- По умолчанию прослушивание выключено; запуск только явным действием. После перезапуска приложения, смены устройства и аварии не начинать запись автоматически. Renderer reload только подключается к существующему snapshot/stream, не останавливает и не запускает capture заново.
- Источники system/microphone раздельны; источник не равен личности говорящего. Не обещать diarization и AEC. Для режима с микрофоном рекомендовать наушники; звук из колонок может дублироваться.
- Существующий draft и PNG queue не изменяются аудиооперациями. Отправка карточки не прикладывает снимки из обычной очереди молча. Аудио+изображения — последующее явное расширение.
- Предварительный transcript исправляемый; отправленный вопрос, происхождение и параметры попытки неизменны. Нет автоотправки и автоматического повторения неизвестного LLM-исхода.
- Данные в `solveo-data` рядом с EXE, атомарная запись через существующий storage; UTC для persisted timestamps, монотонное/sample время для аудио.
- Проверки не используют настоящие LLM-запросы. Реальные STT benchmark и Windows acceptance запускаются отдельно от обычного CI.
- Новые сложные модули получают короткие `AGENTS.md` и соседние `CLAUDE.md` с `@AGENTS.md`; существующие инструкции обновляются только при изменении инвариантов.

### Review Focus

1. Английский термин внутри русской фразы превращается в другое слово, перевод или неверное отрицание — этап 9.0 проверяет terms, числа и смысл, отдельно от WER.
2. Пауза внутри условия, длинная речь и перебивание — этапы 9.2/9.3 проверяют границы и явные gap, не принимают паузу за конец вопроса.
3. Переключение чата/удаление во время STT, late result и renderer reload — этапы 9.3/9.5 проверяют владельца и session generation.
4. Crash между сохранением вопроса и acknowledgement карточки — этап 9.4 проверяет reconciliation без второго запроса.
5. GPU OOM, медленный inference, unplug, sleep и неудачный выход — этапы 9.1/9.2/9.6 проверяют ограничение ресурсов и доступность обычного чата.

## 2. Объём поставки и последовательность

| Подэтап | Проверяемый результат | Условие перехода |
| --- | --- | --- |
| 9.0 | Корпус, benchmark и решение model/runtime/hardware | Приемлемы смешанная речь и скорость, проверен путь Windows→Docker→GPU |
| 9.1 | Один управляемый локальный STT-worker, inference из PCM | Offline smoke, отмена и crash isolation |
| 9.2 | Захват system/mic, нормализация и сегментация | Нативная проверка устройств и bounded pipeline |
| 9.3 | Сессия transcript и ручная карточка | Владение чатом, версии, границы и reload корректны |
| 9.4 | Долговечная карточка и отправка общим pipeline | Crash/retry/save invariants выполнены |
| 9.5 | Рабочий UI всего ручного сценария | Пользователь может проверить и отправить вопрос |
| 9.6 | Windows acceptance и документация | Полная приёмка на целевом ПК |
| 9.7 | Автоматические предложения карточек, отдельный план | Только после оценки ручной версии; автоотправка вне scope |

Первый поставляемый вариант — 9.0–9.6: явный start/stop, локальный transcript, ручной выбор реплик, редактирование, подтверждение. Сегментация речи автоматическая, определение смысла вопроса пока ручное. Это соответствует roadmap: автоматическое предложение идёт затем.

## 3. Выбор модели: этап 9.0 является обязательным gate

### Основной выбор, streaming и резерв

**Решение пользователя:** Qwen3-ASR-1.7B первым, без квантования, через отдельный `stt.Provider`; Nemotron 3.5 ASR Streaming 0.6B — резерв. Причина выбора — приоритет правильных mixed RU/EN технических фраз над минимальной задержкой. Это продуктовый приоритет, а не доказанное превосходство на нашем корпусе.

В присланном пользователем обзоре нет исходных ссылок на независимый benchmark и исследование потерь Nemotron. Цифры WER/VRAM/RTF из него и объяснение через RNNT cache не считаются проверенными данными Solveo. Не переносить monolingual test-clean на RU/EN интервью и RTX 3070 Ti Laptop. Новые сравнительные кандидаты не являются обязательным турниром до первой проверки Qwen.

**Проверено по официальному wrapper qwen-asr:** `init_streaming_state(context=..., language=None, chunk_size_sec=...)`, `streaming_transcribe(pcm16k, state)`, `finish_streaming_transcribe(state)`. Этот API работает с vLLM; не поддерживает batch streaming и timestamps. State содержит накопленное аудио реплики, очередной decode повторно использует его целиком; partial может ревизоваться. Это отличается от cache-aware RNNT. Финиш обрабатывает оставшийся неполный chunk, если он есть; не предполагать дополнительный final decode при пустом хвосте. Привязать поведение к pinned commit, а не к меняющемуся main.

**Ограничение роста:** отдельный Qwen streaming state на каждую реплику/track. Worker исполняет VAD/endpointing, включает pre-roll один раз, вызывает finish при конце речи и создаёт новый state на следующую реплику. Максимум 20 с аудио на state, затем forced final с явной границей и новый UtteranceID/state; не ждать паузы бесконечно. Не переносить весь разговор, предыдущие partial или последние audio windows в новый state автоматически. Captured PCM подаётся worker один раз; повторная обработка ограниченного буфера внутри Qwen — часть алгоритма, отдельно учитывается в latency/RTF.

**Один экземпляр модели, два состояния:** системный звук и микрофон, scheduling decode calls последовательно и справедливо между tracks. Отсутствие batching не запрещает чередовать два state, но это проверяется на реальном wrapper: не предполагать thread safety или concurrent model.generate. Приём PCM/VAD не должен ждать inference под общим lock; очереди bounded (§5). Проверить именно одновременную речь обоих tracks, включая очередь final/preview на длинных репликах. Если real time не достигается, представить измерения; не отключать источник/partial и не менять модель молча.

**Начальная конфигурация эксперимента:** BF16, без INT8/INT4, одна модель, без forced aligner. Model inference chunk 1 с; сравнить 2 с на том же dev-наборе. Исходные prefix rollback параметры взять из pinned upstream, зафиксировать и проверить перестановки/потери слов. `gpu_memory_utilization=0.7` — только начальная настройка vLLM, не лимит или гарантия peak VRAM; проверить CUDA graph/workspace/cache и память Windows. Грубая оценка весов ~3.4 ГБ не заменяет peak замер. Context length/output tokens задаются явно по эксперименту; truncation не превращается в успешный final. Проверить параметры поддерживаемой версии, не копировать большие server defaults. Совместимые qwen-asr/vLLM/torch/CUDA versions зафиксировать после smoke; не выполнять unpinned pip install при старте.

**Streaming final против offline (обязательное сравнение 9.0):** официальный `streaming_transcribe` на каждом chunk повторно подаёт всё накопленное аудио реплики с prompt из предыдущего вывода без последних `unfixed_token_num` (upstream default 5) токенов; `finish_streaming_transcribe` использует тот же prefix. Ошибка в начале реплики, например неверный термин, фиксируется в prompt и не исправляется финалом, поэтому streaming final может быть хуже offline `transcribe()` той же реплики, а offline-результаты из обзоров не переносятся на streaming автоматически. Для каждой реплики dev-набора сохранять три гипотезы на одном PCM с теми же границами VAD, context, language и model revision: streaming final, offline `transcribe()` всей реплики и reference. Сравнивать mixed WER/CER, точность терминов, числа/отрицания и отдельно ошибки, унаследованные из ранних partial (различие в начале реплики при совпадающем хвосте). Offline-проход не является вторым кандидатом модели.

**Гибридный final той же моделью (выбран 2026-10-23, см. «Решения 9.0»):** partial во время речи даёт streaming state; на endpoint, forced 20-секундной границе или Flush final формируется одним offline `transcribe()` по всему PCM реплики (включая pre-roll и хвост) с тем же frozen context/language, а `finish_streaming_transcribe` не вызывается. Тот же загруженный экземпляр модели, без второй модели и без загрузки весов; на pinned версии проверить, что offline и streaming вызовы работают на одном vLLM-экземпляре и сериализуются с decode steps обоих tracks. Цена — дополнительный полный проход на реплику: измерить его вклад в p95 final latency, очередь и RTF при двух активных tracks. Final по-прежнему один на UtteranceID и может полностью заменить последний partial. Предлагаемое правило выбора для ревью: гибрид, если offline final даёт заметный выигрыш (ориентир — ≥2 п.п. точности терминов или устраняет критичные ошибки чисел/отрицаний/терминов, которых нет в streaming final) и укладывается в gates §3; иначе streaming final. Выбор и измерения фиксируются в отчёте до holdout; режим final — неизменяемая часть ModelConfig сессии, не переключается во время записи.

**IT-словарь:** ContextPhrases преобразуется Qwen-адаптером в bounded `context` при создании streaming state. Это контекстная подсказка, не RNNT word boosting с весами. Язык `auto` соответствует `language=None`; принудительный Russian проверить только как dev-сравнение. Контекст и его эффект тестируются именно в streaming API, а не только offline. Короткие названия вроде NestJS/Next.js, TypeORM/QueryBuilder, PostgreSQL и JWT включить в критичные примеры. Автоматическая фонетическая нормализация/замена вне scope: она способна подменить правильно распознанное название похожим; первая версия сохраняет raw STT текст и ручную правку карточки.

**Время и источники:** у transcript сохраняются sample boundaries VAD-реплики, а не обещанные word timestamps. Метки system/microphone означают источник, не установленную личность говорящего. Частичный текст непостоянен до final; только final доступен для карточки/подтверждения.

**Резерв (Nemotron проверен 2026-10-23 и отклонён, см. «Решения 9.0»):** если Qwen не проходит quality/latency gates, после отчёта отдельно рассматривается Nemotron через тот же `stt.Provider`. Его cache-aware streaming, собственный runtime и word boosting проверяются своим адаптером; не добавлять второй runtime/model сейчас. Whisper и Parakeet остаются возможными исследовательскими baseline, без обязательных тестов всех моделей и без controls-заглушек в UI. Нет автоматического переключения между провайдерами.

### Корпус и оценка

- [x] Проверить на целевом ПК версию драйвера/Windows, свободную RAM/VRAM, GPU power limit и режим питания. CPU/GPU уже указаны в §1. Основные замеры выполнять от сети с зафиксированным режимом питания и после прогрева; отдельно проверить конкуренцию за GPU и длительную нагрузку ноутбука. Проверить существующий минимальный Windows target перед выбором API.
- [x] Быстрый отсев до полного корпуса: 20–30 собственных mixed RU/EN фрагментов с IT-терминами через Qwen streaming/vLLM, auto language, с context и без; для каждого фрагмента также offline `transcribe()` той же реплики, чтобы рано увидеть разрыв streaming/offline final. Проверить потери слов на смене языка, повторения и влияние схожих терминов. При систематических ошибках представить примеры до дальнейшей интеграции. Отсев не заменяет holdout.
- [ ] Подготовить разрешённый для использования корпус: минимум 120 фрагментов / 30 минут, минимум 60 mixed RU/EN; обычный RU, EN, тишина/шум, числа/единицы, акценты, длинные условия, паузы и перебивания. Не менее 5 говорящих; часть — реальные записи созвонов, а не только студийная речь.
- [ ] Разделить dev/holdout примерно 2:1 без пересечения говорящих/исходных разговоров. Эталон размечает человек; исходник, ожидаемое написание терминов и допустимые варианты задаются до запуска. Записи и полный transcript не коммитить без проверки права на распространение.
- [ ] Включить примеры «почему context cancellation не остановил goroutine», «сравни read committed и repeatable read», «timeout не меньше 150 milliseconds», «O(n log n), не O(n squared)», буквенные аббревиатуры и непривычные названия. Эти примеры — тестовые условия, а не обещание восстановления точного написания незнакомого идентификатора из произношения.
- [x] Добавить `tools/audio-bench/` с инструкциями и воспроизводимым manifest: sample ID/hash, reference, terms/variants, split; runner принимает локальные WAV, сохраняет hypothesis и timings. Живой вывод модели не является эталоном.
- [x] Считать общий и mixed WER/CER с документированной Unicode/token normalization; отдельно точность терминов и долю фрагментов с сохранёнными числами/отрицаниями/ограничениями. Пунктуацию не выдавать за смысловую ошибку, а WER не использовать как единственный gate.
- [ ] Замерить cold load, задержку partial относительно звука, final latency после конца речи, end-to-end RTF, загрузку GPU при прослушивании тишины, peak RAM/VRAM, потерянные интервалы, очередь, размер runtime/весов; сохранять model hash, runtime commit/version, backend, quantization, threads и decoding settings.
- [ ] Сравнить без glossary и с ним; затем прогнать holdout без подгонки. Проверить минимум 30 минут live pipeline: отсутствие растущей задержки, работа обычного чата/снимков при STT.

**Предлагаемые начальные gates для ревью:** mixed WER ≤15%; сохранение заранее размеченных терминов ≥80% (снижено с 95% решением пользователя 2026-10-23: карточка правится вручную); сохранение критичных чисел/отрицаний/условий ≥98%; на 10 мин тишины/шума — ноль финальных осмысленных выдуманных реплик. На целевом ПК: end-to-end RTF ≤0.5 для одного источника, <1 для двух одновременно; для Qwen при начальном chunk 1 с p95 задержка partial-текста относительно произнесённого ≤3 с и p95 final ≤3 с после окончания речи (включая endpoint silence, конкуренцию двух tracks и offline-проход гибридного final, если он выбран); при chunk 2 с измерить отдельно, изменение gate обсуждается по результатам; peak worker RAM ≤12 ГБ и VRAM ≤6 ГБ. Метрики для двух источников считать по wall-clock, не по суммарной длительности, скрывающей нагрузку. Отдельно показать sample count, худшие примеры и разброс по говорящим; маленький корпус не даёт статистической гарантии.

Это гипотезы требований. Если они не проходят, оформить результат и предложить выбор quality/latency/scope; не ослаблять gate молча. CPU-only режим может быть медленнее: это явно измеренный режим, а не автоматическое обещание real time. Если GPU не поддерживается, не выполнять скрытую смену модели или облачный fallback.

**Deliverable:** `docs/roadmap/2026-10-22-local-stt-evaluation.md` с таблицей, ошибками, raw-result paths и решением. При решении зафиксировать точные runtime/model revision, лицензию, hash весов и backend. До этого адаптер и production packaging остаются условными.

### Решения 9.0 (2026-10-23)

Основание — [отчёт оценки](../../roadmap/2026-10-22-local-stt-evaluation.md): быстрый отсев на 30 записях одного говорящего. Это ещё не holdout.

- **Модель и final (решение пользователя):** Qwen3-ASR-1.7B BF16 + **hybrid final**. Partial даёт streaming state; final — один offline `transcribe()` по PCM реплики, `finish_streaming_transcribe` не вызывается. Offline не хуже streaming ни в одном прогоне и исправляет недетерминированное залипание streaming (q14: шаги упираются в лимит токенов, конец фразы теряется). Цена — около 0,5 с.
- **Gates:** порог по терминам снижен до ≥80%, остальные без изменений. На отсеве hybrid: термины 50/63 (79%), mixed WER 15,2%, критичные места 10/12 до перезаписи q10/q14/q15/q19 (обрезано начало записи), тишина и шум — 0 выдуманных реплик, p95 final 1,4–1,5 с (две дорожки ~2 с по replay), RTF ~0,18, VRAM ~5,7–5,9 ГиБ на процесс.
- **Pins:** `vllm/vllm-openai:v0.14.0@sha256:1d6866b87630d94f5e0cdae55ab5abb4ce0b03fcb84d9d10612f9d518d19d4fd`, qwen-asr 0.0.6, transformers 4.57.6, torch 2.9.1+cu129 (`tools/audio-bench/docker/requirements.lock`); модель `Qwen/Qwen3-ASR-1.7B@7278e1e70fe206f11671096ffdd38061171dd6e5`, Apache-2.0, `weights_sha256` `f94b80a75e7509303c0ad2010895671abd2ac32b977490d39c9bce18ebc69c69`.
- **Настройки vLLM:** bfloat16, `gpu_memory_utilization=0.6`, `max_model_len=2048`, `max_num_seqs=2`, `max_num_batched_tokens=2048`, `limit_mm_per_prompt={"audio": 1}`. Значения vLLM по умолчанию не стартуют на 8 ГиБ. SamplingParams: temperature 0, лимит 32 токена на streaming-шаг (достигнутый лимит — сбой шага, а не текст) и 256 на offline final. Chunk 1 с (2 с не лучше по качеству, а partial lag 2,3 с), rollback 2/5 по upstream.
- **Язык и словарь:** `auto` (принудительный Russian на тишине выдаёт текст словаря). Context — `CONTEXT_TEMPLATE` и термины по одному на строку; голый список терминов хуже. Без словаря термины 29/63, поэтому словарь — обязательная часть первой поставки.
- **Обязанности worker:** проверять регистрацию `Qwen3ASRForConditionalGeneration` в vLLM (qwen-asr проглатывает ошибку в `except: pass`); задавать `HOME`/`USER` при запуске под uid без passwd; прогревать модель до ready. Cold load ~90 с (из них torch.compile ~38 с) при readiness-бюджете 120 с — запас мал, в 9.1 оценить постоянный кэш компиляции. VRAM мерить nvidia-smi: под WSL `torch.cuda.mem_get_info` не видит EngineCore.
- **Nemotron 3.5 ASR отклонён:** термины 2–5/63, английские термины кириллицей в режимах auto и ru-RU, WER на RU 9,4%, на EN 61%. Резервного кандидата нет; при провале holdout — отчёт и решение пользователя.
- **Осталось в 9.0:** перезапись q10/q14/q15/q19 и повтор отсева в hybrid-режиме; полный корпус (≥5 говорящих, holdout); VAD/endpointing; 30 минут live; реальная проверка двух дорожек (сейчас replay измеренных шагов).

## 4. Архитектура и контракты

```text
WASAPI system ─┐
              ├→ отдельные PCM tracks → resampler → bounded PCM queue
WASAPI mic ────┘                                              ↓ stt.Provider
                                 worker: VAD/endpointing + STT state на track
                                                             ↓ started / partial / final
                          session transcript → ручной выбор → question card
                                                             ↓ подтверждение
                                общий save → provider → result/recovery
```

### Границы модулей и файлов

Файлы ниже — планируемые, кроме явно обозначенных существующих. Актуальный checkout содержит несвязанные незакоммиченные изменения; перед реализацией повторно сверить имена/контракты и сохранить чужую работу.

| Область | Создать | Изменить существующее |
| --- | --- | --- |
| PCM и очередь | `internal/audio/{types,queue,resampler}.go`, тесты; `AGENTS.md`, `CLAUDE.md` | — |
| Windows capture | `internal/audio/wasapi/{capture_windows,devices_windows,capture_stub}.go`, Windows-тесты и локальные инструкции | — |
| STT boundary | `internal/stt/{provider,types,stream}.go`, контрактные тесты и инструкции | — |
| Qwen adapter | `internal/stt/qwen/` с transport/worker/config tests и инструкциями | — |
| Docker runner | `tools/stt-worker/` (Dockerfile, worker, dependency lock, tests, AGENTS.md/CLAUDE.md); после 9.0 | `Makefile`, CI для Docker-worker |
| Сессия/карточки | `internal/app/{audio,transcript,audio_question}.go` + тесты | `types.go`, `controller.go`, `operation.go`, `work.go`, `history.go`, composition root |
| Work/архив | `internal/workstate/audio.go`, `internal/chat/audio.go` + тесты | `workstate/store.go`, `chat/chat.go`, `history/store.go`, `history/export.go`, owning инструкции |
| Bridge/UI | `frontend/src/audio/{AudioPanel,TranscriptList,QuestionCard}.tsx`, тесты и инструкции | `app.go`, `frontend/src/bridge/{wails,client,state,test-fixture}.ts`, `frontend/src/app/{App.tsx,theme.css}`, settings UI |
| Настройки/приёмка | STT benchmark tools/report | `internal/settings/store.go`, `README.md`, `docs/windows-acceptance.md`, roadmap |

Не создавать параллельный transcript database, plugin registry, generic workflow engine или второй механизм LLM-операций. `stt.Provider` — единственная STT-граница, отдельного `Engine` с теми же обязанностями нет. Qwen реализуется в `internal/stt/qwen/`; только статическая регистрация одного рабочего провайдера. Worker — тонкий адаптер к официальному Qwen streaming API; transport WebSocket внутри адаптера, без C++ wrapper и native-пути в этой поставке.

### Предлагаемые внутренние интерфейсы

До реализации 9.1/9.2 уточнить представления по выбранным библиотекам, сохраняя семантику. Ниже имена для согласования соседних задач; это не требование создавать заглушки сейчас.

- `audio.Capture.Open(ctx context.Context, in audio.CaptureConfig) (audio.Stream, error)`; `audio.Stream` имеет `Frames() <-chan audio.Frame`, `Errors() <-chan error`, `Close() error`. Frames содержат source/track, sequence, sample offset, rate/channels, owned PCM и discontinuity. Ни один native pointer не выходит из capture callback.
- `audio.Normalize(frame audio.Frame)` концептуально выполняет downmix/resample до 16 kHz mono float32; конкретный stateful resampler оформляется в `resampler.go`, состояние сохраняется между frames. Go передаёт весь нормализованный PCM ровно один раз, включая тишину, без предварительной VAD-фильтрации и повторной подачи pre-roll. Все очереди bounded; native PCM pointers наружу не выходят.
- `stt.Provider.Describe() stt.Description`, `OpenStream(ctx context.Context, in stt.StreamConfig) (stt.Stream, error)`, `Close(ctx context.Context) error`. Description: ID, `PartialResults`, `ContextPhrases`, `Languages` (BCP-47 + `auto`) и ограничения; это свойства выбранного adapter/runtime, не названия модели вообще. Stream: `Push(ctx context.Context, chunk stt.Chunk) error`, `Flush(ctx context.Context) error`, `Updates() <-chan stt.Update`, `Close() error`. Partial необязателен, final обязателен; запрос неподдерживаемого словаря/языка возвращает ошибку, ничего не игнорирует молча.
- StreamConfig: AudioSessionID, Generation, TrackID, StreamID (новый UUID от Go при каждом open/reset), Language, ContextPhrases, DictionaryRevision, EndpointConfig и immutable ModelConfig. Qwen-specific chunk/rollback/output limits остаются в конфигурации адаптера, не становятся обязательными для будущего Nemotron. Language переводится адаптером в native locale/token.
- Update: session/generation/track/stream, UtteranceID, Revision, `started|partial|final|error`, StartSample/EndSample, Text и metadata. Error закрывает stream и содержит bounded причину; не выдаётся за transcript. ID реплики назначает адаптер при speech onset как `StreamID:counter`, counter начинается с 1 и не переиспользуется. `started` приходит до текста; Revision возрастает внутри реплики, final ровно один при успешном завершении и закрывает её навсегда. При аварии вместо final остаётся incomplete/gap. Новые реплики имеют новые IDs; late updates закрытых IDs отбрасываются. В snapshot ID transcript segment соответствует UtteranceID. Gap создаёт Go; после gap новый StreamID, чтобы старый ответ не изменил новую реплику.
- Worker получает непрерывный PCM, исполняет VAD/endpointing и pre-roll по EndpointConfig, затем выдаёт `started/partial/final`; Go владеет конфигурацией, lifecycle, transcript и отправкой. Второй VAD-цикл через Go отсутствует. Qwen audio/prefix state на track не разделяется с другим track; VAD завершает и пересоздаёт его между репликами. Provider обязан ограничивать рабочий state; worker один, до двух streams, fair scheduling; partial коалесцируется, final/start не теряются. Flush — barrier по принятым seq на момент вызова, завершает хвост; не вызывает новую запись.
- `Controller.StartAudio(in StartAudioInput) (AudioSnapshot, error)`, `StopAudio(in StopAudioInput) (AudioSnapshot, error)`, `AudioSnapshot() AudioSnapshot`.
- `CreateAudioQuestion(in CreateAudioQuestionInput) (AudioQuestion, error)`, `EditAudioQuestion(in EditAudioQuestionInput) (AudioQuestion, error)`, `DiscardAudioQuestion(in DiscardAudioQuestionInput) error`, `SubmitAudioQuestion(in SubmitAudioQuestionInput) (string, error)`.
- Start input включает ChatID, source/device selection, expected audio state revision; stop — SessionID/generation. Create input — ChatID/SessionID и ordered `{SegmentID, Revision}` только final сегментов. Edit — CardID/ExpectedRevision/Text. Submit — ChatID/CardID/ExpectedRevision/RequestID. Клиент не присылает «доверенные» source excerpts или provider metadata.

### Словарь и конфигурация провайдера

В `settings.Config.Audio` хранить ProviderID=`qwen`, модель/runtime reference, язык (`auto` по умолчанию; `ru-RU` переводится адаптером в Russian для dev-сравнения), ContextPhrases и DictionaryRevision. Общий schema ceiling: до 256 непустых phrases, каждая до 128 Unicode code points, всего до 32 КиБ UTF-8; Qwen Description задаёт более строгий лимит полного сериализованного context 4 КиБ UTF-8. UI/Go проверяют оба лимита до StartAudio и не обрезают словарь молча; отклонять NUL/невалидный UTF-8, trim и удалять точные дубликаты. Пустой словарь допустим; Qwen получает пустой context. Template непустого context — краткая строка о технических терминах плюс список канонических названий, без ожидаемых ответов/примеров опыта; его точный текст фиксируется до holdout. UI — список слов/фраз по одному на строку; не выдавать его за обучение модели и не делать post-hoc замену текста.

Словарь сохраняется локально без STT/LLM-запроса и фиксируется при StartAudio. Правки во время записи действуют только после следующего start; это явно показано в UI. Не извлекать термины автоматически из всей переписки/общих инструкций. Qwen-адаптер формирует детерминированный терминологический context; числового word boosting/score-slider у него нет. В 9.0 сравнить без словаря и с ним, включая нерелевантный словарь/тишину и риск ложных вставок. Для Qwen проверить context именно в init_streaming_state и его применение ко всем новым utterance state; offline prompt не доказывает streaming-поддержку.

Сохранять provider/model/runtime identity, DictionaryRevision и hash словаря в session/provenance; сами phrases не дублировать в каждом сообщении и не писать в логи. Benchmark хранит полный словарь в локальном manifest для воспроизводимости. RNNT word boosting будущего Nemotron — другой механизм, не эквивалент Qwen context.

### Данные и версии

- `AudioSession`: ID, ChatID, Generation, StartedAt UTC, State, Sources, immutable ModelConfig. Одновременно одна capture session на приложение, до двух tracks.
- `TranscriptSegment`: ID, SessionID, TrackID, Source, StartSample/EndSample, Revision, Text, State (`provisional|final|gap`), gap reason; sample time относительно track с общей монотонной привязкой. Не складывать UTC clocks устройств для синхронизации.
- `AudioQuestion`: ID, ChatID, SessionID, Revision, State (`editing|accepted`), edited Text, frozen selected final excerpts с IDs/revisions/time/source, optional accepted MessageID. До 5 несданных карточек на чат, текст карточки до 64 КиБ UTF-8, provenance до 128 КиБ и 100 исходных сегментов; превышение отклоняется до записи.
- `chat.Message.AudioOrigin` — optional immutable provenance: CardID, card revision, session ID, выбранные source excerpts и model/runtime identity. Изменения `Clone`, validation, history read/save/export обязательны. Старые v1/v2 архивы без поля читаются как раньше; нет массовой миграции.
- Незавершённые карточки — additive optional поле per-chat `workstate.Chat`; существующий version envelope продолжает защищать newer state. Новая версия не должна молча терять карточки при чтении. Downgrade старым бинарником может потерять незнакомые additive поля при сохранении — ограничение описать, не обещать обратную запись старой версией.

## 5. Ресурсы, захват и сегментация

Предлагаемые стартовые параметры (менять только по измерениям с отражением в отчёте):

| Параметр | Начальное значение / поведение |
| --- | --- |
| PCM для STT | 16 kHz, mono float32 на track; native format конвертируется |
| Кадры transport | До 100 мс, sequence и sample count; native packets могут иметь иной размер |
| Аудиобуфер | Pre-roll ring 1 с внутри worker + bounded PCM queue в Go; дополнительные adapter/runtime очереди входят в общий лимит; отдельно Qwen audio_accum ≤20 с на реплику, native format не копить |
| VAD | Готовый VAD выбранного runtime либо Silero внутри worker; измерить на dev. Весь PCM подаётся один раз; worker механически исполняет EndpointConfig, Go не фильтрует речь до подачи. Скрытые native endpointing defaults отключить/согласовать, чтобы не было двух независимых правил |
| Pre-roll / endpoint silence | 300 мс / 800 мс; speech detection и вопрос — разные сущности |
| Model inference chunk | Начальная гипотеза 1 с, сравнить 2 с в 9.0; накопление/rollback относятся к Qwen adapter, PCM frames до 100 мс |
| Длинная речь | Qwen state ≤20 с аудио: forced finish → final → новый UtteranceID/state без старого audio/prefix; новый state не пересматривает старый final |
| Partial | Обновляется на каждом chunk step; в UI коалесцируется (лимит UI events ниже) |
| Chunk queue | До 3 с не обработанного audio на track; fair round-robin между tracks |
| STT timeout | Decode step 5 с, flush 10 с, readiness 120 с; стартовые deadlines, проверить в 9.0, timeout видим как error/gap; cold load измеряется отдельно |
| UI events | Не чаще 5 Гц, только delta/snapshot текста/статуса; никакого PCM через Wails |
| Transcript window | Последние 30 мин, до 2000 сегментов / 2 МиБ текста на сессию, что наступит раньше |

На переполнении очереди capture не блокируется: control path отдельно инициирует bounded flush по последнему принятому seq; при timeout хвост остаётся incomplete. Пропущенные интервалы агрегируются в gap, старый StreamID закрывается; новая реплика начинается в новом stream после освобождения. Flush не идёт синхронно из capture callback и не ждёт места в переполненной PCM queue. Не выбрасывать данные без видимого gap. Final/start не теряются при коалесцировании partial; tracks обслуживаются справедливо. Повторяющиеся gap не создают неограниченные metadata.

Silero/VAD не решает окончания мысли. Короткий «нет» и тихая речь обязаны присутствовать в тестах; не выкидывать все короткие сегменты универсальным фильтром. Qwen повторно обрабатывает накопленное аудио внутри ограниченной реплики; на её границе старый state удаляется. Между репликами нет overlap/retranscription или автоматической склейки похожих слов. Forced 20-секундная граница видима, можно выбрать несколько final-сегментов для одной карточки.

WASAPI: enumerate render/capture endpoints, shared mode, native format, flags silent/discontinuity, правильное GetBuffer/ReleaseBuffer, доступ и COM lifecycle. System capture — выбранный render endpoint, не гарантия «только голос интервьюера». Не добавлять virtual audio driver и browser getUserMedia. Конкретный процесс, echo cancellation и diarization вне первой поставки.

Начальный выбор endpoint фиксируется на сессию. Unplug, invalidation, sleep/resume и смена default не подключают другое устройство молча: остановить с явным статусом, оставить финальный текст, предложить новый start. Перезапуск создаёт новую session/generation. Отсутствие loopback packets при тишине не считать автоматически поломкой; отличать его от HRESULT ошибки/heartbeat worker.

## 6. Qwen worker, transport и доставка

**Основной путь определён:** Windows Solveo.exe + WASAPI → authenticated localhost WebSocket → self-owned Linux Docker-worker с qwen-asr/vLLM/CUDA и VAD → stt.Provider. WSL2 используется Docker backend; прямое управление Linux-дистрибутивом из Solveo и native CUDA wrapper в первую поставку не входят. Начальный benchmark в WSL2 допустим, но end-to-end приёмка выполняется с выбранным Docker image и Windows-клиентом.

**Worker:** тонкий Python adapter к официальным Qwen streaming methods, готовый VAD (начальный кандидат Silero), один загруженный экземпляр модели. Обязан реализовать EndpointConfig, 20-секундные boundaries/reset и независимые state двух tracks. Отдельная ingestion task принимает bounded PCM, VAD/endpointing исполняются без ожидания долгого model.generate в event loop; inference jobs сериализуются. На endpoint final обязан учитывать все принятые samples до границы, а не только последний показанный partial. На gap state теряется явно; cache/prefix не переносится в новую реплику.

**Final и порядок:** Started и UtteranceID назначает адаптер по §4; partial может пересматривать весь текст реплики. Дедупликация по IDs/revisions, а не строковому совпадению (два одинаковых слова могут быть правильными). Finish выдаёт final один раз и освобождает accumulated waveform/decoder state; отсутствие tail означает использование последнего полного decode, не фиктивный дополнительный запрос. В гибридном режиме (§3) final формирует один offline `transcribe()` по уже накопленному PCM реплики (тот же буфер ≤20 с, без отдельной копии), `finish_streaming_transcribe` не вызывается; state освобождается так же, Flush barrier и правило одного final сохраняются. Перед следующим utterance init создаёт новый Qwen state с тем же frozen context. Не обещать word alignment; StartSample/EndSample поступают от VAD/PCM timeline.

**Protocol v1:** WebSocket с JSON control frames и binary PCM. Open передаёт AudioSessionID/Generation/TrackID/StreamID, Language, ContextPhrases, DictionaryRevision, ModelConfig и EndpointConfig; адаптер строит context, проверяет limits и подтверждает open. Push содержит seq/StartSample/sample count; seq с 1 без пропусков, samples непрерывны. PCM — 16 kHz mono float32 little-endian, до 100 мс/6400 байт за frame. Worker агрегирует model chunk 1–2 с; model chunk не равен транспортному frame. Max control header 64 КиБ, update text 64 КиБ. Суммарные ingress/inference queues входят в лимит §5, а не образуют скрытый второй буфер.

Worker ack сообщает принятый seq; успешный Push означает bounded acceptance, не готовый transcript. Responses echo session/generation/track/stream, UtteranceID/revision и sample range. Ошибка последовательности закрывает stream; timeout вызывает gap, не продолжение через пропуск. Partial коалесцируется, started/final/error не теряются. Отправку/чтение updates выполнять независимо, чтобы Flush не ждал чтения собственного final под тем же lock.

Flush содержит request ID и LastSeq barrier; worker дообрабатывает все принятые кадры до него, выдаёт final активной реплики и flush-ack. После 10 с deadline stream закрывается как incomplete/gap; late output отбрасывается. Повтор Flush без нового PCM не создаёт второй final; idle flush не выдумывает пустую реплику. Normal VAD endpoint исполняется внутри worker без round trip через Go. На gap новый StreamID, после endpoint новый UtteranceID. Close/EOF не начинает запись. Readiness после model load сообщает provider/model/runtime/hash/backend/dtype/capabilities и limits. Cold start отдельно от listening; аудио до ready не записывается.

**Ресурсы и версии:** Docker image по digest, lock qwen-asr/vLLM/PyTorch/CUDA/VAD и model revision/hash, BF16 явно проверяется в ready. Никакого latest/pip install/model download при каждом старте. Ограничить RAM/shared memory/logs и model/output context. Runtime не получает remote audio URLs; только PCM bytes и локальные веса. Offline smoke проверяет отсутствие сети/загрузки, включая lazy dependencies. GPU OOM/worker crash оставляет ordinary chat usable; нет бесконечного autorestart, скрытого CPU/offline/model fallback.

**Docker ownership:** подключаться только к явно выбранному локальному Engine, не наследовать удалённый DOCKER_HOST/context молча. Container labels привязаны к installation/session, ID сохраняется для reconciliation. Solveo управляет только своим контейнером, не останавливает Docker Desktop, WSL или чужие workloads. Проверить WSL2 backend и NVIDIA GPU passthrough, а не только наличие docker.exe. Не передавать Docker socket/home/весь solveo-data в контейнер: read-only model mount, минимальные дополнительные mounts по необходимости. Аудио не хранится в WAV.

Публикация server port только на Windows localhost, обязательный случайный session token, проверка server/model identity и отсутствия LAN exposure. Container-side bind и Windows publish — разные настройки. Внешний не принадлежащий приложению endpoint не поддерживается в первой версии. Lease/heartbeat завершает self-owned workload и освобождает GPU при потере EXE: начальный acceptance timeout 30 с. Killing docker.exe/wsl.exe не доказывает остановку контейнера. При restart сверить labels/ownership и удалить только свой осиротевший workload; веса сохраняются. Container mutations исполняются будущим приложением/проверенным operational script, не напрямую агентом при планировании.

**Установка и запуск:** отдельное явное действие подготовки загружает verified image и model package; первоначальная подготовка не входит в hot path прослушивания. Веса по умолчанию solveo-data/models/<model-id>, иной путь — явная проверенная настройка; модель mount read-only, paths с пробелами/кириллицей проверяются на Windows. Start audio проверяет prerequisites, запускает собственный контейнер и ждёт ready; затем начинается capture. Stop listening завершает streams/capture, модель может оставаться загруженной по явному состоянию UI. «Выгрузить модель» и Exit останавливают контейнер; readiness/reset не отправляет LLM-запрос. Ошибки Docker не блокируют текст и снимки. Auto-update/download manager и установка драйвера/WSL2 вне первой поставки; у пользователя prerequisites уже есть. Binaries/weights не коммитить, licenses/notices входят в пакет.

## 7. Владение, сохранность и отправка

### Lifecycle

Audio states: `stopped → starting → listening → stopping → stopped`, ошибка переводит в `failed`; start/stop идемпотентны по session/revision. Все workers регистрируются под тем же lifecycle lock, что Close. I/O, native stop, inference и ожидание drain не выполняются под `Controller.mu`.

Смена чата останавливает аудиосессию: уже полученные final сохраняются в исходном контексте, новая запись автоматически не начинается. Результаты после stop принимаются только в ограниченном drain для той же generation; после его окончания generation закрыта. `OpenChat` должен явно координировать audio stop, но не превращать STT в LLM-operation. Delete записывающего чата сначала останавливает его pipeline; failed deletion не оживляет запись. Renderer reload восстанавливает snapshot текущей сессии, не создаёт worker.

Нормальный Close останавливает capture, делает ограниченный flush и закрывает streams, сохраняет карточки. Ошибка записи оставляет приложение доступным для восстановления; аудио автоматически не возобновляется после неудачного выхода. Нельзя держать приложение заблокированным навсегда из-за зависшего STT.

### Хранение и границы обещаний

- Сырой звук — только bounded RAM, без WAV temp files в production. Если выбранный runtime требует file input, пересмотреть это решение явно до интеграции; benchmark WAV не оправдывает скрытый production spool.
- Provisional и rolling transcript — RAM; после crash могут исчезнуть. UI/README это явно описывают. Не писать каждую реплику в chat JSON.
- Создание карточки синхронно сохраняет frozen source excerpts в workstate до успешного acknowledgement. Редактирование карточки checkpoint с debounce 300 мс; потеря последних правок при crash — до фактической измеренной задержки записи. Submit/discard/close делают явный flush.
- Отправленное сообщение содержит собственный provenance; rolling eviction не ломает его проверяемость. Невыбранный полный разговор не сохраняется по умолчанию. «Сохранить стенограмму/аудио» — отдельная будущая возможность.
- Ошибка записи карточки не блокирует обычный чат глобально, но блокирует отправку этой карточки. После принятия LLM-вопроса действуют существующие глобальные save-pending правила.

### Общий submit без потери draft

Факт текущего кода: `internal/app/operation.go:submit` обновляет draft и создаёт `workstate.Submitted` для обычного Submit. Передача ему текста карточки как обычного composer-ввода нарушит требование сохранности черновика.

Решение: маленький внутренний typed origin (`composer|capture|audio-card`) в существующем pipeline. Текущий captureID оставляет своё владение capture-операцией; origin не заменяет его и не меняет поведение quick capture. Для audio-card проверять CardID/revision/chat, фиксировать выбранный текст/provenance/config, создавать обычный user turn; не читать/писать/очищать composer draft, reference или PNG queue. Единственный provider path остаётся `runSubmit`; не вызывать публичный Submit под удерживаемым mutex.

До provider: принять карточку, сохранить work marker `{CardID, Revision, MessageID}` и архивировать user turn. Запись принятия не должна терять карточку раньше durable user message. Если crash до архива — карточка восстанавливается с неподтверждённой отправкой без autorequest; если архив уже содержит MessageID — reconcile как accepted/interrupted, не показывать её как новый неотправленный вопрос. Изменять текущий composer submitted marker для этого не нужно: audio marker отдельный.

Дубликат CardID+revision после принятия возвращает существующий MessageID/operation либо понятный accepted status; не создаёт ход. In-memory RequestID недостаточен для защиты после рестарта. Ошибка до durable acceptance оставляет редактируемую карточку; после acceptance повтор — только явный Retry существующего сообщения. Retry берёт исходный текст, provenance и RequestConfig. RetrySave не вызывает STT/LLM.

Не отправлять при занятом LLM-слоте и не ставить запрос скрыто в очередь: карточка остаётся редактируемой, UI показывает занятость. Запись/распознавание при этом может продолжаться.

## 8. План задач и проверяемые результаты

Каждый подэтап заканчивается ревью результата; дальнейший код не пишется поверх проваленного gate. В каждом модуле сначала добавить поведенческие тесты критичных инвариантов, подтвердить fail, затем минимальную реализацию и pass. Не писать зеркальные тесты setters/CSS. Имена тестов ниже — предлагаемые новые, не существующая проверка.

### 9.0. Benchmark и выбор

**Файлы:** `tools/audio-bench/`, отчёт из §3; при необходимости только dev dependencies инструмента в отдельном окружении/Docker.

- [ ] Выполнить corpus/hardware/benchmark checklist §3.
- [x] Проверить измеритель на synthetic fixture: правильные denominator WER/term accuracy, split leakage, silence false output, p95 и RTF; отдельная инструкция воспроизведения.
- [ ] Подтвердить Qwen BF16/vLLM/Docker на holdout и Windows→Docker spike. Зафиксировать compatible versions, image/model digest и точные команды воспроизведения; разделить offline-file и live-stream результаты. Не выдавать успешную команду demo за готовый provider.
- [ ] Измерить Qwen chunk 1/2 с, mixed terms с context и без, ложные вставки при нерелевантном context, 20-секундные forced boundaries, оба активно говорящих tracks и конфликт final/partial. Проверить отсутствие роста audio_accum после endpoint/reset и 30 мин длительной работы. Demo только для spike; production использует отдельный authenticated worker.
- [x] Сравнить streaming final, offline `transcribe()` той же реплики и гибридный final по §3 на dev-наборе: качество по репликам, ошибки из ранних partial, p95 final latency и RTF гибрида при двух активных tracks. Выбрать режим final до holdout, затем прогнать holdout в выбранном режиме и записать обоснование.
- [ ] Обновить этот план выбранным IPC/build/dependency путём. При отсутствии приемлемого кандидата остановиться на отчёте, не строить UI вокруг заведомо плохого STT.

### 9.1. STT-провайдер Qwen и Docker-worker

**Файлы:** `internal/stt/`, `internal/stt/qwen/`, `tools/stt-worker/`, Docker image build/package target/CI. **Produces:** `stt.Provider`, `stt.Stream` из §4; Nemotron не реализуется.

- [ ] Fake worker tests: `TestProtocolBoundsAndVersion`, `TestRejectMismatchedResponse`, `TestCancelHungWorker`, `TestWorkerCrashDoesNotCrashApp`, `TestModelNotReloadedPerSegment`, `TestMissingModelDoesNotDownload`, `TestStreamStateIsolatedPerTrack`, `TestGapResetsStreamState`, `TestPushSequenceGapFailsStream`, `TestPartialRevisionsMonotonic`, `TestFlushReturnsFinalWithinDeadline`, `TestTwoStreamsRoundRobin`.
- [ ] Worker tests: `TestQwenContextReusedAfterEndpoint`, `TestQwenStateResetAfterTwentySeconds`, `TestQwenDecodeCallsSerialized`, `TestQwenFinishHandlesEmptyTail`, `TestHybridFinalUsesWholeUtterancePCM`, `TestHybridFinalSkipsStreamingFinish`, `TestFinalModeFixedPerSession`, `TestLongUtteranceDoesNotLeakMemory`, `TestQwenRevisionMayReplaceWholePartial`, `TestContextLimitRejectsBeforeModelCall`, `TestStepTokenCapFailsStepNotText`, `TestVllmRegistrationFailureSurfaced`; fake model/SDK, без загрузки весов. Контрактные тесты: `TestFinalOnlyProviderDoesNotRequirePartial` (fake, без второго адаптера), `TestUnsupportedContextPhrasesRejected`, `TestDictionarySnapshotReachesAdapter`, `TestUtteranceIDsNeverReused`, `TestLateFinalCannotChangeClosedUtterance`, `TestFlushBarrierAndDuplicateFlush`, `TestTransportFramesAggregateToModelChunk`. Проверить seq/sample discontinuity и сохранение trailing final при stop.
- [ ] Реализовать protocol, readiness, bounded requests, stderr sanitization, process ownership и Close. Native heavy deps изолировать от host bindings и Go cross-build.
- [ ] Docker integration: `TestOwnedContainerLeaseExpires`, `TestForeignContainerUntouched`, `TestRemoteDockerContextRejected`, offline PCM smoke, GPU OOM/unsupported backend и model paths с кириллицей. Model остаётся loaded между репликами, waveform/prefix state освобождается. Windows→Docker/GPU тесты выполняются отдельно от fake suite.
- [ ] Проверка: `go test -race ./internal/stt/...`; после введения `make stt-worker` собирает/проверяет ровно выбранный package. CI fake suite не требует Docker/GPU/модели; Docker image/worker protocol проверяются отдельным job, реальный GPU smoke — явная Windows-приёмка. Main `make bindings` не требует STT runtime.

### 9.2. Capture и сегментация

**Файлы:** `internal/audio/`, `internal/audio/wasapi/`, endpoint integration в Qwen worker. **Consumes:** frames WASAPI. **Produces:** нормализованный непрерывный PCM в Provider; границы реплик возвращаются в Updates.

- [ ] Portable tests: `TestResamplerPreservesTimelineAcrossPackets`, `TestShortNegationNotDropped`, `TestPauseInsideQuestionDoesNotSubmit`, `TestLongSpeechStreamsWithoutWindowing`, `TestOverflowProducesBoundedGap`, `TestPreRollIncludedInUtterance`, `TestTwoTracksDoNotStarve`.
- [ ] Fake provider/capture тесты: `TestPCMDeliveredOnceIncludingSilence`, `TestOverflowControlDoesNotBlockCapture`, `TestWorkerEndpointPreservesFirstSyllable`, `TestTwentySecondBoundaryResetsQwenState`. Pre-roll применяется только worker; speech endpoint не вызывает отправку вопроса.
- [ ] Выбрать поддерживаемые VAD/resampler после проверки upstream/license/size; не писать собственный DSP. Для WASAPI сначала оценить существующую поддерживаемую Go-обёртку с нужными loopback/format/lifecycle; иначе узкие COM bindings через существующие Windows/syscall средства, без новой C++ capture-службы. Решение записать до реализации.
- [ ] Реализовать capture lifecycle, normalize и bounded scheduler в Go; VAD/endpointing по EndpointConfig — внутри worker. Source flags/time discontinuity не теряются; двойной VAD/pre-roll pipeline отсутствует.
- [ ] Нативные tests/manual: system playback в наушниках, mic, оба источника, silence без packets, 44.1/48 kHz float/stereo, access denied, unplug, sleep/resume, default change.
- [ ] Проверка: `go test -race ./internal/audio/...`; на Windows `go test ./internal/audio/wasapi/...` плюс ручной capture-path. CI без устройства использует fake packets, нативную приёмку не заменяет.

### 9.3. Audio state и transcript

**Файлы:** `internal/app/audio.go`, `transcript.go`, `types.go`, `controller.go`, composition root. **Consumes:** capture/provider. **Produces:** AudioSnapshot и команды start/stop/create из §4.

- [ ] Tests: `TestAudioContinuesDuringLLM`, `TestSTTFailureLeavesTextSubmitUsable`, `TestLateResultStaysInOriginalChat`, `TestSwitchChatStopsAudio`, `TestOldGenerationCannotOverwriteFinal`, `TestRendererReloadDoesNotStartAudio`, `TestTranscriptEvictionKeepsSelectedExcerpts`.
- [ ] Реализовать одну session/two tracks, scheduler, state transitions и revisions. Bounded transcript из §5; event `solveo:audio` с собственной возрастающей revision; authoritative `AudioSnapshot()` для reload/gap recovery. Применять только newer snapshots той же session/generation.
- [ ] Создание карточки копирует выбранные final excerpts; live provisional нельзя отправить. Stop flush ограничен deadline; UI явно отмечает незавершённый/потерянный хвост.
- [ ] Проверка: `go test -race ./internal/app ./internal/audio/... ./internal/stt/...`.

### 9.4. Persistence, provenance и submit

**Файлы:** `internal/app/{audio_question,operation,work,history}.go`, `internal/chat/audio.go`, `chat.go`, `internal/workstate/{audio,store}.go`, `internal/history/{store,export}.go`. **Produces:** durable cards, frozen Message.AudioOrigin и общий submit.

- [ ] Tests: `TestAudioSubmitPreservesComposerAndQueue`, `TestAudioCardAcceptsOneMessage`, `TestAudioCrashBeforeArchiveRestoresCard`, `TestAudioCrashAfterArchiveDoesNotResubmit`, `TestAudioRetryUsesFrozenOrigin`, `TestAudioRetrySaveDoesNotCallSTTOrProvider`.
- [ ] Добавить typed submit origin без отдельного provider execution path. Старые composer/capture tests обязаны остаться зелёными. Подтвердить, что busy/save-pending и cancellation не изменились.
- [ ] Добавить work cards/markers и reconciliation; malformed/oversized origin и cross-chat references отклоняются; IDs/paths не берутся как доверенные из frontend.
- [ ] Tests storage: старые архивы, новый optional origin, Clone isolation, future work version, disk full при создании/submit/close, удаление чата с карточками, export с origin и без raw audio. Содержание источников — не доверенная инструкция, не вставлять его в developerInstructions.
- [ ] Проверка: `go test -race ./internal/app ./internal/chat ./internal/history ./internal/workstate ./internal/settings ./internal/llm/...`.

### 9.5. Bridge и UI ручного сценария

**Файлы:** `app.go`, bridge files, `frontend/src/audio/`, App/settings/theme. **Consumes:** команды и snapshot §4. **Produces:** полный пользовательский сценарий.

- [ ] Генерировать bindings `make bindings`, добавить Zod schemas и fixture; generated Wails files не редактировать вручную.
- [ ] Panel: source/device, model readiness, start/stop, listening/recognizing/lag/error, system/mic labels, provisional/final/gap. Индикация записи видна и при скрытом основном окне через существующий tray; не создавать всплывающее окно, нарушающее capture-affinity правила.
- [ ] Transcript selection → отдельная редактируемая QuestionCard с источниками/временем, confirm/discard. Ошибки чисел/терминов пользователь видит в исходном тексте; confidence не изображается как достоверность, если движок его не предоставляет.
- [ ] UI tests: draft и PNG queue сохранены; double confirm; stale card revision; busy LLM; reload и cleanup subscriptions; gap виден; manual correction не стирается новыми репликами; keyboard/IME; 520×400, темы/масштаб.
- [ ] Settings сохраняют device preferences/backend/model package reference, но не listening=true. Выбор missing model — понятное состояние, без неработающих controls. Изменение модели во время записи требует stop/new session.
- [ ] Добавить ContextPhrases/DictionaryRevision и их limits в settings, bridge/Zod, AudioPanel/settings UI. Проверки: `TestDictionaryLimitsAndPersistence`, `TestDictionaryEditAppliesNextSession`, `TestDictionarySaveDoesNotCallSTTOrLLM`; frontend проверяет multiline ввод, limits и уведомление о применении после restart audio. Capabilities управляют доступностью partial/словаря; не добавлять placeholder selector Nemotron.
- [ ] Проверка: `make bindings`; из `frontend`: `vp check`, `vp run typecheck`, `vp test run`, `vp run build`.

### 9.6. Полная приёмка и документация

**Файлы:** README, Windows acceptance, benchmark report, roadmap, owning AGENTS/CLAUDE.

- [ ] `make check`, отдельные race suites выше, выбранный runner package/offline smoke. Heavy model benchmark не включать в обычный CI; versioned fixtures/fake worker — включить.
- [ ] На целевом ПК: 30 мин двух источников, параллельный текст/скриншоты, паузы/перебивания, термины/числа, GPU contention, OOM, unplug/default change, sleep, hide/show/tray exit, reload, crash/restart, disk failure, удаление чата во время STT.
- [ ] Измерить tail final latency, ресурсы, lost intervals и освобождение GPU при Exit/taskkill: Docker lease ≤30 с. Проверить отсутствие воздействия на чужие процессы/контейнеры. Отдельно проверить отменённый Close: чат работает, запись сама не возобновилась.
- [ ] README: установка runtime/model, offline STT против remote LLM, что сохраняется/теряется, raw audio default, hardware/backend requirements, шум/эхо/термины, отсутствие automatic submit. Никаких обещаний идеального распознавания.
- [ ] В отчёте раздельно automated/fake, Windows runtime, model quality, ограничения и невыполненные сценарии. Пункт roadmap отмечать готовым только после полного ручного пути с реальным звуком.

### 9.7. Последующее автоматическое предложение карточек

Не входит в реализацию 9.0–9.6. После реальных данных подготовить отдельный план: чем определяем законченный вопрос, как учитываем несколько реплик/поправки/ограничения, какой локальный механизм и его бюджет, как измеряем false positives/negatives. VAD, «?» и ключевое слово не дают достаточной семантики. Если потребуется локальная LLM, сравнить её пользу и конкуренцию за те же 8 ГБ VRAM; это дополнительная модель и отдельное решение, не скрытая зависимость STT.

Метрики: доля корректных предложений, пропущенные вопросы, потерянные условия, дубликаты, время появления карточки и правки пользователя. Новые предложения не перезаписывают ручную редакцию. Автоотправка остаётся отдельным продуктовым решением после измерений.

## 9. Вопросы для независимого ревью

1. Достаточен ли benchmark для RU/EN code-switching и не скрывает ли нормализация ошибки терминов, чисел и отрицаний?
2. Согласны ли с предложенными quality/latency/resource gates? Какие требования важнее при конфликте на этом ПК?
3. Проверены ли pinned Qwen/vLLM Docker-worker, context, VAD, BF16 и освобождение GPU на фактическом ПК? Доказана ли real-time работа при двух активно говорящих tracks, а не только offline RTF?
4. Оправдан ли out-of-process worker, и достаточно ли IPC bounds/process ownership для реальных аварий?
5. Нет ли в submit/reconciliation окна для потери карточки, изменения draft/queue или повторного LLM-запроса?
6. Приемлемо ли, что весь rolling transcript после аварии исчезает, а карточки/отправленные вопросы и их excerpts восстанавливаются?
7. Достаточна ли ручная граница вопроса для первой поставки, прежде чем расходовать ресурсы на semantic detector?
8. Закрывают ли provider contract tests словарь/capabilities, подачу PCM ровно один раз, ID реплик, flush barrier и поздние final? Установка runtime не должна менять бизнес-правила чата.

Порядок исполнения: Qwen выбран, отдельного выбора провайдера/runner перед 9.0 нет. Состояние на 2026-10-23 — в «Решения 9.0»: перед 9.1 по-прежнему нужны повтор отсева, полный корпус и holdout, если пользователь явно не сократит 9.0. Начать с его локального streaming spike и mixed RU/EN оценки. Перед 9.1 обязательны успешный 9.0, pinned compatible environment, проверка двух tracks/ресурсов/context и review актуального плана. Провал gate приводит к отчёту и обсуждению компромисса, а не автоматической замене на Nemotron.

## 10. Первичные источники и границы доказательств

Проверены при подготовке 2026-10-22. Runtime/model версии перед установкой проверить повторно. Ссылки подтверждают возможности upstream, не успешную интеграцию с Solveo и не качество mixed RU/EN на целевом ПК.

- [Whisper large-v3-turbo model card](https://huggingface.co/openai/whisper-large-v3-turbo): multilingual checkpoint; сравнение с large-v3 требуется на нашем корпусе.
- [Whisper upstream](https://github.com/openai/whisper): режимы transcription/translation и семейство моделей.
- [whisper.cpp](https://github.com/ggml-org/whisper.cpp): native runtime, CPU/GPU варианты и Windows build; конкретный backend проверить на выбранной сборке.
- [faster-whisper](https://github.com/SYSTRAN/faster-whisper): CTranslate2 baseline; отдельные GPU runtime dependencies.
- [Qwen3-ASR model card](https://huggingface.co/Qwen/Qwen3-ASR-1.7B), [upstream streaming](https://github.com/QwenLM/Qwen3-ASR#streaming-inference): основной выбранный движок, streaming API vLLM; local mixed RU/EN качество не предполагается по названию модели. Demo-команда проверяется через --help pinned package перед запуском.
- [vLLM GPU installation](https://docs.vllm.ai/en/latest/getting_started/installation/gpu/): Linux requirement; основной запуск Qwen через Docker/WSL2.
- [Nemotron 3.5 ASR Streaming 0.6B](https://huggingface.co/nvidia/nemotron-3.5-asr-streaming-0.6b): резерв, собственный cache-aware путь и runtime; данные о его качестве на нашем сценарии пока отсутствуют.
- [NVIDIA NeMo-Speech.cpp](https://github.com/NVIDIA/NeMo-Speech.cpp), [v0.2.0](https://github.com/NVIDIA/NeMo-Speech.cpp/releases/tag/v0.2.0), [build](https://github.com/NVIDIA/NeMo-Speech.cpp/blob/main/docs/build.md): есть Windows CPU/CUDA/Vulkan release assets; использовать pinned version и сверять возможности с этой версией. Native CUDA runtime требует совместимого драйвера. Default q8_0 не подменяет неквантованный baseline; conversion f16/f32 ещё проверить.
- [ASR customization](https://github.com/NVIDIA/NeMo-Speech.cpp/blob/main/docs/asr/customization.md), [configuration](https://github.com/NVIDIA/NeMo-Speech.cpp/blob/main/docs/asr/configuration.md): word boosting, VAD/endpointing и tokenizer requirements выбранного native runtime; не переносить автоматически на другой backend.
- [Docker Desktop GPU](https://docs.docker.com/desktop/features/gpu/): NVIDIA GPU-доступ Linux-контейнеров на Windows через WSL2 backend; установленное окружение пользователя всё равно проходит GPU smoke.
- [Официальный Qwen wrapper](https://github.com/QwenLM/Qwen3-ASR/blob/main/qwen_asr/inference/qwen3_asr.py), [streaming example](https://github.com/QwenLM/Qwen3-ASR/blob/main/examples/example_qwen3_asr_vllm_streaming.py): источники семантики накопления, context и finish; pinned source проверяется до реализации.
- [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx): возможный portable runtime для дополнительного эксперимента; проверить поддержку конкретной модели/precision/Windows, не предполагать переносимость любого checkpoint.
- [Parakeet-TDT-0.6B-v3](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3): дополнительный многоязычный кандидат.
- [Silero VAD](https://github.com/snakers4/silero-vad): кандидат детектора речи; не детектор смысла вопроса.
- [WASAPI loopback](https://learn.microsoft.com/en-us/windows/win32/coreaudio/loopback-recording), [capture packets](https://learn.microsoft.com/en-us/windows/win32/coreaudio/capturing-a-stream): render endpoint shared-mode capture и packet lifecycle.
- [Microsoft application loopback sample](https://learn.microsoft.com/en-us/samples/microsoft/windows-classic-samples/applicationloopbackaudio-sample/): отдельная возможность захвата процесса с собственными системными требованиями, вне начальной поставки.
