//go:build (!darwin && !linux) || (darwin && !cgo)

package projectplacement

import "github.com/kaxi9527/WorkShelf/internal/projectcontext"

func previewSupported() bool { return false }

func (b *previewBuilder) loadExclusions() error { return ErrUnsupported }

func (b *previewBuilder) rows(projectcontext.PlacementProposal) ([]Row, error) {
	return nil, ErrUnsupported
}
func (b *previewBuilder) checkDirectories() error { return ErrUnsupported }
