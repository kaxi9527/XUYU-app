//go:build !darwin || !cgo

package projectplacement

import "context"

func (e *Executor) reviewUndo(context.Context, string, string) (UndoPreview, error) {
	return UndoPreview{}, ErrUnsupported
}
func (e *Executor) confirmUndo(context.Context, string, UndoPreview, []string) (JournalView, error) {
	return JournalView{}, ErrUnsupported
}
func (e *Executor) executeUndo(context.Context, string, string, string, string) (JournalView, error) {
	return JournalView{}, ErrUnsupported
}
