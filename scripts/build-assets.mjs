import { createHash } from 'node:crypto';
import { cp, mkdir, readFile, readdir, stat, writeFile } from 'node:fs/promises';
import { basename, dirname, join, resolve } from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { pathToFileURL } from 'node:url';

const appRepository = 'LTCpond/neuq-one';
const versionTag = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const maxFileBytes = 25 * 1024 * 1024;
const maxPolicyIndexBytes = 128 * 1024;
const maxReleaseAttempts = 3;
const maxReleaseRetryDelay = 120_000;
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

export async function fetchLatestRelease(request = fetch, { wait = sleep, now = Date.now } = {}) {
  const token = process.env.GITHUB_TOKEN?.trim();
  const headers = { Accept: 'application/vnd.github+json' };
  if (token) headers.Authorization = `Bearer ${token}`;

  for (let attempt = 1; ; attempt++) {
    const backoff = 1000 * 2 ** (attempt - 1);
    let response;
    let body;
    let message;
    let retryDelay = backoff;
    try {
      response = await request(`https://api.github.com/repos/${appRepository}/releases/latest`, {
        headers,
        redirect: 'error',
        signal: AbortSignal.timeout(15_000),
      });
      if (response.status === 404) {
        await response.body?.cancel().catch(() => {});
        return null;
      }
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
        if (!body || typeof body !== 'object' || Array.isArray(body)) {
          throw new Error('GitHub Release 响应无效');
        }
        return body;
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

function makeManifest(release) {
  if (release === null) return { schemaVersion: 1, release: null };
  const tag = release.tag_name;
  const url = `https://github.com/${appRepository}/releases/tag/${tag}`;
  if (typeof tag !== 'string' || !versionTag.test(tag) ||
      release.draft !== false || release.prerelease !== false ||
      !release.published_at || release.html_url !== url ||
      !Array.isArray(release.assets) ||
      !release.assets.some((asset) => asset?.state === 'uploaded' &&
        typeof asset.name === 'string' && /\.apk$/i.test(asset.name))) {
    throw new Error('正式 GitHub Release 应使用 vMAJOR.MINOR.PATCH 标签并包含已上传的 APK');
  }
  return { schemaVersion: 1, release: { version: tag.slice(1), url } };
}

async function validateAssets(source) {
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
  }

  const catalogPath = join(source, 'policies', 'index.json');
  if (!entries.some((entry) => join(entry.parentPath, entry.name) === catalogPath)) return;
  if ((await stat(catalogPath)).size > maxPolicyIndexBytes) throw new Error('学校政策目录过大');
  const catalog = JSON.parse(await readFile(catalogPath, 'utf8'));
  if (catalog?.version !== 1 || !Array.isArray(catalog.policies)) {
    throw new Error('学校政策目录格式错误');
  }
  const ids = new Set();
  for (const policy of catalog.policies) {
    const { id, title, category, sha256 } = policy ?? {};
    if (typeof id !== 'string' || id.length > 64 || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(id) ||
        typeof title !== 'string' || !title.trim() || typeof category !== 'string' || !category.trim() ||
        typeof sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(sha256) || ids.has(id)) {
      throw new Error('学校政策条目格式错误');
    }
    ids.add(id);
    const pdf = await readFile(join(source, 'policies', `${id}.pdf`));
    if (pdf.subarray(0, 5).toString('ascii') !== '%PDF-' || createHash('sha256').update(pdf).digest('hex') !== sha256) {
      throw new Error(`学校政策 PDF 校验失败: ${id}`);
    }
  }
}

export async function buildAssets(output, {
  source = 'assets',
  headersFile = 'internal/exporter/headers.txt',
  release = null,
} = {}) {
  source = resolve(source);
  const manifest = makeManifest(release);
  await validateAssets(source);
  const headers = (await readFile(headersFile, 'utf8')).replaceAll('\r\n', '\n');
  const assetsHeaders = headers.match(/^\/assets\/\*\n(?:[ \t]+[^\n]+\n?)+/m)?.[0];
  if (!assetsHeaders) throw new Error('缺少 Pages 静态资源响应头');

  await mkdir(dirname(output), { recursive: true });
  await mkdir(output);
  const assets = join(output, 'assets');
  await cp(source, assets, { recursive: true, filter: (path) => basename(path) !== '.gitkeep' });
  await writeFile(join(output, '_headers'), assetsHeaders);
  await mkdir(join(assets, 'app'), { recursive: true });
  await writeFile(join(assets, 'app', 'latest.json'), `${JSON.stringify(manifest, null, 2)}\n`, { flag: 'wx' });
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const [output = '.work/assets-public', mode] = process.argv.slice(2);
  if (mode && mode !== '--github-release') throw new Error(`未知构建选项: ${mode}`);
  const release = mode === '--github-release' ? await fetchLatestRelease() : null;
  await buildAssets(output, { release });
}
