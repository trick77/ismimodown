// http.ts — the shared fetch/JSON core. Ported from peeq's api/http.ts, with
// AuthExpiredError removed: ismimodown is public and unauthenticated, so a 401
// would be a server bug rather than an expired session, and a branch for it
// would be dead code pretending to be a feature.

// ApiError carries the HTTP status so callers can branch on specific codes —
// notably 429, which the dashboard must surface as "you are being throttled"
// rather than as "the endpoint is down".
export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export async function expectJSON<T>(
  response: Response,
  fallbackMessage: string,
): Promise<T> {
  if (!response.ok) {
    throw new ApiError(
      response.status,
      await readErrorMessage(response, fallbackMessage),
    );
  }
  return response.json() as Promise<T>;
}

async function readErrorMessage(
  response: Response,
  fallback: string,
): Promise<string> {
  try {
    const body = (await response.json()) as { error?: unknown };
    if (typeof body.error === "string" && body.error !== "") {
      return body.error;
    }
  } catch {
    // Body was empty or not JSON.
  }
  return fallback;
}

// api — read-only by design. There are no write verbs because there is nothing
// to write: the site is a public status page, and adding post/put "for later"
// would be an unauthenticated mutation surface waiting to be used.
export const api = {
  async get<T>(
    path: string,
    signal?: AbortSignal,
    fallbackMessage = `GET ${path} failed`,
  ): Promise<T> {
    const response = await fetch(path, { signal });
    return expectJSON<T>(response, fallbackMessage);
  },
};
