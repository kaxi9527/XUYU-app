//go:build darwin && cgo

#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

int workshelf_cross_operation(int operation, const char *source, const char *target, char **message) {
    @autoreleasepool {
        NSError *error = nil;
        NSString *from = [NSString stringWithUTF8String:source];
        NSString *to = [NSString stringWithUTF8String:target];
        NSFileManager *manager = [NSFileManager defaultManager];
        BOOL ok = operation == 0 ? [manager copyItemAtPath:from toPath:to error:&error] : [manager removeItemAtPath:from error:&error];
        if (ok) return 0;
        const char *description = error.localizedDescription.UTF8String;
        *message = strdup(description ? description : "跨磁盘搬迁失败");
        return -1;
    }
}
