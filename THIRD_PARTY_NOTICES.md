# 第三方素材与商标说明

## AI 品牌图标（desktop/frontend/public/logos/）

WorkShelf 用这些图标标出「这个文件是哪个 AI 写的」，只用来指代对应的产品，**不表示这些公司与 WorkShelf 有任何合作、认可或赞助关系**。
各名称和图标是其所有者的商标。图标按原样使用（只通过 CSS 设定颜色/大小，未改形状）。

来源：Simple Icons v16.36.0（npm 包 `simple-icons@16.36.0`，项目本身以 CC0-1.0 发布；https://simpleicons.org ）。
Simple Icons 的 CC0 只覆盖其收录的 SVG 数据，**不授予任何商标权**；使用须遵守各品牌自己的规范。

| 文件 | 品牌 | 所有者 | Simple Icons 标注的来源 | 品牌规范 | sha256 前 16 位 |
|---|---|---|---|---|---|
| claude.svg | Claude | Anthropic | https://claude.ai | — | 2d6fda79eb18ddcc |
| anthropic.svg | Anthropic | Anthropic | https://www.anthropic.com | — | 1cc599f6ebce2016 |
| cursor.svg | Cursor | Anysphere | https://cursor.com/brand | https://cursor.com/brand | 71572a9be192cc06 |
| googlegemini.svg | Google Gemini | Google | https://gemini.google.com | — | a6228d8846040401 |
| githubcopilot.svg | GitHub Copilot | GitHub | https://primer.style/foundations/icons/copilot-24 | 图标本身 MIT | c72c1e0018e84b31 |
| windsurf.svg | Windsurf | Windsurf | https://windsurf.com/brand | https://windsurf.com/brand | 6a084cb5c1a82bbf |
| deepseek.svg | DeepSeek | DeepSeek | https://www.deepseek.com | — | 7a55a0a7391d116e |
| kimi.svg | Kimi | Moonshot AI | https://moonshotai.github.io/Branding-Guide | 同左 | 01fb4be70032974e |
| qwen.svg | Qwen | Alibaba Cloud | 见 Simple Icons 数据 | — | 36854c60b26bfa5a |
| trae.svg | Trae | ByteDance | 见 Simple Icons 数据 | — | ae5afd5f4d8ef328 |
| codebuddy.svg | CodeBuddy | Tencent | https://www.codebuddy.cn | — | 453a364e21235a82 |

### 暂用中性字母头像的（没拿到可靠的官方图标文件）

| 来源 | 现状 | 原因 |
|---|---|---|
| Codex / OpenAI / ChatGPT | 字母头像「C」/「O」 | OpenAI 官方规范页 https://openai.com/brand/ 没有直接下载链接，Simple Icons 也不收录 OpenAI；不从第三方站转载 |
| Grok / Grok Bot | 字母头像「G」 | 官方素材包 https://data.x.ai/logos/xAI_Grok_Assets.zip （见 https://x.ai/legal/brand-guidelines ）命令行下载被拦（403），待手动下载后原样放入 |
| WorkBuddy | 字母头像「W」 | 腾讯官网没有公开的图标下载；不拿 CodeBuddy 的图标代替 |
| 来源不明 | 「?」 | — |

## daisyUI 5.7.50

WorkShelf 使用 daisyUI 官方 CSS 组件与 Winter / dim 主题（MIT）。源码：<https://github.com/saadeghi/daisyui>；文档：<https://daisyui.com/>。

固定版本文件、逐文件来源 URL、SHA-256 和完整许可文本位于 `desktop/frontend/src/ui/vendor/daisyui-5.7.50/`。上游 CSS 未修改；WorkShelf 通过官方主题 Token 扩展颜色，并在独立样式中组织产品布局。应用包的 `Contents/Resources/daisyui-LICENSE` 保留许可文本。

## 2026-11-09 当前前端与模型模块

本节记录当前实现；上面的旧图标和 daisyUI 记录保留为历史。运行时现已改用 MDUI，品牌图标统一采用 Lobe Icons 单色 SVG，旧的字母头像限制不再适用于已收录品牌。未知名称仍有字母兜底。

- **MDUI 2.1.5**，MIT，作者 zdhxiong。源码 <https://github.com/zdhxiong/mdui>。使用官方独立 Web Components JS/CSS；未修改发行资产。完整许可、包元数据与哈希在 `desktop/frontend/public/vendor/mdui-2.1.5/`，品牌布局与颜色在独立产品主题中。
- **@lobehub/icons-static-svg 1.102.0**，MIT。源码 <https://github.com/lobehub/lobe-icons>。本地收录 357 份单色品牌 SVG；完整许可、版本、来源及逐文件哈希在 `desktop/frontend/public/vendor/lobe-icons-1.102.0/`。标志用于识别产品，不表示品牌认可或授予商标权；图标收录不等于已实现该工具的文件追踪适配。更新脚本 `desktop/frontend/scripts/sync-ai-icons.py` 固定版本并校验下载内容。
- **模型接入与授权管理模块**：从本机 UXQA 的 `native/model-core` 引入所需 Go 源码闭包，来源为 MIT 项目 <https://github.com/yetone/magpie> 及 UXQA 的隔离接入改动。保留 `desktop/model-core/LICENSE`、`NOTICE.md`、`UPSTREAM-MANIFEST.json` 与 `WORKSHELF-IMPORT.json`；WorkShelf 的后续变更另记 `WORKSHELF-CHANGES.json`，不将它们标为未修改上游。没有复制其他应用、账号或私密配置。

静态许可随前端资源嵌入；macOS 打包任务也将模型模块许可和 NOTICE 放入应用 Resources。当前构建与实际安装状态以最新交付报告为准。

跨架构构建补充了 `internal/pii/guard/kernel_amd64.s`；该文件与 `UPSTREAM-MANIFEST.json` 所列 Magpie 版本的公开源码逐字节一致，适用相同的 MIT 许可。

WS-19a 的后续本地修订新增专用 `/control/generate-text`：绕过通用备用路由与重试，限定自有账号、原生接收地址和最终输出上限。改动涉及 helper 入口、gateway 的两个短路点及新增 WorkShelf 专属代码/测试；`WORKSHELF-CHANGES.json` 记录相对于原导入内容的逐文件 SHA-256，上游版本未变。原生订阅中无法保留限额的路径不开放项目生成，不把该限制描述为上游已提供的能力。
