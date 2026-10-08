// Bundles the UI into dist/, which the Go binary embeds.
import { build } from "esbuild";
import { copyFileSync, mkdirSync } from "node:fs";

mkdirSync("dist", { recursive: true });
await build({
  entryPoints: ["src/app.js"],
  bundle: true,
  format: "esm",
  target: ["es2022"],
  outfile: "dist/app.js",
  sourcemap: false,
  minify: true,
  logLevel: "info",
});
// The render worker is already a self-contained module; it must sit next to
// app.js because @bdfkit/render resolves it relative to import.meta.url.
copyFileSync("node_modules/@bdfkit/render/dist/worker.js", "dist/worker.js");
copyFileSync("src/style.css", "dist/style.css");
