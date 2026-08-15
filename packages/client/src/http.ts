// One thin door to the wire. Callers get the status and the parsed body and
// decide for themselves; only transport problems throw from here.

export interface HttpResult {
  status: number
  body: unknown
}

/** The frozen error envelope, {"error":{"code","message"}}. */
interface ErrorEnvelope {
  error?: { code?: string; message?: string }
}

export interface RequestArgs {
  baseUrl: string
  token: string
  method: 'GET' | 'POST' | 'DELETE'
  path: string
  body?: unknown
  /** Abort budget for this one attempt. For a long-poll it must sit ABOVE
   * the server-side wait, never below it: the watchdog exists to catch a
   * dead socket, not to race the server's own deadline. */
  watchdogMs: number
}

/** Performs one HTTP attempt. Throws only on transport failure (dead socket,
 * DNS, watchdog abort); every HTTP status comes back as a result. */
export async function requestJson(args: RequestArgs): Promise<HttpResult> {
  const url = args.baseUrl.replace(/\/+$/, '') + args.path
  const controller = new AbortController()
  const watchdog = setTimeout(() => {
    controller.abort(new Error(`authstunt: no response after ${String(args.watchdogMs)}ms: ${args.method} ${args.path}`))
  }, args.watchdogMs)
  try {
    const init: RequestInit = {
      method: args.method,
      headers: {
        authorization: `Bearer ${args.token}`,
        ...(args.body === undefined ? {} : { 'content-type': 'application/json' }),
      },
      signal: controller.signal,
    }
    if (args.body !== undefined) {
      init.body = JSON.stringify(args.body)
    }
    const response = await fetch(url, init)
    if (response.status === 204) {
      return { status: 204, body: undefined }
    }
    const text = await response.text()
    let parsed: unknown
    try {
      parsed = text === '' ? undefined : JSON.parse(text)
    } catch {
      parsed = undefined
    }
    return { status: response.status, body: parsed }
  } finally {
    clearTimeout(watchdog)
  }
}

/** Turns a non-2xx result into the error a caller reads in a test log. The
 * envelope's code vocabulary is frozen; the message is prose. */
export function apiError(args: RequestArgs, result: HttpResult): Error {
  const envelope = result.body as ErrorEnvelope | undefined
  const code = envelope?.error?.code ?? 'unknown'
  const message = envelope?.error?.message ?? 'no error body'
  return new Error(
    `authstunt: ${code}: ${message} (HTTP ${String(result.status)} ${args.method} ${args.path})`,
  )
}
