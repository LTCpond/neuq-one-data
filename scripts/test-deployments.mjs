import assert from 'node:assert/strict';
import { cp, mkdir, mkdtemp, readFile, readdir, realpath, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, relative } from 'node:path';
import { test } from 'node:test';
import { buildAssets, stageLegacyAssets } from './build-assets.mjs';
import { prepareAzureAssets } from './prepare-azure-assets.mjs';
import { prepareAzureData } from './prepare-azure-data.mjs';

async function workspace(t) {
  const parent = await realpath(tmpdir());
  const dir = await mkdtemp(join(parent, 'neuq-deployment-test-'));
  t.after(async () => {
    assert.equal(dirname(await realpath(dir)), parent);
    await rm(dir, { recursive: true });
  });
  return dir;
}

async function config(site) {
  return JSON.parse(await readFile(join(site, 'staticwebapp.config.json'), 'utf8'));
}

test('Azure and Cloudflare use identical static files and separate deployment configuration', async (t) => {
  const dir = await workspace(t);
  const cloudflare = join(dir, 'cloudflare');
  const azure = join(dir, 'azure');
  await buildAssets(cloudflare);
  await cp(cloudflare, azure, { recursive: true });
  await prepareAzureAssets(azure);

  assert.deepEqual((await readdir(azure)).sort(), ['assets', 'index.html', 'staticwebapp.config.json']);
  assert.deepEqual((await readdir(cloudflare)).sort(), ['_headers', 'assets']);
  assert.match(await readFile(join(azure, 'index.html'), 'utf8'), /href="\/assets\/app\/latest.json"/);
  assert.deepEqual(await config(azure), {
    globalHeaders: { 'X-Content-Type-Options': 'nosniff' },
    routes: [{ route: '/assets/*', headers: { 'Cache-Control': 'public, no-cache, no-transform' } }],
  });
  for (const file of await readdir(join(cloudflare, 'assets'), { recursive: true, withFileTypes: true })) {
    if (file.isDirectory()) continue;
    const assetDir = join(azure, relative(cloudflare, file.parentPath));
    assert.deepEqual(await readFile(join(assetDir, file.name)), await readFile(join(file.parentPath, file.name)));
  }
});

test('static and legacy builds reject unlisted files before writing a deployable directory', async (t) => {
  const dir = await workspace(t);
  const source = join(dir, 'assets');
  const legacy = join(dir, 'public');
  const output = join(dir, 'static');
  await cp('assets', source, { recursive: true });
  await mkdir(legacy);

  for (const name of ['.env', 'policies/unlisted.pdf', 'maps/collector.log', 'maps/other.AAB']) {
    await writeFile(join(source, name), 'synthetic fixture');
    await assert.rejects(buildAssets(output, { source }), /非预期文件|不支持的文件/);
    await assert.rejects(stageLegacyAssets(legacy, source), /非预期文件|不支持的文件/);
    await rm(join(source, name));
  }
  assert.deepEqual((await readdir(dir)).sort(), ['assets', 'public']);
  assert.deepEqual(await readdir(legacy), []);
});

test('Azure assets preparation fails if the deployment copy has an unexpected file', async (t) => {
  const output = join(await workspace(t), 'static');
  await buildAssets(output);
  await writeFile(join(output, 'assets', 'app', 'extra.txt'), 'synthetic fixture');
  await assert.rejects(prepareAzureAssets(output), /非预期文件/);
  assert.deepEqual((await readdir(output)).sort(), ['_headers', 'assets']);
});

test('Azure snapshot contains the three original JSON files on static routes and Azure headers', async (t) => {
  const dir = await workspace(t);
  const source = join(dir, 'cloudflare');
  const output = join(dir, 'azure');
  const files = ['classrooms/week.json', 'classrooms/today.json', 'status.json'];
  await mkdir(join(source, 'api/v1/classrooms'), { recursive: true });
  await mkdir(join(source, 'assets'));
  await writeFile(join(source, '_headers'), 'Cloudflare headers');
  await writeFile(join(source, 'assets', 'fixture.apk'), 'synthetic fixture');
  await writeFile(join(source, 'api/v1', 'collector.log'), 'synthetic fixture');
  await assert.rejects(prepareAzureData(source, output), { code: 'ENOENT' });
  assert.deepEqual(await readdir(dir), ['cloudflare']);

  for (const file of files) {
    await writeFile(join(source, 'api/v1', file), JSON.stringify({ fixture: file }));
  }
  await prepareAzureData(source, output);
  assert.deepEqual((await readdir(output)).sort(), ['data', 'index.html', 'staticwebapp.config.json']);
  assert.deepEqual((await readdir(join(output, 'data/v1'), { recursive: true })).sort(),
    ['classrooms', ...files.map((file) => join(file))].sort());
  for (const file of files) {
    assert.deepEqual(await readFile(join(output, 'data/v1', file)), await readFile(join(source, 'api/v1', file)));
    assert.match(await readFile(join(output, 'index.html'), 'utf8'), new RegExp(`href="/data/v1/${file}"`));
  }
  assert.deepEqual(await config(output), {
    globalHeaders: { 'X-Content-Type-Options': 'nosniff' },
    routes: [{
      route: '/data/v1/*',
      headers: { 'Cache-Control': 'public, max-age=60, must-revalidate', 'Access-Control-Allow-Origin': '*' },
    }],
    mimeTypes: { '.json': 'application/json; charset=utf-8' },
  });
});
