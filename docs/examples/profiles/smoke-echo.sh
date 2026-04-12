#!/usr/bin/env bash
# smoke-echo.sh — minimal executor-cli smoke test profile.
# Ignores its args; emits an artifact signal and a DONE signal.
set -eu
echo "starting smoke run"
echo '{"signal":"CLOCKWORK_ARTIFACT","type":"log","content":"smoke run output"}'
echo "CLOCKWORK_DONE"
