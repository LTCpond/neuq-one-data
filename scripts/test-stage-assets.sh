#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"

mkdir -p assets/maps public/api/v1/classrooms
touch assets/maps/.gitkeep
printf 'first snapshot\n' > public/api/v1/classrooms/week.json
cp "$repo_root/internal/exporter/headers.txt" public/_headers

bash "$repo_root/scripts/stage-assets.sh"
test ! -e public/assets/maps/.gitkeep
test ! -e public/assets/maps/campus-map.png

printf 'first map\n' > assets/maps/campus-map.png
mkdir -p assets/other
printf 'another asset\n' > assets/other/sample.txt
bash "$repo_root/scripts/stage-assets.sh"
cmp assets/maps/campus-map.png public/assets/maps/campus-map.png
cmp assets/other/sample.txt public/assets/other/sample.txt
test "$(cat public/api/v1/classrooms/week.json)" = 'first snapshot'
cmp "$repo_root/internal/exporter/headers.txt" public/_headers
grep -A1 -Fx '/assets/*' public/_headers | grep -Fxq '  Cache-Control: public, no-cache, no-transform'
grep -A1 -Fx '/api/v1/*' public/_headers | grep -Fxq '  Cache-Control: public, max-age=60, must-revalidate'

mv public previous-public
mkdir -p public/api/v1/classrooms
printf 'second snapshot\n' > public/api/v1/classrooms/week.json
cp "$repo_root/internal/exporter/headers.txt" public/_headers
printf 'updated map\n' > assets/maps/campus-map.png
bash "$repo_root/scripts/stage-assets.sh"
test "$(cat previous-public/assets/maps/campus-map.png)" = 'first map'
cmp assets/maps/campus-map.png public/assets/maps/campus-map.png
cmp assets/other/sample.txt public/assets/other/sample.txt
test "$(cat public/api/v1/classrooms/week.json)" = 'second snapshot'
cmp "$repo_root/internal/exporter/headers.txt" public/_headers
