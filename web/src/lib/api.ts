import { demoApiFetch, isDemoMode } from "./demoApi";

let csrfToken = "";
let lastCanonicalRevision: number | null = null;

export function setCSRFToken(token: string) {
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

function trackCanonicalRevision(input: RequestInfo | URL, method: string, response: Response) {
  if (method !== "GET" || !response.ok) return;
  const rawURL = typeof input === "string" ? input : input instanceof URL ? input.pathname : input.url;
  let path = rawURL;
  try {
    path = new URL(rawURL, window.location.origin).pathname;
  } catch {
    // Relative API paths are already safe to compare as-is.
  }
  if (path !== "/api/v1/config") return;
  void response.clone().json().then((body: { revision?: unknown }) => {
    const revision = typeof body.revision === "number" ? body.revision : Number(body.revision);
    if (!Number.isSafeInteger(revision) || revision < 0) return;
    if (lastCanonicalRevision === null) {
      lastCanonicalRevision = revision;
      return;
    }
    if (revision > lastCanonicalRevision) {
      lastCanonicalRevision = revision;
      window.dispatchEvent(new CustomEvent("minimalrouter:canonical-revision", { detail: { revision } }));
    }
  }).catch(() => undefined);
}

function requestPath(input: RequestInfo | URL): string {
  const rawURL = typeof input === "string" ? input : input instanceof URL ? input.pathname : input.url;
  try {
    return new URL(rawURL, window.location.origin).pathname;
  } catch {
    return rawURL;
  }
}

type ChangePreview = {
  changes?: string[];
  risk?: string;
  requires_confirmation?: boolean;
  rollback_seconds?: number;
  expected_interruption?: string;
  error?: string;
};

async function confirmConfigChange(body: BodyInit | null | undefined, headers: Headers): Promise<boolean> {
  if (!body || typeof body !== "string") return true;
  const previewHeaders = new Headers(headers);
  previewHeaders.set("Content-Type", "application/json");
  const response = await fetch("/api/v1/config/preview", {
    method: "POST",
    headers: previewHeaders,
    body,
    credentials: "same-origin",
    cache: "no-store",
  });
  const preview = (await response.json().catch(() => ({}))) as ChangePreview;
  if (!response.ok) {
    throw new Error(preview.error || `Change preview failed (${response.status})`);
  }
  const changes = Array.isArray(preview.changes) && preview.changes.length > 0
    ? preview.changes.map((item) => `• ${item}`).join("\n")
    : "• No effective configuration changes";
  const risk = String(preview.risk || "unknown").toUpperCase();
  const rollback = preview.requires_confirmation
    ? `Automatic rollback: ARMED (${preview.rollback_seconds || 90}s unless confirmed)`
    : "Automatic rollback: not required for this change";
  const message = [
    "Smart Change Preview",
    "",
    changes,
    "",
    `Risk: ${risk}`,
    preview.expected_interruption || "Affected services may restart briefly.",
    rollback,
    "",
    "Apply these changes?",
  ].join("\n");
  return window.confirm(message);
}

export async function apiFetch(
  input: RequestInfo | URL,
  init: RequestInit = {},
): Promise<Response> {
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

  if (method === "PUT" && requestPath(input) === "/api/v1/config") {
    const confirmed = await confirmConfigChange(init.body, headers);
    if (!confirmed) {
      return new Response(JSON.stringify({ error: "Configuration change cancelled." }), {
        status: 409,
        headers: { "Content-Type": "application/json" },
      });
    }
  }

  const response = await fetch(input, {
    ...init,
    headers,
    credentials: "same-origin",
    cache: "no-store",
  });
  if (response.status === 401) {
    csrfToken = "";
    window.dispatchEvent(new Event("minimalrouter:unauthorized"));
  }
  trackCanonicalRevision(input, method, response);
  return response;
}
