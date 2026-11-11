//go:build !darwin || !cgo

package projectplacement

import "context"

func (e *Executor) execute(context.Context, string, string) (JournalView, error) {
	return JournalView{}, ErrUnsupported
}
