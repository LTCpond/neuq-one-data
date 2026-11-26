import { readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { join, relative, resolve, sep } from 'node:path';

const output = resolve(process.argv[2] ?? '.work/assets-public');
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

const index = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>NEUQ One 静态资源测试站</title>
</head>
<body>
  <h1>NEUQ One 静态资源测试站</h1>
  <ul>
    <li><a href="/assets/maps/campus-map.png">校园地图</a></li>
    <li><a href="/assets/policies/index.json">政策目录</a></li>
    <li><a href="/assets/app/latest.json">App 版本清单</a></li>
  </ul>
</body>
</html>
`;

const config = {
  globalHeaders: { 'X-Content-Type-Options': 'nosniff' },
  routes: [
    {
      route: '/assets/*',
      headers: { 'Cache-Control': 'public, no-cache, no-transform' },
    },
  ],
};

await writeFile(join(output, 'index.html'), index, { flag: 'wx' });
await writeFile(join(output, 'staticwebapp.config.json'), `${JSON.stringify(config, null, 2)}\n`, { flag: 'wx' });
await rm(join(output, '_headers'));
