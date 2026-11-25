#define main bundledRunnerMain
#import "main.m"
#undef main
#import <objc/runtime.h>
#include <sys/stat.h>

@interface WKWebView (RendererTerminationFixture)
- (void)_killWebContentProcessAndResetState;
@end

@interface WKPreferences (MediaFixture)
- (void)_setMediaDevicesEnabled:(BOOL)value;
- (void)_setMockCaptureDevicesEnabled:(BOOL)value;
- (void)_setMockCaptureDevicesPromptEnabled:(BOOL)value;
@end

@interface FixtureUploadPanel : NSOpenPanel
@property(nonatomic, copy) NSArray<NSURL *> *fixtureFiles;
@end

@implementation FixtureUploadPanel
- (NSArray<NSURL *> *)URLs { return self.fixtureFiles; }
- (void)beginSheetModalForWindow:(NSWindow *)window completionHandler:(void (^)(NSModalResponse))handler {
    NSCAssert(window.visible && self.allowsMultipleSelection, @"File selection was sent to the wrong window or input");
    dispatch_async(dispatch_get_main_queue(), ^{ handler(NSModalResponseOK); });
}
@end

@interface FixtureDownloadPanel : NSSavePanel
@property(nonatomic, copy) NSURL *destination;
@end

@implementation FixtureDownloadPanel
- (NSURL *)URL { return self.destination; }
- (void)beginSheetModalForWindow:(NSWindow *)window completionHandler:(void (^)(NSModalResponse))handler {
    NSCAssert(window.visible && [self.nameFieldStringValue isEqualToString:self.destination.lastPathComponent],
        @"The save panel got the wrong window or filename");
    dispatch_async(dispatch_get_main_queue(), ^{ handler(NSModalResponseOK); });
}
@end

@interface WebsiteDownloadsProbe : WebsiteDownloads
@property(nonatomic, copy) NSURL *fixtureFolder;
@property(nonatomic, copy) NSURL *fixtureDestination;
@property(nonatomic, copy) NSURL *stagedFile;
@end

@implementation WebsiteDownloadsProbe
- (NSSavePanel *)savePanel {
    if (self.fixtureDestination) {
        FixtureDownloadPanel *panel = [FixtureDownloadPanel new];
        panel.destination = self.fixtureDestination;
        return panel;
    }
    NSSavePanel *panel = [super savePanel];
    panel.directoryURL = self.fixtureFolder;
    return panel;
}
- (void)download:(WKDownload *)download decideDestinationUsingResponse:(NSURLResponse *)response
    suggestedFilename:(NSString *)filename completionHandler:(void (^)(NSURL *))complete {
    [super download:download decideDestinationUsingResponse:response suggestedFilename:filename completionHandler:^(NSURL *stage) {
        self.stagedFile = stage;
        complete(stage);
    }];
}
@end

@interface WebsiteAppProbe : WebsiteApp
@property(nonatomic, copy) NSArray<NSURL *> *fixtureFiles;
@property(nonatomic, strong) NSScreen *fixtureScreen;
@property(nonatomic, copy) WKPermissionDecision (^chooseMedia)(WKSecurityOrigin *, WKFrameInfo *, WKMediaCaptureType, WKPermissionDecision);
@property(nonatomic, strong) NSPasteboard *fixtureAddressBoard;
@property(nonatomic, copy) NSURL *browserAddress;
@property(nonatomic) BOOL cannotOpenBrowser;
@end

@implementation WebsiteAppProbe
- (NSPasteboard *)websiteAddressPasteboard { return self.fixtureAddressBoard; }

- (BOOL)openWebsiteURL:(NSURL *)url {
    self.browserAddress = url;
    return !self.cannotOpenBrowser;
}

- (NSOpenPanel *)uploadPanel {
    if (!self.fixtureFiles) return [super uploadPanel];
    FixtureUploadPanel *panel = [FixtureUploadPanel new];
    panel.fixtureFiles = self.fixtureFiles;
    return panel;
}

- (NSWindow *)windowForWebView:(WKWebView *)web title:(NSString *)title size:(NSSize)size {
    NSWindow *window = [super windowForWebView:web title:title size:size];
    if (self.fixtureScreen) {
        NSRect area = self.fixtureScreen.visibleFrame;
        NSSize frame = window.frame.size;
        [window setFrameOrigin:NSMakePoint(NSMidX(area) - frame.width / 2, NSMidY(area) - frame.height / 2)];
        NSCAssert(NSEqualRects(window.screen.frame, self.fixtureScreen.frame) && NSContainsRect(area, window.frame),
            @"The fixture window did not fit on %@. Select a larger display mode.", self.fixtureScreen.localizedName);
    }
    return window;
}

- (void)webView:(WKWebView *)web requestMediaCapturePermissionForOrigin:(WKSecurityOrigin *)origin
    initiatedByFrame:(WKFrameInfo *)frame type:(WKMediaCaptureType)type decisionHandler:(void (^)(WKPermissionDecision))decide {
    [super webView:web requestMediaCapturePermissionForOrigin:origin initiatedByFrame:frame type:type
        decisionHandler:^(WKPermissionDecision policy) {
            decide(self.chooseMedia ? self.chooseMedia(origin, frame, type, policy) : policy);
        }];
}
@end

static void waitFor(BOOL (^condition)(void), NSString *message) {
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:8];
    while (!condition() && deadline.timeIntervalSinceNow > 0) {
        NSEvent *event = [NSApp nextEventMatchingMask:NSEventMaskAny untilDate:[NSDate dateWithTimeIntervalSinceNow:0.05]
            inMode:NSDefaultRunLoopMode dequeue:YES];
        if (event) [NSApp sendEvent:event];
    }
    NSCAssert(condition(), @"%@", message);
}

@interface FixturePreferences : NSUserDefaults
@property(nonatomic) NSUInteger frameWrites;
@end

@implementation FixturePreferences
- (void)setObject:(id)value forKey:(NSString *)key {
    if ([key isEqualToString:windowFrameKey]) self.frameWrites++;
    [self registerDefaults:@{key: value}];
}

- (void)setInteger:(NSInteger)value forKey:(NSString *)key {
    [self setObject:@(value) forKey:key];
}
@end

static void checkWindowMoveWrites(WebsiteApp *app, NSWindow *window, FixturePreferences *preferences) {
    app.windowSettingsRevision = @"18";
    NSRect original = window.frame;
    CGFloat direction = NSMaxX(original) + 4 <= NSMaxX(window.screen.visibleFrame) ? 1 : -1;
    const NSUInteger moves = 60;
    for (NSUInteger i = 0; i < moves; i++) {
        [window setFrameOrigin:NSMakePoint(original.origin.x + direction * (1 + i % 4), original.origin.y)];
    }
    NSRect latest = window.frame;
    waitFor(^BOOL {
        NSDictionary *saved = [preferences dictionaryForKey:windowFrameKey];
        return [saved[@"revision"] isEqual:@"18"] && NSEqualRects(websiteSavedFrame(saved), latest);
    }, @"Moving the native window did not persist its final position");
    NSLog(@"%lu AppKit window moves updated window placement %lu times.", (unsigned long)moves,
        (unsigned long)preferences.frameWrites);
    NSCAssert(preferences.frameWrites == 1, @"A burst of window moves should save only the final placement");

    [window setFrameOrigin:original.origin];
    NSRect normal = window.frame;
    [app windowWillEnterFullScreen:[NSNotification notificationWithName:NSWindowWillEnterFullScreenNotification object:window]];
    [window setFrameOrigin:latest.origin];
    NSCAssert(preferences.frameWrites == 2 &&
        NSEqualRects(websiteSavedFrame([preferences dictionaryForKey:windowFrameKey]), normal),
        @"Entering fullscreen should save the last normal frame immediately");
    [window setFrameOrigin:normal.origin];
    [app windowDidExitFullScreen:[NSNotification notificationWithName:NSWindowDidExitFullScreenNotification object:window]];
    NSCAssert(preferences.frameWrites == 2 && !app.windowFrameTimer,
        @"Returning to the same frame should not leave a redundant save");
}

static id evaluate(WKWebView *web, NSString *script, void (^respond)(void)) {
    __block BOOL done = NO;
    __block id result;
    __block NSError *failure;
    [web evaluateJavaScript:script completionHandler:^(id value, NSError *error) {
        result = value;
        failure = error;
        done = YES;
    }];
    if (respond) respond();
    waitFor(^BOOL { return done; }, @"WebKit did not finish the script");
    NSCAssert(!failure, @"WebKit failed to run %@: %@", script, failure);
    return result;
}

static void checkMediaCapture(WebsiteAppProbe *app, NSURL *url, BOOL allow) {
    WKWebViewConfiguration *settings = [WKWebViewConfiguration new];
    settings.websiteDataStore = WKWebsiteDataStore.nonPersistentDataStore;
    [settings.preferences _setMediaDevicesEnabled:YES];
    [settings.preferences _setMockCaptureDevicesEnabled:YES];
    [settings.preferences _setMockCaptureDevicesPromptEnabled:YES];
    __block BOOL asked = NO;
    app.chooseMedia = ^WKPermissionDecision(WKSecurityOrigin *origin, WKFrameInfo *frame, WKMediaCaptureType type, WKPermissionDecision policy) {
        NSCAssert(!asked && frame.mainFrame && [origin.host isEqualToString:url.host] && origin.port == url.port.integerValue,
            @"WebKit reported the wrong media permission origin");
        NSCAssert(policy == WKPermissionDecisionPrompt && type == (allow ? WKMediaCaptureTypeCameraAndMicrophone : WKMediaCaptureTypeMicrophone),
            @"The visible website must ask for the requested media devices");
        asked = YES;
        return allow ? WKPermissionDecisionGrant : WKPermissionDecisionDeny;
    };
    WKWebView *web = [app webView:settings];
    NSWindow *window = [app windowForWebView:web title:@"Creator media permission fixture" size:NSMakeSize(720, 480)];
    [window makeKeyAndOrderFront:nil];
    [web loadRequest:[NSURLRequest requestWithURL:url]];
    waitFor(^BOOL { return !web.loading && [web.title isEqualToString:@"Test site"]; }, @"The media fixture did not load");
    NSString *request = [NSString stringWithFormat:@"navigator.mediaDevices.getUserMedia({audio:true, video:%@}).then(stream => {"
        "const tracks = stream.getTracks(); window.mediaResult = tracks.map(t => t.kind + ':' + t.readyState).sort().join(',');"
        "tracks.forEach(t => t.stop()); }, error => { window.mediaResult = error.name; }); true", allow ? @"true" : @"false"];
    evaluate(web, request, nil);
    __block NSString *result = nil;
    waitFor(^BOOL { result = evaluate(web, @"window.mediaResult", nil); return result != nil; }, @"WebKit did not finish the media request");
    NSCAssert(asked && [result isEqualToString:allow ? @"audio:live,video:live" : @"NotAllowedError"],
        @"The website did not receive the chosen capture result: %@", result);
    [window close];
    app.chooseMedia = nil;
}

static NSWindow *waitForSheet(NSWindow *window) {
    waitFor(^BOOL { return window.attachedSheet != nil; }, @"The web page did not open a native sheet");
    return window.attachedSheet;
}

static NSString *fileText(NSURL *file) {
    return [NSString stringWithContentsOfURL:file encoding:NSUTF8StringEncoding error:NULL];
}

static void seedFile(NSURL *file, NSString *contents) {
    NSCAssert([contents writeToURL:file atomically:YES encoding:NSUTF8StringEncoding error:NULL], @"Could not write %@", file);
}

static BOOL exists(NSURL *url) { return [NSFileManager.defaultManager fileExistsAtPath:url.path]; }

static NSURL *startHeldDownload(WebsiteDownloadsProbe *downloads, WKWebView *web, NSString *scenario) {
    downloads.stagedFile = nil;
    NSString *script = [NSString stringWithFormat:@"document.getElementById('attachment').href = '/attachment?mode=%@';"
        "document.getElementById('attachment').click()", scenario];
    evaluate(web, script, nil);
    waitFor(^BOOL { return downloads.stagedFile != nil; }, [@"WebKit did not start the selected download: " stringByAppendingString:scenario]);
    NSCAssert(![downloads.stagedFile isEqual:downloads.fixtureDestination] && exists(downloads.stagedFile.URLByDeletingLastPathComponent),
        @"A download needs its own temporary directory before writing the selected file");
    return downloads.stagedFile.URLByDeletingLastPathComponent;
}

static void releaseDownload(NSURL *website, NSString *scenario) {
    NSString *path = [@"/release-attachment/" stringByAppendingString:scenario];
    NSURL *release = [NSURL URLWithString:path relativeToURL:website].absoluteURL;
    NSCAssert([NSData dataWithContentsOfURL:release].length, @"Could not release the held response");
}

static NSString *textInView(NSView *view) {
    if ([view isKindOfClass:NSTextField.class]) return ((NSTextField *)view).stringValue;
    NSMutableArray<NSString *> *labels = [NSMutableArray array];
    for (NSView *child in view.subviews) [labels addObject:textInView(child)];
    return [labels componentsJoinedByString:@"\n"];
}

static void dismissDownloadFailure(NSWindow *window, NSURL *file, NSString *reason) {
    NSWindow *sheet = waitForSheet(window);
    NSString *text = textInView(sheet.contentView);
    NSCAssert([text containsString:@"下载未完成"] && [text containsString:file.lastPathComponent] && [text containsString:reason],
        @"The download sheet did not identify the file and failure: %@", text);
    [window endSheet:sheet returnCode:NSAlertFirstButtonReturn];
    waitFor(^BOOL { return window.attachedSheet == nil; }, @"The download alert stayed open after dismissal");
}

static void checkDownloads(WebsiteDownloadsProbe *downloads, NSWindow *window, WKWebView *web, NSURL *folder) {
    NSString *click = @"document.getElementById('attachment').click()";
    NSString *exported = @"Exported website file\n";
    NSString *streamed = [[@"" stringByPaddingToLength:32 * 1024 withString:@"x" startingAtIndex:0] stringByAppendingString:exported];
    NSString *original = @"Existing document\n";
    NSURL *saved = [folder URLByAppendingPathComponent:@"workbench.txt"];
    NSURL *address = web.URL;
    evaluate(web, click, ^{
        NSSavePanel *panel = (NSSavePanel *)waitForSheet(window);
        NSCAssert([panel isKindOfClass:NSSavePanel.class] && [panel.nameFieldStringValue isEqualToString:saved.lastPathComponent],
            @"The native save panel lost the suggested attachment name");
        [panel cancel:nil];
    });
    waitFor(^BOOL { return window.attachedSheet == nil; }, @"The native save panel stayed open after cancel");
    NSCAssert(!exists(saved), @"Canceling the attachment saved a file");

    // The save panel is hosted out of process. Supply its chosen URL while
    // leaving response policy, download bytes and file writes to WebKit.
    downloads.fixtureDestination = saved;
    evaluate(web, click, nil);
    waitFor(^BOOL { return [fileText(saved) isEqualToString:exported]; }, @"WebKit did not save the attachment to the chosen destination");

    seedFile(saved, original);
    NSURL *stage = startHeldDownload(downloads, web, @"replace");
    NSCAssert([fileText(saved) isEqualToString:original], @"The original changed before the transfer completed");
    releaseDownload(address, @"replace");
    waitFor(^BOOL { return [fileText(saved) isEqualToString:streamed] && !exists(stage); }, @"The approved replacement never completed");

    seedFile(saved, original);
    stage = startHeldDownload(downloads, web, @"interrupted");
    releaseDownload(address, @"interrupted");
    dismissDownloadFailure(window, saved, @"文件传输未完成");
    NSCAssert([fileText(saved) isEqualToString:original] && !exists(stage), @"An interrupted replacement changed the original or left temporary files");

    stage = startHeldDownload(downloads, web, @"changed");
    NSString *edited = @"User edited the file while downloading\n";
    seedFile(saved, edited);
    releaseDownload(address, @"changed");
    dismissDownloadFailure(window, saved, @"发生了变化");
    NSCAssert([fileText(saved) isEqualToString:edited] && !exists(stage), @"The download discarded another edit");

    NSFileManager *files = NSFileManager.defaultManager;
    NSURL *otherFolder = [folder URLByAppendingPathComponent:@"other" isDirectory:YES];
    NSCAssert([files createDirectoryAtURL:otherFolder withIntermediateDirectories:NO attributes:nil error:NULL], @"Could not create the second download folder");
    NSURL *fresh = [otherFolder URLByAppendingPathComponent:saved.lastPathComponent];
    downloads.fixtureDestination = fresh;
    stage = startHeldDownload(downloads, web, @"new-interrupted");
    releaseDownload(address, @"new-interrupted");
    dismissDownloadFailure(window, fresh, @"文件传输未完成");
    NSCAssert(!exists(fresh) && !exists(stage), @"An interrupted download left an incomplete file");

    stage = startHeldDownload(downloads, web, @"new-collision");
    seedFile(fresh, original);
    releaseDownload(address, @"new-collision");
    dismissDownloadFailure(window, fresh, @"发生了变化");
    NSCAssert([fileText(fresh) isEqualToString:original] && !exists(stage), @"The download replaced a newly created file");

    stage = startHeldDownload(downloads, web, @"retry");
    releaseDownload(address, @"retry");
    waitFor(^BOOL { return [fileText(fresh) isEqualToString:streamed] && !exists(stage); }, @"Retry did not replace the approved file");

    downloads.fixtureDestination = saved;
    NSURL *firstStage = startHeldDownload(downloads, web, @"parallel-first");
    downloads.fixtureDestination = fresh;
    seedFile(fresh, original);
    NSURL *secondStage = startHeldDownload(downloads, web, @"parallel-second");
    NSCAssert(![firstStage isEqual:secondStage], @"Two downloads shared a temporary directory");
    releaseDownload(address, @"parallel-second");
    waitFor(^BOOL { return [fileText(fresh) isEqualToString:streamed] && !exists(secondStage); }, @"The second download did not finish independently");
    NSCAssert([fileText(saved) isEqualToString:edited] && exists(firstStage), @"The second download published or removed the first download");
    releaseDownload(address, @"parallel-first");
    waitFor(^BOOL { return [fileText(saved) isEqualToString:streamed] && !exists(firstStage); }, @"The first download did not finish independently");

    seedFile(fresh, original);
    stage = startHeldDownload(downloads, web, @"quit");
    [downloads cancelPendingDownloads]; // The same cleanup runs when the app quits.
    releaseDownload(address, @"quit");
    NSCAssert([fileText(fresh) isEqualToString:original] && !exists(stage), @"Closing the runner lost the original or left staged data");

    evaluate(web, @"const a = document.createElement('a'); a.download = 'workbench.txt';"
        "a.href = URL.createObjectURL(new Blob(['Generated report\\n'], {type: 'text/plain'}));"
        "document.body.append(a); a.click()", nil);
    waitFor(^BOOL { return [fileText(fresh) isEqualToString:@"Generated report\n"]; }, @"The website's blob export did not save");

    NSURL *csv = [folder URLByAppendingPathComponent:@"report.csv"];
    downloads.fixtureDestination = csv;
    evaluate(web, @"{ const link = document.createElement('a'); link.download = 'report.csv';"
        "link.href = 'data:text/csv;charset=utf-8,' + encodeURIComponent('标题,数量\\n报告,3\\n');"
        "document.body.append(link); link.click(); link.remove(); }", nil);
    waitFor(^BOOL { return [fileText(csv) isEqualToString:@"标题,数量\n报告,3\n"]; }, @"The website's data URL export did not save");
    NSCAssert([web.URL isEqual:address] && web.window == window, @"Exporting a data URL changed the active page or window");

    NSURL *directoryTarget = [folder URLByAppendingPathComponent:@"directory/workbench.txt" isDirectory:YES];
    NSCAssert([files createDirectoryAtURL:directoryTarget withIntermediateDirectories:YES attributes:nil error:NULL],
        @"Could not create the directory with an attachment's name");
    NSURL *containedFile = [directoryTarget URLByAppendingPathComponent:@"kept.txt"];
    seedFile(containedFile, original);
    downloads.fixtureDestination = directoryTarget;
    evaluate(web, @"document.getElementById('attachment').href = '/attachment'; document.getElementById('attachment').click()", nil);
    dismissDownloadFailure(window, directoryTarget, @"无法在所选位置保存文件");
    NSCAssert([fileText(containedFile) isEqualToString:original], @"The download replaced a directory containing user data");
    waitFor(^BOOL { return !web.loading && [web.URL isEqual:address] && window.attachedSheet == nil; },
        @"WebKit did not return to the original page after the final download was dismissed");
    NSCAssert([evaluate(web, @"document.title", nil) isEqual:@"Test site"], @"Downloading a file replaced the original document");
    NSLog(@"Native downloads preserved destinations through replacement, connection loss, simultaneous edits, retry, parallel transfers and quit. Blob and data URL exports saved the exact contents.");
}

static BOOL performMenuKey(NSWindow *window, NSString *key, unsigned short code, NSEventModifierFlags modifiers) {
    NSEvent *event = [NSEvent keyEventWithType:NSEventTypeKeyDown location:NSZeroPoint modifierFlags:modifiers
        timestamp:0 windowNumber:window.windowNumber context:nil characters:key charactersIgnoringModifiers:key isARepeat:NO keyCode:code];
    return [NSApp.mainMenu performKeyEquivalent:event];
}

static void pressMenuKey(NSWindow *window, NSString *key, unsigned short code) {
    NSCAssert(performMenuKey(window, key, code, NSEventModifierFlagCommand), @"The native menu did not handle Command+%@", key);
}

static void pressHomeKey(NSWindow *window) {
    NSCAssert(performMenuKey(window, @"H", 4, NSEventModifierFlagCommand | NSEventModifierFlagShift),
        @"The native menu did not handle Shift+Command+H");
}

static NSMenuItem *websiteMenuItem(NSString *title) {
    for (NSMenuItem *item in NSApp.mainMenu.itemArray) {
        if ([item.submenu.title isEqualToString:@"显示"]) return [item.submenu itemWithTitle:title];
    }
    return nil;
}

static void chooseWebsiteMenuItem(NSString *title) {
    NSMenuItem *item = websiteMenuItem(title);
    NSCAssert(item, @"The native View menu is missing %@", title);
    [item.menu update];
    NSCAssert(item.enabled, @"The native View menu disabled %@", title);
    [item.menu performActionForItemAtIndex:[item.menu indexOfItem:item]];
}

static void checkAddressActions(WebsiteAppProbe *app, NSURL *expected) {
    [app.fixtureAddressBoard clearContents];
    app.browserAddress = nil;
    chooseWebsiteMenuItem(@"复制网页链接");
    NSString *copied = [app.fixtureAddressBoard stringForType:NSPasteboardTypeString];
    NSCAssert([copied isEqualToString:expected.absoluteString], @"The pasteboard got %@, want %@", copied, expected);
    chooseWebsiteMenuItem(@"在浏览器中打开");
    NSCAssert([app.browserAddress isEqual:expected], @"The browser action got %@, want %@", app.browserAddress, expected);
}

// Model the Dock's minimize transition, which may be unavailable in a test
// session, while retaining the real AppKit window and WebKit document.
@interface ReopenWindowFixture : NSWindow
@property(nonatomic) BOOL dockMinimized;
@end

@implementation ReopenWindowFixture
- (BOOL)isMiniaturized { return self.dockMinimized; }
- (void)miniaturize:(id)sender {
    self.dockMinimized = YES;
    [self orderOut:sender];
}
- (void)deminiaturize:(id)sender { self.dockMinimized = NO; }
@end

static void checkReopenedWindow(WebsiteApp *app, WKWebViewConfiguration *config) {
    WKWebView *web = [app webView:config];
    NSWindow *window = [[ReopenWindowFixture alloc] initWithContentRect:NSMakeRect(0, 0, 720, 480)
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskMiniaturizable backing:NSBackingStoreBuffered defer:NO];
    window.releasedWhenClosed = NO;
    window.contentView = [[WebsiteContent alloc] initWithWebView:web];
    app.mainWindow = window;
    [window orderFrontRegardless];
    [web loadHTMLString:@"<input id='draft'>" baseURL:nil];
    waitFor(^BOOL { return !web.loading; }, @"The test document did not finish loading");
    evaluate(web, @"document.getElementById('draft').value = 'unsaved notes'", nil);
    pressMenuKey(window, @"m", 46);
    NSCAssert(window.miniaturized && !window.visible, @"Command+M did not reach the window's minimize action");

    NSCAssert(![app applicationShouldHandleReopen:NSApp hasVisibleWindows:YES], @"A minimized window should be restored by Creator");
    waitFor(^BOOL { return window.visible && !window.miniaturized; }, @"Reopening did not restore the website window");
    NSCAssert(app.mainWindow == window && [evaluate(web, @"document.getElementById('draft').value", nil) isEqual:@"unsaved notes"],
        @"Reopening discarded the original window or its page state");
    NSCAssert([app applicationShouldHandleReopen:NSApp hasVisibleWindows:YES], @"Visible windows should use AppKit's normal activation");
    [window close];
    NSLog(@"With the Dock transition modeled, Command+M and reopen returned to the same WebKit window and draft.");
}

static void checkNavigation(WebsiteAppProbe *app, NSWindow *window, WKWebView *web) {
    NSURL *home = web.URL;
    NSRect frame = window.frame;
    evaluate(web, @"localStorage.setItem('home-session', 'present')", nil);
    evaluate(web, @"document.getElementById('navigation').click()", nil);
    waitFor(^BOOL { return !web.loading && [web.URL.path isEqualToString:@"/navigation"]; }, @"The link did not open");
    NSURL *target = web.URL;
    pressMenuKey(window, @"[", 33);
    waitFor(^BOOL { return !web.loading && [web.URL isEqual:home]; }, @"Command+[ did not go back");
    pressMenuKey(window, @"]", 30);
    waitFor(^BOOL { return !web.loading && [web.URL isEqual:target]; }, @"Command+] did not go forward");
    NSString *title = web.title;
    pressMenuKey(window, @"r", 15);
    waitFor(^BOOL {
        return !web.loading && [web.URL isEqual:target] && [web.title hasPrefix:@"Navigation "] && ![web.title isEqualToString:title];
    }, @"Command+R did not request and render the current URL again");
    evaluate(web, @"window.addressDraft = 'unsaved notes'", nil);
    checkAddressActions(app, target);
    app.cannotOpenBrowser = YES;
    chooseWebsiteMenuItem(@"在浏览器中打开");
    NSWindow *sheet = waitForSheet(window);
    NSCAssert([textInView(sheet.contentView) containsString:@"无法打开默认浏览器"], @"A failed browser launch did not explain what happened");
    [window endSheet:sheet returnCode:NSAlertFirstButtonReturn];
    waitFor(^BOOL { return window.attachedSheet == nil; }, @"The browser warning stayed open after dismissal");
    app.cannotOpenBrowser = NO;
    NSCAssert([evaluate(web, @"window.addressDraft", nil) isEqual:@"unsaved notes"] && [web.URL isEqual:target],
        @"Address actions changed the loaded page or its unsaved draft");
    chooseWebsiteMenuItem(@"回到首页");
    waitFor(^BOOL { return !web.loading && [web.URL isEqual:home] && [web.title isEqualToString:@"Test site"]; },
        @"The Home menu did not return to the saved path, query and fragment");
    NSCAssert(web.window == window && NSEqualRects(window.frame, frame) &&
        [evaluate(web, @"localStorage.getItem('home-session')", nil) isEqual:@"present"],
        @"Going home lost the native window or the website session");
    evaluate(web, @"window.homeDraft = 'unsaved home'", nil);
    pressHomeKey(window);
    waitFor(^BOOL { return !app.readingHome; }, @"The standalone homepage did not resolve");
    NSCAssert(!web.loading && [evaluate(web, @"window.homeDraft", nil) isEqual:@"unsaved home"],
        @"Going home while on the saved URL reloaded the page and its draft");
    pressMenuKey(window, @"[", 33);
    waitFor(^BOOL { return !web.loading && [web.URL isEqual:target]; }, @"Going home removed the previous page from history");
    pressMenuKey(window, @"[", 33);
    waitFor(^BOOL { return !web.loading && [web.URL isEqual:home]; }, @"Reload changed the back history");
}

static void checkHomeFromFailure(WebsiteAppProbe *app, NSWindow *window, WKWebView *web, NSURL *home) {
    WebsiteContent *content = (WebsiteContent *)window.contentView;
    NSURL *broken = [NSURL URLWithString:@"/recover/home?source=retry#notes" relativeToURL:home].absoluteURL;
    [web loadRequest:[NSURLRequest requestWithURL:broken]];
    waitFor(^BOOL { return [content.failedURL isEqual:broken] && web.hidden; }, @"Expected an interrupted secondary page");
    checkAddressActions(app, broken);
    NSCAssert(web.hidden && !web.loading, @"Sharing a failed address retried it in the embedded page");
    pressHomeKey(window);
    waitFor(^BOOL {
        return !web.loading && !web.hidden && content.failure.hidden && [web.URL isEqual:home] && [web.title isEqualToString:@"Test site"];
    }, @"The Home shortcut did not recover the saved page in the original window");
    NSLog(@"Home returned to the full saved URL from both a loaded page and a network failure, and kept the session, history and an existing home draft.");
    NSLog(@"The native address actions copied the full URL of the current page, active popup and failed navigation without changing the embedded page.");
}

static void writeFixtureHome(NSString *path, NSString *appID, NSString *address) {
    NSArray *profiles = @[@{@"id": @"app-1111111111111111", @"url": @"file:///other-fixture"}, @{@"id": appID, @"url": address}];
    NSData *data = [NSJSONSerialization dataWithJSONObject:profiles options:0 error:NULL];
    NSCAssert([data writeToFile:path options:NSDataWritingAtomic error:NULL], @"Cannot commit the fixture profile");
}

static void checkStartupSettings(NSString *directory, NSString *profilePath) {
    NSFileManager *files = NSFileManager.defaultManager;
    NSString *appID = @"app-0123456789abcdef";
    NSString *bundleID = @"com.webcrate.apps.app-0123456789abcdef";
    NSDictionary *saved = @{@"id": appID, @"name": @"项目工作台", @"url": @"https://example.org/saved?from=creator%20link#latest",
        @"width": @936, @"height": @648, @"windowRevision": @2};
    NSMutableDictionary *other = [saved mutableCopy];
    other[@"id"] = @"app-1111111111111111";
    other[@"url"] = @"https://example.org/other";
    NSData *profiles = [NSJSONSerialization dataWithJSONObject:@[other, saved] options:0 error:NULL];
    NSCAssert([profiles writeToFile:profilePath options:NSDataWritingAtomic error:NULL], @"Could not save startup profiles");
    NSString *resource = [directory stringByAppendingPathComponent:@"website.json"];
    NSCAssert([CreatorLoadWebsiteSettings(directory, bundleID) isEqual:saved], @"A missing config must use the app's saved settings");
    NSCAssert(CreatorLoadWebsiteSettings(directory, @"com.webcrate.apps.app-2222222222222222") == nil,
        @"The launcher must use the settings for its own bundle identifier");

    NSMutableDictionary *packaged = [saved mutableCopy];
    packaged[@"url"] = @"https://example.org/bundled";
    NSData *data = [NSJSONSerialization dataWithJSONObject:packaged options:0 error:NULL];
    NSMutableData *oversized = [data mutableCopy];
    NSString *padding = [@"" stringByPaddingToLength:16 * 1024 withString:@" " startingAtIndex:0];
    [oversized appendData:[padding dataUsingEncoding:NSUTF8StringEncoding]];
    NSDictionary *localFile = @{ @"id": appID, @"name": saved[@"name"], @"url": @"file:///fixture",
        @"width": saved[@"width"], @"height": saved[@"height"] };
    for (NSData *bad in @[oversized, [NSJSONSerialization dataWithJSONObject:other options:0 error:NULL],
        [NSJSONSerialization dataWithJSONObject:localFile options:0 error:NULL]]) {
        NSCAssert([bad writeToFile:resource atomically:YES], @"Could not put invalid settings in the fixture");
        NSCAssert([CreatorLoadWebsiteSettings(directory, bundleID) isEqual:saved], @"Invalid settings must yield to the committed profile");
    }
    NSCAssert([files removeItemAtPath:resource error:NULL] && mkfifo(resource.fileSystemRepresentation, 0600) == 0,
        @"Could not create the isolated configuration pipe");
    NSCAssert([CreatorLoadWebsiteSettings(directory, bundleID) isEqual:saved], @"A configuration pipe must not wait for a writer");
    NSCAssert([files removeItemAtPath:resource error:NULL], @"Could not remove the fixture pipe");

    NSCAssert([data writeToFile:resource atomically:YES], @"Could not write usable packaged settings");
    NSCAssert([CreatorLoadWebsiteSettings(directory, bundleID) isEqual:packaged], @"Usable packaged settings must take precedence");
    NSLog(@"Startup settings recovered the app's own saved URL and dimensions, rejecting mismatched, oversized and nonregular package files.");
}

static void checkLiveHome(WebsiteAppProbe *app, NSWindow *window, WKWebView *web, NSString *home) {
    NSCAssert([NSProcessInfo.processInfo.environment[@"HOME"] isEqualToString:home], @"The fixture home must be isolated");
    NSString *directory = [home stringByAppendingPathComponent:@"Library/Application Support/WebCrate"];
    NSFileManager *manager = NSFileManager.defaultManager;
    NSCAssert([manager createDirectoryAtPath:directory withIntermediateDirectories:YES attributes:nil error:NULL],
        @"Cannot create the fixture profile directory");
    NSString *path = [directory stringByAppendingPathComponent:@"apps.json"];
    checkStartupSettings(directory, path);
    app.profileID = @"app-0123456789abcdef";
    NSURL *current = web.URL;
    NSURL *destination = [NSURL URLWithString:@"/navigation?source=menu#review" relativeToURL:current].absoluteURL;
    evaluate(web, @"localStorage.setItem('live-home-session', 'retained'); window.liveHomeDraft = 'current page'", nil);
    writeFixtureHome(path, app.profileID, destination.absoluteString);
    NSCAssert([evaluate(web, @"window.liveHomeDraft", nil) isEqual:@"current page"] && [web.URL isEqual:current],
        @"Saving the profile navigated the running app");
    pressHomeKey(window);
    waitFor(^BOOL { return !web.loading && [web.URL isEqual:destination] && [web.title hasPrefix:@"Navigation "]; },
        @"The Home shortcut did not use the newly saved path, query and fragment");
    NSCAssert(web.window == window && [evaluate(web, @"localStorage.getItem('live-home-session')", nil) isEqual:@"retained"],
        @"Using the saved home replaced the native window or website session");

    evaluate(web, @"window.liveHomeDraft = 'new home'", nil);
    writeFixtureHome(path, app.profileID, @"file:///fixture-not-a-website");
    chooseWebsiteMenuItem(@"回到首页");
    waitFor(^BOOL { return !app.readingHome; }, @"The invalid saved address did not resolve promptly");
    NSCAssert([web.URL isEqual:destination] && !web.loading && [evaluate(web, @"window.liveHomeDraft", nil) isEqual:@"new home"],
        @"An invalid profile address discarded the last homepage or its draft");

    NSCAssert([manager removeItemAtPath:path error:NULL] && mkfifo(path.fileSystemRepresentation, 0600) == 0,
        @"Cannot simulate an unreadable profile");
    pressHomeKey(window);
    waitFor(^BOOL { return !app.readingHome; }, @"Reading a fixture pipe blocked the homepage command");
    NSCAssert([web.URL isEqual:destination] && [evaluate(web, @"window.liveHomeDraft", nil) isEqual:@"new home"],
        @"Losing the profile discarded the last homepage or its draft");
    NSLog(@"The live app used the committed homepage on demand, kept window and website state, and ignored invalid or unreadable profile data.");
}

static void checkZoom(WebsiteApp *app, NSWindow *window, WKWebView *web) {
    NSRect frame = window.frame;
    double width = [evaluate(web, @"innerWidth", nil) doubleValue];
    NSURL *url = web.URL;
    const struct { NSString *__unsafe_unretained key; unsigned short code; double factor; } steps[] = {
        {@"=", 24, 1.10}, {@"=", 24, 1.25}, {@"-", 27, 1.10}, {@"0", 29, 1.0}, {@"-", 27, 0.90}, {@"0", 29, 1.0},
    };
    for (NSUInteger i = 0; i < sizeof(steps) / sizeof(steps[0]); ++i) {
        double factor = steps[i].factor;
        pressMenuKey(window, steps[i].key, steps[i].code);
        NSCAssert(fabs(web.pageZoom - factor) < 0.001, @"The shortcut did not change WebKit zoom to %.0f%%", factor * 100);
        waitFor(^BOOL {
            return fabs([evaluate(web, @"innerWidth", nil) doubleValue] - width / factor) < 2;
        }, @"Changing the zoom did not reflow the webpage");
        NSCAssert(NSEqualRects(frame, window.frame) && [web.URL isEqual:url], @"Zoom changed the window frame or website");
    }
    pressMenuKey(window, @"=", 24);
    WebsiteApp *reopened = [WebsiteApp new];
    reopened.preferences = app.preferences;
    NSCAssert([reopened webView:web.configuration].pageZoom == 1.10, @"A new website view lost the saved zoom");
    [app.preferences setInteger:120 forKey:pageZoomKey];
    NSCAssert([reopened webView:web.configuration].pageZoom == 1, @"Unknown zoom percentages should open at actual size");
    pressMenuKey(window, @"0", 29);
    NSCAssert([app.preferences integerForKey:pageZoomKey] == 100 && web.pageZoom == 1, @"Reset did not persist the actual size");
    NSLog(@"Native zoom reflowed the webpage, kept the window size and persisted the zoom and reset settings.");
}

static NSScreen *fullscreenFixtureScreen;

static NSScreen *fullscreenMainScreen(id receiver, SEL selector) {
    return fullscreenFixtureScreen;
}

static void checkFullscreen(WebsiteAppProbe *app, NSWindow *window, WKWebView *web, BOOL exitFromPage) {
    if (@available(macOS 13.0, *)) {
        [window makeKeyAndOrderFront:nil];
        if (@available(macOS 14.0, *)) [NSApp activate];
        else [NSApp activateIgnoringOtherApps:YES];
        NSScreen *screen = window.screen;
        NSCAssert(screen, @"The original window is missing a display");
        waitFor(^BOOL { return [evaluate(web, @"document.visibilityState", nil) isEqual:@"visible"]; },
            @"The website was not visible before requesting fullscreen");
        NSRect frame = window.frame;
        NSSize viewport = web.bounds.size;
        NSView *container = web.superview;
        NSURL *url = web.URL;
        Method mainScreen = class_getClassMethod(NSScreen.class, @selector(mainScreen));
        IMP original = NULL;
        if (app.fixtureScreen) {
            // WebKit uses AppKit's main screen. Simulate focus on DeskPad in
            // this process so fullscreen cannot take over the user's screen.
            fullscreenFixtureScreen = app.fixtureScreen;
            original = method_setImplementation(mainScreen, (IMP)fullscreenMainScreen);
        }
        @try {
            evaluate(web, @"window.unsavedNote = 'keep this note'; window.fullscreenResult = '';"
                "const player = document.createElement('div'); player.id = 'test-player';"
                "player.style = 'width: 300px; height: 150px; background: #121212'; document.body.append(player);"
                "player.requestFullscreen().catch(error => { window.fullscreenResult = error.name + ': ' + error.message; }); true", nil);
            waitFor(^BOOL {
                return web.fullscreenState == WKFullscreenStateInFullscreen ||
                    [evaluate(web, @"window.fullscreenResult", nil) length] > 0;
            }, @"WebKit did not settle the fullscreen request");
        } @finally {
            if (original) method_setImplementation(mainScreen, original);
            fullscreenFixtureScreen = nil;
        }
        NSCAssert(web.fullscreenState == WKFullscreenStateInFullscreen,
            @"The website could not enter fullscreen: %@", evaluate(web, @"window.fullscreenResult", nil));
        waitFor(^BOOL {
            return [evaluate(web, @"document.fullscreenElement?.id === 'test-player' && "
                "innerWidth >= screen.width - 1 && innerHeight >= screen.height - 1", nil) boolValue];
        }, @"The web player did not fill the screen");
        waitFor(^BOOL { return NSEqualRects(web.window.frame, screen.frame); },
            [NSString stringWithFormat:@"Fullscreen frame %@ did not fill %@ at %@", NSStringFromRect(web.window.frame),
                screen.localizedName, NSStringFromRect(screen.frame)]);
        NSCAssert([app contentForWebView:web] == container, @"The fullscreen web view lost its original content container");

        if (exitFromPage) {
            evaluate(web, @"document.exitFullscreen(); true", nil);
        } else {
            NSEvent *escape = [NSEvent keyEventWithType:NSEventTypeKeyDown location:NSZeroPoint modifierFlags:0
                timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:web.window.windowNumber context:nil
                characters:@"\x1b" charactersIgnoringModifiers:@"\x1b" isARepeat:NO keyCode:53];
            [web.window sendEvent:escape];
        }
        waitFor(^BOOL { return web.fullscreenState == WKFullscreenStateNotInFullscreen && web.superview == container; },
            @"Exiting fullscreen did not return the website to its original window");
        waitFor(^BOOL {
            return [evaluate(web, @"document.fullscreenElement === null && window.unsavedNote === 'keep this note'", nil) boolValue];
        }, @"The original document did not survive leaving full screen");
        waitFor(^BOOL { return NSApp.keyWindow == window; }, @"The original window did not regain keyboard focus after fullscreen");
        NSCAssert(web.window == window && NSEqualRects(window.frame, frame) && NSEqualSizes(web.bounds.size, viewport) &&
            [web.URL isEqual:url], @"Leaving full screen changed the window, viewport or URL");
        NSLog(@"WebKit element fullscreen filled the display. %@ restored the original window and page state.",
            exitFromPage ? @"The website's exit control" : @"Escape");
    }
}

static void checkRecovery(NSWindow *window, WKWebView *web, NSURL *base, NSString *scenario) {
    NSString *path = [NSString stringWithFormat:@"/recover/%@?source=retry", scenario];
    NSURL *destination = [NSURL URLWithString:path relativeToURL:base].absoluteURL;
    WebsiteContent *content = (WebsiteContent *)window.contentView;
    BOOL followsLink = [scenario isEqualToString:@"link"];
    if (followsLink) {
        evaluate(web, @"localStorage.setItem('recovery-session', 'present'); document.getElementById('recover-link').click()", nil);
    } else {
        [content loadInitialURL:destination];
    }
    waitFor(^BOOL { return content.failedURL && !content.failure.hidden; }, @"A transport failure did not get a recovery view");
    NSCAssert([content.failedURL isEqual:destination], @"The failure lost the requested address");
    NSCAssert(!window.attachedSheet && web.hidden && window.visible && content.retryButton.enabled &&
        !content.openingTimer && !window.titlebarAccessoryViewControllers.count,
        @"Retry must be available within the original window");
    NSCAssert([content.failedHost.stringValue isEqualToString:destination.host], @"The failure view showed the wrong host");
    NSURL *restore = [NSURL URLWithString:[@"/restore/" stringByAppendingString:scenario] relativeToURL:base].absoluteURL;
    NSCAssert([NSData dataWithContentsOfURL:restore].length, @"Could not restore the test website");
    [content.retryButton performClick:nil];
    waitFor(^BOOL {
        return !web.loading && !web.hidden && content.failure.hidden && [web.URL isEqual:destination] &&
            [web.title isEqualToString:@"Recovered site"];
    },
        @"Retry did not open the failed destination after the network recovered");
    if (followsLink) {
        NSCAssert([evaluate(web, @"localStorage.getItem('recovery-session')", nil) isEqual:@"present"],
            @"Retry lost the existing website session");
    }
}

static void checkRendererRecovery(NSWindow *window, WKWebView *web) {
    WebsiteContent *content = (WebsiteContent *)window.contentView;
    NSRect frame = window.frame;
    evaluate(web, @"history.replaceState(null, '', '#current'); window.temporaryDraft = 'discarded on crash'", nil);
    waitFor(^BOOL { return [web.URL.fragment isEqualToString:@"current"]; }, @"WebKit did not retain the current fragment");
    NSURL *destination = web.URL;
    NSURL *previous = web.backForwardList.backItem.URL;
    NSCAssert(previous, @"The rendering fixture needs a page in the back history");

    // WebKit's test hook affects only this fixture's actual content process.
    [web _killWebContentProcessAndResetState];
    waitFor(^BOOL { return content.failedURL && !content.failure.hidden; }, @"A stopped content process left the website without a recovery control");
    NSCAssert([content.failedURL isEqual:destination] && content.retryButton.enabled && web.hidden,
        @"Renderer recovery lost the current address or retry control");
    [content.retryButton performClick:nil];
    waitFor(^BOOL {
        return !web.loading && !web.hidden && content.failure.hidden && [web.URL isEqual:destination] &&
            [web.title isEqualToString:@"Recovered site"];
    }, @"Retry after terminating the renderer did not reopen the exact page");
    NSCAssert([evaluate(web, @"localStorage.getItem('recovery-session') === 'present' && window.temporaryDraft === undefined", nil) boolValue],
        @"Renderer recovery lost the saved website session or did not reload the document");
    NSCAssert([web.backForwardList.backItem.URL isEqual:previous], @"Recovering added a duplicate entry to the browser history");
    NSCAssert(web.window == window && NSEqualRects(window.frame, frame), @"Renderer recovery changed the native window");
    NSLog(@"Retry after terminating WebKit's content process restored the full address and saved session in the same window.");
}

static NSRect checkChrome(NSWindow *window, WKWebView *web, NSSize size) {
    NSCAssert([window.title isEqualToString:@"Test site"], @"The window menu needs the app name");
    NSCAssert(window.titleVisibility == NSWindowTitleHidden, @"The title must be hidden in the window");
    NSCAssert(window.titlebarAppearsTransparent && window.titlebarSeparatorStyle == NSTitlebarSeparatorStyleNone,
        @"Chrome must share the web view's background");
    NSCAssert((window.styleMask & NSWindowStyleMaskResizable) != 0, @"The native resize edges must be kept");
    NSRect content = [web convertRect:web.bounds toView:nil];
    NSCAssert(NSEqualSizes(content.size, size), @"Website viewport must match the requested content size");
    for (NSNumber *kind in @[@(NSWindowCloseButton), @(NSWindowMiniaturizeButton), @(NSWindowZoomButton)]) {
        NSButton *button = [window standardWindowButton:kind.unsignedIntegerValue];
        NSCAssert(button && !button.hidden, @"Missing native window button");
        NSRect buttonRect = [button convertRect:button.bounds toView:nil];
        NSCAssert(!NSIntersectsRect(content, buttonRect), @"Native buttons must not consume web view input");
    }
    return content;
}

static NSTextField *inputInView(NSView *view) {
    if ([view isKindOfClass:NSTextField.class] && ((NSTextField *)view).editable) return (NSTextField *)view;
    for (NSView *child in view.subviews) {
        NSTextField *field = inputInView(child);
        if (field) return field;
    }
    return nil;
}

static void checkFilePickers(NSWindow *window, WKWebView *web) {
    evaluate(web, @"document.getElementById('files').click()", ^{
        NSOpenPanel *panel = (NSOpenPanel *)waitForSheet(window);
        NSCAssert([panel isKindOfClass:NSOpenPanel.class] && panel.canChooseFiles && !panel.canChooseDirectories &&
            panel.allowsMultipleSelection, @"The multiple-file control has the wrong picker");
        NSCAssert([panel.message containsString:web.URL.host], @"File picker did not identify the website");
        [panel cancel:nil];
    });
    waitFor(^BOOL { return window.attachedSheet == nil; }, @"File picker stayed open after cancel");
    NSCAssert([evaluate(web, @"document.getElementById('files').files.length === 0", nil) boolValue],
        @"Canceling selected a file");

    evaluate(web, @"document.getElementById('directory').click()", ^{
        NSOpenPanel *panel = (NSOpenPanel *)waitForSheet(window);
        NSCAssert([panel isKindOfClass:NSOpenPanel.class] && panel.canChooseDirectories && !panel.canChooseFiles,
            @"The directory control has the wrong picker");
        [panel cancel:nil];
    });
    waitFor(^BOOL { return window.attachedSheet == nil; }, @"Directory picker stayed open after cancel");
}

static void checkSelectedFiles(WebsiteAppProbe *app, WKWebView *web, NSURL *directory) {
    // Substitute only the system's file selection. The production adapter and
    // WebKit still deliver the selected local files to the page's file input.
    app.fixtureFiles = @[[directory URLByAppendingPathComponent:@"one.txt"], [directory URLByAppendingPathComponent:@"two.txt"]];
    __block id result;
    __block NSError *failure;
    __block BOOL done = NO;
    [web callAsyncJavaScript:@"const input = document.getElementById('files');"
        "const chosen = new Promise(resolve => input.addEventListener('change', () => resolve(Array.from(input.files)), {once:true}));"
        "input.click(); const files = await chosen; return await Promise.all(files.map(async file => file.name + '=' + await file.text()));"
        arguments:@{} inFrame:nil inContentWorld:WKContentWorld.pageWorld completionHandler:^(id value, NSError *error) {
            result = value;
            failure = error;
            done = YES;
        }];
    waitFor(^BOOL { return done; }, @"WebKit did not receive the selected files");
    NSArray<NSString *> *expected = @[@"one.txt=first", @"two.txt=second"];
    NSCAssert(!failure && [result isEqual:expected], @"Wrong website selection: %@ %@", result, failure);
    app.fixtureFiles = nil;
}

static void checkDialogs(NSWindow *window, WKWebView *web) {
    id alerted = evaluate(web, @"alert('File ready'); true", ^{
        [window endSheet:waitForSheet(window) returnCode:NSAlertFirstButtonReturn];
    });
    NSCAssert([alerted boolValue], @"Alert did not return control to the website");

    for (NSNumber *accept in @[@YES, @NO]) {
        id result = evaluate(web, @"confirm('Apply changes?')", ^{
            [window endSheet:waitForSheet(window) returnCode:accept.boolValue ? NSAlertFirstButtonReturn : NSAlertSecondButtonReturn];
        });
        NSCAssert([accept isEqual:result], @"WebKit did not receive the chosen confirmation result");
    }

    id entered = evaluate(web, @"prompt('Rename draft', 'Untitled')", ^{
        NSWindow *sheet = waitForSheet(window);
        NSTextField *field = inputInView(sheet.contentView);
        NSCAssert([field.stringValue isEqualToString:@"Untitled"], @"Prompt lost its default value");
        field.stringValue = @"练习稿";
        [window endSheet:sheet returnCode:NSAlertFirstButtonReturn];
    });
    NSCAssert([entered isEqual:@"练习稿"], @"WebKit did not receive the edited text");

    id canceled = evaluate(web, @"prompt('Rename draft') === null", ^{
        [window endSheet:waitForSheet(window) returnCode:NSAlertSecondButtonReturn];
    });
    NSCAssert([canceled boolValue], @"Canceling the prompt returned text to the website");
}

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        NSCAssert(argc == 4, @"Expected the loopback website address, upload fixtures and temporary home");
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        [NSApp finishLaunching];
        WebsiteAppProbe *app = [WebsiteAppProbe new];
        app.fixtureAddressBoard = [NSPasteboard pasteboardWithUniqueName];
        NSString *targetDisplay = NSProcessInfo.processInfo.environment[@"WEBCRATE_MAC_TEST_DISPLAY"];
        if (targetDisplay.length) {
            for (NSScreen *screen in NSScreen.screens) {
                if (![screen.localizedName isEqualToString:targetDisplay]) continue;
                NSCAssert(app.fixtureScreen == nil, @"More than one display is named %@", targetDisplay);
                app.fixtureScreen = screen;
            }
            if (!app.fixtureScreen) {
                NSLog(@"Test display %@ is unavailable. Open DeskPad and check its display name.", targetDisplay);
                return EXIT_FAILURE;
            }
            NSLog(@"Testing native windows on %@", app.fixtureScreen.localizedName);
        }
        NSString *zoomSuite = [@"org.creator.zoom-test." stringByAppendingString:NSUUID.UUID.UUIDString];
        FixturePreferences *preferences = [[FixturePreferences alloc] initWithSuiteName:zoomSuite];
        app.preferences = preferences;
        NSRect smallArea = NSMakeRect(100, 200, 800, 600);
        NSRect oversized = NSMakeRect(-200, 100, 1200, 900);
        NSCAssert(NSEqualRects(websiteFitFrame(oversized, smallArea, NSMakeSize(640, 510)), smallArea),
            @"A saved window larger than its display should fit the usable area");
        NSCAssert(NSIsEmptyRect(websiteSavedFrame(@{@"frame": @12})), @"Invalid saved dimensions should be ignored");
        NSCAssert(!websiteScreenForFrame(NSMakeRect(-90000, -90000, 680, 520)),
            @"A saved window on a disconnected display should fall back to the main display");
        NSCAssert(isHomeURL([NSURL URLWithString:@"https://example.org/#top"], [NSURL URLWithString:@"https://example.org:443#top"]),
            @"The browser's root slash and omitted default port still identify the saved home page");
        WebsiteDownloadsProbe *downloads = [WebsiteDownloadsProbe new];
        app.downloads = downloads;
        app.popups = [NSMapTable strongToStrongObjectsMapTable];
        WKWebViewConfiguration *config = [WKWebViewConfiguration new];
        config.websiteDataStore = WKWebsiteDataStore.nonPersistentDataStore;
        WKWebView *web = [app webView:config];
        NSSize size = NSMakeSize(720, 480);
        NSWindow *window = [app windowForWebView:web title:@"Test site" size:size];
        app.mainWindow = window;
        NSURL *url = [NSURL URLWithString:[NSString stringWithFormat:@"%s/workspace?source=creator#latest", argv[1]]];
        app.startURL = url;
        [app installMenus:@"Test site"];
        [window orderFrontRegardless];
        checkWindowMoveWrites(app, window, preferences);
        NSRect content = checkChrome(window, web, size);
        NSView *container = web.superview;
        NSPoint top = NSMakePoint(NSMinX(content) + 20, NSMaxY(content) - 16);
        NSView *parent = container.superview;
        NSView *hit = [container hitTest:parent ? [parent convertPoint:top fromView:nil] : top];
        NSCAssert(hit == web || [hit isDescendantOf:web], @"A point at the top of the website hit %@", hit);

        WebsiteContent *website = (WebsiteContent *)container;
        NSRect originalFrame = window.frame;
        [website loadInitialURL:url];
        NSCAssert(!window.titlebarAccessoryViewControllers.count, @"A new window should wait before showing the opening hint");
        waitFor(^BOOL {
            return [textInView(website.openingStatus.view) isEqualToString:@"正在载入…"];
        }, @"The held first page did not get a loading hint");
        NSView *hint = website.openingStatus.view;
        NSRect hintRect = [hint convertRect:hint.bounds toView:nil];
        NSCAssert(hint.window == window && !NSIntersectsRect(content, hintRect) &&
            NSEqualRects(window.frame, originalFrame) && NSEqualRects(checkChrome(window, web, size), content),
            @"The opening hint moved the window or occupied the website viewport");
        NSURL *release = [NSURL URLWithString:@"/release-opening" relativeToURL:url].absoluteURL;
        NSCAssert([NSData dataWithContentsOfURL:release].length, @"Could not release the opening response");
        waitFor(^BOOL {
            NSColor *fill = [window.backgroundColor colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
            return !web.loading && web.URL && fill && fabs(fill.redComponent - 0.8) < 0.01 && fabs(fill.greenComponent - 0.9) < 0.01 &&
                fabs(fill.blueComponent - 0.8) < 0.01;
        }, @"Window background did not follow the website");
        NSCAssert(!website.openingTimer && !window.titlebarAccessoryViewControllers.count &&
            NSEqualRects(checkChrome(window, web, size), content), @"The opening hint did not leave the original viewport");
        NSLog(@"The loading hint stayed in the title bar until the first page loaded, with a stable native window and viewport.");
        checkFullscreen(app, window, web, NO);
        checkZoom(app, window, web);
        checkFilePickers(window, web);
        NSURL *files = [NSURL fileURLWithPath:[NSString stringWithUTF8String:argv[2]] isDirectory:YES];
        checkSelectedFiles(app, web, files);
        downloads.fixtureFolder = files;
        checkDownloads(downloads, window, web, files);
        checkNavigation(app, window, web);
        checkDialogs(window, web);
        checkMediaCapture(app, url, NO);
        checkMediaCapture(app, url, YES);
        [window makeKeyAndOrderFront:nil];
        NSLog(@"Fake microphone refusal and camera/microphone approval reached the website through the real WebKit permission delegate.");

        id opened = evaluate(web, @"const popup = window.open('about:blank'); if (popup) popup.location = '/popup'; !!popup", nil);
        NSCAssert([opened boolValue], @"The website could not open a new window");
        waitFor(^BOOL { return app.popups.count == 1; }, @"WebKit did not open an app window for the popup");
        WKWebView *popup = app.popups.keyEnumerator.nextObject;
        NSWindow *popupWindow = [app.popups objectForKey:popup];
        checkChrome(popupWindow, popup, NSMakeSize(880, 650));
        waitFor(^BOOL { return !popup.loading && [popup.URL.path isEqualToString:@"/popup"]; }, @"Popup did not finish navigating");
        checkFullscreen(app, popupWindow, popup, YES);
        [popupWindow makeKeyAndOrderFront:nil];
        NSMenuItem *homeItem = websiteMenuItem(@"回到首页");
        [homeItem.menu update];
        NSCAssert(!homeItem.enabled, @"The native Home command should be unavailable in a sign-in popup");
        NSCAssert([evaluate(popup, @"window.opener.document.title", nil) isEqual:@"Test site"],
            @"The popup lost its relationship to the opener");
        checkAddressActions(app, popup.URL);
        [popup evaluateJavaScript:@"window.close()" completionHandler:nil];
        waitFor(^BOOL { return app.popups.count == 0; }, @"Window stayed open after the website closed it");
        NSCAssert(window.visible, @"Closing a popup also closed the main window");
        [window makeKeyAndOrderFront:nil];
        [homeItem.menu update];
        checkHomeFromFailure(app, window, web, url);
        checkRecovery(window, web, url, @"link");
        checkRendererRecovery(window, web);
        checkLiveHome(app, window, web, [NSString stringWithUTF8String:argv[3]]);
        checkChrome(window, web, size);
        NSUInteger writesBeforeClosing = preferences.frameWrites;
        [window setFrameOrigin:NSMakePoint(NSMinX(window.frame) + 1, NSMinY(window.frame))];
        NSRect frameAtClose = window.frame;
        [window close];
        NSCAssert(preferences.frameWrites == writesBeforeClosing + 1 && !app.windowFrameTimer &&
            NSEqualRects(websiteSavedFrame([preferences dictionaryForKey:windowFrameKey]), frameAtClose),
            @"Closing immediately after a move should persist the new frame");

        WKWebView *offline = [app webView:config];
        NSWindow *offlineWindow = [app windowForWebView:offline title:@"Test site" size:size];
        app.mainWindow = offlineWindow;
        [offlineWindow orderFrontRegardless];
        checkRecovery(offlineWindow, offline, url, @"startup");
        checkChrome(offlineWindow, offline, size);
        [offlineWindow close];
        checkReopenedWindow(app, config);
        [app.fixtureAddressBoard releaseGlobally];
    }
    return 0;
}
