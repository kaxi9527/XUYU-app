# WorkShelf UI

Authority: MDUI 2.1.5 (MIT), pinned under `public/vendor/mdui-2.1.5` with package metadata, source URLs and hashes. Actual MDUI Web Components implement buttons and dialogs; native inputs retain labels and existing form semantics. All assets work offline. The product theme uses neutral surfaces, graphite text and folder yellow, with no blue theme.

Keep upstream assets unchanged. Product layout and token mappings live in `theme.css` and page styles. ShelfButton preserves the existing confirmation contract while rendering `mdui-button`; it is registered through UXQA component-plan/component-sync. Shadow-root buttons own native focus, keyboard activation and disabled state; do not add a second focus stop to the host. The application does not introduce React or change its Wails API for this migration.

Source: https://github.com/zdhxiong/mdui · https://www.mdui.org/zh-cn/docs/2/getting-started/installation

Historical daisyUI assets/specifications are retained for provenance, but are no longer imported at runtime. The current catalog points to the MDUI revision. Browser QA uses shadow-aware keyboard evidence alongside UXQA, whose current generic focus traversal cannot distinguish MDUI internal button selectors; this limitation is recorded explicitly in both QA scenarios.
