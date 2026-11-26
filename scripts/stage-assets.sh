#!/usr/bin/env bash
set -euo pipefail

# Run from the repository root after the collector has verified public/.
test -d public
mkdir -p public/assets
tar -C assets --exclude=.gitkeep --exclude='*.[aA][pP][kK]' --exclude='*.[aA][aA][bB]' -cf - . |
  tar -C public/assets -xf -
