//go:build darwin && cgo

package organize

/*
#cgo LDFLAGS: -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <unistd.h>

static char* ws_normalize(const char* bytes, long length) {
    CFStringRef input = CFStringCreateWithBytes(NULL, (const UInt8*)bytes, length, kCFStringEncodingUTF8, false);
    if (!input) return NULL;
    CFMutableStringRef value = CFStringCreateMutableCopy(NULL, 0, input);
    CFRelease(input);
    if (!value) return NULL;
    CFStringNormalize(value, kCFStringNormalizationFormC);
    CFIndex capacity = CFStringGetMaximumSizeForEncoding(CFStringGetLength(value), kCFStringEncodingUTF8) + 1;
    char* result = malloc(capacity);
    if (result && !CFStringGetCString(value, result, capacity, kCFStringEncodingUTF8)) { free(result); result = NULL; }
    CFRelease(value);
    return result;
}
static int ws_case_insensitive(const char* path) { return pathconf(path, _PC_CASE_SENSITIVE) == 0; }
*/
import "C"
import "unsafe"

const nativeUnicodeNormalization = true

func normalizeFilename(value string) string {
	input := C.CString(value)
	defer C.free(unsafe.Pointer(input))
	result := C.ws_normalize(input, C.long(len(value)))
	if result == nil {
		return value
	}
	defer C.free(unsafe.Pointer(result))
	return C.GoString(result)
}
func filesystemCaseInsensitive(path string) bool {
	input := C.CString(path)
	defer C.free(unsafe.Pointer(input))
	return C.ws_case_insensitive(input) != 0
}
