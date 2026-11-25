#import "settings.h"

#include <fcntl.h>
#include <sys/stat.h>

BOOL CreatorIsWebsite(NSURL *url) {
    NSString *scheme = url.scheme.lowercaseString;
    return url.host.length && !url.user && !url.password &&
        ([scheme isEqualToString:@"https"] || [scheme isEqualToString:@"http"]);
}

static id readJSONFile(NSString *path, off_t limit) {
    if (!path.length) return nil;
    // Read one snapshot, including when Creator replaces it during an edit.
    int descriptor = open(path.fileSystemRepresentation, O_RDONLY | O_NONBLOCK | O_CLOEXEC);
    if (descriptor < 0) return nil;
    NSFileHandle *file = [[NSFileHandle alloc] initWithFileDescriptor:descriptor closeOnDealloc:YES];
    struct stat info;
    if (fstat(descriptor, &info) != 0 || !S_ISREG(info.st_mode)) return nil;
    if (limit && info.st_size > limit) return nil;
    NSData *data = limit ? [file readDataUpToLength:(NSUInteger)info.st_size + 1 error:NULL] : [file readDataToEndOfFileAndReturnError:NULL];
    [file closeAndReturnError:NULL];
    if (!data.length || (limit && data.length != (NSUInteger)info.st_size)) return nil;
    return [NSJSONSerialization JSONObjectWithData:data options:0 error:NULL];
}

static NSDictionary *savedProfile(NSString *appID) {
    NSString *home = NSProcessInfo.processInfo.environment[@"HOME"];
    if (!appID.length || !home.length) return nil;
    NSString *path = [home stringByAppendingPathComponent:@"Library/Application Support/WebCrate/apps.json"];
    id list = readJSONFile(path, 0);
    if (![list isKindOfClass:NSArray.class]) return nil;
    for (id record in list) {
        if (![record isKindOfClass:NSDictionary.class]) return nil;
        if ([appID isEqual:record[@"id"]]) return record;
    }
    return nil;
}

static NSString *installedProfileID(NSString *bundleID) {
    NSString *prefix = @"com.webcrate.apps.app-";
    if (![bundleID hasPrefix:prefix] || bundleID.length != prefix.length + 16) return nil;
    NSString *hex = [bundleID substringFromIndex:prefix.length];
    NSCharacterSet *invalid = [NSCharacterSet characterSetWithCharactersInString:@"0123456789abcdef"].invertedSet;
    return [hex rangeOfCharacterFromSet:invalid].location == NSNotFound ? [@"app-" stringByAppendingString:hex] : nil;
}

static BOOL validWebsiteSettings(id config, NSString *appID) {
    return [config isKindOfClass:NSDictionary.class] &&
        [config[@"id"] isKindOfClass:NSString.class] && (!appID || [appID isEqualToString:config[@"id"]]) &&
        [config[@"name"] isKindOfClass:NSString.class] && [config[@"url"] isKindOfClass:NSString.class] &&
        [config[@"width"] isKindOfClass:NSNumber.class] && [config[@"height"] isKindOfClass:NSNumber.class] &&
        CreatorIsWebsite([NSURL URLWithString:config[@"url"]]);
}

NSDictionary *CreatorLoadWebsiteSettings(NSString *resources, NSString *bundleID) {
    NSString *appID = installedProfileID(bundleID);
    id config = readJSONFile([resources stringByAppendingPathComponent:@"website.json"], 16 * 1024);
    if (validWebsiteSettings(config, appID)) return config;
    config = savedProfile(appID);
    return validWebsiteSettings(config, appID) ? config : nil;
}

NSURL *CreatorSavedHome(NSString *appID) {
    id address = savedProfile(appID)[@"url"];
    return [address isKindOfClass:NSString.class] ? [NSURL URLWithString:address] : nil;
}
