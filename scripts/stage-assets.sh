#!/usr/bin/env bash
set -euo pipefail

# Run from the repository root after the collector has verified public/.
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
exec node "$script_dir/build-assets.mjs" public --legacy
