#!/bin/sh
set -e

echo "Waiting for Ollama at ${OLLAMA_URL}..."
while ! curl -s "${OLLAMA_URL}/api/tags" > /dev/null; do
    sleep 1
done

echo "Ollama ready. Starting Go app..."
exec ./congopro-bridge "$@"