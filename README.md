# 序屿 XUYU（原 WorkShelf）

把分散的项目文件归拢到清晰的项目目录，追踪文件变化，并按需要接入 AI 工具。桌面整理会搬迁文件，并保留可用于撤销的记录。

[English](README.en.md) · [安装与更新 / Install & update](docs/INSTALL.md) · [更新说明 / Changelog](CHANGELOG.md) · [参与贡献 / Contributing](CONTRIBUTING.md) · [下载 / Releases](https://github.com/kaxi9527/XUYU-app/releases)

首次使用会说明需要读取的位置并请求系统授权。可在「设置 → 文件访问权限」重新检查；macOS 通常会记住授权，但权限可能被系统或用户更改。新增的 AI 会话目录需要另行授权。

安装包按系统和芯片分别提供；请以 Releases 页面实际列出的文件及其说明为准。项目不通过应用商店分发。当前界面主要为中文。

## 当前核心流程：按工种整理项目

桌面首页「整理项目」将项目识别、工种确认、目录预览和实际归位连在一起：

1. 选择已识别的项目，检查来自桌面、下载、文稿等位置的相关文件；可以补充或移除来源。重新识别会更新扫描结果，权限或数量限制会明确显示。
2. 采用识别建议或自行确认工种，选择设计、视频、开发、数据、研究、音频、文档等目录模板，也可以从空白开始。模板的文件夹可以改名、增删、分层，并指定默认用途；无需绑定现有工种表才能整理。
3. 查看每项的来源、目标和理由。拿不准的默认不选；可以修改用途目录，再重新生成预览。识别基于本地文件名、目录和工程标志，不会把仅有图片扩展名当成明确业务工种。
4. 确认后搬迁文件，完成后原位置不留副本。macOS 跨磁盘搬迁先传输并核对内容，再移除源位置；传输失败或核对不一致时保留现场并报告。执行绑定本次预览，不覆盖已有文件。
5. 恢复清单在第一项移动之前落盘。「撤销本次整理」将搬迁的文件搬回原位；已创建的空文件夹和整理记录保留。旧版跨盘复制记录仍按原规则保留副本供检查。

自定义规则支持扩展名（例如 `png,jpg`）、文件名包含、来源路径包含，按显示顺序采用第一条命中规则；预览中的逐项手动调整优先。修改后必须重新预览。模板可另存复用，同名保存会提示换名；成功整理后会在项目中保存 `.workshelf-layout.json`，下次继续使用。

授权 Codex 会话后，自动整理可识别专用 `apply_patch` 工具以及终端中单独执行、带引号 heredoc 的 `apply_patch`；终端调用须与成功退出和补丁结果配对。其他未映射的会话记录会在设置中显示覆盖提示，任意脚本和后台命令尚不能完整识别。

「浏览电脑文件」显示主目录或总文件夹中的文件列表，支持逐层进入和补充整理来源。「显示隐藏文件」默认关闭，开启后可查看 `.codex` 等点目录。隐藏配置不自动重排；删除隐藏路径必须确认专门的风险提醒，后端也校验确认标记。删除移入废纸篓或删除暂存，预先保存恢复清单，并提供撤销；同名目标不会覆盖。

代码、剪辑工程连同内部相对路径整体处理。Git 仓库、依赖环境、软链接、疑似凭据及超出本次检查上限的工程保留原位。默认单次最多 64 个来源、5000 个遍历条目、15 秒预览；工程快照也有条目上限。大文件主要核对文件列表、大小和修改时间，小文件另核对内容摘要；这不是对并发外部写入的文件系统快照。

同盘执行使用操作系统的原子防覆盖重命名，macOS/Linux 构建需要 CGO；macOS 跨盘传输与源位置移除不是原子操作。其他平台当前不支持跨盘搬迁，会保留原文件并报告失败。

以下为最初的命令行功能与目录规范，既有命令保持兼容；桌面按工种整理使用上面的目录方案。

把散落在桌面、下载、文稿和各种项目文件夹里的东西，收进**一个总文件夹**（默认 `~/Work`），一个项目一个文件夹。

第一版只做三件事，而且都很保守：

| 命令 | 做什么 | 会不会改你的文件 |
|---|---|---|
| `workshelf scan` | 扫一遍，认出哪些文件夹是项目、哪些是同一个项目的不同版本 | **不会**，只读 |
| `workshelf plan` | 生成整理计划（Markdown 给人看，JSON 给程序用） | **不会**，只写计划文件 |
| `workshelf put` | 把文件**复制**进某个项目的任务目录，核对 sha256，登记到资产索引 | 只新建，**不移动、不删除、不覆盖** |

桌面的删除功能将用户内容移入废纸篓或删除暂存；整理功能不会永久删除用户内容。

## 安装

从源码构建需要遵循 `go.mod` 和 `desktop/go.mod` 中声明的 Go 版本。以下命令仅构建命令行工具，桌面版请参阅[安装说明](docs/INSTALL.md)。

```sh
go build -o workshelf ./cmd/workshelf
```

可选：装上 [gitleaks](https://github.com/gitleaks/gitleaks)（`brew install gitleaks`），`put` 会在复制前扫一遍文件内容里的密钥。

### 工作者反对自动整理

macOS 应用包附带命令行程序，不需要另行安装。若应用位于 `~/Applications`，工作者可先查看项目的整理记录，再用记录 ID 反对其中一次：

```sh
"$HOME/Applications/WorkShelf.app/Contents/Resources/workshelf" auto list --project "项目名"
"$HOME/Applications/WorkShelf.app/Contents/Resources/workshelf" auto reject 整理记录ID --agent claude
```

`auto list --json` 还会列出来源文件，便于定位批次。反对会立即按该批次回执恢复文件；WorkShelf 运行后同步项目记忆。会话目录授权默认只提取文件操作；设置中可单独同意在本机识别授权后新增的明确用户短句，例如“撤销这次整理”。还可再次单独授权模型理解更多说法：确认框会显示接收方，授权后，与可恢复整理关联的会话新增用户消息及项目名会发送给该模型，可能产生费用。WorkShelf 不另存消息正文；更换模型、账号或会话范围需重新确认。只有能关联同一会话中唯一一批可恢复的整理时才自动恢复；对应多批时显示提醒，让用户选择。它不会主动通知工作者；已接入的旧规则不会自动增加上述命令说明。

## 目标结构

```
~/Work/
├── _总控/                     # 唯一的非项目目录
│   ├── 00-总说明.md
│   ├── 资产索引分片/资产索引.<登记者>.csv
│   ├── 整理日志/              # plan 默认写到这里
│   ├── 待分类清单.md
│   └── 归档/
└── <项目>/
    ├── 00-说明.md
    ├── 任务/YYYY-MM-DD-任务名/  # 过程文件，put 放这里
    ├── 成品/任务名/              # 给人看的定稿
    ├── 资料/
    └── 代码/                    # 代码项目的各个版本
```

目录名默认是中文，可以在配置里改成英文（`"names_preset": "en"`）或者逐个改。

## 用法

### scan：只读扫描

```sh
workshelf scan                 # 扫默认范围：桌面、下载、文稿、~/Projects、主目录第一层
workshelf scan ~/某个文件夹     # 只扫指定的地方
workshelf scan --volumes       # 也扫 /Volumes 下的外置盘（默认不扫）
workshelf scan --json --out 扫描.json
```

怎么认项目：

- 有 `.git`、`package.json`、`go.mod`、`pyproject.toml`、`Cargo.toml`、`Makefile`、`*.xcodeproj` 等就是代码项目；
- 剪辑工程（`.prproj`、`.fcpbundle`、`.aep`、`.drp` 等）、一个文件夹里 ≥10 张图，也算项目；
- `AGENTS.md`、`CLAUDE.md`、`.cursor`、`00-说明.md` 这类只算弱标志，扫描根本身只有弱标志时不算项目。

怎么认「同一个项目的不同版本」：

- git 仓库按 origin（统一成 `host/owner/repo`）认；没有 origin 就按共享的 `.git` 目录认。worktree 和主仓自动归一组。
- 手工副本：名字去掉「副本 / copy / -2 / (1) / -v3 / 日期」之类的后缀后相同，顶层内容重合度（Jaccard）≥ 0.5 且至少 3 个相同条目。
- 两个**不同**的 git 仓库永远不会被并成一组，哪怕名字一样。

扫描时：不跟随软链接；跳过 `Library`、`node_modules`、各种缓存和点目录；跳过总文件夹自己；`.env`、私钥、`*token*` 这类文件只计数，不列名字、不读内容。有文件夹数、文件数和时间上限，到了上限会明确标「没扫完」。

查未提交改动用的是 `git --no-optional-locks status --porcelain`，不会改仓库状态；不想跑可以加 `--no-git-status`。

### plan：只出计划

```sh
workshelf plan                       # 先扫再出计划
workshelf plan --from 扫描.json       # 用已有的扫描结果
workshelf plan --out ~/Work/WorkShelf/任务/某天
```

计划里每一条都写着原路径、新路径、理由和风险。有下面这些情况的条目标「只登记不动」：

- 有未提交改动 / git 状态查不了
- 是 worktree、带着 worktree 的主仓、被锁定的 worktree、子模块
- 最近 14 天改过（活跃）
- 目标位置已经有东西

跨卷（例如外置盘到内置盘）会单独标出来。认不出属于哪个项目的散落文件进「待分类清单」，原地不动。计划文件不会覆盖旧的，重名就加 `-02`。

第一版**没有**执行计划的命令。

### put：复制入库

```sh
workshelf put <项目> <任务> <文件...> [--new-project] [--append] [--agent 名字]
workshelf put 官网改版 首页终稿 ~/Downloads/首页.png ~/Downloads/说明.pdf
```

- 存到 `~/Work/<项目>/任务/<洛杉矶时区当天日期>-<任务>/`；当天同名任务目录已存在就新建 `-02`（加 `--append` 则往原目录里追加）；
- 文件重名加 `-02`，**绝不覆盖**；
- 复制完从磁盘重读，核对 sha256；对不上就报错，已复制的文件留着让你检查；
- 源文件原样保留；
- 登记到 `_总控/资产索引分片/资产索引.<登记者>.csv`（UTF-8 带 BOM，Excel 直接打开不乱码；写入时加文件锁，多个程序同时写也不会乱）；
- 文件名像密钥（`.env`、`*.pem`、`id_rsa`……）直接拒绝；
- PATH 上有 gitleaks 就先扫，发现疑似密钥就拒绝，gitleaks 自己出错也拒绝；没装 gitleaks 会明确提醒；
- 项目不存在时要加 `--new-project` 才会新建（会建好 `00-说明.md`、`任务/`、`成品/`、`资料/`）。

命令输出就是存好的路径，一行一个。

### config

```sh
workshelf config          # 看当前配置
workshelf config --init   # 写一份默认配置（已存在就不写）
```

配置文件默认在 `~/.config/workshelf/config.json`，也可以用 `--config` 或环境变量 `WORKSHELF_CONFIG` 指定。`~/` 开头的路径会自动展开。

```json
{
  "root": "~/Work",
  "timezone": "America/Los_Angeles",
  "scan_paths": ["~/Desktop", "~/Downloads", "~/Documents", "~/Projects", "~"],
  "scan_volumes": false,
  "home_depth": 1,
  "max_depth": 6,
  "max_dirs": 40000,
  "max_files": 20000,
  "timeout_seconds": 60,
  "active_days": 14,
  "extra_skip": [],
  "git_status": true,
  "names_preset": "zh",
  "names": { "tasks": "任务", "outputs": "成品", "materials": "资料" }
}
```

## 开发

```sh
go vet ./...
go test ./...
```

普通测试使用临时目录；涉及真实外部账号的验收测试需要单独配置。桌面程序和模型模块依赖第三方组件，详情见第三方许可说明。

## 桌面版（Mac）

安装和更新步骤见[安装说明](docs/INSTALL.md)。应用尚未经过 Apple 公证；系统可能要求用户确认打开和文件访问权限。

## 许可


MIT

---

## English

WorkShelf gathers scattered projects and files into one root folder (`~/Work` by default), one folder per project.

- `workshelf scan [paths...]` — read-only scan. Detects project folders (git, package manifests, media projects, image sets) and groups versions of the same project: git checkouts and worktrees by normalized origin, manual copies by suffix-stripped name plus content overlap. Two different git repos are never merged. Does not follow symlinks, skips system/cache dirs, and only counts secret-looking files.
- `workshelf plan` — writes a Markdown + JSON reorganization plan. Never moves anything. Items with uncommitted changes, worktrees, recent activity, or an existing target are marked "register only". Cross-volume moves are flagged.
- `workshelf put <project> <task> <files...>` — copies files into `~/Work/<project>/任务/<YYYY-MM-DD>-<task>/`, verifies sha256, and appends to a locked UTF-8-BOM CSV index. Never moves, deletes, or overwrites. Refuses secret-looking filenames and runs gitleaks first when it is on PATH.

Directory names default to Chinese and can be switched to English with `"names_preset": "en"` in `~/.config/workshelf/config.json`. MIT licensed; desktop dependencies are listed in the third-party notices.
