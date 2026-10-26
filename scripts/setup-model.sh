#!/usr/bin/env bash
# Downloads the local language model Ledger uses (GGUF, for llama.cpp's llama-server) into models/.
# Qwen2.5 7B Instruct, 4-bit (Q4_K_M): about 4.7 GB, runs on a 6 GB GPU.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
name="Qwen2.5-7B-Instruct-Q4_K_M.gguf"
url="https://huggingface.co/bartowski/Qwen2.5-7B-Instruct-GGUF/resolve/main/$name"
mkdir -p "$root/models"
if [ -f "$root/models/$name" ]; then
  echo "models/$name is already there"
  exit 0
fi
echo "downloading $url (about 4.7 GB)"
curl -fL --retry 3 -C - -o "$root/models/$name.part" "$url"
mv "$root/models/$name.part" "$root/models/$name"
ls -la "$root/models/$name"
