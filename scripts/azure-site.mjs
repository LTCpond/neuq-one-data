import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';

export async function writeAzureSite(output, { title, links, routes, mimeTypes }) {
  const index = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${title}</title>
</head>
<body>
  <h1>${title}</h1>
  <ul>
${links.map(([path, label]) => `    <li><a href="${path}">${label}</a></li>`).join('\n')}
  </ul>
</body>
</html>
`;
  const config = { globalHeaders: { 'X-Content-Type-Options': 'nosniff' }, routes, mimeTypes };
  await writeFile(join(output, 'index.html'), index, { flag: 'wx' });
  await writeFile(join(output, 'staticwebapp.config.json'), `${JSON.stringify(config, null, 2)}\n`, { flag: 'wx' });
}
