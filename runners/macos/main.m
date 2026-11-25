#import <AppKit/AppKit.h>
#import <WebKit/WebKit.h>
#import "downloads.h"
#import "settings.h"

static char webBackgroundContext;
static NSString *const pageZoomKey = @"WebsitePageZoomPercent";
static NSString *const windowFrameKey = @"WebsiteWindowFrame";
// Keep the same steps as internal/webwindow/zoom.go.
static const NSInteger pageZoomSteps[] = {25, 33, 50, 67, 75, 80, 90, 100, 110, 125, 150, 175, 200, 250, 300, 400, 500};
static const NSInteger pageZoomStepCount = sizeof(pageZoomSteps) / sizeof(pageZoomSteps[0]);

static NSInteger pageZoomIndex(NSInteger percent) {
    for (NSInteger i = 0; i < pageZoomStepCount; ++i) {
        if (pageZoomSteps[i] == percent) return i;
    }
    return NSNotFound;
}

static NSRect websiteSavedFrame(NSDictionary *state) {
    id value = state[@"frame"];
    if (![value isKindOfClass:NSString.class]) return NSZeroRect;
    NSRect frame = NSRectFromString(value);
    if (NSIsEmptyRect(frame) || !isfinite(NSMaxX(frame)) || !isfinite(NSMaxY(frame))) return NSZeroRect;
    return frame;
}

static NSScreen *websiteScreenForFrame(NSRect frame) {
    NSScreen *best = nil;
    CGFloat bestArea = 0;
    for (NSScreen *screen in NSScreen.screens) {
        NSRect overlap = NSIntersectionRect(frame, screen.frame);
        CGFloat area = NSWidth(overlap) * NSHeight(overlap);
        if (area > bestArea) {
            best = screen;
            bestArea = area;
        }
    }
    return best;
}

static NSRect websiteFitFrame(NSRect frame, NSRect area, NSSize minimum) {
    CGFloat width = MAX(minimum.width, MIN(NSWidth(frame), NSWidth(area)));
    CGFloat height = MAX(minimum.height, MIN(NSHeight(frame), NSHeight(area)));
    CGFloat x = MAX(NSMinX(area), MIN(NSMinX(frame), NSMaxX(area) - width));
    CGFloat y = MAX(NSMinY(area), MIN(NSMaxY(frame), NSMaxY(area)) - height);
    return NSMakeRect(x, y, width, height);
}

static BOOL isHomeURL(NSURL *current, NSURL *saved) {
    if ([current isEqual:saved]) return YES;
    // WebKit can include the root slash and omit the default port.
    NSURLComponents *home = [NSURLComponents componentsWithURL:saved resolvingAgainstBaseURL:NO];
    if (!home.percentEncodedPath.length) home.percentEncodedPath = @"/";
    NSInteger port = home.port.integerValue;
    if (([home.scheme isEqualToString:@"https"] && port == 443) || ([home.scheme isEqualToString:@"http"] && port == 80)) {
        home.port = nil;
    }
    return [current isEqual:home.URL];
}

static NSAlert *websiteDialog(NSString *message, WKFrameInfo *frame) {
    NSAlert *alert = [NSAlert new];
    NSString *host = frame.securityOrigin.host;
    alert.messageText = host.length ? host : @"网页消息";
    alert.informativeText = message;
    [alert addButtonWithTitle:@"确定"];
    return alert;
}

@interface WebsiteContent : NSView
@property(nonatomic, strong, readonly) WKWebView *web;
@property(nonatomic, strong) NSURL *failedURL;
@property(nonatomic) BOOL reloadCurrent;
@property(nonatomic, strong) NSStackView *failure;
@property(nonatomic, strong) NSTextField *failedHost;
@property(nonatomic, strong) NSButton *retryButton;
@property(nonatomic, strong) NSTimer *openingTimer;
@property(nonatomic, strong) NSTitlebarAccessoryViewController *openingStatus;
- (instancetype)initWithWebView:(WKWebView *)web;
- (void)loadInitialURL:(NSURL *)url;
- (void)finishOpening;
- (void)showFailureAtURL:(NSURL *)url reloadCurrent:(BOOL)reloadCurrent;
- (void)clearFailure;
- (void)markLoading;
- (void)retry:(id)sender;
@end

@implementation WebsiteContent

- (instancetype)initWithWebView:(WKWebView *)web {
    self = [super initWithFrame:NSZeroRect];
    if (self) {
        _web = web;
        web.frame = self.bounds;
        web.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
        [self addSubview:web];
    }
    return self;
}

- (void)loadInitialURL:(NSURL *)url {
    __weak WebsiteContent *weakContent = self;
    self.openingTimer = [NSTimer timerWithTimeInterval:0.2 repeats:NO block:^(NSTimer *timer) {
        WebsiteContent *content = weakContent;
        content.openingTimer = nil;
        NSWindow *window = content.window;
        if (!window || !content.web.loading) return;

        NSTextField *label = [NSTextField labelWithString:@"正在载入…"];
        label.font = [NSFont systemFontOfSize:12];
        label.textColor = NSColor.secondaryLabelColor;
        [label sizeToFit];
        NSRect frame = label.frame;
        NSView *view = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, NSWidth(frame) + 14, NSHeight(frame))];
        [view addSubview:label];
        NSTitlebarAccessoryViewController *accessory = [NSTitlebarAccessoryViewController new];
        accessory.view = view;
        accessory.layoutAttribute = NSLayoutAttributeRight;
        content.openingStatus = accessory;
        BOOL fade = !NSWorkspace.sharedWorkspace.accessibilityDisplayShouldReduceMotion;
        if (fade) label.alphaValue = 0;
        [window addTitlebarAccessoryViewController:accessory];
        if (fade) {
            [NSAnimationContext runAnimationGroup:^(NSAnimationContext *context) {
                context.duration = 0.12;
                label.animator.alphaValue = 1;
            }];
        }
    }];
    [NSRunLoop.mainRunLoop addTimer:self.openingTimer forMode:NSRunLoopCommonModes];
    if (![self.web loadRequest:[NSURLRequest requestWithURL:url]]) [self finishOpening];
}

- (void)finishOpening {
    [self.openingTimer invalidate];
    self.openingTimer = nil;
    if (!self.openingStatus) return;
    NSWindow *window = self.window;
    NSUInteger index = [window.titlebarAccessoryViewControllers indexOfObjectIdenticalTo:self.openingStatus];
    if (index != NSNotFound) [window removeTitlebarAccessoryViewControllerAtIndex:index];
    self.openingStatus = nil;
}

- (void)showFailureAtURL:(NSURL *)url reloadCurrent:(BOOL)reloadCurrent {
    self.failedURL = url;
    self.reloadCurrent = reloadCurrent;
    if (!self.failure) {
        NSTextField *title = [NSTextField labelWithString:@"无法载入网站"];
        title.font = [NSFont systemFontOfSize:19 weight:NSFontWeightSemibold];
        self.failedHost = [NSTextField labelWithString:@""];
        self.failedHost.font = [NSFont systemFontOfSize:13];
        self.failedHost.textColor = NSColor.secondaryLabelColor;
        self.failedHost.maximumNumberOfLines = 1;
        self.failedHost.lineBreakMode = NSLineBreakByTruncatingTail;
        self.retryButton = [NSButton buttonWithTitle:@"重新载入" target:self action:@selector(retry:)];
        self.retryButton.bezelStyle = NSBezelStyleRounded;
        self.retryButton.keyEquivalent = @"\r";
        self.failure = [NSStackView stackViewWithViews:@[title, self.failedHost, self.retryButton]];
        self.failure.orientation = NSUserInterfaceLayoutOrientationVertical;
        self.failure.alignment = NSLayoutAttributeCenterX;
        self.failure.spacing = 9;
        self.failure.translatesAutoresizingMaskIntoConstraints = NO;
        [self addSubview:self.failure];
        [NSLayoutConstraint activateConstraints:@[
            [self.failure.centerXAnchor constraintEqualToAnchor:self.centerXAnchor],
            [self.failure.centerYAnchor constraintEqualToAnchor:self.centerYAnchor],
            [self.failure.widthAnchor constraintLessThanOrEqualToAnchor:self.widthAnchor constant:-64],
        ]];
    }
    self.failedHost.stringValue = url.host ?: @"";
    self.retryButton.title = @"重新载入";
    self.retryButton.enabled = YES;
    self.web.hidden = YES;
    self.failure.hidden = NO;
    self.window.backgroundColor = NSColor.windowBackgroundColor;
    [self.window makeFirstResponder:self.retryButton];
}

- (void)clearFailure {
    if (!self.failedURL) return;
    self.failedURL = nil;
    self.failure.hidden = YES;
    self.web.hidden = NO;
    self.window.backgroundColor = self.web.underPageBackgroundColor;
    [self.window makeFirstResponder:self.web];
}

- (void)markLoading {
    self.retryButton.enabled = NO;
    self.retryButton.title = @"正在载入…";
}

- (void)retry:(id)sender {
    if (!self.failedURL || !self.retryButton.enabled) return;
    [self markLoading];
    if (!self.reloadCurrent || ![self.web reload]) {
        [self.web loadRequest:[NSURLRequest requestWithURL:self.failedURL]];
    }
}

@end

@interface WebsiteApp : NSObject <NSApplicationDelegate, NSWindowDelegate, NSMenuItemValidation, WKNavigationDelegate, WKUIDelegate>
@property(nonatomic, strong) NSWindow *mainWindow;
@property(nonatomic, strong) NSMapTable<WKWebView *, NSWindow *> *popups;
@property(nonatomic, strong) NSURL *startURL;
@property(nonatomic, copy) NSString *profileID;
@property(nonatomic, copy) void (^pendingHome)(NSURL *);
@property(nonatomic) BOOL readingHome;
@property(nonatomic, strong) WebsiteDownloads *downloads;
@property(nonatomic, strong) NSUserDefaults *preferences;
@property(nonatomic, copy) NSString *windowSettingsRevision;
@property(nonatomic, strong) NSTimer *windowFrameTimer;
@property(nonatomic) NSRect pendingWindowFrame;
@property(nonatomic) NSRect savedWindowFrame;
@property(nonatomic) BOOL fullscreen;
@end

@implementation WebsiteApp

- (instancetype)init {
    self = [super init];
    if (self) {
        _downloads = [WebsiteDownloads new];
        _preferences = NSUserDefaults.standardUserDefaults;
    }
    return self;
}

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    NSBundle *bundle = NSBundle.mainBundle;
    NSDictionary *config = CreatorLoadWebsiteSettings(bundle.resourcePath, bundle.bundleIdentifier);
    if (!config) {
        [self showInvalidConfig];
        return;
    }
    self.startURL = [NSURL URLWithString:config[@"url"]];
    self.profileID = config[@"id"];
    NSString *name = config[@"name"];
    [self installMenus:name];
    self.popups = [NSMapTable strongToStrongObjectsMapTable];

    WKWebViewConfiguration *settings = [WKWebViewConfiguration new];
    if (@available(macOS 14.0, *)) {
        NSString *identifier = self.profileID;
        if (identifier.length == 20 && [identifier hasPrefix:@"app-"]) {
            NSString *tail = [identifier substringFromIndex:4];
            NSString *uuid = [NSString stringWithFormat:@"57435241-5445-4000-%@-%@", [tail substringToIndex:4], [tail substringFromIndex:4]];
            NSUUID *storeID = [[NSUUID alloc] initWithUUIDString:uuid];
            if (storeID) settings.websiteDataStore = [WKWebsiteDataStore dataStoreForIdentifier:storeID];
        }
    }

    NSInteger width = MIN(MAX([config[@"width"] integerValue], 640), 2400);
    NSInteger height = MIN(MAX([config[@"height"] integerValue], 480), 1800);
    NSNumber *revision = config[@"windowRevision"];
    unsigned long long version = [revision isKindOfClass:NSNumber.class] ? revision.unsignedLongLongValue : 0;
    self.windowSettingsRevision = [NSString stringWithFormat:@"%llu", version];
    NSDictionary *state = [self.preferences dictionaryForKey:windowFrameKey];
    NSRect saved = websiteSavedFrame(state);
    if ([state[@"revision"] isEqual:self.windowSettingsRevision]) self.savedWindowFrame = saved;
    NSScreen *previousScreen = websiteScreenForFrame(saved);
    NSRect visible = (previousScreen ?: NSScreen.mainScreen).visibleFrame;
    NSSize minimum = NSMakeSize(640, 480);
    NSSize size = NSMakeSize(MIN(width, MAX(minimum.width, NSWidth(visible) - 24)),
                             MIN(height, MAX(minimum.height, NSHeight(visible) - 64)));
    WKWebView *web = [self webView:settings];
    self.mainWindow = [self windowForWebView:web title:name size:size];
    self.mainWindow.contentMinSize = minimum;
    if (previousScreen) {
        if (![state[@"revision"] isEqual:self.windowSettingsRevision]) {
            // Apply dimensions from Creator while keeping the top-left position on the same display.
            NSSize configured = self.mainWindow.frame.size;
            saved.origin.y = NSMaxY(saved) - configured.height;
            saved.size = configured;
        }
        NSRect minContent = NSMakeRect(0, 0, minimum.width, minimum.height);
        NSSize minFrame = [self.mainWindow frameRectForContentRect:minContent].size;
        [self.mainWindow setFrame:websiteFitFrame(saved, visible, minFrame) display:NO];
    } else if (NSIsEmptyRect(saved)) {
        // Older releases stored only the AppKit autosave entry.
        NSString *legacyKey = version
            ? [NSString stringWithFormat:@"Website-settings-%llu", version]
            : [NSString stringWithFormat:@"Website-%ld-%ld", (long)width, (long)height];
        [self.mainWindow setFrameUsingName:legacyKey];
    }
    [self.mainWindow makeKeyAndOrderFront:nil];
    if (@available(macOS 14.0, *)) [NSApp activate];
    else [NSApp activateIgnoringOtherApps:YES];
    [(WebsiteContent *)self.mainWindow.contentView loadInitialURL:self.startURL];
}

- (void)showInvalidConfig {
    NSAlert *alert = [NSAlert new];
    alert.messageText = @"无法读取应用设置";
    alert.informativeText = @"请在 Creator 应用库中检查此应用。";
    [alert addButtonWithTitle:@"关闭"];
    [alert runModal];
    [NSApp terminate:nil];
}

- (WKWebView *)webView:(WKWebViewConfiguration *)configuration {
    if (@available(macOS 12.3, *)) configuration.preferences.elementFullscreenEnabled = YES;
    WKWebView *web = [[WKWebView alloc] initWithFrame:NSZeroRect configuration:configuration];
    NSInteger zoom = [self.preferences integerForKey:pageZoomKey];
    if (pageZoomIndex(zoom) != NSNotFound) web.pageZoom = zoom / 100.0;
    web.navigationDelegate = self;
    web.UIDelegate = self;
    [web setAllowsBackForwardNavigationGestures:YES];
    return web;
}

- (NSWindow *)windowForWebView:(WKWebView *)web title:(NSString *)title size:(NSSize)size {
    NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
        NSWindowStyleMaskResizable | NSWindowStyleMaskMiniaturizable;
    NSWindow *window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, size.width, size.height)
        styleMask:style backing:NSBackingStoreBuffered defer:NO];
    window.title = title;
    window.titleVisibility = NSWindowTitleHidden;
    window.titlebarAppearsTransparent = YES;
    window.titlebarSeparatorStyle = NSTitlebarSeparatorStyleNone;
    window.releasedWhenClosed = NO;
    window.delegate = self;
    window.contentView = [[WebsiteContent alloc] initWithWebView:web];
    [web addObserver:self forKeyPath:@"underPageBackgroundColor" options:NSKeyValueObservingOptionInitial
        context:&webBackgroundContext];
    [window center];
    return window;
}

- (WebsiteContent *)contentForWebView:(WKWebView *)web {
    // WebKit temporarily hosts a fullscreen web view in its own window.
    if ([web.superview isKindOfClass:WebsiteContent.class]) return (WebsiteContent *)web.superview;
    WebsiteContent *main = (WebsiteContent *)self.mainWindow.contentView;
    if (main.web == web) return main;
    return (WebsiteContent *)[self.popups objectForKey:web].contentView;
}

- (void)observeValueForKeyPath:(NSString *)keyPath ofObject:(id)object change:(NSDictionary *)change context:(void *)context {
    if (context == &webBackgroundContext) {
        WKWebView *web = object;
        WebsiteContent *content = [self contentForWebView:web];
        if (!content.failedURL) content.window.backgroundColor = web.underPageBackgroundColor;
        return;
    }
    [super observeValueForKeyPath:keyPath ofObject:object change:change context:context];
}

- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)sender { return NO; }

- (BOOL)applicationShouldHandleReopen:(NSApplication *)sender hasVisibleWindows:(BOOL)hasVisibleWindows {
    NSWindow *window = self.mainWindow;
    if (!window || (hasVisibleWindows && !window.miniaturized)) return YES;
    if (window.miniaturized) [window deminiaturize:nil];
    [window makeKeyAndOrderFront:nil];
    return NO;
}

- (BOOL)canSaveWindowFrame {
    NSWindow *window = self.mainWindow;
    return self.windowSettingsRevision && window.visible && !self.fullscreen && !(window.styleMask & NSWindowStyleMaskFullScreen);
}

- (void)writePendingWindowFrame {
    [self.windowFrameTimer invalidate];
    self.windowFrameTimer = nil;
    NSRect frame = self.pendingWindowFrame;
    self.pendingWindowFrame = NSZeroRect;
    if (NSIsEmptyRect(frame) || NSEqualRects(frame, self.savedWindowFrame)) return;
    [self.preferences setObject:@{@"frame": NSStringFromRect(frame), @"revision": self.windowSettingsRevision}
        forKey:windowFrameKey];
    self.savedWindowFrame = frame;
}

- (void)saveWindowFrame {
    if ([self canSaveWindowFrame]) self.pendingWindowFrame = self.mainWindow.frame;
    [self writePendingWindowFrame];
}

- (void)windowDidMove:(NSNotification *)notification {
    if (notification.object != self.mainWindow || self.mainWindow.inLiveResize || ![self canSaveWindowFrame]) return;
    self.pendingWindowFrame = self.mainWindow.frame;
    if (self.windowFrameTimer) return;

    // Use the default run-loop mode so the write can wait for mouse tracking.
    __weak WebsiteApp *weakApp = self;
    self.windowFrameTimer = [NSTimer scheduledTimerWithTimeInterval:0.2 repeats:NO block:^(NSTimer *timer) {
        [weakApp writePendingWindowFrame];
    }];
}

- (void)windowDidEndLiveResize:(NSNotification *)notification {
    if (notification.object == self.mainWindow) [self saveWindowFrame];
}

- (void)windowWillEnterFullScreen:(NSNotification *)notification {
    if (notification.object != self.mainWindow) return;
    [self saveWindowFrame];
    self.fullscreen = YES;
}

- (void)windowDidFailToEnterFullScreen:(NSWindow *)window {
    if (window == self.mainWindow) self.fullscreen = NO;
}

- (void)windowDidExitFullScreen:(NSNotification *)notification {
    if (notification.object != self.mainWindow) return;
    self.fullscreen = NO;
    [self saveWindowFrame];
}

- (void)applicationWillTerminate:(NSNotification *)notification {
    [self saveWindowFrame];
    [self.downloads cancelPendingDownloads];
}

- (void)windowWillClose:(NSNotification *)notification {
    NSWindow *window = notification.object;
    if (window == self.mainWindow) {
        self.pendingHome = nil;
        [self saveWindowFrame];
        // Reopening uses the same window and WebKit document.
        return;
    }
    if ([window.contentView isKindOfClass:WebsiteContent.class]) {
        WebsiteContent *content = (WebsiteContent *)window.contentView;
        [content finishOpening];
        WKWebView *view = content.web;
        [view removeObserver:self forKeyPath:@"underPageBackgroundColor" context:&webBackgroundContext];
        // Keep the window alive through its close notification.
        dispatch_async(dispatch_get_main_queue(), ^{ [self.popups removeObjectForKey:view]; });
    }
}

- (WKWebView *)webView:(WKWebView *)webView createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
    forNavigationAction:(WKNavigationAction *)action windowFeatures:(WKWindowFeatures *)features {
    NSURL *url = action.request.URL;
    if (!CreatorIsWebsite(url) && ![url.absoluteString isEqualToString:@"about:blank"]) return nil;
    WKWebView *popup = [self webView:configuration];
    NSWindow *window = [self windowForWebView:popup title:webView.window.title size:NSMakeSize(880, 650)];
    [self.popups setObject:window forKey:popup];
    [window makeKeyAndOrderFront:nil];
    return popup;
}

- (void)webViewDidClose:(WKWebView *)webView { [[self.popups objectForKey:webView] close]; }

- (NSOpenPanel *)uploadPanel { return NSOpenPanel.openPanel; }

- (void)webView:(WKWebView *)webView requestMediaCapturePermissionForOrigin:(WKSecurityOrigin *)origin
    initiatedByFrame:(WKFrameInfo *)frame type:(WKMediaCaptureType)type
    decisionHandler:(void (^)(WKPermissionDecision))decide {
    decide(webView.window.isVisible ? WKPermissionDecisionPrompt : WKPermissionDecisionDeny);
}

- (void)webView:(WKWebView *)webView runOpenPanelWithParameters:(WKOpenPanelParameters *)parameters
    initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(NSArray<NSURL *> *))complete {
    NSWindow *window = webView.window;
    if (!window.isVisible) {
        complete(nil);
        return;
    }
    NSOpenPanel *panel = [self uploadPanel];
    panel.canChooseDirectories = parameters.allowsDirectories;
    panel.canChooseFiles = !parameters.allowsDirectories;
    panel.allowsMultipleSelection = parameters.allowsMultipleSelection;
    NSString *host = frame.securityOrigin.host;
    if (host.length) panel.message = [NSString stringWithFormat:@"上传到 %@", host];
    [panel beginSheetModalForWindow:window completionHandler:^(NSModalResponse answer) {
        complete(answer == NSModalResponseOK ? panel.URLs : nil);
    }];
}

- (void)webView:(WKWebView *)webView runJavaScriptAlertPanelWithMessage:(NSString *)message
    initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(void))complete {
    NSWindow *window = webView.window;
    if (!window.isVisible) {
        complete();
        return;
    }
    [websiteDialog(message, frame) beginSheetModalForWindow:window completionHandler:^(NSModalResponse answer) {
        complete();
    }];
}

- (void)webView:(WKWebView *)webView runJavaScriptConfirmPanelWithMessage:(NSString *)message
    initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(BOOL))complete {
    NSWindow *window = webView.window;
    if (!window.isVisible) {
        complete(NO);
        return;
    }
    NSAlert *alert = websiteDialog(message, frame);
    [alert addButtonWithTitle:@"取消"];
    [alert beginSheetModalForWindow:window completionHandler:^(NSModalResponse answer) {
        complete(answer == NSAlertFirstButtonReturn);
    }];
}

- (void)webView:(WKWebView *)webView runJavaScriptTextInputPanelWithPrompt:(NSString *)prompt
    defaultText:(NSString *)defaultText initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(NSString *))complete {
    NSWindow *window = webView.window;
    if (!window.isVisible) {
        complete(nil);
        return;
    }
    NSAlert *alert = websiteDialog(prompt, frame);
    [alert addButtonWithTitle:@"取消"];
    NSTextField *field = [NSTextField textFieldWithString:defaultText ?: @""];
    field.frame = NSMakeRect(0, 0, 280, field.fittingSize.height);
    alert.accessoryView = field;
    alert.window.initialFirstResponder = field;
    [alert beginSheetModalForWindow:window completionHandler:^(NSModalResponse answer) {
        complete(answer == NSAlertFirstButtonReturn ? field.stringValue : nil);
    }];
}

- (void)webView:(WKWebView *)webView decidePolicyForNavigationAction:(WKNavigationAction *)action
    decisionHandler:(void (^)(WKNavigationActionPolicy))decision {
    NSURL *url = action.request.URL;
    NSString *scheme = url.scheme.lowercaseString;
    BOOL download = action.shouldPerformDownload;
    if (CreatorIsWebsite(url) || [scheme isEqualToString:@"about"] || [scheme isEqualToString:@"blob"] ||
        ([scheme isEqualToString:@"data"] && (download || (action.targetFrame && !action.targetFrame.mainFrame)))) {
        decision(download ? WKNavigationActionPolicyDownload : WKNavigationActionPolicyAllow);
        return;
    }
    if (action.navigationType == WKNavigationTypeLinkActivated &&
        ([scheme isEqualToString:@"mailto"] || [scheme isEqualToString:@"tel"])) {
        [NSWorkspace.sharedWorkspace openURL:url];
    }
    decision(WKNavigationActionPolicyCancel);
}

- (void)webView:(WKWebView *)webView decidePolicyForNavigationResponse:(WKNavigationResponse *)response
    decisionHandler:(void (^)(WKNavigationResponsePolicy))decision {
    NSURLResponse *result = response.response;
    NSString *disposition = nil;
    if ([result isKindOfClass:NSHTTPURLResponse.class]) {
        NSString *header = [(NSHTTPURLResponse *)result valueForHTTPHeaderField:@"Content-Disposition"];
        disposition = [[header componentsSeparatedByString:@";"].firstObject
            stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceCharacterSet];
    }
    BOOL attachment = disposition.length && [disposition caseInsensitiveCompare:@"attachment"] == NSOrderedSame;
    decision(attachment || !response.canShowMIMEType ? WKNavigationResponsePolicyDownload : WKNavigationResponsePolicyAllow);
}

- (void)webView:(WKWebView *)webView navigationAction:(WKNavigationAction *)action didBecomeDownload:(WKDownload *)download {
    if (action.targetFrame.mainFrame) [[self contentForWebView:webView] finishOpening];
    [self.downloads adopt:download fallbackWindow:self.mainWindow];
}

- (void)webView:(WKWebView *)webView navigationResponse:(WKNavigationResponse *)response didBecomeDownload:(WKDownload *)download {
    if (response.forMainFrame) [[self contentForWebView:webView] finishOpening];
    [self.downloads adopt:download fallbackWindow:self.mainWindow];
}

- (void)webView:(WKWebView *)webView didFailProvisionalNavigation:(WKNavigation *)navigation withError:(NSError *)error {
    [self showNavigationFailure:error inWebView:webView];
}

- (void)webView:(WKWebView *)webView didFailNavigation:(WKNavigation *)navigation withError:(NSError *)error {
    [self showNavigationFailure:error inWebView:webView];
}

- (void)showNavigationFailure:(NSError *)error inWebView:(WKWebView *)webView {
    [[self contentForWebView:webView] finishOpening];
    if (![error.domain isEqualToString:NSURLErrorDomain] || error.code == NSURLErrorCancelled) return;
    id failed = error.userInfo[NSURLErrorFailingURLErrorKey];
    NSURL *url = [failed isKindOfClass:NSURL.class] ? failed : nil;
    if (!CreatorIsWebsite(url)) {
        id address = error.userInfo[NSURLErrorFailingURLStringErrorKey];
        if ([address isKindOfClass:NSString.class]) url = [NSURL URLWithString:address];
    }
    [self showFailureAtURL:url inWebView:webView rendererTerminated:NO];
}

- (void)webViewWebContentProcessDidTerminate:(WKWebView *)webView {
    [[self contentForWebView:webView] finishOpening];
    [self showFailureAtURL:nil inWebView:webView rendererTerminated:YES];
}

- (void)showFailureAtURL:(NSURL *)url inWebView:(WKWebView *)webView rendererTerminated:(BOOL)rendererTerminated {
    WebsiteContent *content = [self contentForWebView:webView];
    if (!content.window) return;
    NSURL *current = webView.backForwardList.currentItem.URL;
    if (!CreatorIsWebsite(url)) url = content.failedURL ?: webView.URL;
    if (!CreatorIsWebsite(url)) url = current;
    if (!CreatorIsWebsite(url) && content.window == self.mainWindow) url = self.startURL;
    if (CreatorIsWebsite(url)) [content showFailureAtURL:url reloadCurrent:rendererTerminated && [url isEqual:current]];
}

- (void)webView:(WKWebView *)webView didFinishNavigation:(WKNavigation *)navigation {
    WebsiteContent *content = [self contentForWebView:webView];
    [content finishOpening];
    [content clearFailure];
}

- (WebsiteContent *)activeContent {
    NSWindow *window = NSApp.keyWindow ?: self.mainWindow;
    if (!window.visible) return nil;
    if ([window.contentView isKindOfClass:WebsiteContent.class]) return (WebsiteContent *)window.contentView;
    WebsiteContent *main = (WebsiteContent *)self.mainWindow.contentView;
    if (window && main.web.window == window) return main;
    for (WKWebView *popup in self.popups.keyEnumerator) {
        if (popup.window == window) return [self contentForWebView:popup];
    }
    return nil;
}

- (void)back:(id)sender {
    WKWebView *web = [self activeContent].web;
    if (web.canGoBack) self.pendingHome = nil;
    [web goBack];
}
- (void)forward:(id)sender {
    WKWebView *web = [self activeContent].web;
    if (web.canGoForward) self.pendingHome = nil;
    [web goForward];
}
- (BOOL)canGoHome {
    return self.startURL && self.mainWindow && [self activeContent].window == self.mainWindow;
}

- (BOOL)validateMenuItem:(NSMenuItem *)item {
    if (item.action == @selector(goHome:)) return [self canGoHome];
    if (item.action == @selector(copyWebsiteAddress:) || item.action == @selector(openInBrowser:)) {
        return [self visibleWebsiteURL] != nil;
    }
    return YES;
}

- (void)goHome:(id)sender {
    if (![self canGoHome]) return;
    NSURL *previous = [self activeContent].web.URL;
    __weak WebsiteApp *app = self;
    self.pendingHome = ^(NSURL *address) { [app loadHome:address from:previous]; };
    [self readPendingHome];
}

- (void)readPendingHome {
    void (^request)(NSURL *) = self.pendingHome;
    if (self.readingHome || !request) return;
    self.readingHome = YES;
    NSString *profileID = self.profileID;
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
        @autoreleasepool {
            NSURL *address = CreatorSavedHome(profileID);
            dispatch_async(dispatch_get_main_queue(), ^{
                self.readingHome = NO;
                if (self.pendingHome != request) {
                    [self readPendingHome];
                    return;
                }
                self.pendingHome = nil;
                if (CreatorIsWebsite(address)) self.startURL = address;
                request(self.startURL);
            });
        }
    });
}

- (void)loadHome:(NSURL *)address from:(NSURL *)previous {
    if (![self canGoHome]) return;
    WebsiteContent *content = (WebsiteContent *)self.mainWindow.contentView;
    NSURL *current = content.web.URL;
    if (current != previous && ![current isEqual:previous]) return;
    if (!content.failedURL && !content.web.loading && isHomeURL(current, address)) return;
    if (content.failedURL) [content markLoading];
    [content.web loadRequest:[NSURLRequest requestWithURL:address]];
}

- (void)reload:(id)sender {
    self.pendingHome = nil;
    WebsiteContent *content = [self activeContent];
    if (content.failedURL) [content retry:nil];
    else if (content.web.URL) [content.web reload];
    else if (content.window == self.mainWindow && self.startURL) {
        [content.web loadRequest:[NSURLRequest requestWithURL:self.startURL]];
    }
}
- (void)changeZoom:(NSInteger)step {
    WebsiteContent *content = [self activeContent];
    if (!content) return;
    NSInteger current = lround(content.web.pageZoom * 100);
    NSInteger index = pageZoomIndex(current);
    NSInteger zoom = 100;
    if (step && index != NSNotFound) zoom = pageZoomSteps[MIN(MAX(index + step, 0), pageZoomStepCount - 1)];
    if (zoom == current) return;
    content.web.pageZoom = zoom / 100.0;
    if (content.window == self.mainWindow) [self.preferences setInteger:zoom forKey:pageZoomKey];
}
- (void)zoomIn:(id)sender { [self changeZoom:1]; }
- (void)zoomOut:(id)sender { [self changeZoom:-1]; }
- (void)actualSize:(id)sender { [self changeZoom:0]; }
- (void)minimizeWindow:(id)sender {
    NSWindow *window = NSApp.keyWindow ?: self.mainWindow;
    if (window.visible) [window miniaturize:sender];
}

- (NSURL *)visibleWebsiteURL {
    WebsiteContent *content = [self activeContent];
    if (!content) return nil;
    if (CreatorIsWebsite(content.failedURL)) return content.failedURL;
    if (CreatorIsWebsite(content.web.URL)) return content.web.URL;
    return content.window == self.mainWindow && CreatorIsWebsite(self.startURL) ? self.startURL : nil;
}

- (NSPasteboard *)websiteAddressPasteboard { return NSPasteboard.generalPasteboard; }

- (void)copyWebsiteAddress:(id)sender {
    NSURL *url = [self visibleWebsiteURL];
    if (!url) return;
    NSPasteboard *board = [self websiteAddressPasteboard];
    [board clearContents];
    [board setString:url.absoluteString forType:NSPasteboardTypeString];
}

- (BOOL)openWebsiteURL:(NSURL *)url { return [NSWorkspace.sharedWorkspace openURL:url]; }

- (void)openInBrowser:(id)sender {
    NSURL *url = [self visibleWebsiteURL];
    if (!url || [self openWebsiteURL:url]) return;
    NSWindow *window = [self activeContent].window;
    if (!window.isVisible) return;
    NSAlert *alert = [NSAlert new];
    alert.alertStyle = NSAlertStyleWarning;
    alert.messageText = @"无法打开默认浏览器";
    alert.informativeText = @"请检查系统的默认浏览器设置。";
    [alert addButtonWithTitle:@"确定"];
    [alert beginSheetModalForWindow:window completionHandler:nil];
}

- (void)installMenus:(NSString *)name {
    NSMenu *bar = [NSMenu new];
    NSMenu *app = [self menu:name inBar:bar];
    [app addItemWithTitle:[@"退出 " stringByAppendingString:name] action:@selector(terminate:) keyEquivalent:@"q"];

    NSMenu *edit = [self menu:@"编辑" inBar:bar];
    [edit addItemWithTitle:@"撤销" action:@selector(undo:) keyEquivalent:@"z"];
    [edit addItem:[NSMenuItem separatorItem]];
    [edit addItemWithTitle:@"剪切" action:@selector(cut:) keyEquivalent:@""];
    [edit addItemWithTitle:@"复制" action:@selector(copy:) keyEquivalent:@"c"];
    [edit addItemWithTitle:@"粘贴" action:@selector(paste:) keyEquivalent:@"v"];
    [edit addItemWithTitle:@"全选" action:@selector(selectAll:) keyEquivalent:@"a"];

    NSMenu *view = [self menu:@"显示" inBar:bar];
    [view addItemWithTitle:@"后退" action:@selector(back:) keyEquivalent:@"["].target = self;
    [view addItemWithTitle:@"前进" action:@selector(forward:) keyEquivalent:@"]"].target = self;
    [view addItemWithTitle:@"回到首页" action:@selector(goHome:) keyEquivalent:@"H"].target = self;
    [view addItemWithTitle:@"重新载入" action:@selector(reload:) keyEquivalent:@"r"].target = self;
    [view addItem:[NSMenuItem separatorItem]];
    [view addItemWithTitle:@"放大网页" action:@selector(zoomIn:) keyEquivalent:@"="].target = self;
    [view addItemWithTitle:@"缩小网页" action:@selector(zoomOut:) keyEquivalent:@"-"].target = self;
    [view addItemWithTitle:@"实际大小" action:@selector(actualSize:) keyEquivalent:@"0"].target = self;
    [view addItem:[NSMenuItem separatorItem]];
    [view addItemWithTitle:@"复制网页链接" action:@selector(copyWebsiteAddress:) keyEquivalent:@""].target = self;
    [view addItemWithTitle:@"在浏览器中打开" action:@selector(openInBrowser:) keyEquivalent:@""].target = self;

    NSMenu *window = [self menu:@"窗口" inBar:bar];
    [window addItemWithTitle:@"最小化窗口" action:@selector(minimizeWindow:) keyEquivalent:@"m"].target = self;
    [window addItemWithTitle:@"关闭窗口" action:@selector(performClose:) keyEquivalent:@"w"];
    NSMenuItem *closeShortcut = [window addItemWithTitle:@"关闭窗口" action:@selector(performClose:) keyEquivalent:@"x"];
    closeShortcut.hidden = YES;
    closeShortcut.allowsKeyEquivalentWhenHidden = YES;
    NSApp.windowsMenu = window;
    NSApp.mainMenu = bar;
}

- (NSMenu *)menu:(NSString *)name inBar:(NSMenu *)bar {
    NSMenuItem *item = [NSMenuItem new];
    NSMenu *menu = [[NSMenu alloc] initWithTitle:name];
    item.submenu = menu;
    [bar addItem:item];
    return menu;
}

@end

int main(void) {
    @autoreleasepool {
        NSApplication *app = NSApplication.sharedApplication;
        [app setActivationPolicy:NSApplicationActivationPolicyRegular];
        WebsiteApp *delegate = [WebsiteApp new];
        app.delegate = delegate;
        [app run];
    }
    return 0;
}
