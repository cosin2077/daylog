//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ImageIO/ImageIO.h>
#import <IOKit/ps/IOPowerSources.h>
#import <IOKit/ps/IOPSKeys.h>
#import <Security/Security.h>
#import <pthread.h>

static NSString *frontmost(void) {
    return NSWorkspace.sharedWorkspace.frontmostApplication.bundleIdentifier ?: @"";
}

char *dg_environment(void) {
    @autoreleasepool {
        BOOL battery=NO; int percent=-1;
        CFTypeRef info=IOPSCopyPowerSourcesInfo();
        CFArrayRef sources=info?IOPSCopyPowerSourcesList(info):NULL;
        if(sources) {
            for(CFIndex i=0;i<CFArrayGetCount(sources);i++) {
                NSDictionary *source=(__bridge NSDictionary *)IOPSGetPowerSourceDescription(info,CFArrayGetValueAtIndex(sources,i));
                if(![source[@kIOPSTypeKey] isEqualToString:@kIOPSInternalBatteryType]) continue;
                battery=[source[@kIOPSPowerSourceStateKey] isEqualToString:@kIOPSBatteryPowerValue];
                int max=[source[@kIOPSMaxCapacityKey] intValue];
                if(max>0) percent=100*[source[@kIOPSCurrentCapacityKey] intValue]/max;
            }
            CFRelease(sources);
        }
        if(info) CFRelease(info);
        NSDictionary *session=CFBridgingRelease(CGSessionCopyCurrentDictionary());
        BOOL locked=[session[@"CGSSessionScreenIsLocked"] boolValue] || (session && ![session[@"kCGSessionOnConsoleKey"] boolValue]);
        NSDictionary *env=@{
            @"app":frontmost(),
            @"idle_seconds":@(CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateHIDSystemState,kCGAnyInputEventType)),
            @"screen_off":(CGDisplayIsAsleep(CGMainDisplayID()) || CGMainDisplayID()==0)?@YES:@NO,
            @"locked":locked?@YES:@NO, @"on_battery":battery?@YES:@NO, @"battery_percent":@(percent),
            @"low_power":NSProcessInfo.processInfo.lowPowerModeEnabled?@YES:@NO,
            @"thermal":@(NSProcessInfo.processInfo.thermalState),
            @"screen_permission":CGPreflightScreenCaptureAccess()?@YES:@NO
        };
        NSData *data=[NSJSONSerialization dataWithJSONObject:env options:0 error:nil];
        if(!data)return NULL;
        char *out=malloc(data.length+1);if(!out)return NULL;
        memcpy(out,data.bytes,data.length);out[data.length]=0;return out;
    }
}

int dg_capture(int height,int quality,unsigned int windowID,const char *blockedJSON,void **bytes,int *length) {
    @autoreleasepool {
        pthread_set_qos_class_self_np(QOS_CLASS_UTILITY,0);
        if(!CGPreflightScreenCaptureAccess())return 1;
        NSData *raw=[NSData dataWithBytes:blockedJSON length:strlen(blockedJSON)];
        NSArray *blocked=[NSJSONSerialization JSONObjectWithData:raw options:0 error:nil];
        if(![blocked isKindOfClass:NSArray.class])blocked=@[];
        NSString *app=frontmost();
        if(blocked.count && (app.length==0 || [blocked containsObject:app]))return 4;
        dispatch_semaphore_t done=dispatch_semaphore_create(0);
        __block int result=3; __block NSData *jpeg=nil;
        [SCShareableContent getShareableContentExcludingDesktopWindows:NO onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *content,NSError *error) {
            if(error || !content){dispatch_semaphore_signal(done);return;}
            NSString *current=frontmost();
            if(blocked.count && (current.length==0 || [blocked containsObject:current])){result=4;dispatch_semaphore_signal(done);return;}
            SCDisplay *primary=nil;
            for(SCDisplay *display in content.displays){if(display.displayID==CGMainDisplayID()){primary=display;break;}}
            if(!primary || primary.width<=0 || primary.height<=0){result=5;dispatch_semaphore_signal(done);return;}
            NSMutableArray *excluded=[NSMutableArray array];
            for(SCRunningApplication *running in content.applications) {
                if([blocked containsObject:running.bundleIdentifier] || [running.bundleIdentifier isEqualToString:@"io.daylog.agent"]) [excluded addObject:running];
            }
            SCContentFilter *filter=nil;
            double width=primary.width,sourceHeight=primary.height;
            if(windowID) {
                SCWindow *target=nil;
                for(SCWindow *window in content.windows){if(window.windowID==windowID){target=window;break;}}
                if(!target){result=5;dispatch_semaphore_signal(done);return;}
                if([blocked containsObject:target.owningApplication.bundleIdentifier]){result=4;dispatch_semaphore_signal(done);return;}
                filter=[[SCContentFilter alloc] initWithDesktopIndependentWindow:target];
                width=target.frame.size.width;sourceHeight=target.frame.size.height;
            } else {
                filter=[[SCContentFilter alloc] initWithDisplay:primary excludingApplications:excluded exceptingWindows:@[]];
            }
            if(width<=0 || sourceHeight<=0){result=5;dispatch_semaphore_signal(done);return;}
            double scale=MIN((double)height/sourceHeight,1920.0/width);
            SCStreamConfiguration *configuration=[SCStreamConfiguration new];
            configuration.width=MAX(2,((int)(width*scale)/2)*2);
            configuration.height=MAX(2,((int)(sourceHeight*scale)/2)*2);
            configuration.showsCursor=NO;configuration.scalesToFit=YES;
            [SCScreenshotManager captureImageWithFilter:filter configuration:configuration completionHandler:^(CGImageRef image,NSError *captureError) {
                if(image && !captureError) {
                    pthread_set_qos_class_self_np(QOS_CLASS_UTILITY,0);
                    NSMutableData *data=[NSMutableData data];
                    CGImageDestinationRef destination=CGImageDestinationCreateWithData((__bridge CFMutableDataRef)data,CFSTR("public.jpeg"),1,NULL);
                    if(destination) {
                        NSDictionary *options=@{(__bridge NSString *)kCGImageDestinationLossyCompressionQuality:@((double)quality/100)};
                        CGImageDestinationAddImage(destination,image,(__bridge CFDictionaryRef)options);
                        if(CGImageDestinationFinalize(destination)){jpeg=data;result=0;}
                        CFRelease(destination);
                    }
                }
                dispatch_semaphore_signal(done);
            }];
        }];
        if(dispatch_semaphore_wait(done,dispatch_time(DISPATCH_TIME_NOW,10*NSEC_PER_SEC))!=0)return 2;
        if(result)return result;
        if(jpeg.length==0 || jpeg.length>2*1024*1024)return 3;
        *bytes=malloc(jpeg.length);if(!*bytes)return 3;
        memcpy(*bytes,jpeg.bytes,jpeg.length);*length=(int)jpeg.length;return 0;
    }
}

char *dg_windows(void) {
    @autoreleasepool {
        if(!CGPreflightScreenCaptureAccess())return NULL;
        dispatch_semaphore_t done=dispatch_semaphore_create(0);
        __block NSData *data=nil;
        [SCShareableContent getShareableContentExcludingDesktopWindows:NO onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *content,NSError *error){
            if(content && !error){NSMutableArray *windows=[NSMutableArray array];for(SCWindow *window in content.windows){if(window.owningApplication && window.frame.size.width>100 && window.frame.size.height>100)[windows addObject:@{@"id":@(window.windowID),@"app":window.owningApplication.bundleIdentifier?:@"",@"title":window.title?:@""}];}data=[NSJSONSerialization dataWithJSONObject:windows options:0 error:nil];}
            dispatch_semaphore_signal(done);
        }];
        if(dispatch_semaphore_wait(done,dispatch_time(DISPATCH_TIME_NOW,10*NSEC_PER_SEC))!=0 || !data)return NULL;
        char *out=malloc(data.length+1);if(!out)return NULL;memcpy(out,data.bytes,data.length);out[data.length]=0;return out;
    }
}

int dg_permission(void){return CGRequestScreenCaptureAccess();}
static NSDictionary *secretQuery(void){return @{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"io.daylog.agent.api",(__bridge id)kSecAttrAccount:@"api-key"};}
int dg_secret_set(const char *value) {
    @autoreleasepool {
        NSData *data=[NSData dataWithBytes:value length:strlen(value)];
        NSDictionary *changes=@{(__bridge id)kSecValueData:data};
        OSStatus status=SecItemUpdate((__bridge CFDictionaryRef)secretQuery(),(__bridge CFDictionaryRef)changes);
        if(status==errSecItemNotFound){NSMutableDictionary *item=[secretQuery() mutableCopy];item[(__bridge id)kSecValueData]=data;status=SecItemAdd((__bridge CFDictionaryRef)item,NULL);}
        return status==errSecSuccess?0:1;
    }
}
char *dg_secret_get(void) {
    @autoreleasepool {
        NSMutableDictionary *query=[secretQuery() mutableCopy];query[(__bridge id)kSecReturnData]=@YES;
        CFTypeRef value=NULL;if(SecItemCopyMatching((__bridge CFDictionaryRef)query,&value)!=errSecSuccess)return NULL;
        NSData *data=CFBridgingRelease(value);char *out=malloc(data.length+1);if(!out)return NULL;
        memcpy(out,data.bytes,data.length);out[data.length]=0;return out;
    }
}
int dg_secret_delete(void){OSStatus status=SecItemDelete((__bridge CFDictionaryRef)secretQuery());return status==errSecSuccess||status==errSecItemNotFound?0:1;}
