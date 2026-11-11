package main

import (
	"context"
	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"time"
)

func (s *ShelfService) ProjectAnalysisScopes(key, parentID, expectedSHA string, offset int) (projectcontext.ScopePage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.prepareAnalysisHistory(ctx, key); err != nil {
		return projectcontext.ScopePage{}, err
	}
	_, view, profile, err := s.analysisSnapshot(ctx, key)
	if err != nil {
		return projectcontext.ScopePage{}, err
	}
	index, err := projectcontext.NewScopeIndex(view, profile)
	if err != nil {
		return projectcontext.ScopePage{}, err
	}
	page, err := index.Page(parentID, expectedSHA, offset)
	if err != nil {
		return page, err
	}
	if ctx.Err() != nil {
		return projectcontext.ScopePage{}, ctx.Err()
	}
	return page, nil
}

func (s *ShelfService) PreviewProjectAnalysisGroup(key, expectedSHA, groupID string) (AnalysisPreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.prepareAnalysisHistory(ctx, key); err != nil {
		return AnalysisPreview{}, err
	}
	e, view, profile, err := s.analysisSnapshot(ctx, key)
	if err != nil {
		return AnalysisPreview{}, err
	}
	index, err := projectcontext.NewScopeIndex(view, profile)
	if err != nil {
		return AnalysisPreview{}, err
	}
	packet, err := index.Select(expectedSHA, groupID)
	if err != nil {
		return AnalysisPreview{}, err
	}
	return s.previewAnalysisPacket(ctx, key, e, packet)
}
