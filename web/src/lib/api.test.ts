import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

beforeEach(() => {
  vi.resetModules();
  vi.stubGlobal("window", { location: { origin: "https://router.test" }, dispatchEvent: vi.fn() });
  vi.stubGlobal("document", { hidden: false });
});
afterEach(() => vi.unstubAllGlobals());

describe("passive response cache", () => {
  it("does not publish an unfinished body, or retain a body aborted by its owner", async () => {
    let stream!: ReadableStreamDefaultController<Uint8Array>;
    const owner = new AbortController();
    const fetch = vi.fn().mockImplementationOnce(() => Promise.resolve(new Response(new ReadableStream({ start(controller) {
      stream = controller;
      owner.signal.addEventListener("abort", () => controller.error(new DOMException("aborted", "AbortError")));
    } }), { headers: { "Content-Type": "application/json" } }))).mockImplementation(() => Promise.resolve(Response.json({ revision: 2 })));
    vi.stubGlobal("fetch", fetch);
    const { apiFetch } = await import("./api");
    const pending = apiFetch("/api/v1/config", { signal: owner.signal });
    let finished = false; void pending.then(() => { finished = true; }, () => {});
    await vi.waitFor(() => expect(stream).toBeDefined());
    stream.enqueue(new TextEncoder().encode('{"revision":'));
    expect(finished).toBe(false);
    owner.abort(); await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(await (await apiFetch("/api/v1/config")).json()).toEqual({ revision: 2 });
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("a completed snapshot stays readable after the original request is aborted", async () => {
    const owner = new AbortController();
    const fetch = vi.fn().mockResolvedValue(Response.json({ revision: 3 }));
    vi.stubGlobal("fetch", fetch);
    const { apiFetch } = await import("./api");
    await (await apiFetch("/api/v1/config", { signal: owner.signal })).json();
    owner.abort();
    const cached = await apiFetch("/api/v1/config");
    expect(await cached.json()).toEqual({ revision: 3 });
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("late pre-mutation GETs cannot repopulate the invalidated cache", async () => {
    let finish!: (value: Response) => void;
    const fetch = vi.fn().mockImplementationOnce(() => new Promise<Response>(resolve => { finish = resolve; }))
      .mockResolvedValueOnce(Response.json({ state: "Committed" }))
      .mockResolvedValueOnce(Response.json({ revision: 5 }));
    vi.stubGlobal("fetch", fetch);
    const { apiFetch, setCSRFToken } = await import("./api"); setCSRFToken("test");
    const old = apiFetch("/api/v1/config");
    await apiFetch("/api/v1/config", { method: "PUT", body: "{}" });
    finish(Response.json({ revision: 4 })); await old;
    expect(await (await apiFetch("/api/v1/config")).json()).toEqual({ revision: 5 });
  });
});

describe("unauthorized responses", () => {
  it("keep a live session when a re-authentication check fails", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response("Current administrator password is incorrect", { status: 401 }))
      .mockResolvedValueOnce(Response.json({ csrf_token: "still-valid" }));
    vi.stubGlobal("fetch", fetch);
    const { apiFetch, setCSRFToken } = await import("./api");
    setCSRFToken("still-valid");
    const response = await apiFetch("/api/v1/backup/export", { method: "POST", body: "{}" });
    expect(response.status).toBe(401);
    expect(fetch.mock.calls[1][0]).toBe("/api/v1/auth/session");
    expect(window.dispatchEvent).not.toHaveBeenCalled();
  });

  it("return to sign-in once the session itself no longer validates", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(Response.json({ error: "Unauthorized or expired session" }, { status: 401 }))
      .mockResolvedValueOnce(Response.json({ error: "Unauthorized or expired session" }, { status: 401 }));
    vi.stubGlobal("fetch", fetch);
    const { apiFetch } = await import("./api");
    await apiFetch("/api/v1/system");
    expect(window.dispatchEvent).toHaveBeenCalledWith(expect.objectContaining({ type: "minimalrouter:unauthorized" }));
  });
});
