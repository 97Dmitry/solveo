#!/usr/bin/env bash
# Print the packages that `pip install <args>` adds or replaces in the pinned base image, as requirement lines
# for a --no-deps install. Needs network. Usage: docker/lock-delta.sh 'transformers[torch]==5.19.0' librosa
# LOCK_DELTA_PRE (optional) is installed first with --ignore-installed --no-deps, e.g. 'blinker==1.9.0' for qwen-asr:
# the base ships a distutils blinker 1.4 that pip cannot uninstall.
set -euo pipefail
base=vllm/vllm-openai:v0.14.0@sha256:1d6866b87630d94f5e0cdae55ab5abb4ce0b03fcb84d9d10612f9d518d19d4fd
docker run --rm -e LOCK_DELTA_PRE="${LOCK_DELTA_PRE:-}" --entrypoint bash "$base" -c '
  set -e
  pip freeze | sort > /tmp/before
  if [ -n "$LOCK_DELTA_PRE" ]; then pip install -q --no-cache-dir --ignore-installed --no-deps $LOCK_DELTA_PRE >&2; fi
  pip install -q --no-cache-dir "$@" >&2
  pip freeze | sort > /tmp/after
  comm -13 /tmp/before /tmp/after' lock-delta "$@"
