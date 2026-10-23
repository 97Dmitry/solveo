# Benchmark image for the Nemotron 3.5 ASR comparison: same pinned base (torch/CUDA layers are shared with the
# Qwen image), transformers 5.x instead of vLLM. Weights are never baked in or downloaded at run time.
FROM vllm/vllm-openai:v0.14.0@sha256:1d6866b87630d94f5e0cdae55ab5abb4ce0b03fcb84d9d10612f9d518d19d4fd

COPY docker/requirements-nemotron.lock /tmp/requirements.lock
RUN pip install --no-cache-dir --no-deps -r /tmp/requirements.lock \
    && python3 -c "import transformers, soxr, soundfile, librosa; from transformers import AutoModelForRNNT; assert transformers.__version__ == '5.19.0'"

# See Dockerfile: the caller's uid has no passwd entry.
ENV HOME=/tmp \
    USER=bench \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    DO_NOT_TRACK=1 \
    PYTHONUNBUFFERED=1

WORKDIR /bench
COPY audio_bench /bench/audio_bench
ENTRYPOINT ["python3", "-m", "audio_bench.run_nemotron"]
