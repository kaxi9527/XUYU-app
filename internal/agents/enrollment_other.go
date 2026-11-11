//go:build !linux && (!darwin || !cgo)

package agents

import (
	"context"
	"errors"
	"os"
)

func openEnrollment(string, int) (*os.File, error) {
	return nil, errors.New("此平台尚未支持安全规则接入")
}
func singleEnrollmentLink(os.FileInfo) bool { return false }
func lockEnrollment(context.Context, *os.File) error {
	return errors.New("此平台尚未支持安全规则接入")
}
func unlockEnrollment(*os.File) {}

func enrollmentSupported() bool { return false }
