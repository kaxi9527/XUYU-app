package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func writeProfile(root, path string, p *worktype.Profile, exclusive bool) error {
	if p == nil {
		return errors.New("模板不能为空")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if err := home.CheckPath(root, path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if exclusive {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(b)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return e
		}
		return ce
	}
	// Only replace this application's validated metadata, never arbitrary files.
	if _, err := worktype.ReadProfile(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".layout-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	if err = home.CheckPath(root, path); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (s *ShelfService) OrganizeSaveTemplate(p worktype.Profile) error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	if !worktype.ValidFolder(p.Name) || strings.Contains(p.Name, "/") {
		return errors.New("请填写不含路径分隔符的模板名称")
	}
	path := filepath.Join(cfg.Root, cfg.Names.Control, "整理模板", p.Name+".json")
	if err = writeProfile(cfg.Root, path, &p, true); os.IsExist(err) {
		return errors.New("同名模板已存在，请使用新的名称")
	}
	return err
}
