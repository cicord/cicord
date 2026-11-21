import type { ApiErrorCode, Diagnostic, Json } from "./types.ts";

const CODE_BY_STATUS: Record<number, ApiErrorCode> = {
  400: "invalid",
  401: "unauthenticated",
  403: "forbidden",
  404: "not_found",
  405: "invalid",
  409: "conflict",
  413: "invalid",
  415: "invalid",
  421: "forbidden",
  422: "invalid",
  500: "internal",
  503: "unavailable",
};

/** Error code for a status when the body carries none (plain-text 404s, proxies…). */
export function codeForStatus(status: number): ApiErrorCode {
  return CODE_BY_STATUS[status] ?? (status >= 500 ? "internal" : `http_${status}`);
}

/**
 * Error raised for every non-2xx response of the runtime API (and for network failures,
 * with `status: 0` and code `"network"`).
 */
export class CodiApiError extends Error {
  /** `snake_case` error code from the body (`{"error": {"code"}}`), or derived from the status. */
  readonly code: ApiErrorCode;
  /** HTTP status, 0 for network errors. */
  readonly status: number;
  /** Validation diagnostics (422 on config updates, `POST /plan`). */
  readonly diagnostics: Diagnostic[];
  /** Parsed response body (JSON or text), when any. */
  readonly body: Json | string | undefined;

  constructor(init: {
    code: ApiErrorCode;
    status: number;
    message: string;
    diagnostics?: Diagnostic[];
    body?: Json | string;
    cause?: unknown;
  }) {
    super(init.message, init.cause === undefined ? undefined : { cause: init.cause });
    this.name = "CodiApiError";
    this.code = init.code;
    this.status = init.status;
    this.diagnostics = init.diagnostics ?? [];
    this.body = init.body;
  }

  /** Builds the error from a response status and its (already parsed) body. */
  static fromResponse(
    status: number,
    statusText: string,
    body: Json | string | undefined,
  ): CodiApiError {
    if (body && typeof body === "object" && !Array.isArray(body)) {
      const error = (body as { error?: unknown }).error;
      if (error && typeof error === "object" && !Array.isArray(error)) {
        const e = error as { code?: unknown; message?: unknown; diagnostics?: unknown };
        return new CodiApiError({
          code: typeof e.code === "string" && e.code ? e.code : codeForStatus(status),
          status,
          message:
            typeof e.message === "string" && e.message ? e.message : statusText || `HTTP ${status}`,
          diagnostics: Array.isArray(e.diagnostics)
            ? (e.diagnostics as unknown as Diagnostic[])
            : [],
          body,
        });
      }
    }
    const text = typeof body === "string" ? body.trim() : "";
    return new CodiApiError({
      code: codeForStatus(status),
      status,
      message: text && text.length <= 200 ? text : statusText || `HTTP ${status}`,
      ...(body === undefined ? {} : { body }),
    });
  }
}

export function isCodiApiError(error: unknown, code?: ApiErrorCode): error is CodiApiError {
  return error instanceof CodiApiError && (code === undefined || error.code === code);
}
