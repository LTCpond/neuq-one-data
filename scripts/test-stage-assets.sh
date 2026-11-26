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

mv public empty-public
mkdir -p public/api/v1/classrooms
printf 'first snapshot\n' > public/api/v1/classrooms/week.json
cp "$repo_root/internal/exporter/headers.txt" public/_headers
cp "$repo_root/assets/maps/campus-map.png" assets/maps/
cp -R "$repo_root/assets/policies" assets/
bash "$repo_root/scripts/stage-assets.sh"
cmp assets/maps/campus-map.png public/assets/maps/campus-map.png
cmp assets/policies/index.json public/assets/policies/index.json
cmp assets/policies/grade-point.pdf public/assets/policies/grade-point.pdf
test ! -e public/assets/app/latest.json
test "$(cat public/api/v1/classrooms/week.json)" = 'first snapshot'
cmp "$repo_root/internal/exporter/headers.txt" public/_headers
grep -A1 -Fx '/assets/*' public/_headers | grep -Fxq '  Cache-Control: public, no-cache, no-transform'
grep -A1 -Fx '/api/v1/*' public/_headers | grep -Fxq '  Cache-Control: public, max-age=60, must-revalidate'

mv public previous-public
mkdir -p public/api/v1/classrooms
printf 'second snapshot\n' > public/api/v1/classrooms/week.json
cp "$repo_root/internal/exporter/headers.txt" public/_headers
printf 'installer fixture\n' > assets/maps/app.APK
if bash "$repo_root/scripts/stage-assets.sh" 2> rejection.log; then
  echo 'Legacy staging accepted an APK'
  exit 1
fi
grep -Fq 'app.APK' rejection.log
test ! -e public/assets
rm assets/maps/app.APK
printf 'new map fixture\n' >> assets/maps/campus-map.png
bash "$repo_root/scripts/stage-assets.sh"
cmp "$repo_root/assets/maps/campus-map.png" previous-public/assets/maps/campus-map.png
cmp assets/maps/campus-map.png public/assets/maps/campus-map.png
cmp assets/policies/grade-point.pdf public/assets/policies/grade-point.pdf
test "$(cat public/api/v1/classrooms/week.json)" = 'second snapshot'
cmp "$repo_root/internal/exporter/headers.txt" public/_headers
