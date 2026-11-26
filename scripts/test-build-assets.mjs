import assert from 'node:assert/strict';
import { cp, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';

import { buildAssets, fetchLatestRelease } from './build-assets.mjs';

const release = {
  tag_name: 'v1.10.0',
  html_url: 'https://github.com/LTCpond/neuq-one/releases/tag/v1.10.0',
  draft: false,
  prerelease: false,
  published_at: '2026-11-26T08:00:00Z',
  assets: [{ name: 'neuq-one.apk', state: 'uploaded' }],
};

async function temp(t) {
  const root = await mkdtemp(join(tmpdir(), 'neuq-one-assets-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}

async function readManifest(output) {
  return JSON.parse(await readFile(join(output, 'assets', 'app', 'latest.json'), 'utf8'));
}

test('stages the repository assets byte for byte without a classroom snapshot', async (t) => {
  const output = join(await temp(t), 'site');
  await buildAssets(output);
  assert.deepEqual((await readdir(output)).sort(), ['_headers', 'assets']);
  assert.equal(await readFile(join(output, '_headers'), 'utf8'),
    '/assets/*\n  Cache-Control: public, no-cache, no-transform\n');

  const files = await readdir('assets', { recursive: true, withFileTypes: true });
  for (const file of files) {
    if (!file.isFile() || file.name === '.gitkeep') continue;
    const path = join(file.parentPath, file.name);
    assert.deepEqual(await readFile(join(output, path)), await readFile(path));
  }
  assert.ok(!(await readdir(join(output, 'assets', 'maps'))).includes('.gitkeep'));
  assert.deepEqual(await readManifest(output), { schemaVersion: 1, release: null });
});

test('uses the official latest release and stages only metadata from GitHub', async (t) => {
  const output = join(await temp(t), 'site');
  const latest = await fetchLatestRelease(async (url, options) => {
    assert.equal(url, 'https://api.github.com/repos/LTCpond/neuq-one/releases/latest');
    assert.equal(options.redirect, 'error');
    return Response.json(release);
  });
  await buildAssets(output, { release: latest });
  assert.ok(!(await readdir(join(output, 'assets', 'app'))).includes('neuq-one.apk'));
  assert.deepEqual(await readManifest(output), {
    schemaVersion: 1,
    release: { version: '1.10.0', url: release.html_url },
  });
});

test('handles absence of a stable release and propagates other GitHub failures', async () => {
  assert.equal(await fetchLatestRelease(async () => new Response(null, { status: 404 })), null);
  await assert.rejects(fetchLatestRelease(async () => new Response(null, { status: 503 })), /503/);
});

test('rejects invalid releases before creating a deployment directory', async (t) => {
  const root = await temp(t);
  for (const invalid of [
    { prerelease: true },
    { draft: true },
    { tag_name: 'v1.10.0-rc.1' },
    { html_url: 'https://example.com/releases/latest' },
    { assets: [{ name: 'app.apk', state: 'new' }] },
  ]) {
    await assert.rejects(buildAssets(join(root, 'site'), { release: { ...release, ...invalid } }), /正式 GitHub Release/);
  }
  assert.deepEqual(await readdir(root), []);
});

test('rejects corrupt policy PDFs and APKs in the asset tree', async (t) => {
  const root = await temp(t);
  const source = join(root, 'assets');
  const output = join(root, 'site');
  await mkdir(join(source, 'app'), { recursive: true });
  const apk = join(source, 'app', 'neuq-one.APK');
  await writeFile(apk, 'apk');
  await assert.rejects(buildAssets(output, { source }), /不支持的文件/);
  await rm(apk);
  await cp('assets/policies', join(source, 'policies'), { recursive: true });
  await writeFile(join(source, 'policies', 'grade-point.pdf'), '%PDF-corrupt');
  await assert.rejects(buildAssets(output, { source }), /PDF 校验失败/);
});
