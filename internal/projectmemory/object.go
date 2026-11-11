package projectmemory

import "os"

// ObjectIdentity exposes the same platform evidence used by the observer. An
// empty result means identity is unavailable; it is not permission to infer a
// persistent file identity from a name, time, or content hash.
func ObjectIdentity(info os.FileInfo) string { return objectKey(info) }
