import type { Problem } from "./types";

export class ApiError extends Error {
  status: number;
  code: string;
  fields?: Record<string, string>;
  constructor(status: number, problem: Partial<Problem>) {
    super(problem.detail || problem.title || `HTTP ${status}`);
    this.status = status;
    this.code = problem.code || "E_UNKNOWN";
    this.fields = problem.fields;
  }
}

let csrfToken = "";
export function setCSRFToken(token: string) {
  csrfToken = token;
}
export function getCSRFToken() {
  return csrfToken;
}

export interface RequestOptions {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json", ...(opts.headers || {}) };
  if (opts.body !== undefined) headers["Content-Type"] = "application/json";
  if (opts.method && !["GET", "HEAD"].includes(opts.method) && csrfToken) {
    headers["X-CSRF-Token"] = csrfToken;
  }
  const res = await fetch(`/api/v1${path}`, {
    method: opts.method || "GET",
    credentials: "same-origin",
    headers,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    signal: opts.signal,
  });
  if (!res.ok) {
    let problem: Partial<Problem> = {};
    try {
      problem = (await res.json()) as Problem;
    } catch {
      problem = { title: res.statusText };
    }
    if (res.status === 401 && !path.startsWith("/auth/login") && !path.startsWith("/me")) {
      window.dispatchEvent(new Event("kpcc:unauthorized"));
    }
    throw new ApiError(res.status, problem);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

/** download initiates a browser download and returns the filename when known. */
export async function download(
  path: string,
  body?: unknown,
  opts: { method?: string; headers?: Record<string, string> } = {},
): Promise<void> {
  const headers: Record<string, string> = { ...(opts.headers || {}) };
  if (opts.method && opts.method !== "GET") headers["X-CSRF-Token"] = csrfToken;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(`/api/v1${path}`, {
    method: opts.method || "GET",
    credentials: "same-origin",
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    let problem: Partial<Problem> = {};
    try {
      problem = (await res.json()) as Problem;
    } catch {
      /* ignore */
    }
    throw new ApiError(res.status, problem);
  }
  const blob = await res.blob();
  const cd = res.headers.get("Content-Disposition") || "";
  const match = /filename="?([^";]+)"?/.exec(cd);
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = match ? match[1] : "download";
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}
