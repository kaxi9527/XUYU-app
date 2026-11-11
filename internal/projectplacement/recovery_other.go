//go:build !darwin || !cgo

package projectplacement

import "context"

func (e *Executor) reviewRecovery(context.Context, string, string) (RecoveryPreview, error) {
	return RecoveryPreview{}, ErrUnsupported
}
func (e *Executor) confirmRecovery(context.Context, RecoveryPreview) (JournalView, error) {
	return JournalView{}, ErrUnsupported
}
