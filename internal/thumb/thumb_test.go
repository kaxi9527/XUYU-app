package thumb

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	home := t.TempDir()
	g, err := New(filepath.Join(home, "cache"), home, 64)
	if err != nil {
		t.Fatal(err)
	}
	g.UseQuickLook = false
	w := func(name, s string) string {
		p := filepath.Join(home, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	md := w("a/说明.md", "\n# 标题\n正文一行\napi_key = sk-abcdefghijklmnop\n")
	r, err := g.Get(md)
	if err != nil || r.Kind != "text" || r.Lines[0] != "# 标题" || !strings.Contains(r.Lines[2], "不显示") {
		t.Fatalf("文本预览不对：%+v %v", r, err)
	}
	if r, _ := g.Get(w("a/config.json", `{"token":"x"}`)); r.Kind != "icon" {
		t.Errorf("配置文件应只给图标：%+v", r)
	}
	if r, _ := g.Get(w("a/资料/敏感/身份证.md", "秘密")); r.Kind != "icon" || r.Group != "lock" {
		t.Errorf("敏感目录应只给锁图标：%+v", r)
	}
	if r, _ := g.Get(w("a/id_rsa", "x")); r.Group != "lock" {
		t.Errorf("密钥文件应只给锁图标：%+v", r)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	for x := 0; x < 400; x++ {
		for y := 0; y < 200; y++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 100, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	pp := w("a/图.png", buf.String())
	r, err = g.Get(pp)
	if err != nil || r.Kind != "image" || !strings.HasPrefix(r.Data, "data:image/png;base64,") {
		t.Fatalf("图片缩略图不对：%+v %v", r.Kind, err)
	}
	r2, _ := g.Get(pp)
	if r2.Via != "cache" {
		t.Errorf("第二次应走缓存：%s", r2.Via)
	}
	if s := Scale(img, 128).Bounds(); s.Dx() != 128 || s.Dy() != 64 {
		t.Errorf("缩放尺寸不对：%v", s)
	}
	if r, _ := g.Get(w("a/b.zip", "PK")); r.Kind != "icon" || r.Group != "archive" || r.Label != "ZIP" {
		t.Errorf("压缩包图标不对：%+v", r)
	}
	if _, err := g.Get("/etc/hosts"); err == nil {
		t.Error("主目录外的文件应拒绝")
	}
	if r, _ := g.Get(filepath.Join(home, "a")); r.Group != "folder" {
		t.Errorf("文件夹图标不对：%+v", r)
	}
}
