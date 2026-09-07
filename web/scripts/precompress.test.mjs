import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, writeFile, symlink, rm, lstat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { gunzipSync } from "node:zlib";
import { precompress } from "./precompress.mjs";

test("deterministic sidecars, roundtrip, skip links/fonts and discard larger or stale output", async () => {
  const root = await mkdtemp(join(tmpdir(), "minimalrouter-precompress-"));
  try {
    const dist = join(root, "dist"); await mkdir(dist); await mkdir(join(dist, "assets"));
    const body = "const app = 'repeat';\n".repeat(400);
    for (const name of ["index.html", "assets/app.js", "assets/app.css", "assets/icon.svg"]) await writeFile(join(dist, name), body);
    await writeFile(join(dist, "tiny.js"), "x"); await writeFile(join(dist, "tiny.js.gz"), "stale");
    await writeFile(join(dist, "font.woff2"), body);
    await writeFile(join(root, "outside.js"), body);
    await symlink(join(root, "outside.js"), join(dist, "link.js"));
    await symlink(root, join(dist, "loop"));
    const first = await precompress(dist); assert.equal(first.length, 4);
    const zipped = await readFile(join(dist, "assets/app.js.gz"));
    assert.equal(gunzipSync(zipped).toString(), body);
    assert.equal(zipped.readUInt32LE(4), 0);
    await precompress(dist); assert.deepEqual(await readFile(join(dist, "assets/app.js.gz")), zipped);
    for (const name of ["tiny.js.gz", "font.woff2.gz", "link.js.gz"]) await assert.rejects(lstat(join(dist, name)), { code: "ENOENT" });
    assert.equal(await readFile(join(root, "outside.js"), "utf8"), body);
  } finally { await rm(root, { recursive: true, force: true }); }
});
