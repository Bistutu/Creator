#import "downloads.h"
#include <errno.h>
#include <stdio.h>

@interface DownloadSave : NSObject
@property(nonatomic, copy) NSURL *destination;
@property(nonatomic, copy) NSURL *directory;
@property(nonatomic, copy) NSURL *stagedFile;
@property(nonatomic, copy) NSDictionary<NSFileAttributeKey, id> *original;
@property(nonatomic, weak) NSWindow *window;
@end

@implementation DownloadSave
@end

static BOOL missingFile(NSError *error) {
    return [error.domain isEqualToString:NSCocoaErrorDomain] &&
        (error.code == NSFileNoSuchFileError || error.code == NSFileReadNoSuchFileError);
}

static NSURL *downloadDirectory(NSFileManager *files, NSURL *parent) {
    NSURL *directory = [files URLForDirectory:NSItemReplacementDirectory inDomain:NSUserDomainMask
        appropriateForURL:parent create:YES error:NULL];
    if (directory) return directory;

    // Some volumes cannot provide a system replacement directory.
    NSString *name = [@".creator-download-" stringByAppendingString:NSUUID.UUID.UUIDString];
    directory = [parent URLByAppendingPathComponent:name isDirectory:YES];
    return [files createDirectoryAtURL:directory withIntermediateDirectories:NO
        attributes:@{NSFilePosixPermissions: @0700} error:NULL] ? directory : nil;
}

static NSString *publishSave(DownloadSave *save) {
    NSFileManager *files = NSFileManager.defaultManager;
    NSError *inspection = nil;
    NSDictionary *current = [files attributesOfItemAtPath:save.destination.path error:&inspection];
    if (!current && !missingFile(inspection)) return @"无法访问所选位置。请选择其他位置后重试。";
    if ((current || save.original) && ![current isEqualToDictionary:save.original]) {
        return @"目标文件在下载期间发生了变化。请重新选择保存位置。";
    }

    // Rename a completed file on the same volume. The exclusive flag also
    // protects a newly chosen name if another app creates it during the move.
    const char *source = save.stagedFile.fileSystemRepresentation;
    const char *destination = save.destination.fileSystemRepresentation;
    if (save.original) {
        if (rename(source, destination) == 0) return nil;
    } else if (renamex_np(source, destination, RENAME_EXCL) == 0) {
        return nil;
    } else if (errno == ENOTSUP && [files moveItemAtURL:save.stagedFile toURL:save.destination error:NULL]) {
        return nil;
    }
    return @"无法写入所选位置。请选择其他位置后重试。";
}

@interface WebsiteDownloads ()
@property(nonatomic, strong) NSMapTable<WKDownload *, DownloadSave *> *saves;
@property(nonatomic, weak) NSWindow *fallbackWindow;
@end

@implementation WebsiteDownloads

- (instancetype)init {
    self = [super init];
    if (self) _saves = [NSMapTable strongToStrongObjectsMapTable];
    return self;
}

- (void)adopt:(WKDownload *)download fallbackWindow:(NSWindow *)window {
    self.fallbackWindow = window;
    download.delegate = self;
}

- (NSSavePanel *)savePanel { return NSSavePanel.savePanel; }

- (void)download:(WKDownload *)download decideDestinationUsingResponse:(NSURLResponse *)response
    suggestedFilename:(NSString *)filename completionHandler:(void (^)(NSURL *))complete {
    NSWindow *window = download.webView.window;
    if (!window.isVisible) window = self.fallbackWindow;
    NSSavePanel *panel = [self savePanel];
    panel.nameFieldStringValue = filename.lastPathComponent;
    void (^choose)(NSModalResponse) = ^(NSModalResponse answer) {
        NSURL *chosen = answer == NSModalResponseOK ? panel.URL : nil;
        complete(chosen ? [self prepareSave:download to:chosen inWindow:window] : nil);
    };
    if (window.isVisible) [panel beginSheetModalForWindow:window completionHandler:choose];
    else [panel beginWithCompletionHandler:choose];
}

- (NSURL *)prepareSave:(WKDownload *)download to:(NSURL *)destination inWindow:(NSWindow *)window {
    NSFileManager *files = NSFileManager.defaultManager;
    NSError *inspection = nil;
    NSDictionary *existing = [files attributesOfItemAtPath:destination.path error:&inspection];
    if ((!existing && !missingFile(inspection)) || (existing && ![existing[NSFileType] isEqual:NSFileTypeRegular])) {
        [self reportFailure:@"无法在所选位置保存文件。请选择其他位置后重试。" at:destination inWindow:window];
        return nil;
    }
    NSURL *directory = downloadDirectory(files, destination.URLByDeletingLastPathComponent);
    if (!directory) {
        [self reportFailure:@"无法在所选位置暂存下载。请选择其他位置后重试。" at:destination inWindow:window];
        return nil;
    }

    DownloadSave *save = [DownloadSave new];
    save.destination = destination;
    save.directory = directory;
    save.stagedFile = [directory URLByAppendingPathComponent:destination.lastPathComponent isDirectory:NO];
    save.original = existing;
    save.window = window;
    [self.saves setObject:save forKey:download];
    return save.stagedFile;
}

- (void)downloadDidFinish:(WKDownload *)download {
    DownloadSave *save = [self.saves objectForKey:download];
    if (!save) return;
    [self.saves removeObjectForKey:download];
    NSString *failure = publishSave(save);
    [NSFileManager.defaultManager removeItemAtURL:save.directory error:NULL];
    if (failure) [self reportFailure:failure at:save.destination inWindow:save.window];
}

- (void)download:(WKDownload *)download didFailWithError:(NSError *)error resumeData:(NSData *)resumeData {
    DownloadSave *save = [self.saves objectForKey:download];
    if (!save) return;
    [self.saves removeObjectForKey:download];
    [NSFileManager.defaultManager removeItemAtURL:save.directory error:NULL];
    [self reportFailure:@"文件传输未完成。请重新下载。" at:save.destination inWindow:save.window];
}

- (void)cancelPendingDownloads {
    NSArray<WKDownload *> *pending = self.saves.keyEnumerator.allObjects;
    for (WKDownload *download in pending) {
        DownloadSave *save = [self.saves objectForKey:download];
        [self.saves removeObjectForKey:download];
        [download cancel:nil];
        [NSFileManager.defaultManager removeItemAtURL:save.directory error:NULL];
    }
}

- (void)reportFailure:(NSString *)message at:(NSURL *)destination inWindow:(NSWindow *)window {
    if (!window.isVisible) window = self.fallbackWindow;
    if (!window.isVisible) return;
    NSAlert *alert = [NSAlert new];
    alert.alertStyle = NSAlertStyleWarning;
    alert.messageText = @"下载未完成";
    alert.informativeText = [NSString stringWithFormat:@"%@\n%@", destination.lastPathComponent, message];
    [alert addButtonWithTitle:@"确定"];
    [alert beginSheetModalForWindow:window completionHandler:nil];
}

@end
