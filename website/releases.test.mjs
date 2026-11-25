import { deepEqual, equal, match, doesNotMatch, rejects, throws } from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import { verifyArchitecture } from './binaries.mjs';
import { packageLinux } from './linux-packages.mjs';
import { inventory, platformCards, platforms, readRelease } from './releases.mjs';

test('release names cover each platform and architecture', async () => {
  const release = await readRelease();
  equal(release.name, 'Creator');
  equal(release.slug, 'creator');
  const variants = platforms(release);
  deepEqual(variants.map(({ id }) => id), ['macos', 'windows', 'linux']);
  const files = variants.flatMap(({ artifacts }) => artifacts.map(({ file }) => file));
  deepEqual(files, [
    `Creator-${release.version}-macos-universal.dmg`,
    `Creator-${release.version}-windows-amd64-setup.exe`,
    `Creator-${release.version}-windows-arm64-setup.exe`,
    `Creator-${release.version}-windows-amd64.zip`,
    `Creator-${release.version}-windows-arm64.zip`,
    `Creator-${release.version}-linux-amd64.deb`,
    `Creator-${release.version}-linux-amd64.tar.xz`,
    `Creator-${release.version}-linux-arm64.deb`,
    `Creator-${release.version}-linux-arm64.tar.xz`,
  ]);
  equal(variants[0].artifacts[0].source, `darwin-universal/creator-${release.version}.dmg`);
  equal(variants[1].artifacts[0].source, `windows-amd64/creator-setup-${release.version}-amd64.exe`);
});

test('publishing requires a matching tag and a complete set of packages', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'creator-release-assets-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const release = await readRelease();
  const tag = `v${release.version}`;
  const commit = '0123456789abcdef0123456789abcdef01234567';
  const script = fileURLToPath(new URL('./release-assets.mjs', import.meta.url));
  const run = (...args) => execFileSync(process.execPath, [script, ...args], { stdio: 'pipe' });
  run(tag);
  throws(() => run(`${tag}-other`), /Release tag and package versions must match/);
  throws(() => run(tag, commit, directory), /Missing release packages/);

  const files = platforms(release).flatMap(({ artifacts }) => artifacts.map(({ file }) => file));
  await Promise.all(files.map((file) => writeFile(join(directory, file), file)));
  run(tag, commit, directory);

  const manifest = JSON.parse(await readFile(join(directory, 'release-manifest.json'), 'utf8'));
  equal(manifest.tag, tag);
  equal(manifest.version, release.version);
  equal(manifest.commit, commit);
  deepEqual(manifest.downloads.map(({ file }) => file), files);
  const checksums = files.map((file) => `${createHash('sha256').update(file).digest('hex')}  ${file}\n`).join('');
  equal(await readFile(join(directory, 'SHA256SUMS.txt'), 'utf8'), checksums);
});

test('downloads list only existing, nonempty files and supply a checksum', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'creator-downloads-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const release = { name: 'Creator', slug: 'creator', version: '1.2.3' };
  const [mac, win, linux] = platforms(release);
  await writeFile(join(dir, mac.artifacts[0].file), 'abc');
  await writeFile(join(dir, win.artifacts[0].file), '');
  await mkdir(join(dir, linux.artifacts[0].file));

  const releases = await inventory(release, dir);
  equal(releases[1].artifacts.length, 0);
  equal(releases[2].artifacts.length, 0);
  const artifact = releases[0].artifacts[0];
  equal(artifact.bytes, 3);
  equal(artifact.sha256, 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad');
  const html = platformCards(releases);
  match(html, /href="downloads\/Creator-1\.2\.3-macos-universal\.dmg" download/);
  match(html, /<small>1 KB<\/small>/);
  match(html, new RegExp(artifact.sha256));
  doesNotMatch(html, /windows-amd64(?:-setup\.exe|\.zip)|linux-amd64\.deb/);
  equal(html.match(/此平台的预览包暂未提供/g).length, 2);
});

test('packaging rejects binaries from a different architecture', () => {
  for (const [arch, other, pe, elf] of [
    ['amd64', 'arm64', 0x8664, 62],
    ['arm64', 'amd64', 0xaa64, 183],
  ]) {
    for (const [format, binary] of [['PE', peHeader(pe)], ['ELF', elfHeader(elf)]]) {
      verifyArchitecture(binary, format, arch, 'release');
      throws(() => verifyArchitecture(binary, format, other, 'release'), new RegExp(`expected ${other} ${format}, got ${arch}`));
    }
  }
});

test('packaging rejects truncated headers and non-executable files', () => {
  const pe = peHeader(0x8664);
  const elf = elfHeader(62);
  throws(() => verifyArchitecture(pe.subarray(0, 70), 'PE', 'amd64', 'release'), /invalid or unsupported/);
  throws(() => verifyArchitecture(elf.subarray(0, 63), 'ELF', 'amd64', 'release'), /invalid or unsupported/);
  pe.writeUInt16LE(0x2000, 0x40 + 22); // DLL flag without the executable flag.
  elf.writeUInt16LE(1, 16); // ET_REL object instead of an executable.
  throws(() => verifyArchitecture(pe, 'PE', 'amd64', 'release'), /invalid or unsupported/);
  throws(() => verifyArchitecture(elf, 'ELF', 'amd64', 'release'), /invalid or unsupported/);
});

test('Linux TAR.XZ preserves the launcher and filesystem archive byte for byte', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'creator-tar-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const product = { slug: 'creator', version: '1.2.3' };
  const archive = await linuxFixture(dir, false, 62);
  const source = join(dir, 'creator.tar.gz');
  const output = join(dir, 'creator.tar.xz');
  await writeFile(source, gzipSync(archive));
  const artifact = { file: 'creator.tar.xz', format: 'TAR.XZ', arch: 'amd64' };
  await packageLinux(source, output, artifact, product);
  deepEqual(execFileSync('xz', ['-dc', output]), archive);
  const filters = execFileSync('xz', ['--robot', '-lvv', output], { encoding: 'utf8' });
  match(filters, /--x86/);
  await rejects(packageLinux(source, output, { ...artifact, arch: 'arm64' }, product), /expected arm64 ELF, got amd64/);
  deepEqual(execFileSync('xz', ['-dc', output]), archive);
  const invalid = await linuxFixture(dir, false, 62, '0'.repeat(64));
  await writeFile(source, gzipSync(invalid));
  await rejects(packageLinux(source, output, artifact, product), /runner checksum does not match/);
  deepEqual(execFileSync('xz', ['-dc', output]), archive);
});

test('Linux DEB keeps the original metadata and compresses only the filesystem archive', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'creator-deb-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const product = { slug: 'creator', version: '1.2.3' };
  const tree = join(dir, 'filesystem');
  const archive = await linuxFixture(tree, true);
  await writeFile(join(dir, 'control'), 'Package: creator\nVersion: 1.2.3\nArchitecture: arm64\n');
  const control = gzipSync(execFileSync('tar', ['-cf', '-', '-C', dir, './control']));
  await writeFile(join(dir, 'control.tar.gz'), control);
  await writeFile(join(dir, 'data.tar.gz'), gzipSync(archive));
  await writeFile(join(dir, 'debian-binary'), '2.0\n');
  const source = join(dir, 'original.deb');
  execFileSync('ar', ['-qc', source, 'debian-binary', 'control.tar.gz', 'data.tar.gz'], { cwd: dir });
  const output = join(dir, 'creator.deb');
  const artifact = { file: 'creator.deb', format: 'DEB', arch: 'arm64' };
  await packageLinux(source, output, artifact, product);
  equal(execFileSync('ar', ['-t', output], { encoding: 'utf8' }), 'debian-binary\ncontrol.tar.gz\ndata.tar.xz\n');
  deepEqual(execFileSync('ar', ['-p', output, 'control.tar.gz']), control);
  const packed = execFileSync('ar', ['-p', output, 'data.tar.xz']);
  deepEqual(execFileSync('xz', ['-dc'], { input: packed }), archive);
  await rejects(packageLinux(source, output, artifact, { ...product, version: '9.0.0' }), /unexpected Debian package metadata/);
});

async function linuxFixture(root, debian, machine = 183, digest = createHash('sha256').update(elfHeader(machine)).digest('hex')) {
  const app = debian ? 'opt/creator/' : '';
  const desktop = debian ? 'usr/share/applications/' : '';
  const files = [app + 'creator', app + 'WebCrateRunner', app + 'WebCrateRunner.sha256',
    app + 'icon.png', app + 'Lobe-Icons-LICENSE.txt', app + 'THIRD-PARTY-LICENSES.txt', desktop + 'creator.desktop'];
  await mkdir(join(root, app), { recursive: true });
  await mkdir(join(root, desktop), { recursive: true });
  for (const [index, name] of files.entries()) {
    const executable = index < 2;
    let contents = 'fixture';
    if (executable) contents = elfHeader(machine);
    if (name.endsWith('.sha256')) contents = digest;
    await writeFile(join(root, name), contents, { mode: executable ? 0o755 : 0o644 });
  }
  return execFileSync('tar', ['-cf', '-', '-C', root, ...(debian ? ['.'] : files)]);
}

function peHeader(machine) {
  const bytes = Buffer.alloc(0x40 + 26);
  bytes.write('MZ');
  bytes.writeUInt32LE(0x40, 0x3c);
  bytes.write('PE\0\0', 0x40);
  bytes.writeUInt16LE(machine, 0x40 + 4);
  bytes.writeUInt16LE(2, 0x40 + 20); // Optional header size used by the architecture check.
  bytes.writeUInt16LE(0x0002, 0x40 + 22);
  bytes.writeUInt16LE(0x20b, 0x40 + 24);
  return bytes;
}

function elfHeader(machine) {
  const bytes = Buffer.alloc(64);
  bytes.writeUInt32BE(0x7f454c46, 0);
  bytes.set([2, 1, 1], 4); // ELF64, little-endian, current header version.
  bytes.writeUInt16LE(2, 16);
  bytes.writeUInt16LE(machine, 18);
  return bytes;
}
