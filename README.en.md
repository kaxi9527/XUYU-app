# XUYU (formerly WorkShelf)

XUYU brings scattered project files together, organizes them using editable work-type templates, and keeps a record of changes. Desktop organization moves files and records operations that can be undone. The current interface is primarily in Chinese.

[中文](README.md) · [Installation and updates](docs/INSTALL.md) · [Changelog](CHANGELOG.md) · [Contributing](CONTRIBUTING.md) · [Downloads](https://github.com/kaxi9527/XUYU-app/releases)

On first use, XUYU explains which locations it needs and asks you to check access. You can recheck in Settings; macOS usually remembers grants, but they can change. AI session directories require separate authorization. Model use requires a configured provider and explicit consent where applicable.

Choose an asset matching your operating system and processor from the Releases page. Only assets actually listed there are available. XUYU is distributed outside app stores; see the installation guide for operating-system security prompts and platform limitations.

The desktop organizer moves files. The older `workshelf put` command has different, copy-based behavior; see the Chinese reference in [README.md](README.md). Code repositories and environment directories may be left in place to preserve their internal structure.

## Using XUYU

1. On the welcome screen, choose **organize existing project files** or **manage a new project**. Check access to the listed folders when prompted, or return to Settings later.
2. For an existing project, review the suggested project and source locations. Confirm a work type or choose another; edit the folder template or add your own placement rules as needed.
3. Review the proposed destinations before starting. The organizer moves selected files, keeps the internal structure of supported project folders, and records the operation. Use the operation's undo action to move files back where supported; conflicts and incomplete restores are reported.
4. For a new project, choose a work-type template, edit the folders, and create the project. The project list and file browser let you inspect the result.
5. To include hidden entries such as `.codex`, turn on **show hidden files** in the file browser. Deleting hidden paths requires an additional confirmation.
6. Model and account configuration is in **Settings → Model and accounts**. AI session directories need separate authorization. Review the displayed recipient and consent before enabling features that send session text or project information to a model.

File access is controlled by your operating system and can change. If XUYU reports inaccessible folders, check **Settings → File access**, adjust your system permissions, and retry. Features and background integration vary by platform; the current interface is in Chinese.

Licensed under the [MIT License](LICENSE). Third-party notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
