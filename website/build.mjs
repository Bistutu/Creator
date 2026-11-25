import { copyFile, mkdir, mkdtemp, readFile, rename, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { inventory, packages, platformCards, project, readRelease } from './releases.mjs';

const source = fileURLToPath(new URL('./', import.meta.url));
const dist = join(source, 'dist');
const stage = await mkdtemp(join(source, '.build-'));

try {
  const release = await readRelease();
  const releases = await inventory(release);
  const template = await readFile(join(source, 'index.html'), 'utf8');
  const html = template.replaceAll('{{product}}', release.name)
    .replace('{{version}}', release.version)
    .replace('{{platforms}}', platformCards(releases));
  const downloads = releases.flatMap(({ id, artifacts }) => artifacts.map((item) => ({ platform: id, ...item })));

  await mkdir(join(stage, 'downloads'));
  await mkdir(join(stage, 'assets'));
  await Promise.all([
    copyFile(join(source, 'site.css'), join(stage, 'site.css')),
    copyFile(join(source, 'site.js'), join(stage, 'site.js')),
    ...['icon.png', 'create.png', 'standalone.png', 'settings.png'].map((file) =>
      copyFile(join(source, 'assets', file), join(stage, 'assets', file))),
    copyFile(join(project, 'LICENSE'), join(stage, 'LICENSE.txt')),
    writeFile(join(stage, 'index.html'), html),
    writeFile(join(stage, 'downloads.json'), JSON.stringify({ name: release.name, version: release.version, downloads }, null, 2) + '\n'),
    ...downloads.map(({ file }) => copyFile(join(packages, file), join(stage, 'downloads', file))),
  ]);
  await rm(dist, { recursive: true, force: true });
  await rename(stage, dist);
  console.log(`${dist} · ${downloads.length} downloads`);
} finally {
  await rm(stage, { recursive: true, force: true });
}
