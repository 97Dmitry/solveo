# Benchmark image for stage 9.0: pinned vLLM base + qwen-asr. Weights are never baked in or downloaded at run time.
FROM vllm/vllm-openai:v0.14.0@sha256:1d6866b87630d94f5e0cdae55ab5abb4ce0b03fcb84d9d10612f9d518d19d4fd

COPY docker/requirements.lock /tmp/requirements.lock
# --ignore-installed: the base ships a distutils blinker 1.4 that pip cannot uninstall.
# Base-image pip check already reports unrelated pygobject/pycairo, so verify our imports instead.
RUN pip install --no-cache-dir --no-deps --ignore-installed -r /tmp/requirements.lock \
    && python3 -c "import qwen_asr, soxr, soundfile, vllm, transformers; assert vllm.__version__ == '0.14.0' and transformers.__version__ == '4.57.6'"

# Runs as the caller's uid (--user) without a passwd entry: torch resolves its cache dir via
# getpass.getuser(), and qwen-asr swallows that failure, leaving its vLLM model unregistered.
ENV HOME=/tmp \
    USER=bench \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    VLLM_NO_USAGE_STATS=1 \
    DO_NOT_TRACK=1 \
    PYTHONUNBUFFERED=1

WORKDIR /bench
COPY audio_bench /bench/audio_bench
ENTRYPOINT ["python3", "-m", "audio_bench.run_qwen"]
