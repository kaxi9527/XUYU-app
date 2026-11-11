package projectmemory

import (
	"fmt"
	"sort"
	"strings"
)

// This human-readable view contains no file bodies. Editing this generated view
// is not a command; user decisions must be appended through the typed journal.
func renderMemory(s State, head string) []byte {
	var b strings.Builder
	clean := func(v string) string {
		return strings.NewReplacer("\\", "\\\\", "|", "\\|", "\r", " ", "\n", " ⏎ ", "<", "&lt;", ">", "&gt;", "`", "\\`", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_").Replace(v)
	}
	fmt.Fprintf(&b, "# %s · 文件管理记忆\n\n项目 ID：%s\n\n记录版本：%d · 事件摘要：%s\n\n", clean(s.Project.Name), s.Project.ID, s.Revision, head)
	state := "已开启"
	if !s.Enabled {
		state = "已暂停"
	}
	fmt.Fprintf(&b, "追踪状态：%s。最后观察：%s。\n\n", state, clean(s.LastObservedAt))
	if s.Automatic {
		b.WriteString("自动检查已授权：仅在应用运行时检查已确认的范围。暂停追踪时不执行；退出期间无法记录瞬时变化，再次运行后补扫。\n\n")
	}
	if s.LastObservedAt != "" && !s.ObservationComplete {
		b.WriteString("本轮覆盖不完整。未扫描部分保留最近已知状态，不代表本次仍然存在。\n\n")
	}
	b.WriteString("本文件由同一项目事件账本生成，不按 Agent 或模型分库。未记录的操作者为未知；文件缺失不证明删除。这里没有保存历史文件正文，不能据此保证恢复。直接编辑本文件不会改变账本或授权。\n\n## 覆盖范围\n\n")
	for _, root := range s.Scope.Roots {
		fmt.Fprintf(&b, "- 已登记目录：%s\n", clean(root))
	}
	for _, file := range s.Scope.Files {
		fmt.Fprintf(&b, "- 已登记单个文件：%s\n", clean(file))
	}
	for _, c := range s.Coverage {
		fmt.Fprintf(&b, "- %s：%s %s\n", clean(c.Path), clean(c.Status), clean(c.Reason))
	}
	if len(s.Coverage) == 0 {
		b.WriteString("尚未完成首次文件观察。\n")
	}
	b.WriteString("\n## 目录\n\n| 位置 | 类型 | 记录状态 | 内容核验 | 最近事件 |\n| --- | --- | --- | --- | --- |\n")
	files := append([]File{}, s.Files...)
	sort.Slice(files, func(i, j int) bool {
		if files[i].Fact.Path == files[j].Fact.Path {
			return files[i].ID < files[j].ID
		}
		return files[i].Fact.Path < files[j].Fact.Path
	})
	labels := map[string]string{"present": "最近记录可见", "missing": "原位置未找到", "untracked": "已停止追踪", "verified": "已计算内容摘要", "metadata_only": "仅元数据", "unavailable": "内容未核对"}
	for _, f := range files {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", clean(f.Fact.Path), f.Fact.Kind, labels[f.Status], labels[f.Fact.HashState], f.LastEvent)
	}
	b.WriteString("\n## 项目记忆条目\n\n")
	for _, n := range s.Notes {
		kind := "用户决定"
		if n.Kind == "agent_proposal" {
			kind = "Agent 建议，未视为用户决定"
		}
		fmt.Fprintf(&b, "- %s · 事件 %s · 来源 %s / %s：%s\n", kind, n.EventID, clean(n.Agent), clean(n.Model), clean(n.Text))
		if n.Evidence != "" {
			fmt.Fprintf(&b, "  依据：%s\n", clean(n.Evidence))
		}
	}
	if len(s.Undos) > 0 {
		b.WriteString("\n## WorkShelf 撤销记录\n\n以下为本次已核实的反向移动，保留原整理或核对依据；原核对未知的操作者不会因此改为 WorkShelf。历史回执不覆盖后来的观察或暂停。\n\n")
		for _, u := range s.Undos {
			fmt.Fprintf(&b, "- %s：%s → %s；文件 %s；事件 %s；撤销 %s；原操作 %s / %s；原事件 %s；原依据 %s；撤销计划 %s；物理依据 %s\n", clean(undoLabel(u)), clean(u.From), clean(u.To), u.FileID, u.EventID, u.UndoID, u.OriginalOperation, u.OriginalClaim, u.OriginalEvent, u.OriginalProofSHA, u.PlanSHA, u.PhysicalSHA)
		}
	}
	if len(s.Placements) > 0 {
		b.WriteString("\n## WorkShelf 执行记录\n\n以下是已核实的历史动作；后来的文件改动或追踪暂停不会被这些历史回执覆盖。复制记录保留独立文件身份和来源。\n\n")
		for _, p := range s.Placements {
			fmt.Fprintf(&b, "- %s：%s → %s；文件 %s → %s；事件 %s；操作 %s；物理依据 %s\n", clean(placementLabel(p)), clean(p.From), clean(p.To), p.SourceFileID, p.TargetFileID, p.EventID, p.OperationID, p.PhysicalSHA)
		}
	}
	if len(s.Reconciliations) > 0 {
		b.WriteString("\n## 中断整理核对\n\n以下为用户确认的精确位置检查。原文件动作的操作者与具体发生时间未知；核对没有搬移、删除文件或恢复原计划执行。\n\n")
		outcomes := map[string]string{"at_source": "原文件在原处", "at_target": "原文件对象在目标位置", "copy_published": "找到与暂存对象证据相符的副本", "copy_unpublished": "目标未发布，原件仍在"}
		dispositions := map[string]string{"history_only": "仅保留核对历史，目录已有更新", "applied": "已更新精确成员的目录记录", "already_observed": "已有目录记录一致"}
		for _, r := range s.Reconciliations {
			fmt.Fprintf(&b, "- %s · %s；%s；%s → %s；确认 %s；事件 %s；操作 %s；核对依据 %s\n", clean(r.ObservedAt), outcomes[r.Outcome], dispositions[r.Disposition], clean(r.From), clean(r.To), clean(r.ConfirmedAt), r.EventID, r.OperationID, r.ReviewSHA)
			if r.TemporaryPath != "" {
				fmt.Fprintf(&b, "  暂存位置仍有对象，归属以核对证据为准，未删除：%s\n", clean(r.TemporaryPath))
			}
		}
	}
	if len(s.Sources) > 0 {
		b.WriteString("\n## AI 文件来源\n\n来源来自已授权的本地记录。内容与文件快照匹配也不能证明作者身份；没有复制聊天正文。\n\n")
		for _, g := range s.Sources {
			status := "已停止"
			if g.Enabled && g.ScopeSHA == s.Scope.Digest() {
				status = "已授权"
			}
			if g.Directory {
				read, issues := 0, 0
				for _, d := range g.Documents {
					if d.Cursor.ObjectKey != "" {
						read++
					}
					if d.Problem != "" {
						issues++
					}
				}
				fmt.Fprintf(&b, "- %s · %s：会话目录 %s；发现 %d 份，已读取 %d 份，%d 份暂不可读；未映射 %d 条；%s\n", clean(g.Adapter), status, clean(g.Path), len(g.Documents), read, issues, g.Skipped, clean(g.Problem))
			} else {
				fmt.Fprintf(&b, "- %s · %s：%s；读取至第 %d 行；未映射 %d 条；%s\n", clean(g.Adapter), status, clean(g.Path), g.Cursor.Line, g.Skipped, clean(g.Problem))
			}
		}
		labels := map[string]string{"snapshot_match": "与文件快照内容匹配", "snapshot_differs": "声明与文件快照内容不同", "path_only": "只有路径关联", "not_observed": "尚未观察到对应文件"}
		start := len(s.Origins) - 30
		if start < 0 {
			start = 0
		}
		fmt.Fprintf(&b, "\n最近 %d / %d 条来源（完整记录保留在目录 JSON 与事件账本）：\n", len(s.Origins)-start, len(s.Origins))
		for _, o := range s.Origins[start:] {
			fmt.Fprintf(&b, "- %s · %s；%s；会话 %s；任务 %s；依据 %s:%d；事件 %s\n", clean(o.Claim.Agent), clean(o.Claim.Path), labels[o.Binding], clean(o.Claim.Session), clean(o.Claim.Task), clean(o.SourcePath), o.Claim.Line, o.EventID)
		}
	}
	b.WriteString("\n事件段位于 events/。目录 JSON 位于 index.json；两种视图必须与上面的记录版本一致。\n")
	return []byte(b.String())
}
