#import <Foundation/Foundation.h>

BOOL CreatorIsWebsite(NSURL *_Nullable url);
NSDictionary *_Nullable CreatorLoadWebsiteSettings(NSString *_Nullable resources, NSString *_Nullable bundleID);
NSURL *_Nullable CreatorSavedHome(NSString *_Nullable appID);
