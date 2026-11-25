#import <AppKit/AppKit.h>
#import <WebKit/WebKit.h>

@interface WebsiteDownloads : NSObject <WKDownloadDelegate>
- (void)adopt:(WKDownload *)download fallbackWindow:(NSWindow *)window;
- (NSSavePanel *)savePanel;
- (void)cancelPendingDownloads;
@end
