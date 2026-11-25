import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { readFile, stat } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const project = fileURLToPath(new URL('../', import.meta.url));
export const packages = join(project, 'build', 'downloads');
export const licenseResources = ['Lobe-Icons-LICENSE.txt', 'THIRD-PARTY-LICENSES.txt'];

export async function readRelease() {
  const { name, slug, version } = JSON.parse(await readFile(join(project, 'mygo.json'), 'utf8'));
  if (typeof name !== 'string' || !/^[A-Za-z][A-Za-z0-9-]*$/.test(name) ||
      typeof slug !== 'string' || !/^[a-z][a-z0-9-]*$/.test(slug) ||
      typeof version !== 'string' || !/^\d+\.\d+\.\d+(?:-[\w.]+)?$/.test(version)) {
    throw new Error('Invalid product name, slug or version in mygo.json');
  }
  return { name, slug, version };
}

export function platforms({ name, slug, version }) {
  const archs = [['amd64', 'x64'], ['arm64', 'ARM64']];
  return [
    {
      id: 'macos', name: 'macOS', description: 'macOS 12 或更新版本，支持 Apple 芯片和 Intel。',
      instructions: `打开磁盘映像，将 ${name} 拖入「应用程序」。本地预览包使用临时签名。面向用户分发的版本需要 Developer ID 签名和 Apple 公证。`,
      artifacts: [{
        label: '通用版 · DMG', format: 'DMG', file: `${name}-${version}-macos-universal.dmg`,
        source: `darwin-universal/${slug}-${version}.dmg`,
      }],
    },
    {
      id: 'windows', name: 'Windows', description: '需要 Microsoft Edge WebView2 Runtime。',
      instructions: `运行安装程序，完成后从开始菜单打开 ${name}。使用便携版时，解压整个 ZIP，再打开文件夹中的 ${name}.exe。`,
      artifacts: [
        ...archs.map(([arch, label]) => ({
          arch, label: `${label} · 安装程序`, format: 'EXE', file: `${name}-${version}-windows-${arch}-setup.exe`,
          source: `windows-${arch}/${slug}-setup-${version}-${arch}.exe`,
        })),
        ...archs.map(([arch, label]) => ({
          arch, label: `${label} · 便携版 ZIP`, format: 'ZIP', file: `${name}-${version}-windows-${arch}.zip`, source: `windows-${arch}`,
        })),
      ],
    },
    {
      id: 'linux', name: 'Linux', description: '需要 GTK 3 和 WebKitGTK 4.1。',
      instructions: `Debian 或 Ubuntu 可安装 DEB。也可以解压 TAR.XZ，然后运行其中的 ${slug}。`,
      artifacts: archs.flatMap(([arch, label]) => [
        {
          arch, label: `${label} · DEB`, format: 'DEB', file: `${name}-${version}-linux-${arch}.deb`,
          source: `linux-${arch}/${slug}_${version}_${arch}.deb`,
        },
        {
          arch, label: `${label} · TAR.XZ`, format: 'TAR.XZ', file: `${name}-${version}-linux-${arch}.tar.xz`,
          source: `linux-${arch}/${slug}-${version}-linux-${arch}.tar.gz`,
        },
      ]),
    },
  ];
}

async function metadata(path) {
  let info;
  try {
    info = await stat(path);
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  }
  if (!info.isFile() || info.size === 0) return null;
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return { bytes: info.size, sha256: hash.digest('hex') };
}

export async function inventory(release, directory = packages) {
  return Promise.all(platforms(release).map(async (platform) => {
    const artifacts = await Promise.all(platform.artifacts.map(async ({ label, format, file }) => {
      const info = await metadata(join(directory, file));
      return info && { label, format, file, ...info };
    }));
    return { ...platform, artifacts: artifacts.filter(Boolean) };
  }));
}

function displaySize(bytes) {
  return bytes >= 1_000_000 ? `${(bytes / 1_000_000).toFixed(1)} MB` : `${Math.ceil(bytes / 1000)} KB`;
}

export function platformCards(releases) {
  return releases.map(({ id, name, description, instructions, artifacts }) => {
    const links = artifacts.map(({ label, file, bytes }) => `
            <li><a class="download" href="downloads/${file}" download>
              <span>${label}</span><small>${displaySize(bytes)}</small>
            </a></li>`).join('');
    const hashes = artifacts.map(({ label, sha256 }) => `<dt>${label}</dt><dd><code>${sha256}</code></dd>`).join('');
    const content = artifacts.length === 0 ? '<p class="availability">此平台的预览包暂未提供。</p>' : `
          <ul>${links}</ul>
          <details>
            <summary>安装说明与 SHA-256</summary>
            <p>${instructions}</p>
            <dl>${hashes}</dl>
          </details>`;
    return `
        <article class="platform" id="download-${id}" aria-labelledby="platform-${id}">
          <h3 id="platform-${id}">${name}</h3>
          <p>${description}</p>${content}
        </article>`;
  }).join('');
}
