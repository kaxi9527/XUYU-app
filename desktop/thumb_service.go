package main

import (
	"os"
	"sync"

	"github.com/kaxi9527/WorkShelf/internal/thumb"
)

var (
	thumbOnce sync.Once
	thumbGen  *thumb.Generator
	thumbErr  error
)

// Thumb 返回一个文件的缩略图（图片 data URL、前几行文字，或只给类型让前端画图标）。只读源文件。
func (s *ShelfService) Thumb(path string) (thumb.Result, error) {
	thumbOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil {
			thumbErr = err
			return
		}
		thumbGen, thumbErr = thumb.New("", home, 160)
	})
	if thumbErr != nil {
		return thumb.Result{Kind: "icon"}, thumbErr
	}
	r, _ := thumbGen.Get(path) // 出错时 r 仍是图标结果；不把错误抛给界面
	return r, nil
}
