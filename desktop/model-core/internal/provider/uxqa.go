package provider

// UXQA uses the migrated protocols with its own account pool. No agent discovery,
// account switching, plugin registry or external application's account state.
import (
	"context"
	"errors"
	"fmt"
	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/catalog"
	"io/fs"
	"slices"
	"strings"
)

var uxqaAgents = []string{"codex", "claude", "copilot", "gemini", "antigravity"}

func UXQASupported(agent string) bool { return slices.Contains(uxqaAgents, agent) }

func UXQACapabilities() []map[string]any {
	out := []map[string]any{}
	for _, id := range uxqaAgents {
		cli, missing := missingCLI(id)
		item := map[string]any{"id": id, "available": !missing}
		if missing {
			item["requirement"] = cli.Name
		}
		out = append(out, item)
	}
	return out
}

func UXQALogins() []Login { return uxqaLogins("") }

func WorkShelfAccountStorageError() error {
	b, err := appdir.WorkShelfReadFile(loginsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !validLogins(b) {
		return fmt.Errorf("账号记录无法读取，原文件已保留")
	}
	return nil
}

// WorkShelfSelectLogin atomically selects one account within a subscription
// provider. Other providers and external applications remain unchanged.
func WorkShelfSelectLogin(agent, user string) error {
	if !UXQASupported(agent) {
		return fmt.Errorf("不支持的订阅类型")
	}
	defer ForgetAccounts()
	loginsMu.Lock()
	defer loginsMu.Unlock()
	ls := readLogins()
	found := false
	for _, l := range ls {
		if l.Agent == agent && strings.EqualFold(l.User, user) {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("账号不存在")
	}
	for i := range ls {
		if ls[i].Agent == agent {
			ls[i].On = strings.EqualFold(ls[i].User, user)
		}
	}
	return writeLogins(ls)
}

func uxqaLogins(agent string) []Login {
	loginsMu.Lock()
	defer loginsMu.Unlock()
	out := []Login{}
	for _, l := range readLogins() {
		if !UXQASupported(l.Agent) || agent != "" && agent != l.Agent {
			continue
		}
		out = append(out, Login{Agent: l.Agent, User: l.User, Plan: l.Plan, Seen: l.Seen, On: l.On, Lapsed: l.Lapsed})
	}
	return out
}

// Removal deletes UXQA's saved credential, never another application's login.
func UXQAEditLogin(agent, user string, on *bool, remove bool) (err error) {
	if !UXQASupported(agent) {
		return fmt.Errorf("不支持的订阅类型")
	}
	defer ForgetAccounts()
	loginsMu.Lock()
	defer loginsMu.Unlock()
	ls := readLogins()
	for i := range ls {
		if ls[i].Agent != agent || !strings.EqualFold(ls[i].User, user) {
			continue
		}
		if remove {
			if agent == "claude" {
				if err := workShelfRemoveClaude(user); err != nil {
					return fmt.Errorf("账号目录未能安全移除，授权记录保留")
				}
			}
			ls = slices.Delete(ls, i, i+1)
		} else if on != nil {
			ls[i].On = *on
		} else {
			return fmt.Errorf("缺少账号操作")
		}
		return writeLogins(ls)
	}
	return fmt.Errorf("账号不存在")
}

func uxqaAccounts() []Provider {
	out := []Provider{}
	for _, agent := range uxqaAgents {
		if ps := uxqaAccountPool(agent); len(ps) > 0 {
			out = append(out, ps[0])
		}
	}
	return out
}

func uxqaAccountPool(agent string) []Provider {
	loginsMu.Lock()
	ls := slices.Clone(readLogins())
	loginsMu.Unlock()
	out := []Provider{}
	for _, l := range ls {
		if l.Agent != agent || !l.On || l.Lapsed != "" || len(l.Auth) == 0 {
			continue
		}
		user := l.User
		before := len(out)
		switch agent {
		case "claude":
			a := &Account{Agent: agent, User: user, Plan: l.Plan}
			a.token = func(context.Context) (string, error) { return claudeSavedDir(user) }
			out = append(out, claudeProvider(a))
		case "codex":
			a := &Account{Agent: agent, User: user, Plan: l.Plan, Stream: true, body: codexBody, models: catalog.Codex, listedOnly: true}
			a.token = func(ctx context.Context) (string, error) {
				token, _, err := savedLoginToken(ctx, agent, user)
				return token, err
			}
			a.sign = codexSign(func(ctx context.Context) (string, string, error) { return savedLoginToken(ctx, agent, user) })
			a.renew = func(ctx context.Context, stale string) (string, string, error) {
				return renewSavedLoginFrom(ctx, agent, user, stale)
			}
			a.fetch = func(ctx context.Context) ([]catalog.Model, error) {
				ms, err := codexModels(ctx, a.sign)
				if err != nil {
					return nil, err
				}
				// Fetching alone does not make subscription models selectable:
				// Exposed/Resolve read the per-account and provider live catalogs.
				if err := catalog.SaveLive(accountModels("codex", user), CodexBase, ms); err != nil {
					return nil, err
				}
				codexFetchSaved(ctx)
				ms = codexPoolModels(ms)
				return ms, catalog.SaveLive("codex", CodexBase, ms)
			}
			out = append(out, Provider{ID: agent, Name: "ChatGPT / Codex", Responses: CodexBase, Account: a})
		case "copilot":
			if a, ok := copilotSaved(l); ok {
				out = append(out, copilotProvider(a, l.Plan))
			}
		case "gemini", "antigravity":
			if a, ok := googleSaved(agent, l); ok {
				out = append(out, googleProvider(a, l.Plan))
			}
		}
		if len(out) > before {
			out[len(out)-1].Account.workShelfGeneration = workShelfSavedGeneration(l)
			out[len(out)-1].Account.workShelfID = workShelfSavedID(l)
		}
	}
	return out
}
