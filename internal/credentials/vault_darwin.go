//go:build darwin && cgo

package credentials

/*
#cgo LDFLAGS: -framework Foundation -framework Security -framework LocalAuthentication
#include <stdlib.h>
#include "keychain_darwin.h"
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

type keychainVault struct{ service string }

func newPlatformVault(service string) (Vault, error) { return &keychainVault{service: service}, nil }

func keychainError(code C.int) error {
	switch int(code) {
	case 0:
		return nil
	case -25300:
		return ErrNotFound
	case -25299:
		return ErrExists
	case -25308, -25293, -128:
		return ErrLocked
	case -25291, -25294, -25307, -34018:
		return ErrUnavailable
	default:
		return fmt.Errorf("%w（系统代码 %d）", ErrUnavailable, int(code))
	}
}
func (v *keychainVault) withReference(ctx context.Context, ref string, fn func(*C.char, *C.char) C.int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidReference(ref) {
		return ErrInvalid
	}
	service, reference := C.CString(v.service), C.CString(ref)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(reference))
	// Native calls never open an authentication dialog. Once a write starts,
	// return its actual result even if ctx was cancelled during the OS call.
	return keychainError(fn(service, reference))
}
func (v *keychainVault) Create(ctx context.Context, ref string, secret *Secret) error {
	bytes, err := secret.Bytes()
	if err != nil {
		return err
	}
	defer Wipe(bytes)
	data := C.CBytes(bytes)
	defer C.workshelf_secret_free(data, C.size_t(len(bytes)))
	return v.withReference(ctx, ref, func(service, reference *C.char) C.int {
		return C.workshelf_secret_create(service, reference, data, C.size_t(len(bytes)))
	})
}
func (v *keychainVault) Read(ctx context.Context, ref string) (*Secret, error) {
	var data unsafe.Pointer
	var length C.size_t
	err := v.withReference(ctx, ref, func(service, reference *C.char) C.int {
		return C.workshelf_secret_read(service, reference, &data, &length)
	})
	defer C.workshelf_secret_free(data, length)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	bytes := C.GoBytes(data, C.int(length))
	defer Wipe(bytes)
	return NewSecret(bytes)
}
func (v *keychainVault) Delete(ctx context.Context, ref string) error {
	return v.withReference(ctx, ref, func(service, reference *C.char) C.int { return C.workshelf_secret_delete(service, reference) })
}
