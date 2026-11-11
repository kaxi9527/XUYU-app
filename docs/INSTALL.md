# 安装与更新 · Installation and updates

## 下载安装 · Download

打开 [Releases 下载页](https://github.com/kaxi9527/XUYU-app/releases)，选择实际列出的、与系统和芯片一致的文件。`arm64` 指 Apple 芯片或 ARM 处理器；`amd64` / `x64` 指 Intel 或 AMD 64 位处理器。没有列出的版本尚未提供。

Open [Releases](https://github.com/kaxi9527/XUYU-app/releases) and choose an asset that matches your operating system and processor. `arm64` is for Apple silicon or ARM; `amd64` / `x64` is for 64-bit Intel and AMD processors. An asset not listed there is not yet available.

## macOS

下载对应芯片的 ZIP 并解压，在解压出的文件夹中找到 `WorkShelf.app`，将它拖入「应用程序」文件夹并打开。首次打开时，请阅读系统提示；未经过 Apple 公证的版本可能被 Gatekeeper 阻止，可在「系统设置 → 隐私与安全性」查看系统提供的打开选项。按首次引导检查桌面、文稿、下载、Projects 和主目录；可在设置中再次检查。系统通常记住授权，但重装、签名变化或手动撤销后可能需要重新授权。

Download and extract the ZIP, find `WorkShelf.app` in the extracted folder, move it to Applications, and open it. Read the system prompt: a build without Apple notarization may be blocked by Gatekeeper; check the opening options in System Settings → Privacy & Security. Use the first-run access check for Desktop, Documents, Downloads, Projects, and your home folder; check again in Settings. macOS usually remembers grants, but reinstalling, a signing change, or revocation can require another authorization.

## Windows / Linux

Windows：解压对应芯片的 ZIP，打开其中的 `WorkShelf.exe`。请保留同目录下的 `workshelf-model-core.exe` 和 `tools` 文件夹，以便模型连接和命令行功能使用。Windows 需要 WebView2；未签名文件可能触发系统提示。

Linux：解压对应芯片的 `tar.gz`，在解压目录运行 `./WorkShelf`；如系统提示无执行权限，先运行 `chmod +x WorkShelf workshelf-model-core tools/workshelf`。需安装兼容的 GTK 4 和 WebKitGTK 6.0 运行时。保留同目录下的模型程序和 `tools` 文件夹。

Windows 和 Linux 的跨磁盘整理当前不支持：原文件保留并报告失败。后台登录启动和部分系统集成以各平台实际支持情况为准。

Windows: extract the matching ZIP and open `WorkShelf.exe`. Keep `workshelf-model-core.exe` and the `tools` folder alongside it for model connections and CLI functions. Windows requires WebView2; an unsigned build may trigger a system warning.

Linux: extract the matching `tar.gz` and run `./WorkShelf` from the extracted directory. If execution is denied, first run `chmod +x WorkShelf workshelf-model-core tools/workshelf`. Compatible GTK 4 and WebKitGTK 6.0 runtimes are required. Keep the model binary and `tools` directory alongside the app.

Cross-volume organization is currently unsupported on Windows and Linux: the original remains and the operation reports failure. Background startup and system integrations vary by platform.

## 更新 · Update

先退出序屿及其后台进程，再按下载页说明用新版本替换应用；保留用户配置和项目文件。更新后检查「设置 → 文件访问权限」与模型连接。不要在文件整理进行时替换应用。

Quit XUYU and its background process before replacing the application with the new release. Keep your user configuration and project files. After updating, recheck file access and model connections in Settings. Do not replace the application while organization is in progress.

## 源码构建 · Build from source

命令行：在仓库根目录运行 `go build -o workshelf ./cmd/workshelf`。桌面版位于 `desktop/`，前端位于 `desktop/frontend/`；使用各模块 `go.mod` 中声明的 Go 版本、锁文件对应的 Node 依赖以及 Wails v3 工具。平台构建还需要对应系统的原生依赖。源码构建不等于对应安装包已经过发布验收。

CLI: run `go build -o workshelf ./cmd/workshelf` at the repository root. The desktop app is in `desktop/`, with its frontend in `desktop/frontend/`; use the Go versions specified by each `go.mod`, locked Node dependencies, and Wails v3. Native platform dependencies are also required. A source build does not imply that a release package has passed platform acceptance.
