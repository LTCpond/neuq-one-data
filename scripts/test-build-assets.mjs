import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { cp, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';

import { buildAssets, fetchPublishedReleases } from './build-assets.mjs';

const release = {
  tag_name: 'v1.10.0',
  html_url: 'https://github.com/LTCpond/neuq-one/releases/tag/v1.10.0',
  draft: false,
  prerelease: false,
  published_at: '2026-11-26T08:00:00Z',
  assets: [{ name: 'neuq-one.apk', state: 'uploaded' }],
};

function tagged(tag, changes = {}) {
  return { ...release, tag_name: tag,
    html_url: `https://github.com/LTCpond/neuq-one/releases/tag/${tag}`, ...changes };
}

function distribution(tag, { prerelease = false, edit = () => {} } = {}) {
  const downloads = ['arm64-v8a', 'armeabi-v7a', 'x86_64'].map((abi, index) => {
    const filename = `neuq-one-${tag}-${abi}.apk`;
    return {
      abi, filename,
      url: `https://download.neuq.one/releases/${tag}/${filename}`,
      fallbackUrl: `https://github.com/LTCpond/neuq-one/releases/download/${tag}/${filename}`,
      size: 4000 + index,
      sha256: createHash('sha256').update(abi).digest('hex'),
    };
  });
  const apkAssets = downloads.map(({ filename, fallbackUrl, size, sha256 }) => ({
    name: filename, state: 'uploaded', browser_download_url: fallbackUrl, size, digest: `sha256:${sha256}`,
  }));
  const metadata = { schemaVersion: 1, version: tag.slice(1), downloads };
  edit(metadata, apkAssets);
  const bytes = Buffer.from(JSON.stringify(metadata));
  const attachment = {
    name: 'apk-downloads.json', state: 'uploaded', size: bytes.length,
    browser_download_url: `https://github.com/LTCpond/neuq-one/releases/download/${tag}/apk-downloads.json`,
    digest: `sha256:${createHash('sha256').update(bytes).digest('hex')}`,
  };
  return { release: tagged(tag, { prerelease, assets: [...apkAssets, attachment] }), downloads, bytes, attachment };
}

async function temp(t) {
  const root = await mkdtemp(join(tmpdir(), 'neuq-one-assets-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}

async function readManifest(output) {
  return JSON.parse(await readFile(join(output, 'assets', 'app', 'latest.json'), 'utf8'));
}

function setReleaseToken(t, token) {
  const original = process.env.GITHUB_TOKEN;
  if (token === undefined) delete process.env.GITHUB_TOKEN;
  else process.env.GITHUB_TOKEN = token;
  t.after(() => {
    if (original === undefined) delete process.env.GITHUB_TOKEN;
    else process.env.GITHUB_TOKEN = original;
  });
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
  assert.deepEqual(await readManifest(output), { schemaVersion: 1, release: null, prerelease: null });
});

test('uses the public release list and stages only metadata from GitHub', async (t) => {
  setReleaseToken(t, 'release-token-for-tests');
  const output = join(await temp(t), 'site');
  const latest = await fetchPublishedReleases(async (url, options) => {
    assert.equal(url, 'https://api.github.com/repos/LTCpond/neuq-one/releases?per_page=100&page=1');
    assert.equal(options.redirect, 'error');
    assert.equal(options.headers.Authorization, 'Bearer release-token-for-tests');
    return Response.json([release]);
  });
  await buildAssets(output, latest);
  assert.ok(!(await readdir(join(output, 'assets', 'app'))).includes('neuq-one.apk'));
  assert.deepEqual(await readManifest(output), {
    schemaVersion: 1,
    release: { version: '1.10.0', url: release.html_url, prerelease: false, changelog: [] },
    prerelease: null,
  });
});

test('publishes verified downloads for both channels and preserves schema 1 release links', async (t) => {
  setReleaseToken(t, 'release-token-for-tests');
  const output = join(await temp(t), 'site');
  const stable = distribution('v2.1.0');
  const beta = distribution('v2.2.0', { prerelease: true });
  const old = distribution('v2.0.0');
  const responseUrls = new Map();
  for (const item of [stable, beta]) {
    const signed = `https://release-assets.githubusercontent.com/files/${item.release.tag_name}?signature=test`;
    responseUrls.set(item.attachment.browser_download_url, Response.redirect(signed));
    responseUrls.set(signed, new Response(item.bytes));
  }
  const latest = await fetchPublishedReleases(async () => Response.json([stable.release, beta.release, old.release]));
  await buildAssets(output, {
    ...latest,
    request: async (url, options) => {
      assert.equal(options.headers, undefined);
      assert.equal(options.redirect, url.startsWith('https://github.com/') ? 'manual' : 'error');
      assert.ok(responseUrls.has(url), 'only selected release metadata is fetched');
      return responseUrls.get(url);
    },
  });

  const manifest = await readManifest(output);
  assert.equal(manifest.schemaVersion, 1);
  for (const [slot, fixture] of [['release', stable], ['prerelease', beta]]) {
    const { version, url, prerelease, changelog, downloads } = manifest[slot];
    assert.deepEqual({ version, url, prerelease, changelog }, {
      version: fixture.release.tag_name.slice(1), url: fixture.release.html_url,
      prerelease: fixture.release.prerelease, changelog: [],
    });
    assert.deepEqual(downloads, fixture.downloads);
  }
  assert.deepEqual(await readdir(join(output, 'assets', 'app')), ['latest.json']);
});

test('uses GitHub alone for a historical APK with the expected filename but no attachment', async (t) => {
  const output = join(await temp(t), 'site');
  const fixture = distribution('v1.3.0');
  fixture.release.assets.pop();
  await buildAssets(output, { release: fixture.release, request: () => assert.fail('no download metadata') });
  assert.deepEqual(await readManifest(output), {
    schemaVersion: 1,
    release: { version: '1.3.0', url: fixture.release.html_url, prerelease: false, changelog: [] },
    prerelease: null,
  });
});

test('stops deployment when release metadata or the referenced APKs fail validation', async (t) => {
  const root = await temp(t);
  const output = join(root, 'site');
  for (const edit of [
    (metadata) => { metadata.version = '2.0.0'; },
    (metadata) => { metadata.schemaVersion = 2; },
    (metadata) => { metadata.downloads.pop(); },
    (metadata) => { metadata.downloads[1] = metadata.downloads[0]; },
    (metadata) => { metadata.downloads[0].filename = 'elsewhere.apk'; },
    (metadata) => { metadata.downloads[0].url += '?query=invalid'; },
    (metadata) => { metadata.downloads[0].url = metadata.downloads[0].url.replace('https:', 'http:'); },
    (metadata) => { metadata.downloads[0].url = metadata.downloads[0].url.replace('download.neuq.one', 'download.neuq.one.test'); },
    (metadata) => { delete metadata.downloads[0].url; },
    (metadata) => { metadata.downloads[0].fallbackUrl = metadata.downloads[0].fallbackUrl.replace('/v1.3.0/', '/v2.0.0/'); },
    (metadata) => { metadata.downloads[0].size = 2 ** 31; },
    (metadata) => { metadata.downloads[0].sha256 += '\n'; },
    (_, apks) => { apks[0].size++; },
    (_, apks) => { apks[0].digest = null; },
  ]) {
    const fixture = distribution('v1.3.0', { edit });
    await assert.rejects(buildAssets(output, {
      release: fixture.release, request: async () => new Response(fixture.bytes),
    }), /下载元数据格式无效|APK 下载信息无效/);
  }
  assert.deepEqual(await readdir(root), []);
});

test('bounds and authenticates the metadata response and rejects unsafe redirects', async (t) => {
  const root = await temp(t);
  const output = join(root, 'site');
  const fixture = distribution('v1.3.0');
  for (const invalidResponse of [
    Response.redirect('https://external.test/apk-downloads.json'),
    new Response(null, { status: 404 }),
    new Response(Buffer.concat([fixture.bytes, Buffer.from(' ')])),
    new Response(Buffer.alloc(fixture.bytes.length, 32)),
  ]) {
    await assert.rejects(buildAssets(output, {
      release: fixture.release,
      request: async (url) => {
        assert.equal(url, fixture.attachment.browser_download_url);
        return invalidResponse;
      },
    }), /下载元数据读取或完整性校验失败/);
  }
  fixture.attachment.browser_download_url = 'https://example.org/apk-downloads.json';
  await assert.rejects(buildAssets(output, {
    release: fixture.release, request: () => assert.fail('untrusted attachment URL'),
  }), /下载元数据附件无效/);
  assert.deepEqual(await readdir(root), []);
});

test('an empty list clears both channels; a 404 does not publish an empty manifest', async (t) => {
  setReleaseToken(t, undefined);
  const result = await fetchPublishedReleases(async (_, { headers }) => {
    assert.equal(headers.Authorization, undefined);
    return Response.json([]);
  }, { wait: () => assert.fail('empty list should not retry') });
  assert.deepEqual(result, { release: null, prerelease: null });
  await assert.rejects(fetchPublishedReleases(async () =>
    Response.json({ message: 'Not Found' }, { status: 404 }),
  { wait: () => assert.fail('404 should not retry') }), /HTTP 404/);
});

test('paginates and independently selects the highest valid published stable and beta', async () => {
  const beta = tagged('v2.10.0', { prerelease: true });
  const stable = tagged('v1.12.0');
  const pages = [
    [tagged('v2.9.0', { prerelease: true }), tagged('v9.1.0', { draft: true }),
      tagged('v9.2.0', { assets: [] }), tagged('v9.3.0-rc.1'), release],
    [beta, stable, tagged('v9.4.0', { published_at: null }),
      tagged('v9.5.0', { html_url: 'https://other.test' })],
  ];
  const selected = await fetchPublishedReleases(async (request) => {
    const uri = new URL(request);
    assert.equal(uri.host, 'api.github.com');
    const page = Number(uri.searchParams.get('page'));
    return Response.json(pages[page - 1], { headers: page === 1 ? {
      Link: '<https://untrusted.test/>; rel="next"',
    } : {} });
  });
  assert.deepEqual(selected, { release: stable, prerelease: beta });
  const withdrawn = await fetchPublishedReleases(async () => Response.json([release]));
  assert.deepEqual(withdrawn, { release, prerelease: null });
});

test('extracts only the bounded plain text summary and leaves the stable slot empty for beta', async (t) => {
  const output = join(await temp(t), 'site');
  const body = [
    '```md', '## 本次更新', '- 代码块里的假摘要', '```',
    '# 发布说明', '- 其他段落', '## 本次更新',
    '- **修复课程提醒**', '- 优化空教室', '- [提交记录](https://github.com/example)',
    '- 清晰的教务状态', '- <script>alert(1)</script>',
    `- ${'长'.repeat(180)}`, '- 改善性能', '- 超过数量限制', '## What’s Changed',
    '- 生成的 PR 和贡献者',
  ].join('\n');
  await buildAssets(output, { prerelease: tagged('v2.0.0', { prerelease: true, body }) });
  const manifest = await readManifest(output);
  assert.equal(manifest.release, null);
  assert.deepEqual(manifest.prerelease, {
    version: '2.0.0', url: tagged('v2.0.0').html_url, prerelease: true,
    changelog: ['修复课程提醒', '优化空教室', '清晰的教务状态', '长'.repeat(160), '改善性能'],
  });
});

test('reports permission errors once with safe GitHub diagnostics', async (t) => {
  const token = 'test-secret-release-token';
  setReleaseToken(t, token);
  let calls = 0;
  await assert.rejects(fetchPublishedReleases(async () => {
    calls++;
    return Response.json({ message: `Resource not accessible by integration\n${token}` }, {
      status: 403,
      headers: {
        'x-github-request-id': 'request-id',
        'x-ratelimit-limit': '1000',
        'x-ratelimit-remaining': '991',
        'x-ratelimit-used': '9',
        'x-ratelimit-reset': '1795670400',
        'x-ratelimit-resource': 'core',
        'authorization': `Bearer ${token}`,
      },
    });
  }, { wait: () => assert.fail('permission errors should not retry') }), (error) => {
    assert.match(error.message, /HTTP 403.*Resource not accessible by integration.*\[已隐藏\]/);
    assert.match(error.message, /x-github-request-id=request-id/);
    assert.match(error.message, /x-ratelimit-limit=1000.*remaining=991.*used=9.*reset=1795670400.*resource=core/);
    assert.ok(!error.message.includes(token));
    assert.doesNotMatch(error.message, /authorization/i);
    assert.doesNotMatch(error.message, /\n/);
    return true;
  });
  assert.equal(calls, 1);
});

test('retries a primary rate limit after the quota reset', async (t) => {
  const warn = t.mock.method(console, 'warn', () => {});
  const now = 1_795_670_400_000;
  const waits = [];
  let calls = 0;
  const result = await fetchPublishedReleases(async () => {
    if (++calls > 1) return Response.json([release]);
    return Response.json({ message: 'API rate limit exceeded' }, {
      status: 403,
      headers: { 'x-ratelimit-remaining': '0', 'x-ratelimit-reset': String(now / 1000 + 7) },
    });
  }, { now: () => now, wait: async (ms) => { waits.push(ms); } });
  assert.deepEqual(result, { release, prerelease: null });
  assert.deepEqual(waits, [7000]);
  assert.match(warn.mock.calls[0].arguments[0], /HTTP 403.*rate limit exceeded.*remaining=0.*reset=/);
});

test('honors Retry-After as seconds or an HTTP date for 429', async (t) => {
  t.mock.method(console, 'warn', () => {});
  const now = 1_795_670_400_000;
  for (const [retryAfter, expected] of [['4', 4000], [new Date(now + 8000).toUTCString(), 8000]]) {
    const waits = [];
    let calls = 0;
    const result = await fetchPublishedReleases(async () => {
      if (++calls > 1) return Response.json([release]);
      return new Response(null, { status: 429, headers: { 'retry-after': retryAfter } });
    }, { now: () => now, wait: async (ms) => { waits.push(ms); } });
    assert.deepEqual(result, { release, prerelease: null });
    assert.deepEqual(waits, [expected]);
  }
});

test('backs off secondary limits without retry headers and fails after three requests', async (t) => {
  t.mock.method(console, 'warn', () => {});
  const waits = [];
  let calls = 0;
  await assert.rejects(fetchPublishedReleases(async () => {
    calls++;
    return Response.json({ message: 'You have exceeded a secondary rate limit.' }, { status: 403 });
  }, { wait: async (ms) => { waits.push(ms); } }), /HTTP 403.*secondary rate limit/);
  assert.equal(calls, 3);
  assert.deepEqual(waits, [60_000, 120_000]);
});

test('fails without an early retry when GitHub asks for a long wait', async () => {
  await assert.rejects(fetchPublishedReleases(async () => new Response(null, {
    status: 429,
    headers: { 'retry-after': '600' },
  }), { wait: () => assert.fail('must respect the full rate-limit wait') }), /HTTP 429.*retry-after=600.*超过 120 秒/);
});

test('retries temporary network and upstream errors without logging the token', async (t) => {
  const token = 'test-network-token';
  setReleaseToken(t, token);
  const warn = t.mock.method(console, 'warn', () => {});
  const waits = [];
  let calls = 0;
  const result = await fetchPublishedReleases(async () => {
    if (++calls === 1) throw new TypeError(`socket closed: ${token}`);
    if (calls === 2) return new Response('service unavailable', { status: 503, statusText: 'Service Unavailable' });
    return Response.json([release]);
  }, { wait: async (ms) => { waits.push(ms); } });
  assert.deepEqual(result, { release, prerelease: null });
  assert.deepEqual(waits, [1000, 2000]);
  const messages = warn.mock.calls.map((call) => call.arguments[0]);
  assert.match(messages[0], /socket closed.*\[已隐藏\]/);
  assert.match(messages[1], /HTTP 503.*Service Unavailable/);
  assert.ok(messages.every((message) => !message.includes(token)));
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
    await assert.rejects(buildAssets(join(root, 'site'), { release: { ...release, ...invalid } }), /GitHub Release/);
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
