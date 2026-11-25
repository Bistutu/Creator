import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';

const screenshots = fileURLToPath(new URL('../../test-results/website/', import.meta.url));

async function simulatePlatform(page, properties) {
  await page.addInitScript((values) => {
    for (const [key, value] of Object.entries(values)) {
      Object.defineProperty(navigator, key, { configurable: true, value });
    }
  }, { platform: '', userAgent: '', userAgentData: null, maxTouchPoints: 0, ...properties });
}

test('product view, guide and all versions fit the viewport', async ({ page }, testInfo) => {
  const mobile = testInfo.project.name === 'mobile';
  await simulatePlatform(page, mobile ? { platform: 'iPhone', userAgent: 'iPhone Mobile' } : { platform: 'MacIntel' });
  await page.emulateMedia({ colorScheme: 'light', reducedMotion: 'reduce' });
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(/把常用网站\s*变成桌面应用/);

  const preview = page.getByRole('figure', { name: 'Creator 应用效果' });
  const creator = preview.getByRole('img', { name: /Creator 桌面创建页/ });
  const result = preview.getByRole('img', { name: /工作台在独立的 macOS 窗口/ });
  await expect(creator).toBeInViewport();
  await expect(result).toBeInViewport();
  await expect.poll(() => creator.evaluate((img) => img.naturalWidth / img.naturalHeight)).toBeCloseTo(448 / 640);
  await expect.poll(() => result.evaluate((img) => img.naturalWidth / img.naturalHeight)).toBeCloseTo(720 / 512);

  const primary = page.locator('#primary-download');
  await expect(primary).toBeInViewport();
  if (mobile) {
    await expect(primary).toHaveText('下载 Creator');
    await expect(primary).toHaveAttribute('href', '#downloads');
    await expect(page.getByRole('link', { name: '所有版本' })).toBeHidden();
  } else {
    await expect(primary).toHaveText('下载 macOS 版');
    await expect(primary).toHaveAttribute('href', /-macos-universal\.dmg$/);
    await expect(primary).toHaveAttribute('download', '');
    await expect(page.locator('#download-meta')).toHaveText(/通用版 · [\d.]+ MB/);
  }

  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(testInfo.project.use.viewport.width);
  await expect(page.locator('html')).toHaveCSS('scroll-behavior', 'auto');
  await page.screenshot({ path: `${screenshots}${testInfo.project.name}.png`, fullPage: true, animations: 'disabled' });

  if (!mobile) {
    const highlights = await page.locator('.highlights').boundingBox();
    await page.screenshot({
      path: `${screenshots}desktop-hero.png`,
      clip: { x: 0, y: 0, width: testInfo.project.use.viewport.width, height: Math.ceil(highlights.y + highlights.height + 16) },
      animations: 'disabled',
    });
    await page.keyboard.press('Tab');
    await expect(page.getByRole('link', { name: '跳到正文' })).toBeFocused();
  }
  await page.getByRole('link', { name: '上手指南', exact: true }).click();
  await expect(page).toHaveURL(/#workflow$/);
  const guide = page.getByRole('region', { name: '从网址到桌面' });
  const guideImage = guide.getByRole('img');
  const choose = guide.getByText('选择站点', { exact: true });
  const confirm = guide.getByText('确认创建', { exact: true });
  await expect(guideImage).toHaveAttribute('src', 'assets/create.png');
  await expect(choose.locator('..')).toHaveAttribute('open', '');
  await expect(guide.getByText(/用名称和用途搜索推荐站点/)).toBeVisible();

  if (mobile) {
    await confirm.tap();
  } else {
    await confirm.focus();
    await page.keyboard.press('Enter');
  }
  await expect(choose.locator('..')).not.toHaveAttribute('open', '');
  await expect(guide.getByText(/核对名称和图标/)).toBeVisible();
  await expect(guideImage).toHaveAttribute('src', 'assets/settings.png');
  await expect(guideImage).toHaveAccessibleName(/新建应用表单/);
  await expect.poll(() => guideImage.evaluate((img) => img.complete && img.currentSrc.endsWith('/settings.png'))).toBe(true);
  expect(await guideImage.evaluate((img) => img.getAnimations().length)).toBe(0);
  await guide.getByText('打开应用', { exact: true }).click();
  await expect(guideImage).toHaveAttribute('src', 'assets/standalone.png');
  await expect(guide.getByText(/搜到唯一结果时按回车/)).toBeVisible();
  await expect(guide.getByText(/独立应用仍可以运行/)).toBeVisible();
  if (!mobile) {
    await expect.poll(() => guideImage.evaluate((img) => img.complete && img.currentSrc.endsWith('/standalone.png'))).toBe(true);
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await guideImage.evaluate((image) => {
      image.addEventListener('load', () => {
        requestAnimationFrame(() => {
          image.dataset.transition = image.getAnimations().map((animation) => animation.effect.getTiming().duration).join(',');
        });
      }, { once: true });
    });
  }
  await choose.click();
  await expect(guideImage).toHaveAttribute('src', 'assets/create.png');
  if (!mobile) {
    await expect(guideImage).toHaveAttribute('data-transition', '165');
    await page.emulateMedia({ reducedMotion: 'reduce' });
  }

  await (mobile ? primary : page.getByRole('link', { name: '所有版本', exact: true })).click();
  await expect(page.getByRole('heading', { name: '下载 Creator' })).toBeInViewport();
  const macos = page.getByRole('article', { name: 'macOS' });
  await macos.getByText('安装说明与 SHA-256').click();
  await expect(macos.getByRole('link', { name: /通用版 · DMG/ })).toBeVisible();
  await expect(macos.getByText(/打开磁盘映像/)).toBeVisible();
  await expect(macos.getByText(/Apple 公证/)).toBeVisible();
  await expect(macos.locator('code')).toHaveText(/^[a-f0-9]{64}$/);
  const windows = page.getByRole('article', { name: 'Windows' });
  await expect(windows.getByRole('link', { name: /x64 · 安装程序/ })).toBeVisible();
  await expect(windows.getByRole('link', { name: /ARM64 · 安装程序/ })).toBeVisible();
  await expect(page.locator('#downloads a[download]')).toHaveCount(9);
});

for (const [name, properties] of [
  ['Windows', { userAgentData: { platform: 'Windows', mobile: false }, platform: 'Win32' }],
  ['Linux', { platform: 'Linux aarch64' }],
]) {
  test(`${name} routes to the available architectures`, async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'desktop');
    await simulatePlatform(page, properties);
    await page.goto('/');
    const action = page.locator('#primary-download');
    await expect(action).toHaveText(`查看 ${name} 版本`);
    await expect(action).not.toHaveAttribute('download', '');
    await action.click();
    await expect(page.locator('article:target')).toHaveAccessibleName(name);
    await expect(page.getByRole('article', { name }).getByRole('link', { name: /^ARM64/ }).first()).toBeInViewport();
    await expect(page.getByRole('link', { name: '所有版本', exact: true })).toBeVisible();
  });
}

test('macOS falls back to the downloads list when the DMG is missing', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop');
  await simulatePlatform(page, { platform: 'MacIntel' });
  await page.route('**/', async (route) => {
    const response = await route.fetch();
    const markup = (await response.text()).replace(
      /<article[^>]+id="download-macos"[^>]*>[\s\S]*?<\/article>/,
      '<article class="platform" id="download-macos" aria-label="macOS"><h3>macOS</h3><p>此平台的预览包暂未提供。</p></article>',
    );
    await route.fulfill({ response, body: markup });
  });
  await page.goto('/');
  await expect(page.locator('#primary-download')).toHaveAttribute('href', '#downloads');
  await expect(page.getByRole('link', { name: '所有版本' })).toBeHidden();
  await page.getByRole('link', { name: '下载 Creator', exact: true }).click();
  await expect(page.getByRole('article', { name: 'macOS' })).toContainText('此平台的预览包暂未提供。');
  await expect(page.getByRole('article', { name: 'Windows' }).locator('a[download]')).toHaveCount(4);
});

test('dark appearance keeps the guide usable and iPad uses all downloads', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'mobile');
  await simulatePlatform(page, { platform: 'MacIntel', userAgent: 'Macintosh', maxTouchPoints: 5 });
  await page.setViewportSize({ width: 320, height: 780 });
  await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' });
  await page.goto('/');
  await expect(page.locator('html')).toHaveCSS('background-color', 'rgb(23, 25, 33)');
  await expect(page.locator('#primary-download')).toHaveAttribute('href', '#downloads');
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(320);
  await page.screenshot({ path: `${screenshots}mobile-dark.png`, fullPage: true, animations: 'disabled' });
});

test('the first page load stays lightweight and defers the settings preview', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop');
  await page.goto('/');
  const resources = await page.evaluate(() => {
    const seen = new Map();
    for (const entry of performance.getEntries()) {
      if (entry.entryType === 'navigation' || entry.entryType === 'resource') seen.set(entry.name, entry.decodedBodySize);
    }
    return [...seen].map(([url, bytes]) => ({ path: new URL(url).pathname, bytes, local: new URL(url).origin === location.origin }));
  });
  expect(resources.some(({ path }) => path === '/site.js')).toBe(true);
  expect(resources.every(({ local, path }) => local && !path.includes('settings.png') && !path.startsWith('/downloads/'))).toBe(true);
  expect(resources.reduce((sum, { bytes }) => sum + bytes, 0)).toBeLessThan(170_000);
});

test.describe('without JavaScript', () => {
  test.use({ javaScriptEnabled: false, reducedMotion: 'reduce' });

  test('the guide and downloads still work', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'desktop');
    await page.goto('/');
    await page.getByRole('link', { name: '上手指南', exact: true }).press('Enter');
    await page.getByText('确认创建', { exact: true }).press('Enter');
    await expect(page.getByText(/核对名称和图标/)).toBeVisible();
    await page.getByRole('link', { name: '下载 Creator', exact: true }).press('Enter');
    await expect(page).toHaveURL(/#downloads$/);
    await expect(page.locator('#downloads a[download]')).toHaveCount(9);
  });
});

test('each download returns the advertised filename, size and SHA-256', async ({ page, request }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop');
  await simulatePlatform(page, { platform: 'MacIntel' });
  await page.goto('/#downloads');
  const response = await request.get('/downloads.json');
  expect(response.ok()).toBeTruthy();
  const { name, version, downloads } = await response.json();
  expect(name).toBe('Creator');
  expect(downloads).toHaveLength(9);
  expect(downloads.find((item) => item.platform === 'macos')?.format).toBe('DMG');
  expect(downloads.filter((item) => item.platform === 'windows' && item.format === 'EXE')).toHaveLength(2);
  await expect(page.locator('#downloads')).toContainText(version);
  await expect(page.locator('#downloads a[download]')).toHaveCount(downloads.length);
  for (const item of downloads) {
    const link = item.platform === 'macos'
      ? page.getByRole('link', { name: '下载 macOS 版', exact: true })
      : page.locator(`#downloads a[download][href="downloads/${item.file}"]`);
    const [download] = await Promise.all([page.waitForEvent('download'), link.click()]);
    expect(download.suggestedFilename()).toBe(item.file);
    const data = await readFile(await download.path());
    expect(data.length).toBe(item.bytes);
    expect(createHash('sha256').update(data).digest('hex')).toBe(item.sha256);
  }
});
