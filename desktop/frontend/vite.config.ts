import { defineConfig } from "vite";
import { existsSync } from "node:fs";
import { readdir, unlink } from "node:fs/promises";
import { resolve, join } from "node:path";

// Source provenance belongs beside editable files. Vite copies public files
// verbatim, so remove only those local sidecar copies from the built artifact.
function sourceProvenance() {
  let output = "";
  async function strip(directory: string): Promise<void> {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name);
      if (entry.isDirectory()) await strip(path);
      else if (entry.isFile() && entry.name === ".workshelf-origin.jsonl") await unlink(path);
    }
  }
  return {
    name: "workshelf-source-provenance",
    apply: "build" as const,
    configResolved(config: {root: string; build: {outDir: string}}) { output = resolve(config.root, config.build.outDir); },
    async closeBundle() { if (output && existsSync(output)) await strip(output); },
  };
}

// https://vitejs.dev/config/
export default defineConfig({
  cacheDir: ".cache/vite",
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [sourceProvenance()],
});
