import { demoApiFetch, isDemoMode } from "./demoApi";

let csrfToken = "";

const WAN_ESTIMATE_STORAGE_KEY = "minimalrouter:wan-speed-estimate";

type CachedResponse = {
  response: Response;
  storedAt: number;
};

// Passive dashboard data does not need to wake the appliance at animation-like
// rates. Keep the one live bandwidth source reasonably fresh while allowing
// stable configuration/status cards to settle at an appliance-friendly cadence.
// Mutations invalidate this cache immediately below, so operator actions never
// wait for a polling TTL to become visible.
const PASSIVE_GET_TTL_MS = new Map<string, number>([
  ["/api/v1/system", 5_000],
  ["/api/v1/transactions/pending", 5_000],
  ["/api/v1/config", 30_000],
  ["/api/v1/gateway/summary", 30_000],
  ["/api/v1/gateway/settings", 30_000],
  ["/api/v1/gateway/history", 30_000],
  ["/api/v1/snapshots", 30_000],
  ["/api/v1/audit/events", 30_000],
  ["/api/v1/health", 30_000],
]);

const passiveGetCache = new Map<string, CachedResponse>();
const passiveGetInFlight = new Map<string, Promise<Response>>();
let passiveGetEpoch = 0;

export function setCSRFToken(token: string) {
  if (csrfToken !== token) clearPassiveGetCache();
  csrfToken = token;
}

export async function refreshSession(): Promise<boolean> {
  if (isDemoMode) {
    csrfToken = "public-demo";
    return true;
  }
  try {
    const response = await fetch("/api/v1/auth/session", {
      credentials: "same-origin",
      cache: "no-store",
    });
    if (!response.ok) {
      csrfToken = "";
      return false;
    }
    const contentType = response.headers.get("content-type") ?? "";
    if (!contentType.includes("application/json")) {
      csrfToken = "";
      return false;
    }
    const session = (await response.json()) as { csrf_token?: string };
    csrfToken = session.csrf_token ?? "";
    return csrfToken !== "";
  } catch {
    csrfToken = "";
    return false;
  }
}

function requestPath(input: RequestInfo | URL): string {
  const rawURL = typeof input === "string" ? input : input instanceof URL ? input.pathname : input.url;
  try {
    return new URL(rawURL, window.location.origin).pathname;
  } catch {
    return rawURL;
  }
}

function trackWANSpeedEstimate(input: RequestInfo | URL, method: string, response: Response) {
  if (method !== "POST" || !response.ok || requestPath(input) !== "/api/v1/qos/speedtest") return;
  void response.clone().json().then((body: { download_mbps?: unknown; upload_mbps?: unknown }) => {
    const download = Number(body.download_mbps);
    const upload = Number(body.upload_mbps);
    if (!Number.isFinite(download) || download <= 0 || !Number.isFinite(upload) || upload <= 0) return;
    try {
      window.localStorage.setItem(WAN_ESTIMATE_STORAGE_KEY, JSON.stringify({
        download_mbps: download,
        upload_mbps: upload,
        measured_at: Date.now(),
      }));
      window.dispatchEvent(new Event("minimalrouter:wan-speed-estimate"));
    } catch {
      // Browser storage is optional. The speed test result itself is still valid.
    }
  }).catch(() => undefined);
}

function requestCacheKey(input: RequestInfo | URL): string {
  const rawURL = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
  try {
    const url = new URL(rawURL, window.location.origin);
    return `${url.pathname}${url.search}`;
  } catch {
    return rawURL;
  }
}

function passiveTTL(path: string): number | null {
  for (const [prefix, ttl] of PASSIVE_GET_TTL_MS) {
    if (path === prefix || path.startsWith(`${prefix}?`)) return ttl;
  }
  return null;
}

function clearPassiveGetCache() {
  // An older GET may already be on the wire when a mutation succeeds. Bumping
  // the epoch prevents that pre-mutation response from repopulating the cache
  // after invalidation and hiding the freshly committed configuration/status.
  passiveGetEpoch += 1;
  passiveGetCache.clear();
  passiveGetInFlight.clear();
}

async function networkFetch(
  input: RequestInfo | URL,
  init: RequestInit,
  headers: Headers,
  method: string,
): Promise<Response> {
  const response = await fetch(input, {
    ...init,
    headers,
    credentials: "same-origin",
    cache: "no-store",
  });
  if (response.status === 401) {
    csrfToken = "";
    clearPassiveGetCache();
    window.dispatchEvent(new Event("minimalrouter:unauthorized"));
  }
  trackWANSpeedEstimate(input, method, response);
  return response;
}

export async function apiFetch(
  input: RequestInfo | URL,
  init: RequestInit = {},
): Promise<Response> {
  if (init.signal?.aborted) throw init.signal.reason || new DOMException("Request aborted", "AbortError");
  if (isDemoMode) return demoApiFetch(input, init);

  const method = (init.method ?? "GET").toUpperCase();
  const mutating = !["GET", "HEAD", "OPTIONS"].includes(method);
  if (mutating && !csrfToken && !(await refreshSession())) {
    throw new Error("Authenticated session required");
  }

  const headers = new Headers(init.headers);
  if (mutating) {
    headers.set("X-CSRF-Token", csrfToken);
  }
  // FormData must keep the browser-generated multipart boundary. Setting a
  // JSON content type here makes authenticated backup restore uploads
  // impossible even though the backend correctly accepts multipart/form-data.
  const isFormData = typeof FormData !== "undefined" && init.body instanceof FormData;
  if (init.body && !headers.has("Content-Type") && !isFormData) {
    headers.set("Content-Type", "application/json");
  }

  if (method === "GET") {
    const path = requestPath(input);
    const ttl = passiveTTL(path);
    if (ttl !== null) {
      const key = requestCacheKey(input);
      const cached = passiveGetCache.get(key);
      const now = Date.now();
      // Background tabs must not keep waking the router. A stale last-known-good
      // passive snapshot is preferable until the operator returns to the page.
      if (init.cache !== "reload" && init.cache !== "no-store" && cached && (document.hidden || now - cached.storedAt < ttl)) {
        return cached.response.clone();
      }

      // Only coalesce requests that do not carry their own AbortSignal. A
      // component-owned signal must retain its cancellation semantics.
      if (!init.signal) {
        const existing = init.cache === "reload" || init.cache === "no-store" ? undefined : passiveGetInFlight.get(key);
        if (existing) return (await existing).clone();

        const epoch = passiveGetEpoch;
        const inFlight = networkFetch(input, init, headers, method).then(async (response) => {
          const master = await materializeResponse(response);
          if (response.ok && epoch === passiveGetEpoch) {
            passiveGetCache.set(key, { response: master.clone(), storedAt: Date.now() });
          }
          return master;
        }).finally(() => {
          // Cache invalidation intentionally clears the in-flight registry so a
          // post-mutation caller cannot coalesce onto a stale request. If that
          // older request completes after a newer one has claimed the same key,
          // it must not delete the newer coalescing entry.
          if (passiveGetInFlight.get(key) === inFlight) {
            passiveGetInFlight.delete(key);
          }
        });
        passiveGetInFlight.set(key, inFlight);
        return (await inFlight).clone();
      }

      const epoch = passiveGetEpoch;
      const response = await materializeResponse(await networkFetch(input, init, headers, method));
      if (response.ok && epoch === passiveGetEpoch) {
        passiveGetCache.set(key, { response: response.clone(), storedAt: Date.now() });
      }
      return response;
    }
  }

  const response = await networkFetch(input, init, headers, method);
  if (mutating && response.ok) {
    clearPassiveGetCache();
  }
  return response;
}

// Cache only a fully consumed body. A component abort must never poison a
// cached stream that a later mount or another consumer will read.
async function materializeResponse(response: Response): Promise<Response> {
  const body = await response.arrayBuffer();
  return new Response([204, 205, 304].includes(response.status) ? null : body, {
    status: response.status, statusText: response.statusText, headers: response.headers,
  });
}

export async function responseError(response: Response, fallback: string): Promise<string> {
  const text = await response.text().catch(() => "");
  try { return (JSON.parse(text) as { error?: string }).error || fallback; }
  catch { return text.trim() || fallback; }
}
