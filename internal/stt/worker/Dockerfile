# solveo-stt: speech recognition worker. Built by Solveo from the context embedded in Solveo.exe.
# Weights are never baked in; serving never downloads (only the `download` subcommand uses the network).
FROM vllm/vllm-openai:v0.14.0@sha256:1d6866b87630d94f5e0cdae55ab5abb4ce0b03fcb84d9d10612f9d518d19d4fd

COPY requirements.lock /tmp/requirements.lock
# --ignore-installed: the base ships a distutils blinker 1.4 that pip cannot uninstall.
# The base already fails `pip check` (pygobject/pycairo), so verify our imports and pins instead.
RUN pip install --no-cache-dir --no-deps --ignore-installed -r /tmp/requirements.lock \
    && python3 -c "import qwen_asr, silero_vad, soxr, websockets, vllm, transformers; assert vllm.__version__ == '0.14.0' and transformers.__version__ == '4.57.6' and websockets.__version__ == '17.2'"

# Solveo runs the container without a passwd entry for the user: torch resolves its cache dir via getpass.getuser().
ENV HOME=/tmp \
    USER=stt \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    VLLM_NO_USAGE_STATS=1 \
    DO_NOT_TRACK=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app
COPY solveo_stt /app/solveo_stt
EXPOSE 8765
ENTRYPOINT ["python3", "-m", "solveo_stt"]
