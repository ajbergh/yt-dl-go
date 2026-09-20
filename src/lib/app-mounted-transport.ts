/** Optional embedded-preview signal; the standalone downloader does not need a parent host. */

import { COWORK_PARENT_ORIGINS } from "./cowork-parent-transport";
import { APP_MOUNTED_TYPE, type AppMountedMessage } from "@/types/app-message";

// StrictMode may replay the effect and queue multiple animation frames. Send
// only the first signal for this page lifetime.
let appMountedSignalSent = false;

/**
 * Notify the embedding host that React committed and reached an animation-frame
 * checkpoint. `requestAnimationFrame` runs before paint, so this is not proof
 * that the browser has already displayed the frame.
 *
 * Fixed, detail-free signal — carries only a timestamp, no route/URL/app ID/
 * customer content. No-op when running top-level (not embedded in a parent
 * frame) or if already sent once.
 */
export function postAppMountedToCoworkParent(): void {
  if (window.parent === window || appMountedSignalSent) {
    return;
  }
  appMountedSignalSent = true;
  const message: AppMountedMessage = {
    type: APP_MOUNTED_TYPE,
    timestamp: Date.now(),
  };
  for (const targetOrigin of COWORK_PARENT_ORIGINS) {
    window.parent.postMessage(message, targetOrigin);
  }
}
