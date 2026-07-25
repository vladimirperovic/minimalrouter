import { mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const workerURL = new URL("../dist/server/index.js", import.meta.url);
workerURL.searchParams.set("prerender", `${Date.now()}`);
const { default: worker } = await import(workerURL.href);

const response = await worker.fetch(
  new Request("https://minimalrouter.invalid/", {
    headers: { accept: "text/html" },
  }),
  {
    ASSETS: {
      fetch: async () => new Response("Not found", { status: 404 }),
    },
  },
  {
    waitUntil() {},
    passThroughOnException() {},
  },
);

if (!response.ok) {
  throw new Error(`appliance prerender failed with HTTP ${response.status}`);
}
const contentType = response.headers.get("content-type") ?? "";
if (!contentType.startsWith("text/html")) {
  throw new Error(`appliance prerender returned ${contentType || "no content type"}`);
}
const html = await response.text();
if (!html.includes("<title>Minimal Router OS")) {
  throw new Error("appliance prerender did not contain the expected application shell");
}
const output = resolve(root, "dist/client/index.html");
await mkdir(dirname(output), { recursive: true });
await writeFile(output, html, { mode: 0o644 });
console.log(`Prerendered appliance UI: ${output}`);
