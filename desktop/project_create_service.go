package main

import "github.com/kaxi9527/WorkShelf/internal/projectcreate"

// CreateProject is called only after the user reviews the name and folder tree.
func (s *ShelfService) CreateProject(in projectcreate.Input) (projectcreate.Result, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return projectcreate.Result{}, err
	}
	return projectcreate.Create(cfg.Root, cfg.Names.Readme, in)
}
