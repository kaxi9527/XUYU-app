// Package worktype 是工种表（v4，88 行，23 个行业）。表随程序一起编译进去。
package worktype

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed worktypes.json
var raw []byte

// WorkType 是工种表的一行。
type WorkType struct {
	Industry  string   `json:"industry"`
	Name      string   `json:"name"`
	Template  string   `json:"template"`
	Aux       []string `json:"aux"`
	Merged    string   `json:"merged"`
	Sensitive bool     `json:"sensitive"`
}

// Label 是「行业·工种」，写进索引和说明.md 的就是这个。
func (w WorkType) Label() string { return w.Industry + "·" + w.Name }

var all []WorkType

func init() {
	if err := json.Unmarshal(raw, &all); err != nil {
		panic("worktypes.json 坏了：" + err.Error())
	}
}

// All 返回整张表（副本）。
func All() []WorkType { return append([]WorkType(nil), all...) }

func norm(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "／", "/")
	return strings.ReplaceAll(s, "・", "·")
}

// Resolve 认这些写法：「行业·工种」「行业/工种」「工种」「合并后的名字」。
// 合并名对应多行时，要求写清行业。
func Resolve(s string) (WorkType, error) {
	q := norm(s)
	if q == "" {
		return WorkType{}, fmt.Errorf("工种不能为空")
	}
	for _, w := range all {
		if q == w.Label() || q == w.Industry+"/"+w.Name {
			return w, nil
		}
	}
	// 「行业·合并名」
	for _, sep := range []string{"·", "/"} {
		if i := strings.Index(q, sep); i > 0 {
			ind, rest := q[:i], q[i+len(sep):]
			for _, w := range all {
				if w.Industry == ind && w.Merged == rest {
					return w, nil
				}
			}
		}
	}
	var hits []WorkType
	for _, w := range all {
		if q == w.Name {
			return w, nil
		}
		if q == w.Merged {
			hits = append(hits, w)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	if len(hits) > 1 {
		var ls []string
		for _, h := range hits {
			ls = append(ls, h.Label())
		}
		return WorkType{}, fmt.Errorf("「%s」对应好几个工种，请写清楚：%s", q, strings.Join(ls, "、"))
	}
	return WorkType{}, fmt.Errorf("工种表里没有「%s」（用 workshelf worktypes 查；确实是新工种就同时给 --模板 Txx）", q)
}

// Industries 返回行业列表（按表里第一次出现的顺序）。
func Industries() []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range all {
		if !seen[w.Industry] {
			seen[w.Industry] = true
			out = append(out, w.Industry)
		}
	}
	return out
}

var templateKind = map[string]string{
	"T01": "图片组", "T02": "视频", "T03": "文档", "T04": "报表", "T05": "报表", "T06": "文档",
	"T07": "文档", "T08": "报表", "T09": "代码版本", "T10": "文档", "T11": "文档", "T12": "音频",
	"T13": "网页", "T14": "工作流", "T15": "看板",
}

// KindOf 由模板编号给出索引里的「类型」。
func KindOf(template string) string {
	if k, ok := templateKind[strings.ToUpper(template)]; ok {
		return k
	}
	return "文件"
}

// ValidTemplate 检查 T01…T15。
func ValidTemplate(t string) bool {
	_, ok := templateKind[strings.ToUpper(t)]
	return ok
}

// Templates 返回所有模板编号（排好序）。
func Templates() []string {
	var out []string
	for k := range templateKind {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
