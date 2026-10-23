# Benchmark локального STT (этап 9.0)

Инструмент для решения по Qwen3-ASR-1.7B (BF16, vLLM, Docker) на смешанной RU/EN речи. Он не входит в приложение и `make check`; реальные прогоны запускаются вручную на целевом ПК.

Что измеряется на каждом фрагменте одного PCM с одинаковыми границами:

- **streaming final** — официальный `init_streaming_state` → `streaming_transcribe` кадрами по 100 мс → `finish_streaming_transcribe`;
- **offline** — `transcribe()` того же PCM; его compute-время даёт задержку гибридного final (§3 плана);
- время каждого decode-шага, усечение вывода (`finish_reason == "length"`), cold load, занятость памяти GPU.

Фрагмент длиннее 20 с делится принудительными границами, каждая часть получает новый streaming state.

## Предусловия

Docker Desktop с WSL2 backend и NVIDIA GPU, около 25 ГБ свободного места (образ ~20 ГБ, веса ~4,7 ГБ). Команды ниже выполняются из корня репозитория в WSL.

## 1. Образ

```bash
make audio-bench-image
```

Базовый образ `vllm/vllm-openai:v0.14.0` закреплён по digest, Python-зависимости — `docker/requirements.lock` с `--no-deps` и `pip check`.

## 2. Веса (однократно, с сетью)

```bash
MODEL_DIR=$HOME/.cache/solveo-audio-bench/models/Qwen3-ASR-1.7B-7278e1e
```

```bash
mkdir -p "$MODEL_DIR" && docker run --rm --user "$(id -u):$(id -g)" -e HF_HUB_OFFLINE=0 -e HF_HOME=/tmp/hf -v "$MODEL_DIR":/out --entrypoint hf solveo-audio-bench:dev download Qwen/Qwen3-ASR-1.7B --revision 7278e1e70fe206f11671096ffdd38061171dd6e5 --local-dir /out
```

```bash
echo 7278e1e70fe206f11671096ffdd38061171dd6e5 > "$MODEL_DIR/REVISION"
```

Лицензия модели — Apache-2.0. Веса не коммитятся.

## 3. Корпус

Каталог корпуса содержит WAV (любая частота/каналы, приводятся к 16 kHz mono через soxr HQ) и `manifest.json`; пример полей — `examples/manifest.example.json`. Правила:

- `split`: `dev` или `holdout`; говорящий и исходный разговор не могут попасть в оба (проверяется перед прогоном и оценкой);
- `reference` размечает человек до запуска; живой вывод модели эталоном не является;
- `terms` — ожидаемое написание терминов с допустимыми вариантами; `critical` — числа (`number`), отрицания (`negation`), ограничения (`constraint`). Каждая форма обязана встречаться в reference;
- категории: `mixed`, `ru`, `en`, `silence`, `noise`, `numbers`, `accent`, `long`, `pause`, `interruption`, `call`. У `silence` reference пустой;
- `context_phrases` — словарь для Qwen context (до 256 фраз по 128 символов, итоговый context ≤ 4 КиБ UTF-8);
- фрагмент обрезается по концу речи: конец файла считается концом речи при расчёте задержки final.

Записи и полные стенограммы не коммитятся без подтверждённого права на распространение.

### Корпус из списка фраз

`corpus/` хранит записи быстрого отсева: `phrases.txt` (`id | файл записи | текст`, ` / ` — пауза, не входит в эталон) и `annotations.json` (категории, термины, критичные места, словарь context). В git попадают только эти два файла. Записи декодируются в `corpus/wav/<id>.wav` без ресемплинга, затем собирается manifest:

```bash
cd tools/audio-bench/corpus && docker run --rm --network none --user "$(id -u):$(id -g)" -v "$PWD":/c --entrypoint bash solveo-audio-bench:dev -c 'mkdir -p /c/wav; grep -E "^q[0-9]+ \|" /c/phrases.txt | while IFS="|" read -r id src _; do id=$(echo $id); src=$(echo $src); ffmpeg -nostdin -v error -y -i "/c/$src" -map_metadata -1 -c:a pcm_s16le "/c/wav/$id.wav"; done'
```

```bash
cd tools/audio-bench && python3 -m audio_bench.phrases --phrases corpus/phrases.txt --annotations corpus/annotations.json --audio-dir corpus/wav --out corpus/manifest.json
```

## 4. Прогон

```bash
docker run --rm --gpus all --network none --shm-size 2g --memory 12g --user "$(id -u):$(id -g)" -v "$MODEL_DIR":/model:ro -v "$CORPUS":/corpus:ro -v "$OUT":/out solveo-audio-bench:dev --model /model --manifest /corpus/manifest.json --split dev --out /out/dev-chunk1-ctx.jsonl
```

Варианты для сравнения: `--chunk-sec 2`, `--context none`, `--language Russian`. Без сети контейнер не может ничего скачать: успешный прогон подтверждает отсутствие ленивых загрузок. `--audio a.wav b.wav` вместо `--manifest` — быстрый smoke без оценки.

### Сравнение с Grok STT (облако xAI)

Записи отправляются в xAI. Ключ передаётся только через окружение:

```bash
cd tools/audio-bench && XAI_API_KEY=... python3 -m audio_bench.run_grok --manifest corpus/manifest.json --out corpus/results/grok-ctx.jsonl
```

`--context none` — без словаря, `--language ru` — подсказка языка. Это пакетная загрузка каждого файла (REST), а не потоковый режим приложения: задержка в отчёте означает время ответа на целый файл. Результат оценивается тем же `report.py`.

### Сравнение с Nemotron 3.5 ASR

```bash
make audio-bench-nemotron-image
```

Веса: `hf download nvidia/nemotron-3.5-asr-streaming-0.6b config.json generation_config.json model.safetensors processor_config.json tokenizer.json tokenizer_config.json --revision ea30d66debe3740a08b573244286791d423d6b3e` тем же способом, что в разделе 2, только из образа `solveo-audio-bench-nemotron:dev`. Прогон — тот же `docker run`, но с образом `solveo-audio-bench-nemotron:dev` и параметрами `--language auto|ru-RU --lookahead 13|6|3|0`. Результат имеет тот же формат и оценивается тем же отчётом. Word boosting в transformers недоступен.

## 5. Оценка

```bash
cd tools/audio-bench && python3 -m audio_bench.report --manifest "$CORPUS/manifest.json" --results "$OUT/dev-chunk1-ctx.jsonl" --split dev --out "$OUT/dev-chunk1-ctx.md" --json "$OUT/dev-chunk1-ctx.json"
```

### Правила метрик

- Нормализация: NFKC, casefold, `ё→е`; знаки препинания и символы — разделители слов, кроме `+`, `#` и точки между буквами/цифрами (`C++`, `C#`, `Next.js`, `3.5`). Пунктуация не считается ошибкой распознавания.
- WER/CER — суммарные ошибки, делённые на суммарную длину reference (не среднее по фрагментам); CER без пробелов. `silence` в WER не входит: для него считается любой выданный токен.
- Термины: число распознанных вхождений (каноническая форма или вариант как последовательность токенов, не подстрока) к ожидаемым. Критичные элементы: доля фрагментов, где сохранены все.
- Задержка: измеренные decode-шаги воспроизводятся по реальному времени поступления аудио на одной последовательной модели. Partial lag — верхняя граница для слова в начале chunk (chunk + ожидание + decode). Final-работа стартует после endpoint silence (по умолчанию 0,8 с), задержка отсчитывается от конца речи. Две дорожки — соседние пары фрагментов, начатые одновременно на одной модели.
- p95 — nearest-rank. RTF — суммарный compute к длительности аудио.

## Ограничения

- WER считает разную запись чисел (`150` и `ста пятидесяти`) ошибкой; сохранность чисел оценивается по `critical` с вариантами, а не по WER.
- Нет VAD: фрагмент корпуса считается одной репликой (кроме 20-секундных границ). Паузы внутри вопроса и endpointing проверяются отдельным шагом после выбора VAD.
- Replay двух дорожек использует compute-время, измеренное без конкуренции; реальный worker с двумя state проверяется в 9.1.
- Память контейнер не измеряет: под WSL `torch.cuda.mem_get_info` не видит процесс EngineCore. Пик снимать на хосте параллельно с прогоном (`nvidia-smi --query-gpu=memory.used --format=csv,noheader -lms 500`) и вычитать занятость до запуска. vLLM заранее резервирует `--gpu-memory-utilization` от всего объёма.
- Первые запросы компилируют ядра; runner делает прогрев на 2,5 с тишины и пишет его время в `warmup_sec`, cold load — в `cold_load_sec`.
- Синтетические SAPI-фразы проверяют только тракт Windows→Docker→GPU, а не качество.

## Подсказки произношения Qwen (2026-10-23)

Текущий worker добавляет подсказки из `internal/stt/worker/solveo_stt/pronunciations.json` только к терминам, которые пользователь включил в словарь. Для воспроизведения передайте runner параметр `--pronunciations /pronunciations.json` и смонтируйте этот файл в контейнер read-only, например `-v "$PWD/internal/stt/worker/solveo_stt/pronunciations.json:/pronunciations.json:ro`. Используйте шаблон `instruction`; запуск без этого параметра остаётся исходным baseline. Подсказки применяются только к offline final, промежуточный streaming сохраняет исходный словарь. В заголовке JSONL сохраняются SHA256 файла подсказок и обоих контекстов.

Канонические названия сохраняются целиком; дополнительные подсказки занимают только оставшееся место в лимите context 4096 байт. Текстовые автозамены не выполняются. Результаты подбора — в `docs/roadmap/2026-10-22-local-stt-evaluation.md`; это dev-корпус одного говорящего.
