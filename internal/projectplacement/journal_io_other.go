//go:build (!darwin && !linux) || !cgo

package projectplacement

import (
	"context"
	"time"
)

type journalSession struct{ view JournalView }

func (j *Journal) prepare(context.Context, Intent) (JournalView, error) {
	return JournalView{}, ErrUnsupported
}
func (j *Journal) change(context.Context, string, func(*journalSession) error) (JournalView, error) {
	return JournalView{}, ErrUnsupported
}
func (j *Journal) Read(context.Context, string) (JournalView, error) {
	return JournalView{}, ErrUnsupported
}
func (j *Journal) RequestStop(context.Context, string, string, time.Time) (JournalView, error) {
	return JournalView{}, ErrUnsupported
}
func (j *Journal) RequestUndoStop(context.Context, string, string, string, string, time.Time) (JournalView, error) {
	return JournalView{}, ErrUnsupported
}
func (s *journalSession) append(journalRecord) error { return ErrUnsupported }
func (s *journalSession) memory(string, string, MemoryReceipt, time.Time) error {
	return ErrUnsupported
}
