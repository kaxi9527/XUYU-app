//go:build !darwin || !cgo

package credentials

func newPlatformVault(service string) (Vault, error) { return nil, ErrUnavailable }
