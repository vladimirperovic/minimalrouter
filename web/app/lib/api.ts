"use client";

let csrfToken = "";

export function setCSRFToken(token: string) {
  csrfToken = token;
}

export async function refreshSession(): Promise<boolean> {
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

export async function apiFetch(
  input: RequestInfo | URL,
  init: RequestInit = {},
): Promise<Response> {
  const method = (init.method ?? "GET").toUpperCase();
  const mutating = !["GET", "HEAD", "OPTIONS"].includes(method);
  if (mutating && !csrfToken && !(await refreshSession())) {
    throw new Error("Authenticated session required");
  }

  const headers = new Headers(init.headers);
  if (mutating) {
    headers.set("X-CSRF-Token", csrfToken);
  }
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
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
  return response;
}
