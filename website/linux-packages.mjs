import { execFileSync } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { gunzipSync } from 'node:zlib';
import { verifyArchitecture, verifyRunnerDigest } from './binaries.mjs';
import { licenseResources } from './releases.mjs';

const archiveLimit = 64 * 1024 * 1024;
const debianMembers = ['debian-binary', 'control.tar.gz', 'data.tar.xz'];
const xzOptions = {
  maxBuffer: archiveLimit,
  env: { ...process.env, XZ_DEFAULTS: '', XZ_OPT: '' },
};

export async function packageLinux(source, output, { file, format, arch }, { slug, version }) {
  const debian = format === 'DEB';
  const original = debian ? readDebian(source, 'data.tar.gz') : null;
  if (original) {
    const [debianVersion, control] = original;
    const fields = execFileSync('tar', ['-xzOf', '-', './control'], { input: control, encoding: 'utf8' }).split('\n');
    if (debianVersion.toString() !== '2.0\n' || ![
      `Package: ${slug}`, `Version: ${version}`, `Architecture: ${arch}`,
    ].every((field) => fields.includes(field))) {
      throw new Error(`${file} has unexpected Debian package metadata`);
    }
  }
  const tar = gunzipSync(original ? original[2] : await readFile(source), { maxOutputLength: archiveLimit });
  verifyLinuxTar(tar, file, slug, arch, debian);
  // The two Go executables share code up to about 14 MiB apart in the tar.
  // The x86 filter also compresses relative branch addresses. Keep ARM64
  // decodable on systems whose XZ predates the ARM64 filter.
  const packed = execFileSync('xz', [
    '--threads=1', '--memlimit-compress=192MiB', '--no-adjust',
    ...(arch === 'amd64' ? ['--x86'] : []),
    '--lzma2=preset=6,dict=16MiB', '--stdout',
  ], { ...xzOptions, input: tar });
  const unpacked = execFileSync('xz', ['--decompress', '--stdout'], { ...xzOptions, input: packed });
  if (!unpacked.equals(tar)) throw new Error(`${file} changed the filesystem archive during compression`);

  if (original) {
    await writeDebian(output, [original[0], original[1], packed]);
  } else {
    await writeFile(output, packed);
  }
}

function verifyLinuxTar(tar, file, slug, arch, debian) {
  const input = { input: tar, maxBuffer: archiveLimit };
  const root = debian ? `./opt/${slug}/` : '';
  const desktop = debian ? `./usr/share/applications/${slug}.desktop` : `${slug}.desktop`;
  const runner = root + 'WebCrateRunner';
  const digest = runner + '.sha256';
  const binaries = [root + slug, runner];
  const names = execFileSync('tar', ['-tf', '-'], { ...input, encoding: 'utf8' }).trim().split('\n');
  for (const name of [...binaries, digest, root + 'icon.png', ...licenseResources.map((name) => root + name), desktop]) {
    if (!names.includes(name)) throw new Error(`${file} is missing ${name}`);
  }
  for (const name of binaries) {
    const binary = execFileSync('tar', ['-xOf', '-', name], input);
    verifyArchitecture(binary, 'ELF', arch, `${file}/${name}`);
    if (name === runner) {
      verifyRunnerDigest(binary, execFileSync('tar', ['-xOf', '-', digest], input), file);
    }
  }
}

function readDebian(path, payload) {
  const expected = [...debianMembers.slice(0, 2), payload];
  const actual = execFileSync('ar', ['-t', path], { encoding: 'utf8' }).trim().split('\n');
  if (actual.join('\n') !== expected.join('\n')) throw new Error(`${path} has unexpected Debian archive members`);
  return expected.map((name) => execFileSync('ar', ['-p', path, name], { maxBuffer: archiveLimit }));
}

async function writeDebian(output, members) {
  const scratch = await mkdtemp(join(dirname(output), '.deb-'));
  try {
    await Promise.all(debianMembers.map((name, i) => writeFile(join(scratch, name), members[i])));
    execFileSync('ar', ['-qc', output, ...debianMembers], {
      cwd: scratch,
      env: { ...process.env, ZERO_AR_DATE: '1' },
    });
    const written = readDebian(output, 'data.tar.xz');
    if (!written.every((member, i) => member.equals(members[i]))) {
      throw new Error(`${output} changed a Debian archive member`);
    }
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
}
