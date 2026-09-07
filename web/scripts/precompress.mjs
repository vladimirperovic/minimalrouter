import { readdir, readFile, writeFile, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";
import { gzipSync } from "node:zlib";

// Vite empties dist before this step. Only regular supported assets are read;
// links are never traversed and existing sidecars are replaced or removed.
export async function precompress(directory) {
  const results = [];
  for (const entry of (await readdir(directory, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
    const file = join(directory, entry.name);
    if (entry.isSymbolicLink()) continue;
    if (entry.isDirectory()) { results.push(...await precompress(file)); continue; }
    if (!entry.isFile() || !/\.(html|js|css|svg)$/i.test(entry.name)) continue;
    const input = await readFile(file);
    // Node zlib writes a zero gzip timestamp; gzipSync has no timestamp option.
    const output = gzipSync(input, { level: 9 });
    await rm(file + ".gz", { force: true });
    if (output.length < input.length) {
      await writeFile(file + ".gz", output);
      results.push({ file, original: input.length, compressed: output.length });
    }
  }
  return results;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const results = await precompress(resolve(process.argv[2] || "dist"));
  console.log(`Precompressed ${results.length} HTML/JS/CSS/SVG assets (${results.reduce((sum, item) => sum + item.original - item.compressed, 0)} bytes saved).`);
}
