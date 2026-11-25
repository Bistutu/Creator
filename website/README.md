# Creator 官网

`dist/` 是可部署的静态站点。首屏展示实际创建页和独立窗口，下载入口跟随浏览器可识别的桌面系统。安装包、大小和 SHA-256 从本地发布包生成。所有平台列表也可直接访问。

截图位于 `assets/`。创建页和独立窗口使用项目的真实 macOS 截图，表单来自 MyGo 操作回归。官网的 96 像素图标由 `cmd/assets` 与桌面图标一起生成。

## 准备下载与预览

在 Mac 的项目根目录构建通用 macOS 包，以及 Windows、Linux 的 x64 和 ARM64 包。发布机可以用 Homebrew 安装 `makensis`、`sevenzip` 和 `xz`，分别用于构建 Windows 安装器、检查安装内容和压缩 Linux 发布包。

```sh
go run ./cmd/package -platform darwin/universal,windows/amd64,windows/arm64,linux/amd64,linux/arm64 -- -o build/release
cd website
npm run package
npm run build
npm run preview
```

在浏览器打开 `http://127.0.0.1:41737`。上传整个 `dist/` 目录即可部署，包括 `downloads/` 和 `downloads.json`。产品名称和版本取自项目的 `mygo.json`。

打包步骤以只读方式挂载 macOS DMG，验证「应用程序」入口、签名、版本和两种架构。Windows 安装器采用 16 MiB 字典的固实 LZMA 压缩，并逐项核对主程序、运行时、图标和卸载器。 [NSIS 压缩命令](https://nsis.sourceforge.io/Reference/SetCompressorDictSize)

Linux 的发布下载提供 DEB 和 TAR.XZ。打包器单线程压缩文件归档，逐字节验证解压后的内容，保留原 DEB 的控制文件与安装依赖。 [Debian 包格式](https://manpages.debian.org/stable/dpkg-dev/deb.5.en.html)

## 测试

先按上面的步骤生成 `dist/`，然后从 `website/` 运行。Playwright 会自行启动和关闭预览服务。

```sh
npm ci
npm test
npx playwright install chromium
npm run test:browser
```

本机已安装 Chrome 时，可通过 `WEBCRATE_CHROMIUM` 指定其可执行文件。开发机上的浏览器验收使用项目的资源保护入口和一个 worker。回归截图保存在项目的 `test-results/website/`。 [本机资源控制](../docs/testing.md)
