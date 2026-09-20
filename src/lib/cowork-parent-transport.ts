/**
 * Optional fixed runtime-error signal for supported embedding-preview origins.
 *
 * It sends a fixed, detail-free message so no dynamic error data crosses the
 * parent-frame boundary. The receiving parent is external to this repository
 * and must validate `event.source` itself.
 *
 * Security posture (do not weaken):
 *   - No wildcard (`"*"`) target origin. The message is posted to the
 *     configured origins; the browser delivers it only to a matching parent.
 *   - No inferred-origin logic (no `ancestorOrigins`, `document.referrer`,
 *     or `location.origin` fallback). If the embedding host origin is not in
 *     the allow-list below, the parent receives no signal.
 *   - No general-purpose "post to parent" API. Every other purpose-built
 *     signal lives in its own dedicated sibling file and imports
 *     `COWORK_PARENT_ORIGINS` from here rather than duplicating it. The
 *     app-mounted timing signal and bounded app-view snapshot use this list.
 *     Console forwarding remains a separate, runtime-derived-origin path in
 *     `console-capture.ts`.
 */

import {
  APP_RUNTIME_ERROR_MESSAGE,
  type AppRuntimeErrorMessage,
} from "@/types/app-message";

/**
 * Explicit allow-list of embedding-preview origins the app may signal.
 *
 * Only list origins that should receive these optional signals. Never add
 * `"*"`, a suffix match, or a runtime-derived origin.
 *
 * Exported so purpose-built parent-frame transports use the same list.
 */
export const COWORK_PARENT_ORIGINS: readonly string[] = [
  // Local development host with a certificate.
  "https://agentbuilder.local.microsoft.com:44300",
  // Local / CI no-certificate fallback.
  "http://localhost:44300",
  // Local development host.
  "https://local.loop.microsoft.com:8080",
  // Deployed Microsoft host.
  "https://m365.cloud.microsoft",
];

/** The single, immutable message posted to the parent frame. */
const CRASH_MESSAGE: AppRuntimeErrorMessage = APP_RUNTIME_ERROR_MESSAGE;

/**
 * Notify a supported embedding host that the app preview crashed.
 *
 * Takes NO arguments and sends NO dynamic data — only the two fixed string
 * literals in `APP_RUNTIME_ERROR_MESSAGE`. No-op when running top-level (not
 * embedded in a parent frame).
 */
export function postAppRuntimeErrorToCoworkParent(): void {
  if (window.parent === window) {
    return;
  }
  for (const targetOrigin of COWORK_PARENT_ORIGINS) {
    window.parent.postMessage(CRASH_MESSAGE, targetOrigin);
  }
}
