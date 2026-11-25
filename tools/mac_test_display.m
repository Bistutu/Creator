#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>

static NSScreen *findScreen(NSString *name) {
    NSScreen *match = nil;
    for (NSScreen *screen in NSScreen.screens) {
        if (![screen.localizedName isEqualToString:name]) continue;
        if (match) return nil;
        match = screen;
    }
    return match;
}

static CGDirectDisplayID screenID(NSScreen *screen) {
    return [screen.deviceDescription[@"NSScreenNumber"] unsignedIntValue];
}

static BOOL fitsTestWindows(CGDisplayModeRef mode) {
    return CGDisplayModeGetWidth(mode) >= 1280 && CGDisplayModeGetHeight(mode) >= 800 &&
        CGDisplayModeIsUsableForDesktopGUI(mode);
}

static size_t framePixels(CGDisplayModeRef mode) {
    return CGDisplayModeGetPixelWidth(mode) * CGDisplayModeGetPixelHeight(mode);
}

static CGError lowerDeskPadMode(CGDirectDisplayID display, CGDisplayModeRef current) {
    NSDictionary *options = @{(__bridge NSString *)kCGDisplayShowDuplicateLowResolutionModes: @YES};
    NSArray *modes = CFBridgingRelease(CGDisplayCopyAllDisplayModes(display, (__bridge CFDictionaryRef)options));
    CGDisplayModeRef best = fitsTestWindows(current) ? current : NULL;
    for (id option in modes) {
        CGDisplayModeRef mode = (__bridge CGDisplayModeRef)option;
        if (fitsTestWindows(mode) && (!best || framePixels(mode) < framePixels(best))) best = mode;
    }
    if (!best) return kCGErrorNoneAvailable;
    if (best == current) return kCGErrorSuccess;

    CGDisplayConfigRef config;
    CGError result = CGBeginDisplayConfiguration(&config);
    if (result != kCGErrorSuccess) return result;
    result = CGConfigureDisplayWithDisplayMode(config, display, best, NULL);
    if (result != kCGErrorSuccess) {
        CGCancelDisplayConfiguration(config);
        return result;
    }
    // Keep the smaller mode across checks during the current macOS session.
    return CGCompleteDisplayConfiguration(config, kCGConfigureForSession);
}

static int prepareDeskPad(NSString *name, BOOL started) {
    NSScreen *screen = nil;
    for (int attempt = 0; attempt < 35 && !screen; attempt++) {
        screen = findScreen(name);
        if (!screen) {
            NSDate *retry = [NSDate dateWithTimeIntervalSinceNow:0.15];
            [NSRunLoop.currentRunLoop runUntilDate:retry];
            [NSThread sleepUntilDate:retry];
        }
    }
    if (!screen) {
        fprintf(stderr, "DeskPad display %s is unavailable or ambiguous.\n", name.UTF8String);
        return EXIT_FAILURE;
    }
    CGDirectDisplayID display = screenID(screen);
    // Match the virtual display descriptor published by DeskPad. Never apply
    // a mode that could change a physical or mirrored screen as a side effect.
    if (!display || CGDisplayIsBuiltin(display) || CGDisplayIsInMirrorSet(display) ||
        CGDisplayVendorNumber(display) != 0x3456 || CGDisplayModelNumber(display) != 0x1234 ||
        CGDisplaySerialNumber(display) != 1) {
        fprintf(stderr, "Expected a DeskPad virtual display outside a mirror set.\n");
        return EXIT_FAILURE;
    }
    BOOL isMain = CGDisplayIsMain(display);
    if (isMain) {
        // With no other online screen, a headless Mac can use DeskPad as
        // its primary screen. Verify that case and keep its current mode.
        CGDirectDisplayID online[2];
        uint32_t count = 0;
        if (CGGetOnlineDisplayList(2, online, &count) != kCGErrorSuccess || count != 1 || online[0] != display) {
            fprintf(stderr, "Expected DeskPad as an extended display or the only online display.\n");
            return EXIT_FAILURE;
        }
    }
    CGDisplayModeRef original = CGDisplayCopyDisplayMode(display);
    if (!original) return EXIT_FAILURE;
    size_t width = CGDisplayModeGetPixelWidth(original), height = CGDisplayModeGetPixelHeight(original);
    CGError result = started && !isMain ? lowerDeskPadMode(display, original) : kCGErrorSuccess;
    CGDisplayModeRelease(original);
    if (result != kCGErrorSuccess) {
        fprintf(stderr, "Using the current DeskPad mode (Core Graphics error %d).\n", result);
    }
    CGDisplayModeRef actual = CGDisplayCopyDisplayMode(display);
    if (!actual) return EXIT_FAILURE;
    printf("%s %u: %zux%zu -> %zux%zu pixels (%s)\n", name.UTF8String, display, width, height,
        CGDisplayModeGetPixelWidth(actual), CGDisplayModeGetPixelHeight(actual), started ? "started" : "reused");
    CGDisplayModeRelease(actual);
    return EXIT_SUCCESS;
}

static NSRect centeredFrame(NSRect area, NSSize size) {
    return NSMakeRect(round(NSMidX(area) - size.width / 2), round(NSMidY(area) - size.height / 2), size.width, size.height);
}

static BOOL checkWindow(pid_t pid, NSScreen *screen) {
    CGRect display = CGDisplayBounds(screenID(screen));
    NSArray<NSDictionary *> *windows = CFBridgingRelease(CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
    BOOL found = NO;
    for (NSDictionary *window in windows) {
        if ([window[(id)kCGWindowOwnerPID] intValue] != pid ||
            [window[(id)kCGWindowLayer] intValue] != CGWindowLevelForKey(kCGNormalWindowLevelKey)) continue;
        CGRect bounds;
        if (!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)window[(id)kCGWindowBounds], &bounds) ||
            bounds.size.width < 100 || bounds.size.height < 60) continue;
        if (!CGRectContainsRect(display, bounds)) {
            fprintf(stderr, "Window %s is outside %s\n", NSStringFromRect(bounds).UTF8String, screen.localizedName.UTF8String);
            exit(EXIT_FAILURE);
        }
        found = YES;
    }
    return found;
}

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        if (argc < 3) return EXIT_FAILURE;
        NSString *mode = @(argv[1]);
        NSString *name = @(argv[2]);
        if (argc == 3 && [mode isEqualToString:@"deskpad"]) return prepareDeskPad(name, YES);
        if (argc == 3 && [mode isEqualToString:@"reuse-deskpad"]) return prepareDeskPad(name, NO);
        NSScreen *screen = findScreen(name);
        if (!screen) {
            fprintf(stderr, "Test display %s is unavailable or ambiguous. Check DeskPad before starting a native test.\n", name.UTF8String);
            return EXIT_FAILURE;
        }
        if ([mode isEqualToString:@"verify"] && argc == 4) return checkWindow(atoi(argv[3]), screen) ? EXIT_SUCCESS : 2;
        if (argc != 5) return EXIT_FAILURE;
        NSSize size = NSMakeSize(atoi(argv[3]), atoi(argv[4]));
        NSRect usable = screen.visibleFrame;
        NSRect frame = centeredFrame(usable, size);
        if ([mode isEqualToString:@"creator"] && NSContainsRect(usable, frame)) {
            // MyGo uses top-left screen coordinates, like Quartz.
            printf("{\"portrait\":{\"x\":%.0f,\"y\":%.0f,\"width\":%.0f,\"height\":%.0f}}\n",
                NSMinX(frame), NSMaxY(NSScreen.screens.firstObject.frame) - NSMaxY(frame), size.width, size.height);
            return EXIT_SUCCESS;
        }
        BOOL legacy = [mode isEqualToString:@"website-legacy"];
        if (legacy || [mode isEqualToString:@"website"]) {
            NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
                NSWindowStyleMaskResizable | NSWindowStyleMaskMiniaturizable;
            NSRect content = NSMakeRect(0, 0, size.width, size.height);
            frame = centeredFrame(usable, [NSWindow frameRectForContentRect:content styleMask:style].size);
            if (legacy) frame = NSOffsetRect(frame, -24, 13);
            if (NSContainsRect(usable, frame)) {
                if (legacy) {
                    [NSApplication sharedApplication];
                    NSWindow *window = [[NSWindow alloc] initWithContentRect:content styleMask:style
                        backing:NSBackingStoreBuffered defer:YES];
                    [window setFrame:frame display:NO];
                    printf("%s\n", window.stringWithSavedFrame.UTF8String);
                }
                printf("%s\n", NSStringFromRect(frame).UTF8String);
                return EXIT_SUCCESS;
            }
        }
        fprintf(stderr, "Cannot place the %s test window on %s. Select a larger DeskPad display mode.\n", mode.UTF8String, name.UTF8String);
        return EXIT_FAILURE;
    }
}
