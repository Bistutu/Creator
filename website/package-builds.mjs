import { execFileSync } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, readFile, readlink, rename, rm, rmdir, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { licenseResources, packages, platforms, project, readRelease } from './releases.mjs';
import { verifyArchitecture, verifyRunnerDigest } from './binaries.mjs';
import { packageLinux } from './linux-packages.mjs';

const build = join(project, 'build');
const release = join(build, 'release');
const product = await readRelease();
const targets = platforms(product);
const winRunner = 'WebCrateRunner.exe';
const winDigest = `${winRunner}.sha256`;
const winBinaries = [`${product.name}.exe`, winRunner];
const license = join(project, 'LICENSE');
const winFiles = [...winBinaries, winDigest, 'icon.png', ...licenseResources];
await mkdir(packages, { recursive: true });
const stage = await mkdtemp(join(build, 'downloads-'));

try {
  for (const { id, artifacts } of targets) {
    for (const artifact of artifacts) {
      const { file, source, format, arch } = artifact;
      const input = join(release, source);
      const output = join(stage, file);
      if (id === 'macos') {
        await copyFile(input, output);
        await verifyMacDiskImage(output, file);
      } else if (format === 'EXE') {
        await copyFile(input, output);
        await verifyWindowsSetup(output, file, arch, dirname(input));
      } else if (id === 'windows') {
        const files = winFiles.map((name) => join(input, name));
        for (const path of files) {
          if (!(await stat(path)).isFile()) throw new Error(`Expected a file at ${path}`);
        }
        execFileSync('zip', ['-9', '-q', '-j', output, ...files, license], { stdio: 'inherit' });
        execFileSync('unzip', ['-tqq', output]);
        for (const name of winBinaries) {
          const binary = execFileSync('unzip', ['-p', output, name], { maxBuffer: Infinity });
          verifyArchitecture(binary, 'PE', arch, `${file}/${name}`);
          if (name === winRunner) {
            verifyRunnerDigest(binary, execFileSync('unzip', ['-p', output, winDigest]), file);
          }
        }
      } else {
        await packageLinux(input, output, artifact, product);
      }
    }
  }
  for (const { artifacts } of targets) {
    for (const { file } of artifacts) await rename(join(stage, file), join(packages, file));
  }
} finally {
  await rm(stage, { recursive: true, force: true });
}

async function verifyMacDiskImage(image, file) {
  const diskTool = '/usr/bin/hdiutil';
  execFileSync(diskTool, ['verify', '-quiet', image]);
  const mount = await mkdtemp(join(tmpdir(), 'creator-disk-image-'));
  let mounted = false;
  try {
    execFileSync(diskTool, ['attach', '-readonly', '-nobrowse', '-noautoopen', '-mountpoint', mount, image]);
    mounted = true;
    if (await readlink(join(mount, 'Applications')) !== '/Applications') {
      throw new Error(`${file} has the wrong Applications shortcut`);
    }
    const app = join(mount, `${product.name}.app`);
    execFileSync('/usr/bin/codesign', ['--verify', '--strict', '--deep', app]);
    for (const name of licenseResources) {
      if (!(await stat(join(app, 'Contents', 'Resources', name))).isFile()) throw new Error(`${file} is missing ${name}`);
    }
    for (const binary of [`MacOS/${product.name}`, 'Resources/WebCrateRunner']) {
      execFileSync('/usr/bin/lipo', [join(app, 'Contents', binary), '-verify_arch', 'arm64', 'x86_64']);
    }
    const plist = join(app, 'Contents', 'Info.plist');
    const appVersion = execFileSync('/usr/bin/plutil', ['-extract', 'CFBundleShortVersionString', 'raw', '-o', '-', plist], { encoding: 'utf8' }).trim();
    if (appVersion !== product.version) throw new Error(`Expected macOS ${product.version}, got ${appVersion}`);
  } finally {
    if (mounted) {
      try {
        execFileSync(diskTool, ['detach', mount]);
      } catch {
        execFileSync(diskTool, ['detach', '-force', mount]);
      }
    }
    await rmdir(mount).catch((error) => {
      if (error.code !== 'ENOENT') throw error;
    });
  }
}

async function verifyWindowsSetup(output, file, arch, directory) {
  const listing = execFileSync('7zz', ['l', '-slt', output], { encoding: 'utf8' });
  const [header] = listing.split(/\r?\n----------\r?\n/);
  if (!/^Type = Nsis\r?$/m.test(header)) throw new Error(`${file} is not an NSIS installer`);
  if (!/^Method = LZMA:24\r?$/m.test(header) || !/^Solid = \+\r?$/m.test(header)) {
    throw new Error(`${file} did not use solid LZMA with a 16 MiB dictionary`);
  }
  const entries = [...listing.matchAll(/^Path = (.+)\r?$/gm)].map(([, name]) => name);
  requireEntries(file, entries, [...winFiles, '.mygo-files', 'Uninstall.exe']);
  execFileSync('7zz', ['t', '-bd', '-bso0', output]);
  for (const name of winFiles) {
    const embedded = execFileSync('7zz', ['x', '-so', output, name], { maxBuffer: Infinity });
    if (winBinaries.includes(name)) verifyArchitecture(embedded, 'PE', arch, `${file}/${name}`);
    if (name === winDigest) verifyRunnerDigest(await readFile(join(directory, winRunner)), embedded, file);
    if (!embedded.equals(await readFile(join(directory, name)))) {
      throw new Error(`${file} contains the wrong ${name}`);
    }
  }
}

function requireEntries(file, entries, required) {
  for (const name of required) {
    if (!entries.includes(name)) throw new Error(`${file} is missing ${name}`);
  }
}
