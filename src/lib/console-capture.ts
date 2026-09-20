/**
 * Optional preview diagnostics. It posts console messages to the resolved
 * parent origin and best-effort POSTs browser errors to Vite's `./__dev/console` route.
 * The Go executable does not implement that diagnostics route; failed posts
 * are intentionally dropped. This module does not query downloader/API state,
 * but it forwards caller-supplied console arguments, so callers must avoid
 * logging secrets or private user data while the page is embedded.
 */

const originalConsole = {
  log: console.log.bind(console),
  warn: console.warn.bind(console),
  error: console.error.bind(console),
};

const DEV_CONSOLE_PATH = "./__dev/console";

// Embedded preview URLs may contain a bearer token in their path. Scrub that
// segment from the diagnostic body before the Vite dev server receives it.
const PREVIEW_TOKEN_RE = /\/v1\/preview\/[^/\s"'\\]+/g;
const scrubPreviewToken = (s: string): string =>
  s.replace(PREVIEW_TOKEN_RE, "/v1/preview/<token>");

// Target origin for window.parent.postMessage. Never use wildcard `*`, which
// would allow any embedding origin to receive console text.
// Resolution order:
//   (1) VITE_COWORK_PARENT_ORIGIN — explicit development override.
//   (2) ``window.location.ancestorOrigins[0]`` — Chromium/WebKit only, but
//       it's the actual embedding origin and can't be spoofed by the child.
//   (3) window.location.origin — final same-origin fallback.
// Computed once at module load because a document's parent does not change.
const PARENT_ORIGIN: string = (() => {
  const fromEnv = import.meta.env.VITE_COWORK_PARENT_ORIGIN;
  if (typeof fromEnv === "string" && fromEnv.length > 0) {
    return fromEnv;
  }
  const ancestors = window.location.ancestorOrigins;
  if (ancestors && ancestors.length > 0 && ancestors[0]) {
    return ancestors[0];
  }
  return window.location.origin;
})();

// Dedup identical errors fired in bursts — an infinite-loop component
// can emit the same stack 1000 times per second. Keyed on (kind + first
// stack line). 500ms quiet window is short enough to still catch a
// legitimate retry and long enough to suppress render-loop spam.
const DEDUP_WINDOW_MS = 500;
let lastSignature: string | null = null;
let lastSignatureTime = 0;

function postToParent(level: string, args: unknown[]) {
  try {
    const message = args
      .map((a) => (typeof a === "object" ? JSON.stringify(a, null, 2) : String(a)))
      .join(" ");
    window.parent.postMessage(
      { type: "console", level, message, timestamp: Date.now() },
      PARENT_ORIGIN
    );
  } catch {
    /* ignore serialization errors */
  }
}

function postToDevServer(kind: string, payload: Record<string, unknown>) {
  const sig = `${kind}|${(payload.stack as string | undefined)?.split("\n", 2)[0] ?? payload.message ?? ""}`;
  const now = Date.now();
  if (sig === lastSignature && now - lastSignatureTime < DEDUP_WINDOW_MS) {
    return;
  }
  lastSignature = sig;
  lastSignatureTime = now;

  try {
    void fetch(DEV_CONSOLE_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: scrubPreviewToken(
        JSON.stringify({
          kind,
          time: now,
          url: location.href,
          ...payload,
        }),
      ),
      keepalive: true,
    }).catch(() => {
      /* dev server may be restarting — drop silently */
    });
  } catch {
    /* ignore */
  }
}

function serializeArgs(args: unknown[]): { message: string; stack: string | null } {
  let stack: string | null = null;
  const parts: string[] = [];
  for (const a of args) {
    if (a instanceof Error) {
      parts.push(a.message);
      stack = stack ?? a.stack ?? null;
    } else if (typeof a === "object" && a !== null) {
      try {
        parts.push(JSON.stringify(a));
      } catch {
        parts.push(String(a));
      }
    } else {
      parts.push(String(a));
    }
  }
  return { message: parts.join(" "), stack };
}

/**
 * Runtime-error reporter for the ErrorBoundary.
 *
 * Writes details to the original local console and makes a best-effort POST to
 * Vite's `./__dev/console` diagnostics route. It deliberately does not forward
 * error details to the parent; embedded hosts receive only the separate fixed
 * crash signal. The packaged Go server has no diagnostics route, so that POST
 * is dropped there.
 */
export function reportPrivateRuntimeError(
  label: string,
  error: unknown,
  detail?: string | null,
): void {
  // Bypass this module's parent-forwarding wrapper. An earlier bootstrap script
  // may already have wrapped console.error for local websocket noise filtering.
  originalConsole.error(label, error, detail ?? "");
  const { message, stack } = serializeArgs([error]);
  postToDevServer("app.error", {
    label,
    message,
    stack,
    componentStack: detail ?? null,
  });
}
console.log = (...args: unknown[]) => {
  originalConsole.log(...args);
  postToParent("log", args);
};
console.warn = (...args: unknown[]) => {
  originalConsole.warn(...args);
  postToParent("warn", args);
};
console.error = (...args: unknown[]) => {
  originalConsole.error(...args);
  postToParent("error", args);
  const { message, stack } = serializeArgs(args);
  postToDevServer("console.error", { message, stack });
};

window.addEventListener("error", (ev) => {
  postToDevServer("window.error", {
    message: ev.message,
    filename: ev.filename,
    lineno: ev.lineno,
    colno: ev.colno,
    stack: ev.error?.stack ?? null,
  });
});

window.addEventListener("unhandledrejection", (ev) => {
  const reason = ev.reason;
  const hasMessage = typeof reason === "object" && reason !== null && "message" in reason;
  postToDevServer("unhandledrejection", {
    message: hasMessage ? String((reason as { message: unknown }).message) : String(reason),
    stack: (reason as { stack?: string } | null)?.stack ?? null,
  });
});
