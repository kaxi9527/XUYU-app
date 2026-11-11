package put

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/config"
)

// ApproveOptions 是一次「批准」的参数。
type ApproveOptions struct {
	Config  config.Config
	Project string
	Task    string // 任务目录名，如 2026-11-05-双11主图
	By      string // 谁批准的（默认 $USER）
	Now     time.Time
}

// ApproveResult 是批准的结果。
type ApproveResult struct {
	OutputDir string   `json:"output_dir"`
	Files     []string `json:"files"`
	IndexPath string   `json:"index_path"`
	NotePath  string   `json:"note_path,omitempty"`
	Changed   []string `json:"changed_since_put,omitempty"` // 入库后内容又变过的文件（照样批准，但提示）
}

// HumanShard 是人工操作（批准等）写的索引分片名。
const HumanShard = "人工"

// Approve 把 成品/<任务目录>/ 下的文件标成「已交」：往索引追加一行（不改旧行），
// 在任务 说明.md 末尾追加一行状态，再写活动记录。不移动、不改任何成品文件。
func Approve(opt ApproveOptions) (*ApproveResult, error) {
	cfg := opt.Config
	n := cfg.Names
	if err := CheckName("项目名", opt.Project); err != nil {
		return nil, err
	}
	if err := CheckName("任务目录名", opt.Task); err != nil {
		return nil, err
	}
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.In(cfg.Location())
	by := opt.By
	if by == "" {
		by = os.Getenv("USER")
	}
	if by == "" {
		by = "用户"
	}
	projDir := filepath.Join(cfg.Root, opt.Project)
	outDir := filepath.Join(projDir, n.Outputs, opt.Task)
	res := &ApproveResult{OutputDir: outDir}
	if fi, err := os.Stat(outDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("没有这个成品文件夹：%s", outDir)
	}
	err := filepath.WalkDir(outDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && p != outDir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			res.Files = append(res.Files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(res.Files) == 0 {
		return nil, errors.New("成品文件夹是空的，没东西可批准")
	}

	// 找入库时登记的那一行，带上工种、模板等信息。
	known := map[string]Entry{}
	for _, e := range ReadIndexDir(filepath.Join(cfg.Root, n.Control, n.IndexShards), cfg.Root) {
		if e["_abs"] != "" {
			known[e["_abs"]] = e
		}
	}
	notePath := filepath.Join(projDir, n.Tasks, opt.Task, "说明.md")
	if _, err := os.Stat(notePath); err == nil {
		res.NotePath = notePath
	}
	var rows []Row
	var recs []activity.Record
	for _, f := range res.Files {
		fi, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		sum, err := FileSHA256(f)
		if err != nil {
			return nil, err
		}
		e := known[f]
		if e != nil && e["sha256"] != "" && e["sha256"] != sum {
			res.Changed = append(res.Changed, f)
		}
		agent := ""
		if e != nil {
			agent = e["负责智能体"]
		}
		rows = append(rows, Row{
			Date: now.Format("2006-01-02"), Project: opt.Project, WorkType: e["工种"], Template: e["模板"], Task: opt.Task,
			Kind: e["类型"], Version: e["版本标识"], Status: StatusDone, Path: relTo(cfg.Root, f), NotePath: relTo(cfg.Root, res.NotePath),
			Agent: agent, Sensitive: e["敏感"], Note: "批准人：" + by, Time: now.Format(time.RFC3339), Source: e["原路径"],
			Size: fi.Size(), SHA256: sum,
		})
		recs = append(recs, activity.Record{Time: now.Format(time.RFC3339), Path: f, SHA256: sum, Agent: agent,
			WorkType: e["工种"], Project: opt.Project, Task: opt.Task, Source: activity.SourceApprove, Action: "批准",
			Status: StatusDone, Note: "批准人：" + by})
	}
	res.IndexPath = filepath.Join(cfg.Root, n.Control, n.IndexShards, n.IndexFile+"."+HumanShard+".csv")
	if _, err := AppendRows(res.IndexPath, rows); err != nil {
		return res, err
	}
	if res.NotePath != "" {
		if err := appendLine(res.NotePath, fmt.Sprintf("- 状态：%s（批准于 %s，批准人 %s）", StatusDone, now.Format("2006-01-02 15:04"), by)); err != nil {
			return res, fmt.Errorf("索引已登记，但 说明.md 没写上：%w", err)
		}
	}
	if err := activity.Append(activity.Dir(cfg), cfg.Location(), recs...); err != nil {
		return res, fmt.Errorf("索引已登记，但活动记录没写上：%w", err)
	}
	return res, nil
}

func appendLine(path, line string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	pre := ""
	if len(b) > 0 && b[len(b)-1] != '\n' {
		pre = "\n"
	}
	if _, err := f.WriteString(pre + line + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
