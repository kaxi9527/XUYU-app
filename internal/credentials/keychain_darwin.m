//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <LocalAuthentication/LocalAuthentication.h>
#import <Security/Security.h>
#include "keychain_darwin.h"

static NSMutableDictionary *workshelf_query(const char *service, const char *reference, LAContext *context) {
    context.interactionNotAllowed = YES;
    // SecItem's default macOS file-based keychain works with the current local
    // desktop signing setup. No search-list, sync, access-group or ACL changes.
    return [NSMutableDictionary dictionaryWithDictionary:@{
        (id)kSecClass: (id)kSecClassGenericPassword,
        (id)kSecAttrService: [NSString stringWithUTF8String:service],
        (id)kSecAttrAccount: [NSString stringWithUTF8String:reference],
        (id)kSecUseAuthenticationContext: context
    }];
}

int workshelf_secret_create(const char *service, const char *reference, const void *data, size_t length) {
    @autoreleasepool {
        LAContext *context = [[LAContext alloc] init];
        NSMutableDictionary *query = workshelf_query(service, reference, context);
        query[(id)kSecValueData] = [NSData dataWithBytes:data length:length];
        OSStatus status = SecItemAdd((CFDictionaryRef)query, NULL);
        [context release];
        return (int)status;
    }
}

int workshelf_secret_read(const char *service, const char *reference, void **data, size_t *length) {
    *data = NULL; *length = 0;
    @autoreleasepool {
        LAContext *context = [[LAContext alloc] init];
        NSMutableDictionary *query = workshelf_query(service, reference, context);
        query[(id)kSecReturnData] = @YES;
        query[(id)kSecMatchLimit] = (id)kSecMatchLimitOne;
        CFTypeRef result = NULL;
        OSStatus status = SecItemCopyMatching((CFDictionaryRef)query, &result);
        if (status == errSecSuccess) {
            if (!result || CFGetTypeID(result) != CFDataGetTypeID()) status = errSecDecode;
            else {
                CFIndex size = CFDataGetLength((CFDataRef)result);
                if (size <= 0 || size > 65536) status = errSecDecode;
                else {
                    *data = malloc((size_t)size);
                    if (!*data) status = errSecAllocate;
                    else { *length = (size_t)size; memcpy(*data, CFDataGetBytePtr((CFDataRef)result), *length); }
                }
            }
        }
        if (result) CFRelease(result);
        [context release];
        return (int)status;
    }
}

int workshelf_secret_delete(const char *service, const char *reference) {
    @autoreleasepool {
        LAContext *context = [[LAContext alloc] init];
        NSMutableDictionary *query = workshelf_query(service, reference, context);
        OSStatus status = SecItemDelete((CFDictionaryRef)query);
        [context release];
        return (int)status;
    }
}

void workshelf_secret_free(void *data, size_t length) {
    if (!data) return;
    volatile unsigned char *bytes = data;
    for (size_t i=0; i<length; ++i) bytes[i]=0;
    free(data);
}
