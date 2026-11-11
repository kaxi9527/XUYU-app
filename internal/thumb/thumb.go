// Package thumb 给文件生成缩略图（只读源文件）。
//
// 顺序：文本/代码/Markdown → 前几行文字；Mac 上其他类型 → 系统 QuickLook（qlmanage -t，无界面）；
// 各平台都能用的退路 → Go 自带解码 PNG/JPEG/GIF 再缩小；都不行 → 文件类型图标（前端画）。
// 缓存放在应用缓存目录，按 路径+大小+修改时间+尺寸 做键，源文件一改就自然换新。
// 敏感文件（密钥、凭据、资料/敏感 下的）一律只给图标，不读内容。
package thumb

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kaxi9527/WorkShelf/internal/scan"
)

var _ = gif.Decode // 注册 GIF 解码器
var _ = jpeg.Decode

// Result 是一个文件的缩略图。
type Result struct {
	Kind  string   `json:"kind"` // image / text / icon
	Data  string   `json:"data,omitempty"`
	Lines []string `json:"lines,omitempty"`
	Ext   string   `json:"ext"`
	Label string   `json:"label"`         // 图标上显示的类型字样
	Group string   `json:"group"`         // 图标分类：image/pdf/doc/sheet/slide/code/text/archive/video/audio/design/lock/file
	Via   string   `json:"via,omitempty"` // quicklook / go / text：调试用
}

// Generator 生成并缓存缩略图。
type Generator struct {
	CacheDir string
	Px       int
	Home     string
	slots    chan string
	// UseQuickLook 默认在 macOS 上为 true。
	UseQuickLook bool
}

// New 建一个生成器。cacheDir 为空时用系统缓存目录/WorkShelf/thumbs。
func New(cacheDir, home string, px int) (*Generator, error) {
	if cacheDir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		cacheDir = filepath.Join(base, "WorkShelf", "thumbs")
	}
	if px <= 0 {
		px = 160
	}
	g := &Generator{CacheDir: cacheDir, Px: px, Home: home, slots: make(chan string, 3), UseQuickLook: runtime.GOOS == "darwin"}
	for i := 1; i <= 3; i++ {
		d := filepath.Join(cacheDir, fmt.Sprintf("work-%d", i))
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
		g.slots <- d
	}
	return g, nil
}

var textExts = map[string]bool{
	".md": true, ".markdown": true, ".txt": true, ".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".mjs": true, ".py": true, ".css": true, ".scss": true, ".html": true, ".htm": true, ".vue": true, ".svelte": true,
	".sh": true, ".zsh": true, ".sql": true, ".swift": true, ".rs": true, ".java": true, ".kt": true, ".c": true,
	".h": true, ".cpp": true, ".rb": true, ".php": true, ".csv": true, ".tsv": true, ".log": true, ".mdc": true,
}

// 配置类文件常带密钥：只给图标。
var configExts = map[string]bool{".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".conf": true,
	".cfg": true, ".env": true, ".properties": true, ".plist": true, ".jsonl": true}

var groups = map[string][]string{
	"image":   {".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic", ".tif", ".tiff", ".bmp", ".avif", ".svg", ".ico"},
	"pdf":     {".pdf"},
	"doc":     {".doc", ".docx", ".pages", ".rtf", ".odt"},
	"sheet":   {".xls", ".xlsx", ".numbers", ".ods", ".csv", ".tsv"},
	"slide":   {".ppt", ".pptx", ".key", ".odp"},
	"archive": {".zip", ".tar", ".gz", ".tgz", ".7z", ".rar", ".dmg", ".xz", ".bz2"},
	"video":   {".mp4", ".mov", ".m4v", ".avi", ".mkv", ".webm"},
	"audio":   {".mp3", ".wav", ".m4a", ".aac", ".flac", ".ogg"},
	"design":  {".fig", ".sketch", ".psd", ".ai", ".xd", ".prproj", ".aep", ".drp"},
}

// GroupOf 返回扩展名的图标分类。
func GroupOf(ext string) string {
	ext = strings.ToLower(ext)
	for g, es := range groups {
		for _, e := range es {
			if e == ext {
				return g
			}
		}
	}
	if textExts[ext] {
		if ext == ".md" || ext == ".markdown" || ext == ".txt" || ext == ".log" || ext == ".mdc" {
			return "text"
		}
		return "code"
	}
	if configExts[ext] {
		return "code"
	}
	return "file"
}

func label(ext string) string {
	l := strings.ToUpper(strings.TrimPrefix(ext, "."))
	if l == "" {
		return "文件"
	}
	if utf8.RuneCountInString(l) > 4 {
		l = string([]rune(l)[:4])
	}
	return l
}

// Sensitive 判断是不是不该读内容的文件。
func Sensitive(path string) bool {
	if scan.IsSensitiveName(filepath.Base(path)) {
		return true
	}
	p := filepath.ToSlash(path)
	return strings.Contains(p, "/敏感/") || strings.Contains(p, "/sensitive/")
}

var secretLine = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|passw|bearer|authorization|private[_-]?key|sk-[a-z0-9]{8,}|AKIA[0-9A-Z]{12,})`)

// Get 返回一个文件的缩略图。出错时也尽量给出图标结果。
func (g *Generator) Get(path string) (Result, error) {
	p := filepath.Clean(path)
	ext := strings.ToLower(filepath.Ext(p))
	icon := Result{Kind: "icon", Ext: ext, Label: label(ext), Group: GroupOf(ext)}
	if g.Home != "" && !strings.HasPrefix(p, filepath.Clean(g.Home)+string(filepath.Separator)) {
		return icon, errors.New("只看主目录里的文件")
	}
	fi, err := os.Stat(p)
	if err != nil {
		return icon, err
	}
	if fi.IsDir() {
		icon.Group, icon.Label = "folder", "文件夹"
		return icon, nil
	}
	if !fi.Mode().IsRegular() {
		return icon, nil
	}
	if Sensitive(p) {
		icon.Group, icon.Label = "lock", "敏感"
		return icon, nil
	}
	if textExts[ext] {
		if r, ok := textPreview(p, ext); ok {
			r.Label, r.Group = icon.Label, icon.Group
			return r, nil
		}
		return icon, nil
	}
	if configExts[ext] || fi.Size() == 0 {
		return icon, nil
	}
	key := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%d", p, fi.Size(), fi.ModTime().UnixNano(), g.Px)))
	k := hex.EncodeToString(key[:])
	cached := filepath.Join(g.CacheDir, k[:2], k+".png")
	if b, err := os.ReadFile(cached); err == nil && len(b) > 0 {
		return Result{Kind: "image", Data: dataURL(b), Ext: ext, Label: icon.Label, Group: icon.Group, Via: "cache"}, nil
	}
	none := strings.TrimSuffix(cached, ".png") + ".none"
	if _, err := os.Stat(none); err == nil {
		return icon, nil // 上次生成不了，别再等一次
	}
	var png []byte
	via := ""
	if g.UseQuickLook && quickLookable[icon.Group] && fi.Size() < 2<<30 {
		png = g.quickLook(p)
		via = "quicklook"
	}
	if png == nil && icon.Group == "image" && fi.Size() < 40<<20 {
		png = goThumb(p, g.Px)
		via = "go"
	}
	if png == nil {
		if os.MkdirAll(filepath.Dir(none), 0o700) == nil {
			_ = os.WriteFile(none, nil, 0o600)
		}
		return icon, nil
	}
	if err := os.MkdirAll(filepath.Dir(cached), 0o700); err == nil {
		tmp := fmt.Sprintf("%s.tmp-%d", cached, time.Now().UnixNano())
		if os.WriteFile(tmp, png, 0o600) == nil {
			_ = os.Rename(tmp, cached)
		}
	}
	return Result{Kind: "image", Data: dataURL(png), Ext: ext, Label: icon.Label, Group: icon.Group, Via: via}, nil
}

// QuickLook 只用在它确实能出图的类型上；压缩包、音频、未知类型会一直等到超时。
var quickLookable = map[string]bool{"image": true, "pdf": true, "doc": true, "sheet": true, "slide": true, "video": true, "design": true}

func dataURL(b []byte) string { return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b) }

func textPreview(p, ext string) (Result, bool) {
	f, err := os.Open(p)
	if err != nil {
		return Result{}, false
	}
	defer f.Close()
	buf := make([]byte, 2048)
	n, _ := f.Read(buf)
	b := buf[:n]
	if bytes.IndexByte(b, 0) >= 0 {
		return Result{}, false
	}
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		l = strings.TrimRight(strings.ReplaceAll(l, "\t", "  "), " ")
		if len(lines) == 0 && strings.TrimSpace(l) == "" {
			continue
		}
		if secretLine.MatchString(l) {
			l = "••••（这一行可能有密钥，不显示）"
		}
		if utf8.RuneCountInString(l) > 60 {
			l = string([]rune(l)[:60]) + "…"
		}
		lines = append(lines, l)
		if len(lines) == 7 {
			break
		}
	}
	if len(lines) == 0 {
		return Result{}, false
	}
	return Result{Kind: "text", Lines: lines, Ext: ext, Via: "text"}, true
}

func (g *Generator) quickLook(p string) []byte {
	slot := <-g.slots
	defer func() { g.slots <- slot }()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/qlmanage", "-t", "-s", fmt.Sprint(g.Px*2), "-o", slot, p)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Run(); err != nil {
		return nil
	}
	out := filepath.Join(slot, filepath.Base(p)+".png")
	b, err := os.ReadFile(out)
	if err != nil || len(b) == 0 {
		return nil
	}
	// 读完改名成 last.png 留在工作目录里（下次覆盖），不删文件。
	_ = os.Rename(out, filepath.Join(slot, "last.png"))
	return b
}

func goThumb(p string, px int) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width*cfg.Height > 60_000_000 {
		return nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	dst := Scale(src, px*2)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil
	}
	return buf.Bytes()
}

// Scale 把图片按长边缩到 max 像素（区域平均，只缩不放）。
func Scale(src image.Image, max int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return src
	}
	nw, nh := max, h*max/w
	if h > w {
		nw, nh = w*max/h, max
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+(y+1)*h/nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+(x+1)*w/nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, gg, bb, a, n uint64
			stepY, stepX := 1, 1
			if y1-y0 > 8 {
				stepY = (y1 - y0) / 8
			}
			if x1-x0 > 8 {
				stepX = (x1 - x0) / 8
			}
			for yy := y0; yy < y1; yy += stepY {
				for xx := x0; xx < x1; xx += stepX {
					c := color.NRGBAModel.Convert(src.At(xx, yy)).(color.NRGBA)
					r, gg, bb, a, n = r+uint64(c.R), gg+uint64(c.G), bb+uint64(c.B), a+uint64(c.A), n+1
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{uint8(r / n), uint8(gg / n), uint8(bb / n), uint8(a / n)})
		}
	}
	return dst
}
