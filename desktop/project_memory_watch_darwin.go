//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework CoreServices
#include <CoreServices/CoreServices.h>
#include <dispatch/dispatch.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    FSEventStreamRef stream;
    dispatch_queue_t queue;
    CFMutableArrayRef paths;
    char **matches;
    int *recursive;
    int count;
    char *exclude;
    _Atomic int changed;
} WSMemoryWatch;

static void ws_memory_events(ConstFSEventStreamRef stream, void *context,
    size_t count, void *eventPaths, const FSEventStreamEventFlags flags[],
    const FSEventStreamEventId ids[]) {
    WSMemoryWatch *w = context;
    char **paths = eventPaths;
    for (size_t i = 0; i < count; i++) {
        if (flags[i] & kFSEventStreamEventFlagHistoryDone) continue;
        // Overflow/root changes require a complete snapshot, never a guessed delta.
        if (flags[i] & (kFSEventStreamEventFlagMustScanSubDirs |
                       kFSEventStreamEventFlagUserDropped |
                       kFSEventStreamEventFlagKernelDropped |
                       kFSEventStreamEventFlagRootChanged)) {
            atomic_store(&w->changed, 1); return;
        }
        int excluded = 0;
        if (w->exclude && w->exclude[0]) {
            size_t n = strlen(w->exclude);
            excluded = !strcmp(paths[i], w->exclude) || (!strncmp(paths[i], w->exclude, n) && paths[i][n] == '/');
        }
        for (int j = 0; j < w->count; j++) {
            // Explicitly granted source logs may live inside the control
            // directory. Recursive project roots still exclude our own ledger.
            if (excluded && w->recursive[j]) continue;
            size_t n = strlen(w->matches[j]);
            if (!strcmp(paths[i], w->matches[j]) ||
                (w->recursive[j] && !strncmp(paths[i], w->matches[j], n) && paths[i][n] == '/')) {
                atomic_store(&w->changed, 1); return;
            }
        }
    }
}

static WSMemoryWatch *ws_memory_alloc(int count, const char *exclude) {
    WSMemoryWatch *w = calloc(1, sizeof(*w));
    if (!w) return NULL;
    atomic_init(&w->changed, 0);
    w->matches = calloc(count, sizeof(char *));
    w->recursive = calloc(count, sizeof(int));
    w->paths = CFArrayCreateMutable(NULL, 0, &kCFTypeArrayCallBacks);
    w->exclude = strdup(exclude);
    if (!w->matches || !w->recursive || !w->paths || !w->exclude) {
        if (w->paths) CFRelease(w->paths);
        free(w->matches); free(w->recursive); free(w->exclude); free(w); return NULL;
    }
    return w;
}
static int ws_memory_add(WSMemoryWatch *w, const char *path, const char *parent, int recursive) {
    CFStringRef s = CFStringCreateWithCString(NULL, parent, kCFStringEncodingUTF8);
    char *match = strdup(path);
    if (!s || !match) { if (s) CFRelease(s); free(match); return 0; }
    CFArrayAppendValue(w->paths, s); CFRelease(s);
    w->matches[w->count] = match; w->recursive[w->count++] = recursive;
    return 1;
}
static void ws_memory_drain(void *unused) {}
static void ws_memory_close(WSMemoryWatch *w) {
    if (w->stream) {
        FSEventStreamStop(w->stream);
        FSEventStreamInvalidate(w->stream);
        // All callbacks are C-only and finish before their context is released.
        dispatch_sync_f(w->queue, NULL, ws_memory_drain);
        FSEventStreamRelease(w->stream);
    }
    if (w->queue) dispatch_release(w->queue);
    for (int i = 0; i < w->count; i++) free(w->matches[i]);
    CFRelease(w->paths); free(w->matches); free(w->recursive); free(w->exclude); free(w);
}
static int ws_memory_start(WSMemoryWatch *w) {
    FSEventStreamContext context = {0, w, NULL, NULL, NULL};
    w->queue = dispatch_queue_create("workshelf.project-files", DISPATCH_QUEUE_SERIAL);
    if (!w->queue) return 0;
    w->stream = FSEventStreamCreate(NULL, ws_memory_events, &context, w->paths,
        kFSEventStreamEventIdSinceNow, 0.25,
        kFSEventStreamCreateFlagFileEvents | kFSEventStreamCreateFlagWatchRoot | kFSEventStreamCreateFlagNoDefer);
    if (!w->stream) return 0;
    FSEventStreamSetDispatchQueue(w->stream, w->queue);
    return FSEventStreamStart(w->stream);
}
static int ws_memory_take(WSMemoryWatch *w) { return atomic_exchange(&w->changed, 0); }
*/
import "C"

import (
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

type darwinMemoryWatch struct{ ref *C.WSMemoryWatch }

func newMemoryNativeWatch(scope projectmemory.Scope, exclude string) (memoryNativeWatch, error) {
	n := len(scope.Roots) + len(scope.Files)
	if n == 0 || n > 128 {
		return nil, errMemoryWatchUnavailable
	}
	x := C.CString(memoryWatchPath(exclude))
	w := C.ws_memory_alloc(C.int(n), x)
	C.free(unsafe.Pointer(x))
	if w == nil {
		return nil, errMemoryWatchUnavailable
	}
	ok := false
	defer func() {
		if !ok {
			C.ws_memory_close(w)
		}
	}()
	for i, paths := range [][]string{scope.Roots, scope.Files} {
		for _, path := range paths {
			// FSEvents reports the canonical macOS /private aliases.
			path = memoryWatchPath(path)
			parent, recursive := path, 1
			if i == 1 {
				parent, recursive = filepath.Dir(path), 0
			}
			p, base := C.CString(path), C.CString(parent)
			added := C.ws_memory_add(w, p, base, C.int(recursive))
			C.free(unsafe.Pointer(p))
			C.free(unsafe.Pointer(base))
			if added == 0 {
				return nil, errMemoryWatchUnavailable
			}
		}
	}
	if C.ws_memory_start(w) == 0 {
		return nil, errMemoryWatchUnavailable
	}
	ok = true
	return &darwinMemoryWatch{ref: w}, nil
}

func (w *darwinMemoryWatch) Take() bool { return C.ws_memory_take(w.ref) != 0 }
func (w *darwinMemoryWatch) Close()     { C.ws_memory_close(w.ref) }

func memoryWatchPath(path string) string {
	for _, alias := range []string{"/var", "/tmp"} {
		if path == alias || strings.HasPrefix(path, alias+"/") {
			return "/private" + path
		}
	}
	return path
}
