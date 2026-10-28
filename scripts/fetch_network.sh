#!/usr/bin/env bash
# Downloads the datameet Indian Railways dataset (CC0, https://github.com/datameet/railways) that
# scripts/build_network.py turns into YatraX's real network. About 99 MB; kept out of git.
set -euo pipefail
dir="$(cd "$(dirname "$0")/.." && pwd)/data/raw/datameet"
mkdir -p "$dir"
for f in stations trains schedules; do
  curl -sSfL -o "$dir/$f.json" "https://raw.githubusercontent.com/datameet/railways/master/$f.json"
  echo "$f.json $(wc -c < "$dir/$f.json") bytes"
done
