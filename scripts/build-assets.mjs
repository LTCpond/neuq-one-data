import { createHash } from 'node:crypto';
import { copyFile, lstat, mkdir, readFile, readdir, stat, writeFile } from 'node:fs/promises';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { pathToFileURL } from 'node:url';

const appRepository = 'LTCpond/neuq-one';
const versionTag = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const downloadMetadata = 'apk-downloads.json';
const downloadOrigin = 'https://download.neuq.one';
const apkAbis = ['arm64-v8a', 'armeabi-v7a', 'x86_64'];
const maxManifestBytes = 16 * 1024;
const maxApkBytes = 2 * 1024 ** 3 - 1;
const maxFileBytes = 25 * 1024 * 1024;
const maxPolicyIndexBytes = 128 * 1024;
const mapFile = 'maps/campus-map.png';
const policyIndex = 'policies/index.json';
const maxReleaseAttempts = 3;
const maxReleaseRetryDelay = 120_000;
const releasePageSize = 100;
const maxReleasePages = 20;
const releaseDiagnosticHeaders = [
  'x-github-request-id',
  'x-ratelimit-limit',
  'x-ratelimit-remaining',
  'x-ratelimit-used',
  'x-ratelimit-reset',
  'x-ratelimit-resource',
  'retry-after',
];

function safeLogValue(value, token) {
  const redacted = token ? value.replaceAll(token, '[已隐藏]') : value;
  return redacted.replace(/\b(?:github_pat_|gh[pousr]_)[A-Za-z0-9_]+\b/g, '[已隐藏]')
    .replace(/[\p{Cc}\u2028\u2029]+/gu, ' ').trim().slice(0, 300);
}

function releaseApiFailure(response, body, token, now, backoff) {
  const reason = typeof body?.message === 'string' ? body.message : response.statusText;
  const details = [safeLogValue(reason, token)];
  for (const name of releaseDiagnosticHeaders) {
    const value = response.headers.get(name);
    if (value !== null) details.push(`${name}=${safeLogValue(value, token)}`);
  }
  const message = [`读取 GitHub Release 失败（HTTP ${response.status}）`, ...details.filter(Boolean)].join(' | ');

  const retryAfter = response.headers.get('retry-after');
  const remaining = response.headers.get('x-ratelimit-remaining');
  const reset = response.headers.get('x-ratelimit-reset');
  const rateLimited = response.status === 429 || (response.status === 403 &&
    (remaining === '0' || retryAfter !== null || /\brate[- ]limit\b/i.test(reason)));
  if (!rateLimited && response.status < 500 && response.status !== 408) {
    return { message, retryDelay: null };
  }

  const requestedDelay = /^\d+$/.test(retryAfter)
    ? Number(retryAfter) * 1000
    : Date.parse(retryAfter) - now;
  const resetDelay = rateLimited && remaining === '0' && /^\d+$/.test(reset)
    ? Number(reset) * 1000 - now
    : NaN;
  const fallback = rateLimited && Number.isNaN(requestedDelay) && Number.isNaN(resetDelay)
    ? 60 * backoff
    : backoff;
  return {
    message,
    retryDelay: Math.max(fallback, requestedDelay || 0, resetDelay || 0),
  };
}

export async function fetchPublishedReleases(request = fetch, { wait = sleep, now = Date.now } = {}) {
  const token = process.env.GITHUB_TOKEN?.trim();
  const headers = { Accept: 'application/vnd.github+json' };
  if (token) headers.Authorization = `Bearer ${token}`;

  const latest = { release: null, prerelease: null };
  for (let page = 1; page <= maxReleasePages; page++) {
    const { releases, hasNext } = await fetchReleasePage(page, request, headers, token, wait, now);
    for (const candidate of releases) {
      if (!eligibleRelease(candidate)) continue;
      const field = candidate.prerelease ? 'prerelease' : 'release';
      if (!latest[field] || compareTags(candidate.tag_name, latest[field].tag_name) > 0) {
        latest[field] = candidate;
      }
    }
    if (!hasNext) return latest;
  }
  throw new Error('GitHub Release 列表超过可检查的页数');
}

async function fetchReleasePage(page, request, headers, token, wait, now) {
  for (let attempt = 1; ; attempt++) {
    const backoff = 1000 * 2 ** (attempt - 1);
    let response;
    let body;
    let message;
    let retryDelay = backoff;
    try {
      response = await request(`https://api.github.com/repos/${appRepository}/releases?per_page=${releasePageSize}&page=${page}`, {
        headers,
        redirect: 'error',
        signal: AbortSignal.timeout(15_000),
      });
      body = await response.json().catch((error) => {
        if (!response.ok || error instanceof SyntaxError) return null;
        throw error;
      });
    } catch (error) {
      const reason = safeLogValue(String(error?.message ?? '请求失败'), token);
      message = `读取 GitHub Release 时发生网络错误或超时（${reason}）`;
    }

    if (message === undefined) {
      if (response.ok) {
        if (!Array.isArray(body)) throw new Error('GitHub Release 列表响应无效');
        return { releases: body, hasNext: /;\s*rel="next"/.test(response.headers.get('link') ?? '') };
      }
      ({ message, retryDelay } = releaseApiFailure(response, body, token, now(), backoff));
    }

    if (retryDelay === null || attempt === maxReleaseAttempts) throw new Error(message);
    if (retryDelay > maxReleaseRetryDelay) {
      throw new Error(`${message} | GitHub 要求的等待时间超过 120 秒，本次构建已停止`);
    }
    console.warn(`${message} | ${Math.ceil(retryDelay / 1000)} 秒后重试（第 ${attempt + 1}/${maxReleaseAttempts} 次请求）`);
    await wait(retryDelay);
  }
}

function eligibleRelease(release) {
  const tag = release?.tag_name;
  return typeof tag === 'string' && tag.length <= 129 && tag.match(versionTag)?.[0] === tag &&
    release.draft === false && typeof release.prerelease === 'boolean' &&
    typeof release.published_at === 'string' && Number.isFinite(Date.parse(release.published_at)) &&
    release.html_url === `https://github.com/${appRepository}/releases/tag/${tag}` &&
    Array.isArray(release.assets) && release.assets.some((asset) => asset?.state === 'uploaded' &&
      typeof asset.name === 'string' && /\.apk$/i.test(asset.name));
}

function compareTags(first, second) {
  const a = first.slice(1).split('.').map(BigInt);
  const b = second.slice(1).split('.').map(BigInt);
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return a[i] > b[i] ? 1 : -1;
  }
  return 0;
}

function changelog(body) {
  if (typeof body !== 'string') return [];
  const changes = [];
  let inSummary = false;
  let fence;
  for (const line of body.split(/\r?\n/)) {
    const delimiter = line.trimStart().match(/^(`{3,}|~{3,})/);
    if (delimiter) {
      if (!fence) fence = delimiter[1];
      else if (delimiter[1][0] === fence[0] && delimiter[1].length >= fence.length) fence = undefined;
      continue;
    }
    if (fence) continue;
    if (/^\s{0,3}#{1,6}\s+/.test(line)) {
      if (inSummary) break;
      inSummary = /^\s{0,3}##\s+本次更新\s*#*\s*$/.test(line);
      continue;
    }
    if (!inSummary) continue;
    const bullet = line.match(/^\s{0,3}[-*+]\s+(.+)$/)?.[1];
    if (!bullet || /https?:\/\/|<[^>]*>|\]\s*\(/i.test(bullet)) continue;
    const plain = bullet.replace(/[\p{Cc}\u2028-\u202e\u2066-\u2069]+/gu, ' ')
      .replace(/([*`])\1?([^*`]+)\1\1?/g, '$2').trim();
    if (plain) changes.push([...plain].slice(0, 160).join('').trimEnd());
    if (changes.length === 5) break;
  }
  return changes;
}

function githubDownload(tag, filename) {
  return `https://github.com/${appRepository}/releases/download/${tag}/${filename}`;
}

async function readDownloadMetadata(asset, request) {
  const options = { redirect: 'manual', signal: AbortSignal.timeout(15_000) };
  let response = await request(asset.browser_download_url, options);
  if ([301, 302, 303, 307, 308].includes(response.status)) {
    const location = new URL(response.headers.get('location'));
    await response.body?.cancel();
    if (!['https://release-assets.githubusercontent.com', 'https://objects.githubusercontent.com'].includes(location.origin) ||
        location.username || location.password || location.hash) {
      throw new Error('下载元数据重定向地址无效');
    }
    response = await request(location.href, { ...options, redirect: 'error' });
  }
  if (response.status !== 200 || !response.body) {
    await response.body?.cancel();
    throw new Error('无法读取下载元数据附件');
  }
  const chunks = [];
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    if (size > asset.size) throw new Error('下载元数据附件过大');
    chunks.push(chunk);
  }
  const bytes = Buffer.concat(chunks, size);
  if (size !== asset.size || asset.digest !== `sha256:${createHash('sha256').update(bytes).digest('hex')}`) {
    throw new Error('下载元数据附件完整性校验失败');
  }
  return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
}

async function releaseDownloads(release, request) {
  const tag = release.tag_name;
  const assets = release.assets.filter((asset) => asset?.name === downloadMetadata);
  if (assets.length === 0) return undefined;
  const [asset] = assets;
  if (assets.length !== 1 || asset.state !== 'uploaded' ||
      asset.browser_download_url !== githubDownload(tag, downloadMetadata) ||
      !Number.isSafeInteger(asset.size) || asset.size < 1 || asset.size > maxManifestBytes) {
    throw new Error(`GitHub Release 下载元数据附件无效（${tag}）`);
  }
  let metadata;
  try {
    metadata = await readDownloadMetadata(asset, request);
  } catch {
    throw new Error(`GitHub Release 下载元数据读取或完整性校验失败（${tag}）`);
  }
  if (metadata?.schemaVersion !== 1 || metadata.version !== tag.slice(1) ||
      !Array.isArray(metadata.downloads) || metadata.downloads.length !== apkAbis.length) {
    throw new Error(`GitHub Release 下载元数据格式无效（${tag}）`);
  }
  return apkAbis.map((abi) => {
    const matches = metadata.downloads.filter((download) => download?.abi === abi);
    const { filename, url, fallbackUrl, size, sha256 } = matches[0] ?? {};
    const expectedFilename = `neuq-one-${tag}-${abi}.apk`;
    const apkAssets = release.assets.filter((candidate) => candidate?.name === expectedFilename);
    const [apk] = apkAssets;
    if (matches.length !== 1 || filename !== expectedFilename ||
        url !== `${downloadOrigin}/releases/${tag}/${expectedFilename}` ||
        fallbackUrl !== githubDownload(tag, expectedFilename) ||
        !Number.isSafeInteger(size) || size < 1 || size > maxApkBytes ||
        typeof sha256 !== 'string' || sha256.length !== 64 || !/^[a-f0-9]+$/.test(sha256) ||
        apkAssets.length !== 1 || apk.state !== 'uploaded' || apk.size !== size ||
        apk.digest !== `sha256:${sha256}` || apk.browser_download_url !== fallbackUrl) {
      throw new Error(`GitHub Release APK 下载信息无效（${tag} ${abi}）`);
    }
    return { abi, filename, url, fallbackUrl, size, sha256 };
  });
}

async function manifestRelease(release, prerelease, request) {
  if (release === null) return null;
  if (!eligibleRelease(release) || release.prerelease !== prerelease) {
    throw new Error('GitHub Release 应匹配发布渠道、使用 vMAJOR.MINOR.PATCH 标签并包含已上传的 APK');
  }
  const downloads = await releaseDownloads(release, request);
  return { version: release.tag_name.slice(1), url: release.html_url, prerelease, changelog: changelog(release.body), downloads };
}

async function validateAssets(source) {
  if (!(await lstat(source)).isDirectory()) throw new Error('静态资源来源必须为目录');
  const files = new Set();
  const entries = await readdir(source, { recursive: true, withFileTypes: true });
  for (const entry of entries) {
    if (entry.isDirectory()) continue;
    const path = join(entry.parentPath, entry.name);
    if (!entry.isFile() || /\.(apk|aab)$/i.test(entry.name)) {
      throw new Error(`静态发布包含不支持的文件: ${path}`);
    }
    if ((await stat(path)).size > maxFileBytes) {
      throw new Error(`静态资源超过 Pages 单文件限制: ${path}`);
    }
    if (entry.name !== '.gitkeep') files.add(relative(source, path).split(sep).join('/'));
  }

  const allowed = new Set([mapFile, policyIndex]);
  if (files.has(policyIndex)) {
    for (const file of await validatePolicies(source, files)) allowed.add(file);
  }
  for (const file of files) {
    if (!allowed.has(file)) throw new Error(`静态发布包含非预期文件: ${file}`);
  }
  return files;
}

async function validatePolicies(source, files) {
  const catalogPath = join(source, policyIndex);
  if ((await stat(catalogPath)).size > maxPolicyIndexBytes) throw new Error('学校政策目录过大');
  const catalog = JSON.parse(await readFile(catalogPath, 'utf8'));
  if (catalog?.version !== 1 || !Array.isArray(catalog.policies)) {
    throw new Error('学校政策目录格式错误');
  }
  const policies = new Set();
  for (const policy of catalog.policies) {
    const { id, title, category, sha256 } = policy ?? {};
    if (typeof id !== 'string' || id.length > 64 || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(id) ||
        typeof title !== 'string' || !title.trim() || typeof category !== 'string' || !category.trim() ||
        typeof sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(sha256)) {
      throw new Error('学校政策条目格式错误');
    }
    const file = `policies/${id}.pdf`;
    if (policies.has(file)) throw new Error('学校政策目录包含重复条目');
    if (!files.has(file)) throw new Error(`学校政策缺少 PDF: ${id}`);
    policies.add(file);
    const pdf = await readFile(join(source, file));
    if (pdf.subarray(0, 5).toString('ascii') !== '%PDF-' || createHash('sha256').update(pdf).digest('hex') !== sha256) {
      throw new Error(`学校政策 PDF 校验失败: ${id}`);
    }
  }
  return policies;
}

async function copyPublicAssets(source, destination, files) {
  await mkdir(destination);
  for (const file of files) {
    const target = join(destination, file);
    await mkdir(dirname(target), { recursive: true });
    await copyFile(join(source, file), target);
  }
}

export async function stageLegacyAssets(output, source = 'assets') {
  source = resolve(source);
  const files = await validateAssets(source);
  await copyPublicAssets(source, join(output, 'assets'), files);
}

export async function buildAssets(output, {
  source = 'assets',
  headersFile = 'internal/exporter/headers.txt',
  release = null,
  prerelease = null,
  request = fetch,
} = {}) {
  source = resolve(source);
  const manifest = {
    schemaVersion: 1,
    release: await manifestRelease(release, false, request),
    prerelease: await manifestRelease(prerelease, true, request),
  };
  const manifestText = `${JSON.stringify(manifest, null, 2)}\n`;
  if (Buffer.byteLength(manifestText) > maxManifestBytes) throw new Error('App 版本清单过大');
  const files = await validateAssets(source);
  if (!files.has(mapFile) || !files.has(policyIndex)) throw new Error('静态发布缺少校园地图或政策目录');
  const headers = (await readFile(headersFile, 'utf8')).replaceAll('\r\n', '\n');
  const assetsHeaders = headers.match(/^\/assets\/\*\n(?:[ \t]+[^\n]+\n?)+/m)?.[0];
  if (!assetsHeaders) throw new Error('缺少 Pages 静态资源响应头');

  await mkdir(dirname(output), { recursive: true });
  await mkdir(output);
  const assets = join(output, 'assets');
  await copyPublicAssets(source, assets, files);
  await writeFile(join(output, '_headers'), assetsHeaders);
  await mkdir(join(assets, 'app'), { recursive: true });
  await writeFile(join(assets, 'app', 'latest.json'), manifestText, { flag: 'wx' });
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const [output = '.work/assets-public', mode] = process.argv.slice(2);
  if (mode === '--legacy') {
    await stageLegacyAssets(output);
  } else {
    if (mode && mode !== '--github-release') throw new Error(`未知构建选项: ${mode}`);
    await buildAssets(output, mode === '--github-release' ? await fetchPublishedReleases() : {});
  }
}
