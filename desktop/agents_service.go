package main

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"

	"github.com/kaxi9527/WorkShelf/internal/agents"
	"github.com/kaxi9527/WorkShelf/internal/put"
)

// AgentsScan 只读：列出可以接入的 AI 规则文件。
func (s *ShelfService) AgentsScan() ([]agents.Candidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return agents.Scan(home), nil
}

// AgentsBlock 返回会追加的那一段原文，给用户预览。
func (s *ShelfService) AgentsBlock(agent string) string { return agents.Block(agent) }

func agentOptions(dry bool) (agents.Options, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return agents.Options{}, err
	}
	return agents.Options{BackupRoot: filepath.Join(cfg.Root, cfg.Names.Control, cfg.Names.AgentBackup), DryRun: dry}, nil
}

func checkTargets(paths []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("没有选文件")
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) || !within(home, p) {
			return errors.New("只能处理主目录下的文件：" + p)
		}
	}
	return nil
}

// AgentsInstall 给选中的规则文件末尾追加身份标识段。dry=true 只预演。前端必须先让用户确认。
func (s *ShelfService) AgentsInstall(targets []agents.Target, dry bool) ([]agents.Outcome, error) {
	var ps []string
	for _, t := range targets {
		ps = append(ps, t.Path)
	}
	if err := checkTargets(ps); err != nil {
		return nil, err
	}
	opt, err := agentOptions(dry)
	if err != nil {
		return nil, err
	}
	return agents.Install(targets, opt)
}

// AgentsUninstall 只删掉 WorkShelf 追加的那一段。前端必须先让用户确认。
func (s *ShelfService) AgentsUninstall(paths []string, dry bool) ([]agents.Outcome, error) {
	if err := checkTargets(paths); err != nil {
		return nil, err
	}
	opt, err := agentOptions(dry)
	if err != nil {
		return nil, err
	}
	return agents.Uninstall(paths, opt)
}

// Approve 批准某个任务的成品（状态改「已交」，不动文件本身）。前端必须先让用户确认。
func (s *ShelfService) Approve(project, task string) (*put.ApproveResult, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	by := ""
	if u, err := user.Current(); err == nil {
		by = u.Username
	}
	return put.Approve(put.ApproveOptions{Config: cfg, Project: project, Task: task, By: by})
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(p))
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && len(rel) >= 1 && !(len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator))
}
