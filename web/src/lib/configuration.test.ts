import { afterEach, beforeEach, expect, it, vi } from "vitest";

beforeEach(() => {
  vi.resetModules();
  vi.stubGlobal("window", { location: { origin: "https://router.test" }, confirm: vi.fn(), dispatchEvent: vi.fn() });
  vi.stubGlobal("document", { hidden: false });
});
afterEach(() => vi.unstubAllGlobals());

it("cancelled preview is explicit and never sends a config PUT", async () => {
  const fetch = vi.fn().mockResolvedValue(Response.json({ changes: ["DHCP"], requires_confirmation: false }));
  vi.stubGlobal("fetch", fetch);
  const { setCSRFToken } = await import("./api"); setCSRFToken("test");
  const { previewAndApplyConfig } = await import("./configuration");
  const candidate = { revision: 1 } as Parameters<typeof previewAndApplyConfig>[0];
  expect(await previewAndApplyConfig(candidate)).toEqual({ cancelled: true });
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(fetch.mock.calls[0][0]).toBe("/api/v1/config/preview");
});

it("a failed preview cannot be bypassed and surfaces its real error", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(Response.json({ error: "stale revision" }, { status: 422 })));
  const { setCSRFToken } = await import("./api"); setCSRFToken("test");
  const { previewAndApplyConfig } = await import("./configuration");
  await expect(previewAndApplyConfig({ revision: 1 } as Parameters<typeof previewAndApplyConfig>[0])).rejects.toThrow("stale revision");
  expect(fetch).toHaveBeenCalledTimes(1);
});

it("keeps an accepted pending outcome even if the canonical refresh fails", async () => {
  vi.mocked(window.confirm).mockReturnValue(true);
  const fetch = vi.fn()
    .mockResolvedValueOnce(Response.json({ changes: ["LAN"], requires_confirmation: true }))
    .mockResolvedValueOnce(Response.json({ id: "pending-1", state: "AwaitingConfirmation", confirmation_deadline: "2026-09-06T20:00:00Z" }))
    .mockResolvedValueOnce(Response.json({ error: "temporarily unavailable" }, { status: 503 }));
  vi.stubGlobal("fetch", fetch);
  const { setCSRFToken } = await import("./api"); setCSRFToken("test");
  const { previewAndApplyConfig } = await import("./configuration");
  const result = await previewAndApplyConfig({ revision: 1 } as Parameters<typeof previewAndApplyConfig>[0]);
  expect(result).toMatchObject({ cancelled: false, transaction: { state: "AwaitingConfirmation", id: "pending-1" }, refreshError: expect.stringContaining("accepted") });
  expect(window.dispatchEvent).toHaveBeenCalledWith(expect.objectContaining({ type: "minimalrouter:config-applied" }));
  expect(fetch).toHaveBeenCalledTimes(3);
});

it("does not republish an in-flight configuration after logout clears its generation", async () => {
  let finish!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(resolve => { finish = resolve; })));
  const { readConfiguration, clearConfiguration } = await import("./configuration");
  const pending = readConfiguration();
  await vi.waitFor(() => expect(finish).toBeDefined());
  clearConfiguration();
  const config = { revision: 1, ...Object.fromEntries(["wan", "lan", "dhcp", "firewall", "wireguard", "cloudflare", "squid_proxy", "adguard", "qos", "wifi"].map(name => [name, {}])) };
  finish(Response.json(config));
  await expect(pending).rejects.toMatchObject({ name: "AbortError" });
});
