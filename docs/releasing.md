# Releases / 发布

The GitHub Actions workflow builds and tests Creator on pushes to `main` and on pull requests. A pushed version tag also publishes a GitHub Release after the build, website checks, and installed-app regressions succeed.

1. Set the same version in `mygo.json`, `website/package.json`, and `website/package-lock.json`.
2. Add English and Chinese notes in `docs/releases/<version>.md`. Include installation requirements and links to the nine platform packages. [Version 0.0.1 notes](releases/0.0.1.md)
3. Commit the changes, then create and push the matching tag. The first release uses these commands.

```sh
git tag -a v0.0.1 -m 'Creator 0.0.1'
git push origin v0.0.1
```

The release includes a macOS universal DMG, Windows installers and portable ZIPs for x64 and ARM64, and Linux DEBs and TAR.XZ archives for both architectures. `SHA256SUMS.txt` lists the hashes and `release-manifest.json` records the source commit, version, and downloads. The publishing job uses the repository's GitHub Actions token.

GitHub Actions 会在主分支推送和 PR 时打包、测试。更新三个版本配置、写好双语说明并推送匹配的 `v` 标签后，通过构建和安装回归的版本会发布到 GitHub Releases。公开的 macOS DMG 使用临时签名。 [测试指南](testing.md) · [GitHub Actions](https://github.com/Bistutu/Creator/actions)
