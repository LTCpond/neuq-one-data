import { readFile, readdir, rm } from 'node:fs/promises';
import { join, relative, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';
import { writeAzureSite } from './azure-site.mjs';

export async function prepareAzureAssets(output) {
  output = resolve(output);
  const catalog = JSON.parse(await readFile(join(output, 'assets/policies/index.json'), 'utf8'));
  const expectedFiles = new Set([
    '_headers',
    'assets/maps/campus-map.png',
    'assets/policies/index.json',
    'assets/app/latest.json',
    ...catalog.policies.map(({ id }) => `assets/policies/${id}.pdf`),
  ]);

  for (const entry of await readdir(output, { recursive: true, withFileTypes: true })) {
    if (entry.isDirectory()) continue;
    const path = relative(output, join(entry.parentPath, entry.name)).split(sep).join('/');
    if (!entry.isFile() || !expectedFiles.delete(path)) {
      throw new Error(`Azure 部署产物包含非预期文件：${path}`);
    }
  }
  if (expectedFiles.size) {
    throw new Error(`Azure 部署产物缺少文件：${[...expectedFiles].join('、')}`);
  }

  await writeAzureSite(output, {
    title: 'NEUQ One 静态资源',
    links: [
      ['/assets/maps/campus-map.png', '校园地图'],
      ['/assets/policies/index.json', '政策目录'],
      ['/assets/app/latest.json', 'App 版本清单'],
    ],
    routes: [{ route: '/assets/*', headers: { 'Cache-Control': 'public, no-cache, no-transform' } }],
  });
  await rm(join(output, '_headers'));
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  await prepareAzureAssets(process.argv[2] ?? '.work/assets-public');
}
