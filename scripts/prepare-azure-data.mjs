import { copyFile, lstat, mkdir } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { writeAzureSite } from './azure-site.mjs';

const snapshotFiles = [
  ['classrooms/week.json', '七日空教室快照'],
  ['classrooms/today.json', '当天空教室快照'],
  ['status.json', '采集状态'],
];

export async function prepareAzureData(snapshot, output) {
  const source = join(snapshot, 'api/v1');
  const target = join(output, 'data/v1');
  for (const [file] of snapshotFiles) {
    if (!(await lstat(join(source, file))).isFile()) throw new Error(`快照文件必须为普通文件：${file}`);
  }

  await mkdir(dirname(output), { recursive: true });
  await mkdir(output);
  await mkdir(join(target, 'classrooms'), { recursive: true });
  for (const [file] of snapshotFiles) {
    await copyFile(join(source, file), join(target, file));
  }
  await writeAzureSite(output, {
    title: 'NEUQ One 空教室',
    links: snapshotFiles.map(([file, label]) => [`/data/v1/${file}`, label]),
    routes: [{
      route: '/data/v1/*',
      headers: {
        'Cache-Control': 'public, max-age=60, must-revalidate',
        'Access-Control-Allow-Origin': '*',
      },
    }],
    mimeTypes: { '.json': 'application/json; charset=utf-8' },
  });
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const [snapshot = 'public', output = '.work/classrooms-azure'] = process.argv.slice(2);
  await prepareAzureData(snapshot, output);
}
