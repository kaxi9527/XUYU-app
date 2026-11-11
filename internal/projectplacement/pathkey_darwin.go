//go:build darwin && cgo

package projectplacement

/*
#cgo LDFLAGS: -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
static char *shelf_path_key(const char *bytes, long length) {
    CFStringRef source = CFStringCreateWithBytes(NULL, (const UInt8 *)bytes,
        length, kCFStringEncodingUTF8, false);
    if (!source) return NULL;
    CFMutableStringRef result = CFStringCreateMutableCopy(NULL, 0, source);
    CFRelease(source);
    if (!result) return NULL;
    CFLocaleRef locale = CFLocaleCreate(NULL, CFSTR("en_US_POSIX"));
    if (!locale) { CFRelease(result); return NULL; }
    CFStringNormalize(result, kCFStringNormalizationFormD);
    CFStringFold(result, kCFCompareCaseInsensitive, locale);
    CFStringNormalize(result, kCFStringNormalizationFormD);
    CFRelease(locale);
    CFIndex capacity = CFStringGetMaximumSizeForEncoding(CFStringGetLength(result),
        kCFStringEncodingUTF8) + 1;
    char *out = (char *)malloc(capacity);
    if (out && !CFStringGetCString(result, out, capacity, kCFStringEncodingUTF8)) {
        free(out); out = NULL;
    }
    CFRelease(result);
    return out;
}
*/
import "C"

import (
	"strings"
	"unsafe"
)

// Canonical keys are only for conservative exclusions and reservations. They
// do not grant scope membership or prove that two files are identical.
func pathKey(path string) string {
	for _, alias := range []string{"/var", "/tmp"} {
		if path == alias || strings.HasPrefix(path, alias+"/") {
			path = "/private" + path
			break
		}
	}
	in := C.CString(path)
	defer C.free(unsafe.Pointer(in))
	out := C.shelf_path_key(in, C.long(len(path)))
	if out == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoString(out)
}
