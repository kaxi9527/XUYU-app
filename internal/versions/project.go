package versions

import (
	"context"
	"errors"
	"os"
)

// EnsureProject gives file-management memory the same identity as registered
// works. It is an explicit mutation; FindProject/Read remain allocation-free.
func (s *Store) EnsureProject(ctx context.Context, revision uint64, operationID string, ref ProjectInput) (Receipt, error) {
	if ref.Key == "" || len(ref.Key) > 4096 || !titleOK(ref.Name) || !validPath(ref.Path) {
		return Receipt{}, errors.New("项目身份或目录无效")
	}
	return s.update(ctx, revision, operationID, "ensure_project", ref, func(c *Catalog) ([]string, error) {
		project, err := c.FindProject(ref)
		if err != nil {
			return nil, err
		}
		if project != nil {
			return []string{project.ID}, nil
		}
		info, err := os.Lstat(ref.Path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("项目位置不是实际文件夹")
		}
		id, err := newID("project")
		if err != nil {
			return nil, err
		}
		c.Projects = append(c.Projects, Project{ID: id, Name: ref.Name, Path: ref.Path, LegacyKey: ref.Key})
		return []string{id}, nil
	})
}
