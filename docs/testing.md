# Creator 回归测试

在项目根目录运行。先准备本机启动器，才能在测试中验证真实应用包。图标测试启动临时回环 HTTP 服务。

## 本机测试资源控制

本机的构建、原生窗口和浏览器测试通过 `tools/local_check.py` 串行执行。入口会检查宿主负载、剩余磁盘和 macOS 内存压力，运行中每隔三秒重新检查。Go 设置 `GOMAXPROCS=2` 并同时构建一个包，Playwright 本机使用一个 worker。测试超时或被中断时，入口清理本次启动的进程组与临时目录。Windows 和 Linux 安装回归优先使用同架构的 CI 执行器，本机 Linux 只使用下文的无桌面来宾。禁止用 Parallels 启动 Windows 或 Linux 桌面进行测试。

使用 `--deskpad` 或 `--lima` 时，入口在准备虚拟屏或来宾前、以及准备完成后等宿主的一分钟平均负载低于逻辑 CPU 数的 40%，阈值最低为 1.0。十核 Mac 的启动阈值为 4.0。两次等待各最多 60 秒，第二次同时受剩余的 `--timeout` 限制。通过首次预检后开始计算 `--timeout`。准备期间和运行中继续检查内存、磁盘和负载，负载达到逻辑 CPU 数的 80% 时停止原生测试并清理，停止阈值最低为 1.0。普通构建与测试在负载达到逻辑 CPU 数时停止。

先跑与改动相关的最小回归，确认本轮的测试进程和来宾都已退出再开始下一批。DeskPad 在本轮结束后继续运行，后续测试复用同一虚拟屏。已有的用户窗口与资料保持独立。`--timeout` 接受 1 到 180 秒，覆盖启动与执行。超时返回 124，资源条件不足或已有测试运行返回 75。

```sh
python3 -B tools/local_check.py --timeout 20 -- python3 -B -m unittest discover -s tools/tests
python3 tools/local_check.py --timeout 90 -- go run ./cmd/package -prepare
python3 tools/local_check.py --timeout 120 -- go test -count=1 ./... -coverpkg=./... -coverprofile=coverage.out
go tool cover -func=coverage.out
```

Go 语句覆盖率包含本次构建所用的平台代码。启动器的 Objective-C 代码、系统级行为和其他平台代码由各自测试提供证据。

下文的构建和测试命令在用户电脑运行时也使用此入口，将原有的环境变量放在 `python3` 前，或通过 `-- env` 传入。

## 打包与资源

`go test ./cmd/package ./cmd/assets` 覆盖目标校验、通用版启动器去重、编译失败保留现有文件，以及图标源无效时保留现有资源。

`TestPortableRunnerDoesNotLoadCreatorPackages` 检查 Windows 和 Linux 发布启动器的依赖图。`TestReadApplicationConfig` 核对早期配置的默认窗口尺寸、保存的尺寸和版本，以及缺失或无效的配置。更新启动器后，使用上一版的独立应用执行 `TestPortableInstalledCreatorWorkflow`，核对原系统入口、窗口和会话。

`TestReadWindowIcon` 检查 Windows 和 Linux 启动器对已有 PNG 图标的兼容性，以及路径缺失、文件异常、字节数或像素尺寸超限时继续打开应用的图标处理。`TestLinuxInstalledWebsiteOpensWithNonregularIcon` 从隔离的系统入口启动应用，将其窗口图标替换为命名管道后再打开，核对原网页会话和窗口标识。

`TestMacExecutableDeclaredBounds` 检查单架构 Mach-O 的签名范围和通用版的各架构文件范围。`TestMacRuntimeUpdateKeepsSignedAppAndItsResources` 与 `TestRuntimeRepairsTruncatedExecutableWithCurrentMarker` 保留版本标记，只截短隔离应用的启动文件。前者核对修复后的签名和原应用目录，后者核对两个共享启动文件的应用能依次恢复。安装版回归从原系统入口重开，核对图标、缩放和网页会话。 [Mach-O 文件头](https://github.com/apple-oss-distributions/xnu/blob/main/EXTERNAL_HEADERS/mach-o/loader.h) · [通用二进制文件头](https://github.com/apple-oss-distributions/xnu/blob/main/EXTERNAL_HEADERS/mach-o/fat.h)

在 Apple M4 上各运行三次 200 毫秒基准。新检查的中位数为 macOS 20.3 微秒和共享启动器路径 21.0 微秒，改动前分别为 10.3 和 19.2 微秒。检查健康应用时读取文件头或文件大小，沿用现有安装。

`npm run package` 在更新官网下载包前挂载 macOS DMG，检查「应用程序」入口、签名、版本和两个架构。它会验证两个 Windows 安装器的固实 LZMA 字典和实际文件，并核对 Windows 便携版与 Linux 归档的实际架构。 [Windows PE 规范](https://learn.microsoft.com/en-us/windows/win32/debug/pe-format#machine-types) · [ELF 机器类型](https://gabi.xinuos.com/elf/a-emachine.html)

发布包测试验证 Linux 的 XZ 压缩往返后文件归档逐字节一致，并检查 DEB 的包名、版本、架构和控制文件。x64 使用 XZ 的 x86 指令过滤器。ARM64 使用 LZMA2 以兼容 Ubuntu 22.04 自带的解压工具。发布机每次压缩一个归档，使用单线程，XZ 压缩器的内存上限是 192 MiB。 [Debian 包格式](https://manpages.debian.org/stable/dpkg-dev/deb.5.en.html) · [Ubuntu 22.04 的 XZ 版本](https://manpages.ubuntu.com/manpages/jammy/man1/xz.1.html) · [XZ 5.4 发布说明](https://github.com/tukaani-project/xz/blob/v5.4.0/NEWS#L2181)

## 界面和操作

MyGo 的 `ui.NewTesterWith` 直接构建原生视图。回归用可见名称和无障碍标签触发点击、输入和键盘键。截图由同一个测试入口导出。

```sh
WEBCRATE_SCREENSHOTS="$PWD/test-results/ui" go test -count=1 -run 'TestNative|TestCreationNeverWaits|TestOutdated|TestLateSiteIcon|TestEditingKeepsResolvedArtwork' .
```

`TestNativeCompactEditorWindowSize` 在最小窗口对照底部三个操作的实际位置，用键盘展开、填写并收起窗口尺寸，再验证保存的宽高。`TestNativeWindowDimensionsEaseInAndOut` 检查展开和收起的中间帧、快速反向展开时的焦点，以及收起时跳过宽高字段。`TestNativeCardAndDialogMotion` 对照弹窗开关时的面板和遮罩像素，检查立即恢复入口焦点以及减少动态效果时直接到位。DeskPad 的设置和关于回归与 Linux 的键盘回归也核对同一底部布局、键盘焦点和尺寸草稿。

`TestNativeEditorRevealsInvalidDimensions` 和 `TestNativeEditorFocusesTheInvalidAddressOrName` 检查表单能定位到无效输入，保留草稿并向辅助技术提供字段错误。尺寸回归确认错误会展开收起的区域，修改另一栏时保留提示，修正后从尺寸栏按回车提交。`TestNativeEditorIconHintDoesNotInterruptEscape` 检查键盘聚焦图标时按一次 `Esc` 关闭表单，以及指针悬停仍会显示提示。

`TestNativeDroppedWebsiteUsesExistingCreation` 从应用库接收链接，检查预览的输入焦点、站点图标请求次数及保存的完整地址。`TestNativeDroppedWebsiteIgnoresOtherData` 覆盖普通文本、文件和多条链接。`TestMacCreatorReceivesWebsiteLinkOnDeskPad` 使用独立粘贴板把 URL 交给 AppKit 窗口，核对草稿与签名应用。`TestLinuxInstalledCreatorReceivesWebsiteLink` 在专用 Xvfb 会话中从 GTK 窗口拖入 `text/uri-list`，核对安装版切页、预览和创建结果。 [AppKit 拖放协议](https://developer.apple.com/documentation/appkit/nsdragginginfo)

涵盖目录筛选、创建、窗口尺寸、编辑、文件管理器、移除、网址验证和失败重试。推荐目录回归检查 DeepSeek 和小红书的首屏位置、别名、预填网址与内置图标。应用库回归覆盖重复提交、历史副本的名称和菜单、窄窗对齐与搜索。保存窗口尺寸期间，按 Escape 或点击弹窗外侧时表单应保持打开。布局回归检查默认窗口、最小窗口、浅色和深色以及标题栏按钮的预留位置。图标回归覆盖选图取消、导入失败、恢复网站图标、重建应用及自动请求晚于手动选图的场景。macOS 构建测试读取签名应用中的 ICNS 帧，核对保存后的实际图标。动效回归比较悬停和弹窗的中间帧，并验证减少动态效果设置。

`TestCatalogLoadsOnlyRequestedArtwork` 检查读取及搜索目录时的图标访问次数，并核对并发使用同一个图标时只解压一次。`TestCatalogChecksIndividualTileChecksumAndSize` 检查每张图标的校验和及像素长度。`TestCatalogCanSearchWithoutArtwork` 检查图标缺失时仍可按站点名称和网址查找。`TestCatalogRejectsMalformedMetadataWithoutReadingArtwork` 检查目录格式错误时的返回结果和图标读取次数。

`BenchmarkCreatorFirstFrame` 测量从载入推荐目录到 MyGo 完成创建页或空应用库首帧的时间和分配量。Apple M4 上各运行三次，0.4.69 到 0.4.70 的创建页中位数从 7.27 毫秒降到 5.72 毫秒，单次分配从 7.80 MB 降到 6.56 MB。29 个图标的 RGBA 字节与旧 PNG 解码结果相同，内置资源从 172,076 字节降到 124,274 字节。空应用库首帧从 0.70 毫秒变为 0.72 毫秒。

对照两版官网的九个发布包，每个减少约 41 至 125 KB。其中 macOS 通用 DMG 从 10,051,072 字节降到 9,925,632 字节。

```sh
python3 tools/local_check.py --timeout 32 -- \
  go test -run '^$' -bench '^BenchmarkCreatorFirstFrame$' -benchtime=350ms -count=3 -benchmem .
```

`TestNativeCreatorBrandMenu` 在最小窗口检查标识菜单的键盘选择和取消后的焦点。`TestNativePreferencesEditor` 检查尺寸校验、回车提交、保存状态、重复提交、失败重试、恢复默认和取消。`TestNativeAboutCopiesVersion` 检查复制失败重试后只复制版本号。`TestNativeAboutShowsLicense` 用键盘打开、选择和复制只读的许可全文，返回时核对焦点。`TestDefaultWindowDimensionsApplyToNewDrafts` 核对新建表单使用已保存的默认尺寸，已打开的表单和已有应用保留各自的尺寸。`go test ./internal/settings` 检查旧配置缺少字段、配置损坏及替换文件失败时的恢复。

`TestNativeCreateSearchKeyboardNavigation` 和 `TestNativeCreateKeyboardNavigatesRowsAndAddress` 在最小窗口按键搜索并选择卡片，检查焦点、当前分类、图标、原始网址和滚动位置。取消表单后继续用键盘验证焦点回到原卡片。`TestLinuxInstalledCreatorKeyboard` 使用真实 GTK 键盘事件和 AT-SPI 验证同一流程。

`TestNativeRecommendationsResetScrollOnSearchChange` 从最后一行返回搜索，清空或替换网址后确认推荐首行恢复可见且立即可点。`TestNativeLibrarySearchStartsAtFirstMatch` 在有 120 条记录的应用库中滚动，检查输入筛选词后第一条匹配位于顶部，清空后第一条应用恢复可见，并在最小窗口核对七条完整记录。

`TestNativeLibrarySearchOpensWithKeyboard` 验证搜索后跳过忙碌的行、移动到原本不可见的结果、按 `Esc` 返回筛选词，以及回车只打开唯一匹配或明确选中的应用。`TestNativeLibraryDistinguishesCollidingAddresses` 核对简写相同的不同首页显示完整单行网址，同一完整网址创建的多个应用仍显示简写，并检查按片段筛选。

`TestCatalogSearchCombinesTermsWithinTheCategory` 和 `TestNativeScenarioSearchKeepsChoiceAndExplicitAddresses` 核对组合词、全角空格、分类限制与单一结果的回车操作。DeskPad 和已安装的 Linux Creator 使用「推理 DeepSeek」与「攻略 小红书」打开表单，随后组合应用名称和网址片段打开实际网站窗口。Windows 的设置回归在重启后通过 UI Automation 输入组合词并按回车，检查 DeepSeek 的网址和已保存的默认尺寸。

`BenchmarkDesktopLibrarySearchChange` 在 1,200 条记录间交替搜索，测量连续匹配和组合词筛选的耗时与分配。

`TestNativeKeyboardFocusKeepsCardsUnobstructed` 检查 Tab 导航、网址卡片和应用库定位时没有遮挡搜索的浮层，鼠标停在推荐名称上仍能查看完整名称和域名。`TestNativeLibraryCopiesFullAddress` 在两种外观下建立同名、同域名、不同路径的应用，核对行内地址与完整剪贴板内容。测试覆盖复制失败和重试。`TestAddressSummary` 覆盖国际化域名、IPv6、端口和转义路径。`TestLinuxInstalledCreatorUI` 把应用菜单复制的网址贴回隔离窗口的搜索框，核对实际 GTK 剪贴板里的查询参数和片段。

`TestNativeLibraryKeepsAppAfterFailedRemoval` 暂停移除，检查行内状态、重复点击和同一时间操作其他应用。失败后打开原应用，再重试移除。`TestRemoveKeepsInstalledAppWhenSystemEntryOrProfileCannotBeRemoved` 分别模拟快捷方式清理不完整和配置提交失败，核对还原的安装文件、系统入口和图标副本。`TestLinuxInstalledCreatorUI` 临时将隔离的系统入口目录设为只读，验证真实 `.desktop` 入口仍可打开并恢复会话，再恢复权限和重试。`TestWindowsStartMenuTracksAppLifecycle` 以拒绝删除的共享模式打开 `.lnk`，验证系统返回错误，释放句柄后重试成功。

`TestStartupCleansCommittedRemovalsAndAbandonedBuilds` 分别在移除提交前后中断，重开后核对原安装标识及残留目录。`TestStartupRetainsRemovalBackupUntilItCanConfirmTheCommit` 用缺失文件、空值、损坏的 JSON 和无效应用 ID 验证保留备份，恢复有效配置后再检查清理。`TestStartupReclaimSkipsBuildInProgress` 在打包暂停时运行启动清理，核对当前打包目录和继续创建的结果。

`TestReclaimRetriesWhenBackupCannotBeDeleted` 将隔离的备份目录设为只读，验证清理失败后保留移除标记，权限恢复后完成清理。`BenchmarkReclaimCleanInstallRoot` 测量检查 1,200 个正常安装目录的耗时和分配。

`TestRemovalCanRetryAfterAMarkerCollision` 检查移除标记被其他文件占用时恢复原安装。`TestReclaimSkipsSymlinkedStages` 检查取消清理后的目录、符号链接和链接目标。

`TestSaveRecognizesEquivalentPortsAndIPv6Addresses` 验证等价地址复用已安装应用，且同名的不同协议、端口、路径、查询和片段可以分别保存。`TestNativeCreationRevealsAnExistingAppInALargeLibrary` 用 120 个条目验证单双列下的滚动、图标和回车打开。`TestNativeDuplicateIsReachableBeforeLibraryLoads` 暂停启动时的库快照，检查重复提交后立即可用的条目及后续刷新。`TestLinuxInstalledCreatorUI` 用真实 GTK 键盘回车打开等价网址对应的原应用。

`TestNativePastedURLShowsWebsitePreview` 在最小窗口输入推荐站点和任意网址，检查图标预览、原有推荐、路径、参数与片段。手动输入时，卡片从本机读取缓存。`TestNativeAddressCardReusesCachedArtwork` 核对切换同域名网址时立即读取新卡片、旧结果回传后仍只读取一次、编辑片段沿用图标，以及从卡片创建时只打包一次。`TestNativeAddressCardSkipsObsoleteCacheReads` 在切换到应用库后执行排队的地址任务，核对返回创建页时只读取当前地址。`TestAddressCacheReadsKeepOnlyTheLatestQueuedAddress` 暂停一个图标读取和一个名称读取，再输入两个地址。放行首项后，只有最新的地址继续读取图标并回传结果。

`TestNativeCreateSearchPreservesDirectActions` 验证空输入的「添加」、按方向键进入建议和键盘清除。`TestNativeClearSearchCancelsPastedArtwork` 暂停粘贴触发的图标查找，清除后验证取消请求、移除网址卡片并保留所选分类。`TestNativeAddressWorkflowAndRetry` 从空应用库进入创建页，验证已有查询被选中，可以马上替换。该用例也检查清除错误输入、创建失败后重试和清除应用库筛选。macOS DeskPad、Linux 安装版和 Windows UI Automation 的回归脚本也加入了原生的输入焦点与清除检查。

`TestNativePastedURLLoadsWebsiteArtwork` 通过 MyGo 的剪贴板粘贴完整网址，检查表单打开前已显示站点图标，以及表单和安装文件复用同一图标。`TestNativePastedURLSharesPendingArtworkWithCreation` 暂停取图，直接创建，再让同一次查找更新安装文件。`TestNativePastedArtworkIgnoresEarlierAddress` 延迟旧请求的回传并更换同一域名下的网址，检查两张卡片使用各自的图标。`TestNativePastedArtworkReplacesPendingCacheRead` 在输入触发的缓存读取排队时粘贴相同网址，检查只进行一次查找。`TestLinuxInstalledCreatorUI` 用 Xvfb 的 GTK 剪贴板粘贴首次访问的测试网址，在网站仍保留正文时采样预览卡和创建表单的图标像素，再核对系统入口图标和完整网址。

`TestLateSiteIconSurvivesRenamingAndResizing` 暂停一次图标查找，完成创建，再编辑、取消、改名、调整尺寸和更改首页片段，最后检查同一次查找更新了已安装的图标。`TestNativeHomeFragmentKeepsInstalledArtworkOffline` 在缓存和网站都不可用时修改首页片段，比较保存前后的图标并核对查找次数。`TestAutomaticArtworkMatchesTheHTTPResource` 验证路径、查询参数、转义的井号和手选图片的匹配。`TestOutdatedSiteIconCannotChangeAnEditedApp` 在旧图标更新排队时保存新网址，检查已安装的图标和应用库显示保持一致。`TestRemovingAppCancelsPendingSiteArtwork` 检查移除时取消等待中的图标查找。

`TestNativeEditingReturnsToLibraryRow` 检查保存后保留匹配的搜索、失败时保留草稿和旧应用，以及改名后原搜索隐藏该应用时能直接定位并用回车打开。`TestNativeEditingKeepsTheLibraryScroll` 在 120 项应用中检查无改动的保存保留滚动位置、列表索引和应用包。

`TestResolveIconFromDocumentHead` 分别用持续加载的正文和排在图标前面的 48 条预加载链接验证自动取图。macOS 安装验收检查签名应用内的实际 ICNS，Linux 安装验收检查系统入口引用的 PNG，并在 Creator 关闭后打开应用验证网页运行。

`TestResolveIconWithImplicitBodyWhileResponseStreams` 让页面省略 `</head>` 和 `<body>`，发送 `<main>` 后保持响应开启，核对已声明的图标可以在正文仍在传输时取得。站点名称回归也覆盖了正文里的名称、嵌套模板和 UTF-8 文件头。Apple M4 上以 `-benchtime=1x -count=3` 测量同一无缓存环回页面，0.4.95 和 0.4.96 的中位数分别为 1,155 和 2.83 毫秒。DeskPad 和 Linux 安装验收沿用省略标签的流式页面，核对网址卡、表单和系统入口的真实图标。 [HTML 可省略的标签](https://html.spec.whatwg.org/multipage/syntax.html#optional-tags)

`TestResolveDeclaredArtworkBeforeSlowManifest` 验证没有写尺寸的页头图标也会按实际像素判断。足够清晰时直接使用，跳过慢速网站清单。`TestResolveAfterMisleadingDeclaredArtwork` 覆盖图标尺寸太小或图片缺失时继续读清单。`TestResolveInlineWebsiteArtwork` 验证页头和清单里的内嵌 PNG、SVG 以及离线复用。`TestResolveInvalidInlineArtwork` 检查超限、非图片和编码错误的回退。内嵌地址上限为 64 KiB，通过图片解码器处理。 [数据 URL 规范](https://www.rfc-editor.org/rfc/rfc2397.html)

`TestResolveSecondPageIconWhilePreferredIsSlow` 阻塞页头首选图标，验证及时取得第二个合格图标、取消首个请求、缓存结果，以及跳过无需读取的网站清单。一次查找最多同时下载两个候选图标，逐个解码。`TestProbePageArtworkKeepsAnAvailableImage` 检查清晰度和已取得的小图标。其他解析回归验证取消后停止请求和写缓存，以及重复链接不占用候选数量。

`BenchmarkResolveSecondPageIcon` 用环回服务器把首选图标的失败响应延迟 800 毫秒。在 Apple M4 上各运行三次，0.4.72 与 0.4.73 的完成耗时中位数分别为 805 和 166 毫秒。`TestMacCreatorUsesSecondPageIconOnDeskPad` 从网址卡片打开表单，等备用图标显示后取消，再从原卡片重新进入并创建。它对照表单、卡片、应用库和签名应用包里的图标。`TestLinuxInstalledCreatorUI` 从系统菜单运行安装版，粘贴完整网址后读取备用图标，核对表单和系统入口引用的 PNG，再打开生成的应用。

`TestDeclaredWebsiteNameUsesValidHeadMetadata` 检查 `application-name` 和 `og:site_name` 的优先顺序、长度、有效字符和位置。`TestInspectReportsWebsiteNameBeforeArtworkAndUsesItOffline` 分别暂停 HTML 头部和图标响应，核对名称只上报一次，并检查离线复用。`TestInspectKeepsTheNameWhenArtworkIsMissing` 检查没有图标时仍保留可用名称。 [HTML 应用名称](https://html.spec.whatwg.org/multipage/semantics.html#meta-application-name) · [Open Graph 站点名称](https://ogp.me/)

`TestMacCreatorNamesAppBeforeHeadCompletesOnDeskPad` 暂停网页头部，检查创建表单的名称提示，留空创建后再放行图标，核对安装包的图标和签名。`TestResolveReusesHTTPSConnectionForStreamingHeadAndIcon` 验证 HTTPS 页面提前结束读取后，图标使用同一条 HTTP/2 连接。`TestIconURLPolicyAndDiscovery` 核对 Go 拨号器尝试连接前对实际地址的检查，覆盖数字地址和主机名。 [Go 拨号器](https://pkg.go.dev/net#Dialer)

`TestNativeWebsiteNamePreservesManualText` 检查站点名称到达时的输入内容和保存结果。`TestNativeWebsiteNameIgnoresResultsForAnOldAddress` 切换网址后再放行旧回调。`TestSuggestedNameKeepsAnExistingApplicationReachable` 检查名称留空时复用等价网址的已有应用，及手动命名的独立副本。DeskPad 和 Linux 安装版都留空创建，核对系统使用的实际名称和图标。

`TestNativeCancelCreationKeepsWebsiteArtworkOnTheCard` 在粘贴网址并打开表单后按 `Esc`，再放行图标查找。测试检查回焦、显示名称和图标，以及再次进入表单时复用同一份结果。`TestNativeCancelCreationStopsArtworkForChangedAddress` 确认取消时停止已放弃网址的查找。`TestMacCreatorReturnsPendingWebsiteArtworkOnDeskPad` 和 `TestLinuxInstalledCreatorReturnsPendingWebsiteArtwork` 暂停真实的本机 HTTP 图标响应，在界面返回原网址卡后才放行。两项回归核对实际图标像素、完整网址、安装包中的图标及请求次数。

`TestNativePendingWebsiteArtworkIsSharedAcrossApps` 在等待图标时同站创建两次，核对只启动一次查找，并在移除其中一个应用时继续更新另一个。首次图标不可用时再次粘贴网址，检查新的查找能正常显示图标。`TestMacCreatorSharesPendingWebsiteArtworkOnDeskPad` 和 `TestLinuxInstalledCreatorSharesPendingWebsiteArtwork` 在放行 HTTP 图标响应前分别保存两个名称和不同的首页片段，核对各自的应用标识和完整首页。测试统计实际网页与图标请求，并检查应用库像素及两个签名包的 ICNS 或桌面入口的 PNG。

`TestInspectUsesManifestNameAndValidIcons` 在图标传输结束前验证清单名称，并检查重定向后的相对图片地址、损坏条目和断网后的缓存。`TestResolveSecondManifestIconWhilePreferredIsSlow` 检查备用图标成功后取消慢请求。`TestResolveKeepsBestFallbackAcrossManifest` 核对跨网页与清单的路径去重及按实际尺寸保留备用图标。macOS 的 `TestMacCreatorUsesManifestArtworkOnDeskPad` 对照网址卡、创建表单、应用库和签名应用里的名称与图标。Linux 的 `TestLinuxInstalledCreatorUsesManifestArtwork` 从剪贴板粘贴网址并核对系统入口图标，再从入口打开独立应用验证原网址、查询参数与片段。 [W3C 网站应用清单](https://www.w3.org/TR/appmanifest/) · [W3C 图像资源](https://www.w3.org/TR/image-resource/)

`BenchmarkResolveSecondManifestIcon` 在 Apple M4 上用环回服务器延迟首选图标的失败响应 800 毫秒，每个版本各运行三次。0.4.84 的图标取得耗时中位数为 805 毫秒，0.4.85 为 166 毫秒。

`TestInspectUsesManifestWhilePageArtworkWaits` 暂停一个或两个网页图标响应，检查清单名称、备用图标、重复网址去重以及成功后取消等待中的请求。`TestResolveKeepsAvailablePageArtworkWhileManifestWaits` 先开始读清单，再放行网页图标，核对先到的图标及清单请求的取消。`TestManifestLookupReturnsWhileCanceledRequestsUnwind` 验证取消能通知两项请求，查询可在网络清理期间返回。首选图标立即返回时，`TestResolveDeclaredArtworkBeforeSlowManifest` 核对没有额外请求，`TestResolveUsesReadyPreferredManifestIcon` 核对清单中的首选图标。DeskPad 和 Linux 的清单回归确认读取清单时网页图标仍在等待，并核对预览和安装包中的图标。选择本地图片的用例也检查清单名称提前到达。

`BenchmarkInspectManifestWithSlowPageArtwork` 模拟网页图标延迟 800 毫秒、清单和备用图标可立即取得。Apple M4 上每版运行三组，每组两次。0.4.91 的中位数为 805 毫秒，0.4.92 为 166 毫秒。

`TestNativeCreationUsesCachedArtworkBeforePreview` 暂停预览任务，核对打开表单和输入时的读取次数。快速创建时，第一次保存就使用本机缓存图标。重复提交同名同网址时，验证仍返回已有应用，保存任务跳过缓存读取和打包。`TestCreationNeverWaitsForSiteIcon` 检查冷缓存时立即创建，以及后台图标更新。

`TestMacCreatedAppKeepsInlineArtwork` 通过 MyGo 离屏界面粘贴并提交网址，延迟查找任务，再改名和调整宽度。测试核对同一个签名包的目录标识、Finder 图标和最后保存的设置。关闭测试站点并延后预览任务，再确认首次安装就包含已缓存的图标。`TestLinuxInstalledCreatorUI` 在网站关闭后从安装版创建另一个应用，采样网址卡片与表单的实际图标，再核对系统菜单的 PNG 和移除操作。

`BenchmarkMacCachedWebsiteCreation` 测量快速提交表单至缓存图标签名落盘的过程。测试网站先填好缓存，然后关闭。在 Apple M4 上运行三次 300 毫秒基准，取中位数，耗时从 0.4.44 的 31.9 毫秒降至 0.4.45 的 18.0 毫秒，每次新建省去一次图标更新。重复提交的中位数约为 0.10 毫秒。

`TestReadInstalledIconFormats` 检查 PNG、ICO 和 ICNS 的读取及损坏帧回退。安装验收会清除临时网站图标缓存或自定义图片副本，比较已安装图标、应用库和编辑窗口。Linux 还会重启真实 Creator，在 Xvfb 中用实际窗口像素对照已安装图标。大量应用的回归覆盖图标在后台读取时打开应用、进入编辑和保存设置。

`TestImportFileHonorsExifOrientation` 覆盖 JPEG 和 PNG 的八种方向、TIFF 字节序和透明边距。`TestImportFileReadsWebPExif` 检查两种 WebP EXIF 头与区块填充，`TestImportFileIgnoresDamagedExif` 和 `TestIconExifStopsAtContainerBounds` 检查无效数据。`TestLinuxInstalledCreatorUI` 从 GTK 选择器导入带旋转信息的 PNG，核对已安装图标的像素，以及改尺寸或修复后的原图标字节。 [JPEG Exif](https://www.cipa.jp/std/documents/e/DC-008-2012_E_C.pdf) [PNG eXIf](https://www.w3.org/TR/png/#eXIf) [WebP EXIF](https://developers.google.com/speed/webp/docs/riff_container#metadata)

`TestMacRestoresCachedWebsiteIconAfterRestart` 先生成临时图标，填好缓存后关闭测试网站。重开应用库时核对原图标先显示、后台补图期间仍可编辑、同一签名包最后换成网站图标。再移除缓存并编辑尺寸，确认没有触发新查找。`TestEditingCanSaveWebsiteIconAfterItsBackgroundUpdateFails` 覆盖补图失败后从编辑器保存已有预览。`TestLinuxInstalledCreatorUI` 用旧版格式的临时图标验证安装版重启恢复。`TestMatchesLegacyPlaceholderAtInstalledSizes` 覆盖已改名的旧图标。

`TestMacSettingsKeepInstalledIconAndRepairMissingIcon` 在改尺寸和不改设置的两种情况下验证损坏图标能修复，检查图标字节、保存的设置和签名。`TestMacInstalledCreatorWorkflow` 在独立应用运行时损坏测试 ICNS，从设置保存新尺寸并核对自定义图标、系统入口和会话。`TestLinuxInstalledCreatorUI` 在安装版里保存原设置，修复受损的 PNG 并比较原自定义图标。`TestWindowsRepairsMissingIconFormatUsingInstalledArtwork` 分别删除和截断 PNG、ICO，核对修复时保留可用的原图标和配置。`TestWriteIconsRestoresDamagedFormatFromItsInstalledSibling` 在各平台运行同样的资源级检查。`TestReadRegularRejectsFIFOWithoutWaitingForWriter` 用本机临时管道验证图标文件读取会立即返回。

`TestCustomArtworkUsesTheSavedPNGInEachPackageFormat` 比较 PNG、ICNS 和 ICO 中的 256×256 图标与保存的自定义 PNG。macOS 和 Linux 安装验收也比较实际生成的图标字节。保存尺寸时优先读取已安装图标。测试覆盖可用的同包图标优先于备用副本，以及所有已安装图标都受损时从副本恢复。

`TestMacIconContainsPNGFrames` 和 `TestWindowsIconContainsPNGFrames` 验证五个图标帧的偏移、大小和可解码性。0.4.75 将各帧直接写入系统图标缓冲区。内置图标与带透明通道的自定义图标在改动前后的 ICNS、ICO 文件大小和 SHA-256 相同。`TestWriteIconsReusesLegacyArtwork` 验证没有图标校验值的旧包能继续使用原图标。`TestWriteIconsRestoresDamagedFormatFromItsInstalledSibling` 验证校验值匹配、截断后拒绝以及修复后重新生成校验值。

Apple M4 上以 250 毫秒为目标各测量三次。0.4.74 到 0.4.75 的未修改设置保存中位数从 0.182 毫秒降至 0.082 毫秒，单次分配从 165 KiB 减至 43 KiB。保存窗口尺寸时的单次分配从 202 KiB 减至 79 KiB，耗时约 15 毫秒，包含应用签名。

`BenchmarkCustomArtworkApplication` 用同一张 256×256 图片测量创建应用包和保存尺寸，涵盖图标、配置以及 macOS 应用签名。三次 300 毫秒测量的中位数显示，0.4.47 到 0.4.48 的创建耗时在 Apple M4 上从 31.65 毫秒降到 26.15 毫秒，在单核 ARM64 Linux 来宾里从 8.42 毫秒降到 4.23 毫秒。Linux 保存尺寸从 0.607 毫秒降到 0.326 毫秒。

```sh
go test -run '^$' -bench '^BenchmarkCustomArtworkApplication$' -benchtime=300ms -count=3 -benchmem ./internal/bundle
```

`TestNativeLibraryOpensAnotherAppWhileTheFirstIsStarting` 在第一行等待启动时尝试重复点击、打开第二个应用和修改其设置。`TestSlowLaunchKeepsOtherApplicationsUsable` 暂停系统启动确认，检查其他应用的保存和打开，并覆盖等待中更改同一应用的超时和启动失败后的重试。

`TestBackgroundPackagingLeavesOtherApplicationsUsable` 分别暂停启动修复和站点图标更新的写包步骤。每次都创建并打开另一个应用，再验证同一应用的写入顺序、等待后台任务期间的超时以及完成后的设置保存。用 `go test -race ./internal/library` 同时检查这些操作和重复提交对应用配置的并发访问。

`TestOpenStartsWhileAnotherSystemEntryIsBeingRegistered` 暂停第一个应用的系统入口登记，再从另一个应用的已验证路径启动。放行前需把准确的路径交给操作系统，并恢复第二个应用缺失的入口。`TestLinuxInstalledCreatorUI` 删除隔离的 `.desktop` 文件，从应用库搜索完整网址后按回车，核对原网址、窗口尺寸和重新写出的入口文件。

`TestNativeStartupAllowsCreationWhileLibraryLoads` 先读取旧配置，延迟回传结果。在此期间创建应用，再核对应用库和系统入口恢复使用的是最新记录。`TestNativeLibraryReadCanBeRetried` 覆盖读取失败、重试和恢复系统入口。

`TestNativeLibraryShowsAvailableArtworkWhileAnotherAppStarts` 暂停首行应用的启动，验证第二行先显示图标。筛掉首行时结束等待，重新显示时读取一次图标。`TestNativeLibrarySkipsArtworkForEarlierFilters` 在 120 项应用库里暂存图标任务，连续输入 24 个筛选词，再放行所有任务。0.4.93 读取 35 张图标，0.4.94 只读取最终可见项的图标。`TestSettingsCanSaveWhileInstalledArtworkDecodes` 也核对已有解码任务占用读取名额时，取消后续的排队读取。

`TestNativeLibraryReleasesOffscreenArtwork` 在默认窗口滚过 240 张 256 × 256 图标，核对旧视口释放图像、回到首项时重新读取一次，以及修改尺寸时使用原图。该用例记录垃圾回收后新增的 Go 堆内存。Apple M4 上同一用例从 142.9 MiB 降至 16.4 MiB，缓存条目从 240 个降至 23 个。`TestNativeWideLibraryKeepsVisibleArtwork` 验证宽窗口内的图标跨重绘复用，离开应用库后释放超过预算的图像。

`TestNativeLargeLibraryCanOpenEditAndRemoveWhileArtworkLoads` 用 120 个配置验证应用库分批加载图标，检查图标尚未载入时的编辑表单和临时图标。`TestSlowArtworkKeepsOtherApplicationsUsable` 暂停图标文件读取，确认其他应用的保存和启动可以继续，以及同一应用在文件读取完成后能重试。`TestSettingsCanSaveWhileInstalledArtworkDecodes` 在文件关闭后暂停解码，核对同一应用可以保存名称和尺寸，原图标文件字节一致。以下基准测量应用库读取和索引总耗时、重复显示同一搜索结果和通过标识查找已安装应用的配置。

```sh
go test -run '^$' -bench '^(BenchmarkDesktopLibrary|BenchmarkStoredApplicationLookup)' -benchmem .
```

`BenchmarkRestoreStoredLaunchers` 用 120 和 1,200 份配置测量启动修复时读取与查找配置的耗时和堆分配，使用测试替身提供系统入口和已安装应用。`BenchmarkStoreEditAndRead` 测量同样规模下保存一项尺寸设置并读回的成本。配置回归覆盖同长度、同时间戳的原地修改和替换、旧文件里的重复 ID、缓存与实际写入内容的一致性，以及配置路径被命名管道替换后报错和恢复。

`TestIndependentSettingsSaveWhileAnotherApplicationIsPackaging` 暂停一个应用的包写入，核对另一应用的包和尺寸已保存。`TestAddressChangeKeepsDuplicateChecksConsistent` 核对改址时新旧网址的查重、等待取消和提交后的重复提示。`TestAddressReservationReleasesFirstKeyOnCancellation` 验证改址等待取消时释放已取得的网址。`TestSharedCustomIconSurvivesWhileAnotherSaveIsPending` 验证正在保存的应用仍可使用共享图标，并在最后一次引用消失后清理文件。

`BenchmarkMacRestoreLaunchers` 在 DeskPad 测试环境使用 24 个已登记的签名应用。Apple M4 上测量三组，每组 20 次。系统入口检查全程的中位耗时从强制登记时的 112.5 毫秒降至 2.0 毫秒。`TestLibraryReusesRegistrationUntilTheApplicationChanges` 核对启动、打开、定位和保存未修改设置时复用登记，并检查重建、改名和更换图标后刷新登记。`TestMacLaunchServicesFindsCreatedApp` 用 `NSWorkspace` 检查补登及改名后的实际系统入口。 [Apple 的登记参数](https://developer.apple.com/documentation/coreservices/1446350-lsregisterurl)

`TestOpenStartsWhileAnotherSystemEntryIsBeingRegistered` 暂停一个应用的登记，核对另一应用成功打开并补齐自身的系统入口。DeskPad 上的 `TestMacLaunchServicesFindsCreatedApp` 在同样的暂停点确认 macOS 能找到刚改名的签名应用。Windows 在开始菜单实现中同步更新快捷方式名称和索引。

```sh
go test -run '^$' -bench '^(BenchmarkStoreEditAndRead|BenchmarkRestoreStoredLaunchers)$' -benchtime=24x -count=3 -benchmem ./internal/profile ./internal/library
go test -race ./internal/fileutil ./internal/profile ./internal/library
```

## 独立桌面应用

### 在 DeskPad 虚拟屏测试和查看界面

本机 macOS 原生窗口验收和界面检查使用 DeskPad。该工具在 macOS 中添加一块扩展显示器，并在 DeskPad 窗口里显示其画面。 [DeskPad](https://github.com/Stengo/DeskPad)

```sh
brew install --cask deskpad  # 首次安装
```

本机的原生回归使用 `tools/local_check.py --deskpad`。入口按当前模式复用已有 DeskPad。首次启动且 DeskPad 用作扩展屏时，入口选择能容纳测试窗口的最低像素模式，常用的是 1280×800。这个模式在当前 macOS 登录期间生效。DeskPad 是唯一在线显示器时沿用当前模式。通过、失败或超时后都保持 DeskPad 运行，避免连续验收时反复接入和移除显示器。 [Core Graphics 显示模式的生效范围](https://developer.apple.com/documentation/coregraphics/cgconfigureoption/forsession)

入口在 `~/Applications` 下创建本轮独有的临时目录，用于登记需要从 Launch Services 打开的测试应用。套接字使用较短的 `/tmp` 路径。Launch Services 可在另一进程组打开应用，并按应用的系统标识存放 WebKit 数据和偏好。验收在首次打开前记录本轮新生成的标识。入口按可执行文件所在的目录定位这些进程，确认退出后再清理临时目录和已记录的应用数据。回归覆盖超时后的跨进程组清理和偏好清理。

手动查看时，在「系统设置 → 显示器」确认有单独的 `DeskPad Display`，再把独立的测试窗口拖到虚拟屏，或从应用的「窗口」菜单移动。预览已安装版时使用单独标识和临时数据目录的测试副本。需要取证时仅捕获测试窗口。虚拟屏已出现但 DeskPad 窗口没有画面时，检查「隐私与安全性 → 屏幕与系统音频录制」中的 DeskPad 权限。 [DeskPad 排查说明](https://github.com/Stengo/DeskPad#troubleshooting)

```sh
WEBCRATE_GUI_SMOKE=1 \
  python3 tools/local_check.py --deskpad --timeout 60 -- \
  go test -count=1 -v -run '^TestMacWebsiteWindowInteractions$' ./internal/bundle

WEBCRATE_RELEASE_APP=/Applications/Creator.app \
  python3 tools/local_check.py --deskpad --timeout 90 -- \
  go test -count=1 -v -run '^TestMacInstalledCreatorWorkflow$' .
```

运行器测试会核对主窗口和弹窗的位置，再执行全屏、文件和恢复操作。全屏用例在测试进程内指定 AppKit 的屏幕，WebKit 创建的真实全屏窗口仍会检查位置和大小。安装验收为隔离的管理器和独立应用预置虚拟屏上的位置，再核对实际窗口。改尺寸、改名或重开时沿用应用保存的位置。指定显示器找不到、名称重复或放不下测试窗口时，预检直接失败。

`TestMacWebsiteWindowInteractions` 连续移动 DeskPad 上的 AppKit 窗口 60 次，核对最后的坐标和偏好设置的调用次数。0.4.94 调用了 60 次，0.4.95 合并为 1 次。测试还核对进入全屏前保存普通窗口位置、退出全屏后跳过相同位置，以及拖动后立即关闭时保存最后的窗口位置。安装包验收通过 AppKit 的旧版记录恢复窗口，再读取退出时按当前格式保存的坐标。 [AppKit 窗口位置恢复](https://developer.apple.com/documentation/appkit/nswindow/setframeusingname(_:))

通过 `local_check.py --deskpad` 运行安装与升级回归时，首次打开、从系统入口冷启动和打开升级用管理器前检查负载。每次确认独立页面及会话状态后至少等待两秒，再检查同一阈值。达到入口的启动阈值时最多再等 25 秒。检查通过后，测试退出并确认进程结束，立即重开并核对保存的会话。最后一次立即重开的进程继续用于运行中升级，升级后的重开一起核对缩放和会话，配置修复后打开的同一个窗口继续用于调整尺寸。整个测试继续受入口的负载、内存和超时检查约束。

0.4.72 的完整 DeskPad 升级回归将独立应用的冷启动从 11 次降至 8 次，保留三次立即重开的检查。Apple M4 上本次运行耗时 27.44 秒，启动之间采样到的最高负载为 2.9，宿主有 10 个逻辑 CPU。

检查运行中的 Creator 界面时，用官网安装包提供的资源和带 MyGo 自动化的临时可执行文件。测试暂缓初次读取应用库，确认窗口能填写网址、点击卡片和打开表单。放行后读取实际命名管道，验证创建及时报错并保留草稿。放回普通配置文件后，用应用库的「重试」恢复载入并创建应用。测试通过独立 Unix 套接字操作，并核对实际窗口位于 DeskPad。`WEBCRATE_SCREENSHOTS` 指定网址卡片和重试界面的截图目录。自动化接口只编译进测试版本。

`TestMacCreatorPreferencesAndAboutOnDeskPad` 从窗口里的 Creator 标识打开设置和关于，并在隔离进程的 AppKit 事件队列投递 `⌘,`。它确认打开设置后获得键盘焦点，从关于窗口按快捷键时切回同一个设置窗口并保留输入。测试还检查 `Esc` 放弃更改时保留原表单、设置写入失败后保留输入并能重试、未更改的设置直接关闭、显式保存修复损坏的配置，以及重启后的启动页和新建尺寸。关于窗口检查显示的版本和只读许可全文，以及在两者间切换时的焦点。截图包含标识菜单、设置的两套外观、写入失败提示、关于和许可证。复制版本号使用内存剪贴板回归和 Linux 隔离会话验证。

`TestMacCreatorReopensFromSystemOnDeskPad` 在创建草稿填写名称，通过 AppKit 的窗口菜单最小化，再用 Launch Services 重开同一个签名副本。测试核对原进程、原窗口、仍在 DeskPad 的位置和未提交的名称。`TestLinuxInstalledCreatorReopensFromMenu` 在独立 Xvfb 中用 Openbox 最小化，再从已安装的 `.desktop` 入口重开，检查同一个窗口恢复前台并保留草稿。`TestWindowsInstalledCreatorPreferences` 在 Windows 的安装版中通过开始菜单检查原窗口和草稿。

`TestMacCreatorSavesPreferencesWithoutBlockingOnDeskPad` 暂停隔离副本的设置写入。在「正在保存…」期间打开关于，再检查 `Esc` 和 `⌘Q` 后设置仍在保存，原配置和新建草稿仍在。放行后核对实际写入的尺寸与原草稿。

`TestMacCreatorRevealsInvalidDimensionsOnDeskPad` 和 `TestLinuxInstalledCreatorKeyboard` 在原生窗口输入超出范围的高度，收起尺寸并提交，核对展开后的错误和焦点。修正后按回车创建，回读实际安装的宽高。DeskPad 设置回归也检查填写无效高度时聚焦对应输入框，并保存截图。

`TestMacCreatorCachedURLArtworkOnDeskPad` 填好隔离缓存后关闭测试网站。测试采样网址卡片和表单的图标像素，核对带片段的网址。创建应用并清除缓存后修改首页片段，检查保存前后的图标、签名、完整安装网址、搜索和行焦点。`TestLinuxInstalledCreatorUI` 在安装版中检查同样的离线编辑，并比较系统入口使用的 PNG。

`TestMacCreatorCustomIconOnDeskPad` 在 DeskPad 的隔离窗口点击「更换图标」，由测试提供临时照片路径。它核对旋转后的预览、已保存的 PNG 和签名应用的 ICNS。删掉原照片后重开 Creator，检查编辑窗口图标，并验证修改尺寸后图标字节相同。`TestNativeCustomIconCanBeSelectedAndReset` 检查取消选图时保留草稿与原网站图标。

`TestLateSiteIconCannotReplaceChosenImage` 在站点查找挂起时手选图片，再让结果回传，核对建议名称和图片各自生效。`TestNativeCustomIconDraftUsesTheCurrentWebsiteName` 在选图后更改网址，检查只应用当前站点的名称并复用推荐目录的名称。`TestNativeCreationWithCustomIconDoesNotWaitForWebsiteName` 检查立即提交和完成创建后停止待处理的查找。`TestMacCreatorCustomIconUsesWebsiteNameOnDeskPad` 和 `TestLinuxInstalledCreatorCustomIconUsesWebsiteName` 先选照片，再粘贴只在网站清单中声明名称的网址，对照表单与安装图标。Linux 用原生文件选择器选图，从系统入口重开时核对完整地址。`TestParseAddress`、`TestNativeCreatesFromBareAddressWithURLParameter` 和 `TestMacCreatorLoadsRecommendationsAfterLibraryStartupOnDeskPad` 验证省略协议时含嵌套网址的路径、参数或片段仍能保存。

`TestMacCreatorLibraryArtworkDuringRepairOnDeskPad` 暂停缺少启动器的测试应用的启动修复，在实际应用库中采样另一项的已安装图标。放行后核对首项图标也在原窗口显示，并验证修复后的应用签名。

`TestMacCreatorCachedURLArtworkOnDeskPad` 暂停旧地址的缓存名称读取，再输入同一站点另一个已缓存地址。网站关闭时，在放行旧读取前核对当前名称和图标像素。创建和保存首页片段后核对签名、完整网址及图标文件。

`TestMacCreatorReloadsAnEvictedIconOnDeskPad` 使用隔离应用中超过缓存预算的 1024 像素图标。切换到创建页后替换测试图标，返回应用库后采样新图标，并验证修改尺寸后 ICNS 字节和应用签名。

`TestMacCreatorRetriesAppRemovalOnDeskPad` 将隔离测试应用的配置目录暂时设为只读，检查移除失败时的界面、应用记录、原安装目录和签名。恢复权限后重试，核对应用库和文件。

`TestMacCreatorRecoversInterruptedPackagesOnDeskPad` 和 `TestLinuxInstalledCreatorUI` 模拟 Creator 退出时留下的安装目录。重开安装版后验证原应用仍在库中、原目录恢复、已提交移除和中断打包的文件清除。macOS 核对原签名，Linux 继续从系统入口打开并验证会话。

`TestMacCreatorLoadsRecommendationsAfterLibraryStartupOnDeskPad` 从应用库启动隔离的 Creator，首次切换到创建页后打开 DeepSeek 和小红书。测试根据实际渲染的颜色核对表单里的站点图标，检查各自的完整网址，然后创建小红书独立应用并验证签名及实际 ICNS 的 128 像素帧。

`TestNativeFirstWindowAcceptsAPastedAddress` 检查从首次输入到创建和打开应用的键盘流程。`TestNativeLibraryStartupFocus` 覆盖空库、已有应用、读取错误与重试，以及读取期间用户操作页签的情况。`TestNativePendingLaunchKeepsKeyboardNavigation` 暂停启动，检查连续回车只提交一次、方向键能继续移动，以及读屏描述包含打开状态和完整网址。

`TestMacCreatorKeyboardOnDeskPad` 直接向隔离窗口输入完整网址，`TestLinuxInstalledCreatorKeyboard` 通过隔离的 GTK 剪贴板粘贴。两项检查都在首次输入前读取当前焦点，再检查回车后保留网址的查询参数和片段。DeskPad 和 Linux 的空库启动检查从首次聚焦的创建按钮按回车进入网址框。Windows 安装版设置回归检查相同的两个启动焦点。推荐、网址卡片和应用库的原生检查读取无障碍描述里的完整网址，并检查焦点处的界面和无障碍树。

`TestMacCreatorKeyboardOnDeskPad` 和 `TestLinuxInstalledCreatorKeyboard` 也会从末行返回并按键清空网址，核对首行的 DeepSeek。`TestMacCreatorLibrarySearchStartsAtFirstMatchOnDeskPad` 和 `TestLinuxInstalledCreatorLibrarySearch` 在真实窗口滚动预置的应用库，再输入和清除筛选词，验证搜索焦点与首行位置。用方向键移动到原本不可见的行后返回搜索，核对完整的筛选内容和焦点。DeskPad 回归保存两种搜索的结果截图。

`TestMacCreatorRevealsInstalledAppOnDeskPad` 在隔离窗口创建环回测试站点，再用带前导零的端口提交同一个名称。核对两次都聚焦同一应用，配置仍只有一条，并保存应用库截图。按回车验证独立应用在 DeskPad 上打开并载入原地址，记录从回车到网页就绪的耗时。

在 Apple M4 和 3360 × 2100 的 DeskPad 上，对 0.4.71 的隔离签名副本分段采样。两次从回车到环回网页保存会话并报告就绪分别为 855 和 779 毫秒。查找路径、核对配置和运行时不到 1 毫秒。在另外四次交替采样中，进程创建至启动器进入 `main` 约为 422 至 522 毫秒。延后加载 WebKit 的对照中，进入 `main` 时只消耗约 13 至 16 毫秒 CPU 时间，整体耗时处于相同范围。这些测量使用本机页面、每次一个新测试应用和隔离会话。 [Apple 应用启动性能文档](https://developer.apple.com/documentation/xcode/reducing-your-app-s-launch-time)

隔离的 Creator 通过检查后正常退出，Go 会把覆盖数据写入 `GOCOVERDIR`。检查失败或退出超时，测试仍会清理该进程。以下命令分批运行，并保留原生窗口截图和语句覆盖文件。

```sh
report_dir="$PWD/test-results/ui"
(
  set -eu
  preview_dir=$(mktemp -d /tmp/creator-deskpad.XXXXXX)
  trap 'rm -rf -- "$preview_dir"' EXIT
  mkdir -p "$preview_dir/coverage" "$report_dir"
  python3 tools/local_check.py --timeout 45 -- \
    go build -trimpath -cover -coverpkg=./... -tags creator_e2e -o "$preview_dir/Creator" .
  python3 tools/local_check.py --timeout 45 -- go test -c -o "$preview_dir/installed.test" .
  for tests in \
    'CustomIcon|CustomIconUsesWebsiteName|LoadsRecommendationsAfterLibraryStartup' \
    'AddressPreview|CachedURLArtwork|UsesManifestArtwork|ReturnsPendingWebsiteArtwork|SharesPendingWebsiteArtwork' \
    'Keyboard|PreferencesAndAbout|SavesPreferencesWithoutBlocking' \
    'RevealsInvalidDimensions|LibrarySearchStartsAtFirstMatch' \
    'NamesAppBeforeHeadCompletes|LibraryArtworkDuringRepair|ReloadsAnEvictedIcon' \
    'RecoversInterruptedPackages|RetriesAppRemoval|RevealsInstalledApp'; do
    WEBCRATE_RELEASE_APP=/Applications/Creator.app \
      WEBCRATE_AUTOMATED_CREATOR="$preview_dir/Creator" WEBCRATE_SCREENSHOTS="$report_dir" \
      GOCOVERDIR="$preview_dir/coverage" python3 tools/local_check.py --deskpad --timeout 36 -- \
      "$preview_dir/installed.test" -test.count=1 -test.v -test.run "^TestMacCreator($tests)OnDeskPad$"
  done
  go tool covdata textfmt -i="$preview_dir/coverage" -o="$report_dir/deskpad.out"
)
```

用同一份 macOS 源码和默认 `set` 模式生成本节的 `deskpad.out` 和开头的 `coverage.out`。合并时略去自动化专用的源码，并由 `go tool cover` 合并同一语句的结果。

```sh
awk 'FNR == 1 && NR > 1 { next } $0 !~ /\/desktop_e2e(_darwin)?\.go:/' \
  coverage.out "$report_dir/deskpad.out" > "$report_dir/coverage-with-deskpad.out"
go tool cover -func="$report_dir/coverage-with-deskpad.out"
```

本机在 DeskPad 上验收时，用当前安装包生成不同签名的运行器副本来检验替换。需要运行 0.4.31 之前的真实旧版时，交给独立 macOS CI 桌面验收。

### 原生运行和安装验收

```sh
WEBCRATE_GUI_SMOKE=1 go test -count=1 -v ./internal/bundle
WEBCRATE_GUI_SMOKE=1 go test -count=1 -v ./internal/launcher
go test -run '^$' -bench '^BenchmarkMacCreate$' -benchtime=20x ./internal/bundle
go test -run '^$' -bench '^(BenchmarkPackagedIcon|BenchmarkMacInstalledPath|BenchmarkMacSaveSettings)$' -benchtime=250ms -count=3 -benchmem ./internal/bundle
go test -run '^$' -bench '^BenchmarkPortableRuntimeInstall$' -benchtime=30x ./internal/bundle
go test -run '^$' -bench '^BenchmarkPortableRuntimeCheckAfterRestart$' -benchtime=64x -count=3 -benchmem ./internal/bundle
```

Windows 和 Linux 安装包在启动器旁包含 `.sha256` 校验值。官网打包验收核对各平台安装包和便携包里的二进制与校验值。重启基准测量从新建后端到确认一份 10 MiB 的已安装启动器仍是当前版本。`TestRuntimeChecksAnInstalledVersionUsingThePackageDigest` 验证检查期间可以不读程序文件，`TestRuntimeDoesNotPublishBytesThatDisagreeWithThePackageDigest` 检查字节不符时保留原安装和版本记录，以及恢复来源后的重试。

`TestRuntimeCurrentChecksContinueDuringRepair` 暂停一个应用的启动器修复，再检查另一个应用的版本。它还核对排队的安装超时后未写入启动器或版本记录，修复结束后可以重试。在 Apple M4 上，0.4.88 和 0.4.89 的重启基准中位数均约为 21 微秒，每版运行三次，每次至少 200 毫秒。

系统试跑需要登录桌面。它创建两份临时独立应用，使用本机网页验证系统 WebView 执行、会话隔离和重新打开后的状态保留。测试还会预置旧窗口布局，修改尺寸并改回原尺寸，读取真实网页视口验证每次打开的结果。macOS 的签名测试把 PATH 指向空目录，验证用户侧创建不依赖 Go 或 Xcode。入口测试在专用临时目录创建应用，验证 Launch Services 能按标识找到创建和改名后的应用。

从官网 DMG 安装 Creator 后，使用上面的 DeskPad 安装验收命令。本机也可将 `WEBCRATE_RELEASE_APP` 指向从镜像提取的 Creator。

这项测试用 MyGo 测试工具操作创建和应用库界面，在隔离用户目录下生成实际签名应用。它核对系统入口、名称和图标，从应用库及 macOS 的应用入口打开同一个网址。系统 WebKit 回报实际视口与网站状态，覆盖重复打开时复用原进程、关闭重开、恢复已保存的缩放、改名改尺寸及移除应用。

回归在每次独立应用退出后立即点击应用库的「打开」，并通过原生进程和 WebKit 回报核对启动结果与会话。应用库通过 AppKit 打开独立应用。`TestWorkspaceOpen` 相关测试检查短暂的进程连接错误、永久错误与取消。从系统入口验收已退出的应用时，如果系统仍引用旧进程，测试记录 `-600` 或 `-609` 后短暂等待并重试原入口。 [Apple 的 NSWorkspace 启动接口](https://developer.apple.com/documentation/appkit/nsworkspace/openapplication(at:configuration:completionhandler:))

测试给 DMG 中的 Creator 另建独立签名身份和临时用户目录。新版在旧应用运行时更新原位置的启动器。测试从应用库和 macOS 入口打开，核对原进程和网页状态，退出后从原入口启动新版运行组件。重启 Creator 后，测试在旧 `WebCrate` 目录里修复已有应用，并核对原应用标识、图标、窗口尺寸和会话。新应用写入 `Creator` 目录。设置 `WEBCRATE_PREVIOUS_RELEASE_APP` 为旧版签名应用的绝对路径，可以核对真实旧版启动器升级。

macOS 安装验收在独立应用运行时分别修改尺寸、名称和图标。测试核对原 `.app` 目录标识，再从应用库和系统入口打开，验证当前进程和网页状态，退出重开后核对新设置和原会话。`TestMacSettingsKeepDirectoryAcrossSaveAndRecovery` 覆盖旧版配置文件、仅修改名称大小写、设置写入失败和写入前后的中断。恢复后检查签名、目录标识和实际保存的设置。

通用应用替换回归还会在重新保存或移除时模拟写入失败。创建基准在每轮计时结束后移除临时应用，避免同名同网址的重复保护影响后续测量。

图标基准分别生成默认站点尺寸和自定义图片尺寸的 ICNS、ICO。`TestResizeKeepsPackagedIconPixels` 逐像素核对带透明度的图标和非方形输入。用 `go test -race ./internal/icons` 检查共享缩放器。macOS 路径基准比较当前和旧版安装目录。`TestMacRestoresCompleteBackupForMatchingRevision` 验证从旧目录恢复健康副本。`TestMacSettingsKeepInstalledIconAndRepairMissingIcon` 核对版本标记匹配时仍能识别和修复错误的应用设置及图标。

安装验收在 macOS 截断临时签名应用的 `website.json`，在 Linux 和 Windows 删除临时应用的同名配置文件，再重启实际安装的 Creator。测试从原系统入口打开修复后的应用，核对已保存的配置、窗口尺寸、图标和网站会话。另有回归覆盖配置版本不匹配、便携启动器丢失及 macOS 图标文件为空。

保存基准分别测量设置无变化、修改尺寸和改名。回归核对未修改时保留应用文件和配置，应用文件丢失时可按原配置补回。改名或改尺寸时对照实际安装的图标文件和签名，图标文件缺失时重新生成。

`TestMacWebsiteWindowInteractions` 运行实际 AppKit 和 WebKit。它检查系统窗口按钮、网页顶部的点击区域和底色同步。测试在真实文件选择面板中验证多文件、目录和取消，并用两份临时文件代替系统选择结果，由 WebKit 回读文件名与内容。测试通过 AppKit 菜单及快捷键后退、前进、回到首页和刷新，核对首页的路径、参数和片段，确认保留网站会话与历史，已打开的首页草稿也能继续编辑。临时网站还会触发网页对话框，从空白页打开独立弹窗，验证弹窗里的「回到首页」处于禁用状态，弹窗能和发起页通信并自行关闭。

首次载入回归在原生窗口显示后暂缓本地网页响应。DeskPad 检查提示位于标题栏，网页尺寸和位置保持稳定，响应放行后提示消失。Linux 安装版通过 AT-SPI 看到相同提示，再检查连接失败、提示消失和原窗口里的键盘重试。`TestOpeningStatusTimer` 覆盖快速载入、已入队的取消和一次提示。`TestOpeningStatusKeepsViewportAndRespectsReduceMotion` 核对淡入的中间帧、减少动态效果和网页焦点。

同一项测试在隔离的用户目录中保存新首页，核对当前网页的未保存输入。使用「回到首页」后，检查新地址、原窗口和网站会话。再用无效网址和命名管道替换隔离的应用记录，核对仍能使用最近读取的首页并保留草稿。

同一项测试通过应用的「窗口」菜单触发 `⌘ M`。AppKit 窗口夹具模拟 Dock 的状态变化。调用重开委托后，测试确认同一个 WebKit 窗口重新显示，网页的未保存输入仍在。 [Apple 的应用重开事件](https://developer.apple.com/documentation/appkit/nsapplicationdelegate/applicationshouldhandlereopen(_:hasvisiblewindows:))

同一项测试用 `⌘ =`、`⌘ -` 和 `⌘ 0` 核对放大、缩小和实际大小，读取 WebKit 的实际网页视口与外侧窗口尺寸，再新建 WebKit 视图验证缩放设置，并检查无效设置时使用实际大小。

在 macOS 13 及更新版本，同一项测试还会让主窗口和网页弹窗里的播放器请求全屏。它检查实际显示器尺寸和原 WebKit 视图的归属，然后分别用 Esc 和网页退出操作核对原窗口、网址、视口和未保存内容。回到原窗口后等待它恢复键盘焦点，再测试菜单快捷键。 [WebKit 的全屏设置](https://developer.apple.com/documentation/webkit/wkpreferences/iselementfullscreenenabled)

同一项测试打开并取消原窗口内的系统保存面板，再提供选定的临时路径，验证 WebKit 实际保存服务端附件、Blob 文件和使用 `data:` 链接导出的中文 CSV。测试核对下载文件名、内容和原网页网址。服务器分段发送附件，用来核对完整传输后才替换原文件、断线时保留已有内容、下载期间的文件改动受到保护、同时下载各自完成，以及重试和退出时清理临时文件。错误提示会显示在原窗口，网页保持可用。

同一项窗口测试使用 WebKit 的模拟摄像头和麦克风。拒绝麦克风请求时，网页收到拒绝结果。允许视频和音频请求时，网页取得两条媒体轨道，并在验证后停止。签名包回归核对新建及旧版升级后的两项系统用途说明，并验证升级过程中签名失败时原应用仍可用。

此测试还分别在首次打开和点击网页链接时断开目标地址。重新连通后点击原窗口内的「重新载入」，验证 WebKit 打开失败地址并保留查询参数及原有网站会话。`go test ./internal/webwindow` 覆盖 Windows 和 Linux 共用的 MyGo 重试面板、载入期间的重复点击和失败后再次重试。

测试通过 WebKit 的测试钩子终止临时窗口的实际渲染进程。点击原窗口内的「重新载入」后，核对原窗口尺寸、网址的查询参数与片段以及已保存的网站状态。

可用 Clang 记录这项回归执行的 Objective-C 源码覆盖率。 [LLVM 源码覆盖率](https://clang.llvm.org/docs/SourceBasedCodeCoverage.html)

```sh
profiles=$(mktemp -d)
WEBCRATE_GUI_SMOKE=1 WEBCRATE_MAC_COVERAGE="$profiles" \
  go test -v -count=1 -run '^TestMacWebsiteWindowInteractions$' ./internal/bundle
for probe in "$profiles"/window-*; do
  xcrun llvm-profdata merge -sparse "$probe"/*.profraw -o "$probe/window.profdata"
  xcrun llvm-cov report "$probe/window-test" -instr-profile="$probe/window.profdata" \
    "$PWD/runners/macos/downloads.m" "$PWD/runners/macos/settings.m" "$PWD/runners/macos/main.m"
done
```

`TestMyGoStandaloneReusesRunningInstance` 可在 macOS 14 或更新版本上运行 Windows 和 Linux 共用的 MyGo 启动器源码，并用系统 WebKit 验证主窗口能打开页面、重复启动会退出并交给已有窗口、同名应用可以同时运行且会话隔离。Windows 和 Linux 的安装验收再核对各自安装包提供的启动器。

Windows 和 Linux 的 Go 回归验证离线创建、配置、图标、改名和移除。启动器存储测试核对硬链接、复制回退和版本迁移，并在移除运行时缓存入口后运行两个独立应用。基准使用 8 MiB 的启动器文件，测量磁盘上的启动器安装步骤。Windows 在 PowerShell 中运行 WebView2 试跑。

`TestPortableLaunchesDuringAnotherRuntimeRepair` 使用打包的启动器，在一个应用的修复暂停时，从另一个已安装应用加载本机测试页面，读取实际网页视口。修复完成后检查两份应用仍共享已验证的启动器。

`TestPortableRunnerSettings` 覆盖只修改首页时保存失败和重试、同时修改首页与尺寸时各阶段中断后的恢复，以及应用资源缺失时的修复。恢复用例使用早期版本的 `.window-size-backup` 目录，核对提交的完整网址、尺寸和图标。`TestWindowsPortableSettings` 用真实文件句柄锁定启动器或配置，验证启动器被占用时仍能保存，以及配置被占用时可在解除锁定后重试。

`BenchmarkPortableSaveWindowSize` 测量连续保存尺寸的耗时。在一核 Linux ARM64 测试环境中，用随安装包提供的启动器运行三次 300 毫秒基准，取中位数。禁用硬链接时，从 0.4.43 的每次 4.72 毫秒降到 0.4.44 的 0.17 毫秒。启用硬链接时，两版均约为 0.17 毫秒。

`BenchmarkPortableSaveHomeAndDimensions` 用相同方法测量同时保存首页片段和宽高。禁用硬链接时，从 0.4.66 的每次 4.95 毫秒降到 0.4.67 的 0.17 毫秒。启用硬链接时，分别为 0.18 和 0.17 毫秒。

```powershell
$env:WEBCRATE_GUI_SMOKE = '1'
go test -count=1 -v -run '^TestPortable(StandaloneLoadsInSystemWebView|LaunchesDuringAnotherRuntimeRepair)$' ./internal/bundle
```

Linux 在已登录桌面上运行同一测试，也可以使用 Xvfb。

```sh
WEBCRATE_GUI_SMOKE=1 xvfb-run -a dbus-run-session -- go test -count=1 -v -run '^TestPortable(StandaloneLoadsInSystemWebView|LaunchesDuringAnotherRuntimeRepair)$' ./internal/bundle
```

## Playwright 的适用范围

- 官网验证桌面和手机布局、浅色深色、键盘和触屏展开指南、减少动态效果及按系统定位下载。首屏只使用站内资源，未选择的表单截图延后加载，已验证首屏资源总量小于 170 KB。桌面用例逐个下载九个发布包并校验文件名、大小和 SHA-256。见 [官网说明](../website/README.md)。
- Windows 可以尝试对独立测试进程的 WebView2 内容做 CDP 测试。仅在测试进程中启用临时端口，给它单独的用户数据目录。见 [Playwright WebView2 文档](https://playwright.dev/docs/webview2)。
- 管理器用 MyGo 原生视图。桌面导航、卡片、焦点、菜单和弹窗回归使用 MyGo 测试工具。系统 WebKit 和 WebKitGTK 的包装窗口需要相应系统的实际进程测试。

## Windows 和 Linux 系统安装回归

Windows 和 Linux 的系统测试可以在 [GitHub 托管执行器](https://docs.github.com/en/actions/reference/runners/github-hosted-runners) 上运行。Apple Silicon Mac 也可以用下文的无桌面 Debian 环境验收 Linux ARM64 安装包。

官网构建任务将 x64 和 ARM64 的 Windows 安装器与 Linux DEB 传给同架构的执行器。Windows 读取安装登记，通过真实的「开始」菜单快捷方式启动。Linux 通过包管理器安装，再在 Xvfb 中用 GIO 启动。两套流程都检查 Creator 的窗口确实出现。 [回归配置](../.github/workflows/regression.yml)

Linux 执行器还解压 TAR.XZ，把解压目录放到 PATH 的首位，通过随包的桌面入口启动，再运行 `TestPortableInstalledCreatorWorkflow` 验证创建、打开、保存设置和会话。测试会核对实际进程的可执行文件路径。

`TestPortableInstalledCreatorWorkflow` 用 MyGo 测试工具操作管理器视图，生成的应用使用安装目录里的启动器。它核对系统菜单名称、图标和实际启动目标，使用系统 WebView 验证网址、窗口尺寸、已保存的缩放及会话状态。测试覆盖重复打开原窗口、关掉后重开、改名改尺寸和移除。Linux 用 `website.json` 和 `AppIcon.png` 验证资源同名时仍可以正常安装，并保留用户填写的显示名称。把 `WEBCRATE_PREVIOUS_RELEASE_APP` 设为前一版 Creator 可执行文件的绝对路径，可以用旧版启动器生成应用，验证新版 Creator 更新该应用、恢复系统菜单入口并沿用原会话。Linux 在旧版应用保持打开时更新，验证新启动器能把打开请求交给原进程。旧版启动器需与旧版 Creator 可执行文件放在同一目录。

设置用例保持独立应用打开，通过应用库保存新首页路径和尺寸。测试核对系统入口、安装目录、启动器和图标的文件身份，并重复打开以确认能回到原窗口的网页。退出后从系统入口打开，核对完整的新首页网址、窗口尺寸和原会话。

`TestLinuxInstalledCreatorUI` 从系统菜单打开已安装的 Creator。它在最小窗口里通过实际键盘和鼠标验证 DeepSeek、小红书、输入网址、点击网址卡片、系统选图、重复创建、打开、编辑和移除。测试检查生成应用的实际图标、网址和视口，退出 Creator 后从系统菜单启动独立应用并核对原网页会话，最后重开 Creator 并确认移除会同时清理程序文件和系统菜单入口。

保存首页与尺寸的用例保持原窗口打开，记录 GTK 设置表单从保存到返回应用库的时间，并核对应用目录、进程与运行中的窗口。离线更改自动图标应用的首页片段时，检查原安装目录及图标字节，并确认应用库保留搜索条件和焦点。

`TestLinuxInstalledCreatorPreferences` 在同一套 Xvfb 会话中点击 Creator 标识打开设置，再检查安装版的 `Ctrl+,`、关于窗口的完整 MIT 许可证、复制到系统剪贴板的版本号、按 `Esc` 放弃更改，以及从应用库启动后首次打开推荐应用时填入的网址和已保存的默认尺寸。

`TestLinuxInstalledCreatorRecoversNonregularProfiles` 用命名管道替代隔离的配置文件，检查应用库和创建表单的失败提示。恢复文件后，在原草稿中再次提交，核对实际的应用记录、可执行文件、图标与系统菜单入口，再重试应用库读取。

`TestLinuxInstalledCreatorRecoversWindowState` 和 `TestLinuxInstalledWebsiteRecoversWindowState` 从系统菜单打开临时应用，窗口状态文件和网站缩放记录用命名管道替代。测试核对默认窗口尺寸和实际大小的网页，移动窗口后正常关闭，再检查新保存的窗口位置、缩放和原网页会话。`TestWindowStateFilePreservesMyGoFrames` 检查旧窗口数据的兼容性，包括跨屏坐标、Windows 像素坐标和最大化状态。窗口记录最多读取 1 MiB，网站缩放记录最多读取 64 字节，超限回退到默认值。

`TestLinuxInstalledWebsiteInteractions` 用已安装的桌面启动器打开真实 WebKitGTK 窗口。它通过 AT-SPI 找到重试按钮，分别用回车和点击恢复首次打开和后续访问失败的网址，核对原窗口与登录会话。测试还验证直接打开和先打开空白页这两种登录弹窗都能使用原会话、向发起页传回结果并自行关闭。GTK 子窗口使用 WebKit 的关联视图，测试网页也会确认原生消息通道已关闭。

`TestGtkMediaPermissions` 在隔离的 GTK 会话中使用 WebKitGTK 模拟设备。测试点击实际的权限对话框按钮，验证拒绝后网页收到错误，允许后网页取得音频和视频轨道。另测在请求未完成时打开下一页，等待新文档加载完成再限时读取结果，并检查旧权限框已关闭。启用 `WEBCRATE_LINUX_MEDIA_SMOKE=1`，在 Xvfb 和 D-Bus 会话中运行 `go test -run '^TestGtkMediaPermissions$' ./internal/webwindow`。 [WebKitGTK 的模拟设备接口](https://www.webkitgtk.org/reference/webkit2gtk/stable/method.Settings.set_enable_mock_capture_devices.html)

`TestLinuxInstalledWebsiteNavigation` 在同一个独立 WebKitGTK 窗口使用 `Alt+←`、`Alt+→`、`Alt+Home`、`Ctrl+R` 和 `Ctrl+W`，并通过 `F10` 打开菜单点击「回到首页」。断网后分别验证重载、后退和回到首页，核对完整网址、登录状态、浏览历史和网页键盘焦点。关闭窗口后检查应用进程退出。`TestNavigateHomeUsesSavedAddress` 与 `TestNavigateHomeFromRecovery` 覆盖 Windows 和 Linux 共用的首页判断、载入与恢复逻辑。

Linux 安装回归还会在独立应用运行时保存首页。配置已写入应用而资料提交失败时，快捷键仍使用上一次成功保存的地址。真正保存后，检查当前网页、键盘和进程仍在，再按首页快捷键核对完整的新地址。`TestSavedHomeReadsCommittedProfile` 检查按应用标识读取及无效配置，`TestHomeCommandUsesLatestRequestAndKeepsLastKnownAddress` 检查连续请求、取消和读失败。`TestReplaceProfilesWaitsForReader` 在 Windows 上持有真实文件句柄，确认读句柄释放后能够继续替换应用记录。 [Windows 文件共享权限](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew)

`TestLinuxInstalledWebsiteAddressActions` 从系统入口打开安装版独立应用，通过 `F10` 后点击复制链接和浏览器入口。测试把剪贴板内容粘贴回网页，核对草稿和包含参数、片段的完整网址，再通过隔离的默认浏览器记录器验证系统交接。断线时重复两项操作，核对实际失败的网址和原窗口。

`TestLinuxInstalledWebsiteRuntimeUnavailable` 给测试进程提供无法加载的 WebKitGTK 库，随后通过 AT-SPI 检查原生提示，点击浏览器按钮核对完整首页网址，再用 `Ctrl+W` 关闭。恢复测试进程的库路径后，从原入口启动并核对登录会话。`TestWindowsInstalledWebsiteRuntimeUnavailable` 在 Windows x64 和 ARM64 的安装验收中把同一个独立应用的 WebView2 目录指向空的测试目录，用 UI Automation 检查提示和可用的按钮，再重新打开验证会话。`TestRuntimeFailureOffersBrowserOnlyOnRequest` 检查两端共用的按钮操作。 [WebView2 运行时目录](https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/webview2-idl#createcorewebview2environmentwithoptions)

`TestReadInstalledSettingsFromLibrary` 检查配置丢失、损坏及标识错配时从应用库读取已提交的设置，并核对正常配置优先及应用库缺少该记录的情况。`TestPortableInstalledCreatorWorkflow` 删除隔离应用的配置后先打开原系统入口，确认完整首页、视口、缩放和会话，再启动 Creator 恢复配置文件。`TestLinuxInstalledWebsiteSettingsUnavailable` 同时移走应用库记录，通过 AT-SPI 确认原生提示和关闭按钮。`TestMacInstalledCreatorWorkflow` 重新签署含截断配置的测试副本，通过 Launch Services 启动并核对同一会话，再由 Creator 恢复包内文件。`TestMacWebsiteWindowInteractions` 还会检查启动器读取的配置文件大小和类型，以及匹配的应用标识。 [Apple 应用签名资源说明](https://developer.apple.com/documentation/Technotes/tn3126-inside-code-signing-hashes)

`TestMacWebsiteWindowInteractions` 使用独立剪贴板和浏览器替身，检查 AppKit 菜单复制和打开当前页、相关弹窗及载入失败的网址，并确认默认浏览器无法启动时的提示。`TestVisibleWebsiteAddressPreservesContext` 检查 Windows 和 Linux 共用的网址选择逻辑。

`TestLinuxInstalledWebsiteRecovery` 从系统菜单打开独立应用并访问下一页，再终止该测试应用的 WebKitGTK 渲染进程。通过 AT-SPI 点击「重新载入」，核对原窗口、进程、原网址参数、登录 Cookie 和键盘焦点，并用 `Alt+←` 确认浏览历史。

`TestLinuxInstalledWebsiteZoom` 从系统入口打开应用后直接发送 `Ctrl+=`、`Ctrl+-` 和 `Ctrl+0`，对照网页视口和外侧窗口尺寸。关闭进程后从真实系统入口重新打开原应用和同站点的另一个应用，核对缩放只保存在原应用中。`go test ./internal/webwindow` 也检查 Windows 和 Linux 共用的缩放档位、设置写入、无效值和写入失败。

`TestLinuxInstalledWebsiteFullscreen` 在独立的 Xvfb 会话中启动 Openbox，通过键盘触发网站的全屏操作。测试用 `xprop` 和网页视口核对全屏状态，验证 Esc 和网页退出操作都能恢复原窗口和未保存内容。从全屏退出进程后，通过系统入口再次启动同一个应用，确认它恢复日常窗口尺寸。`TestWindowGeometryStorePreservesBoundsWithoutFullscreen` 另外检查带有旧版全屏状态的设置仍能恢复原有尺寸和最大化状态。

`TestLinuxInstalledWebsitePlacement` 在 Xvfb 和 Openbox 中从系统入口启动独立应用。移动窗口后退出，在 Creator 的应用库保存新尺寸，再打开原入口，核对坐标、尺寸和网站会话。手动移动和缩放后再次重开，核对这次手动调整的结果。设置 `WEBCRATE_PREVIOUS_RELEASE_APP` 可以先用旧版启动器保存窗口位置，再用新版重开。Windows 和 Linux 共用的设置测试覆盖从旧记录恢复，以及保留 Windows 识别不同缩放率显示器所需的像素位置。Wayland 的初始位置由合成器安排。 [XDG Shell 协议](https://gitlab.freedesktop.org/wayland/wayland-protocols/-/blob/main/stable/xdg-shell/xdg-shell.xml)

`TestLinuxInstalledWebsiteFiles` 在 GTK 文件选择器中分别取消、选择临时文件，并由网站读回文件名和内容。测试在下载目录检查中断提示和重试、两份同时下载的同名附件，以及退出时清理正在下载的文件。它在两份附件传输期间写入同名文件，再核对三份文件的完整内容和原网页的文件选择状态。`TestDownloads` 系列测试也覆盖 Windows 和 Linux 共用的下载保存逻辑。

`TestLinuxInstalledWebsitePopupDownloads` 用原网站的会话打开导出子窗口，在隔离的下载目录核对同名原件、新附件和中文 CSV。它检查断线提示属于发起下载的窗口。随后关闭子窗口，分别验证仍可下载成功和断线时主窗口会提示。回归同时核对主窗口里的未保存文字。 [WebKitGTK 下载接口](https://webkitgtk.org/reference/webkit2gtk/stable/class.Download.html)

`TestLinuxInstalledDesktopIdentity` 从系统菜单同时启动两个同名应用，检查 X11 窗口类与各自的 `.desktop` 入口匹配。测试给正在运行的应用改名并启动新入口对应的运行程序，核对原窗口和网页状态。退出后从系统菜单重开，检查入口、网站会话和另一个应用窗口。`TestLinuxInstalledWaylandIdentity` 用 Cage 的无头模式打开应用，读取窗口发给 Wayland 的 `set_app_id` 并验证网页运行。 [桌面入口规范](https://specifications.freedesktop.org/desktop-entry/latest/recognized-keys.html)

Windows 开始菜单回归用两个同名应用检查路径冲突，移除一个后重新注册另一个，验证已安装快捷方式保留原路径。`TestWindowsMenuDirectoryReusesIndexUntilFilesChange` 核对本地目录连续读取复用索引，并在外部修改后重新读取。`TestWindowsStartMenuRechecksAnExternallyChangedShortcut` 核对重复登记时保留原快捷方式且无需临时目录、刷新较新的图标，以及在同名快捷方式的时间戳恢复后仍识别其新归属。`TestWindowsShortcutMoveDoesNotReplaceAnExistingFile` 模拟发布前已有另一个文件占位。两种 Windows 架构的安装验收都执行这些回归。 [Windows 目录变更通知](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-findfirstchangenotificationw)

```sh
go test -count=1 -run '^TestWindows(StartMenu|MenuDirectory|Shortcut)' ./internal/launcher
go test -run '^$' -bench '^BenchmarkStartMenuPinnedName$' -benchtime=250ms -count=3 -benchmem ./internal/launcher
```

命名基准在 120 和 1,200 个菜单项中查找应用已有的编号入口。它测量共享 Go 代码中的查找耗时。实际 Windows 验收会通过 Shell Link 保存和读取已发布的快捷方式。

`TestWindowsWebsiteShortcutIdentity` 通过 Windows Shell 重新打开写入磁盘的 `.lnk`，核对旧入口升级、同名应用分别分组以及改名后沿用标识。`TestWindowsWebsiteProcessIdentity` 在独立进程中使用 Windows API 回读窗口创建前设置的同一标识。这两项回归由 Windows x64 和 ARM64 验收运行。Wine 10.0 的 Shell Link 属性存储仍为空实现，属性回读需要 Windows。 [Windows 应用标识](https://learn.microsoft.com/zh-cn/windows/win32/shell/appids) · [Wine Shell Link 实现](https://github.com/wine-mirror/wine/blob/wine-10.0/dlls/shell32/shelllink.c#L2475-L2493)

### 在 Apple Silicon 上试跑 Windows ARM64 文件操作

可以在下文的一核无桌面 Debian 来宾里用 Wine 执行 Windows ARM64 测试二进制。试跑覆盖英文路径快捷方式的生命周期、目录变更通知、离线创建和图标修复。两个架构的 Windows 系统回归都执行英文和中文路径用例，并用系统 WebView2 验收。Wine 10.0 的 Shell Link 实现用系统代码页写本地目标路径，默认代码页下的中文字符可能丢失。 [Wine 10.0 的 Shell Link 源码](https://github.com/wine-mirror/wine/blob/wine-10.0/dlls/shell32/shelllink.c#L844-L898)

先在专用来宾准备带 ARM64 指针认证修复的 Wine、Xvfb 和 xauth。试跑使用了 Debian 的 `10.0~repack-12.2`，包括 ARM64 的 `wine64`、`libwine` 和通用的 `wine-common`。安装或临时解包前用签名的软件包索引校验文件。临时解包时，真实入口在 `usr/lib/aarch64-linux-gnu/wine/wine`，同目录有 `wineserver`。在解包目录的 `usr/share/wine/wine` 中把 `nls` 链接到 `../nls`，供重定位后的 Wine 服务读取字符数据。 [Debian ARM64 初始化问题](https://bugs.debian.org/1140177)

从项目根目录准备测试文件。`stage` 指向本轮独有的临时目录。

```sh
python3 tools/local_check.py --timeout 60 -- go run ./cmd/package -platform windows/arm64 -prepare
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 python3 tools/local_check.py --timeout 60 -- \
  go test -c -o "$stage/launcher.test.exe" ./internal/launcher
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 python3 tools/local_check.py --timeout 60 -- \
  go test -c -o "$stage/bundle.test.exe" ./internal/bundle
```

在 `local_check.py --timeout 120 --lima creator-check` 管理的同一次运行中，把这两个测试文件、`tools/windows_compat_check.sh`、`resources/windows-arm64/WebCrateRunner.exe` 和相邻的 `WebCrateRunner.exe.sha256` 传到来宾的临时目录，保留启动器及校验值的相对路径。在来宾内用 `sh windows_compat_check.sh "$wine_binary" "$fixture_directory"` 执行，两个参数都是来宾里的路径。脚本串行执行检查，限时 60 秒，使用 640×480 的 Xvfb，并清理自己的 Wine 前缀。运行结束后移除本轮上传的文件。

### 在测试桌面验收安装版

也可以在已安装 Creator 的测试桌面上，把 `WEBCRATE_RELEASE_APP` 设为 Creator 可执行文件的绝对路径，把 `WEBCRATE_RELEASE_MENU` 设为对应的 `.lnk` 或 `.desktop` 入口，运行 `go test -v -count=1 -run '^TestPortableInstalledCreatorWorkflow$' .`。Linux 的窗口验证使用 X11 或 Xvfb 及 `xdotool`。真实窗口的键鼠操作应在专用 Xvfb 会话里运行，并使用 D-Bus、`at-spi2-core` 和 `python3-pyatspi` 读取控件。回归会为本地测试网址设置代理排除项，等待 GIO 启动进程进入实际应用，再核对窗口和启动路径。测试将生成的应用放在临时目录，结束后移除自己的系统菜单入口。

Windows x64 和 ARM64 的安装版回归同时运行 `go test -v -count=1 -run '^TestWindowsInstalledCreatorPreferences$' .`。沿用上述两个路径变量，在隔离的交互式 Windows 桌面运行。用例从开始菜单启动安装版，通过 Windows UI Automation 读取实际控件，确认 Creator 标识菜单和 `Ctrl+,` 都能打开设置。它检查 `Esc` 放弃更改、保存时保留新建草稿，以及重开后的应用库和新建默认尺寸。关于窗口检查版本、只读许可证全文和系统剪贴板。测试通过临时 `APPDATA` 隔离偏好，仅向当前测试进程拥有的前台窗口发送按键。

现有系统 WebView 试跑继续覆盖独立会话、旧窗口布局恢复和启动器替换。AppKit 与 WebKit 在提交和拉取请求时运行，也可在手动触发时选择。手动工作流还可选择 Windows 的独立 WebView2 试跑。发布包和官网的 Playwright 测试在 macOS 执行器运行。

### 在 Apple Silicon Mac 验收 Linux ARM64

在项目根目录用 Lima 建立 Debian 13 来宾。配置限制为 1 核和 1 GiB，关闭桌面输出、宿主目录挂载、SSH 代理转发和测试应用端口转发。先创建专用来宾，后续复用它。`--lima` 只会启动配置符合要求且已经停止的来宾，并在测试后关机、核对状态。 [Lima 安装](https://lima-vm.io/docs/installation/) · [验收配置](../tools/linux-headless.yaml)

```sh
export LIMA_HOME="/tmp/creator-linux-$(id -u)"
python3 tools/local_check.py --timeout 180 -- \
  limactl create --tty=false --name=creator-check tools/linux-headless.yaml
```

首次启动时，通过 `--lima creator-check` 在来宾安装测试依赖。依赖准备完成后，后续测试只传当前的安装包和测试程序。可以通过来宾的 `/var/tmp` 暂存文件，避免挂载宿主机的工作区。

```sh
python3 tools/local_check.py --timeout 180 --lima creator-check -- sh -eu -c '
  limactl shell creator-check -- sudo apt-get update
  limactl shell creator-check -- sudo apt-get install -y --no-install-recommends \
    libgtk-3-0t64 libwebkit2gtk-4.1-0 dbus-daemon desktop-file-utils xvfb xauth xdotool \
    fonts-dejavu-core at-spi2-core python3-pyatspi cage openbox x11-utils
'
```

`TestRuntimeRefreshRepairsAPipeMarker` 验证 Unix 上的损坏版本记录不会等待管道写入。在 `TestLinuxInstalledCreatorUI` 中用命名管道替换隔离应用的真实记录，重启安装版，检查恢复后的记录、系统入口和网页会话。另一项单元回归检查过大记录和同路径的目录。

示例选择创建应用的安装回归。来宾已准备依赖时，可用 `dpkg --install` 直接安装本地包，整段验收使用本地文件与回环测试站点。其他操作可把最后一行换成相应测试名，Wayland 身份测试按上文使用无头 Cage。

```sh
version=$(node -p 'require("./mygo.json").version')
package="Creator-$version-linux-arm64.deb"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 python3 tools/local_check.py --timeout 60 -- \
  go test -c -o "$LIMA_HOME/installed.test" .
python3 tools/local_check.py --timeout 120 --lima creator-check -- sh -eu -c '
  limactl copy "website/dist/downloads/$1" "$LIMA_HOME/installed.test" creator-check:/tmp/
  limactl shell --workdir=/tmp creator-check -- sudo dpkg --install "/tmp/$1"
  limactl shell --workdir=/tmp creator-check -- env \
    WEBCRATE_RELEASE_APP=/opt/creator/creator WEBCRATE_RELEASE_MENU=/usr/share/applications/creator.desktop \
    xvfb-run -a --server-args="-screen 0 1280x800x24 -noreset" dbus-run-session -- \
    ./installed.test -test.v -test.run "^TestLinuxInstalledCreator(UI|Preferences|RecoversNonregularProfiles)$"
' sh "$package"
```

### 测量 Linux 原生进程覆盖率

用以下命令给测试专用的 Creator 和独立启动器加入覆盖率记录。这些回归会关闭受测进程，Go 在退出时写入数据。 [Go 集成测试覆盖率](https://go.dev/doc/build-cover)

```sh
(
  set -eu
  stage=$(mktemp -d)
  trap 'rm -rf -- "$stage"' EXIT
  mkdir "$stage/coverage"
  go build -cover -coverpkg=./... -o "$stage/creator" .
  go build -cover -coverpkg=./... -o "$stage/WebCrateRunner" ./cmd/runner
  sha256sum "$stage/WebCrateRunner" | cut -d ' ' -f 1 | tr -d '\n' > "$stage/WebCrateRunner.sha256"
  cat > "$stage/creator.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Creator
Exec=$stage/creator
Terminal=false
EOF
  WEBCRATE_RELEASE_APP="$stage/creator" WEBCRATE_RELEASE_MENU="$stage/creator.desktop" \
    GOCOVERDIR="$stage/coverage" xvfb-run -a --server-args="-screen 0 1280x800x24 -noreset" \
    dbus-run-session -- go test -v -count=1 -run '^TestLinuxInstalled(Creator(UI|Keyboard|LibrarySearch|Preferences|ReopensFromMenu|UsesManifestArtwork|CustomIconUsesWebsiteName)|Website(Interactions|Navigation|AddressActions|Recovery|Files|PopupDownloads|Zoom|Fullscreen|Placement|OpensWithNonregularIcon))$' .
  go test -c -coverpkg=./... -o "$stage/media.test" ./internal/webwindow
  GOCOVERDIR="$stage/coverage" WEBCRATE_LINUX_MEDIA_SMOKE=1 \
    xvfb-run -a --server-args="-screen 0 1024x768x24 -noreset" \
    dbus-run-session -- "$stage/media.test" -test.count=1 -test.v -test.run '^TestGtkMediaPermissions$'
  go tool covdata percent -i="$stage/coverage"
)
```

使用 Apple Silicon 来宾时，可以交叉编译带计数的程序和测试二进制，一并传入来宾。运行后取回覆盖率目录，在宿主机读取结果。`TestGtkMediaPermissions` 的网页和权限框在子进程中执行。通过 `GOCOVERDIR` 收集它的计数后再查看合并百分比。
