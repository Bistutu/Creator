import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { inventory, platforms, project, readRelease } from './releases.mjs';

const [tag, commit, directory] = process.argv.slice(2);
const release = await readRelease();
const packageInfo = JSON.parse(await readFile(join(project, 'website/package.json'), 'utf8'));
const lockfile = JSON.parse(await readFile(join(project, 'website/package-lock.json'), 'utf8'));

if (tag !== `v${release.version}` || [packageInfo.version, lockfile.version, lockfile.packages[''].version]
  .some((version) => version !== release.version)) {
  throw new Error('Release tag and package versions must match mygo.json');
}

const notes = await readFile(join(project, 'docs/releases', `${release.version}.md`), 'utf8');
if (!/^## English$/m.test(notes) || !/^## 中文$/m.test(notes)) {
  throw new Error('Release notes must include English and Chinese sections');
}

if (commit || directory) {
  if (!/^[0-9a-f]{40}$/.test(commit) || !directory) {
    throw new Error('Pass the source commit SHA and the directory containing the release packages');
  }

  const downloaded = await inventory(release, directory);
  const downloads = downloaded.flatMap(({ id, artifacts }) => artifacts.map((item) => ({ platform: id, ...item })));
  const present = new Set(downloads.map(({ file }) => file));
  const missing = platforms(release).flatMap(({ artifacts }) => artifacts.filter(({ file }) => !present.has(file)));
  if (missing.length > 0) {
    throw new Error(`Missing release packages: ${missing.map(({ file }) => file).join(', ')}`);
  }

  await Promise.all([
    writeFile(join(directory, 'SHA256SUMS.txt'), downloads.map(({ file, sha256 }) => `${sha256}  ${file}\n`).join('')),
    writeFile(join(directory, 'release-manifest.json'), JSON.stringify({ ...release, tag, commit, downloads }, null, 2) + '\n'),
  ]);
}
