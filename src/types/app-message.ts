/**
 * Typed messages for optional embedded-preview diagnostics. The downloader
 * executable runs without a parent host; these shapes are used only when the
 * app is embedded by a compatible preview host. Runtime-error and mounted
 * messages contain no app content. The separately bounded app-view snapshot
 * intentionally includes an accessibility summary of the visible interface.
 */

/** Fixed message `type` for the runtime-error preview signal. */
export const PREVIEW_ERROR_TYPE = "preview-error" as const;

/** Fixed message `code` for a render-phase runtime crash. */
export const APP_RUNTIME_ERROR_CODE = "APP_RUNTIME_ERROR" as const;

/**
 * The complete, immutable runtime-error message. Two string literals only.
 * Deliberately has NO `payload`, `message`, `source`, `name`, `stack`,
 * `appId`, `url`, or any other field.
 */
export interface AppRuntimeErrorMessage {
  readonly type: typeof PREVIEW_ERROR_TYPE;
  readonly code: typeof APP_RUNTIME_ERROR_CODE;
}

/** The fixed runtime-error signal posted to a compatible embedding host. */
export const APP_RUNTIME_ERROR_MESSAGE: AppRuntimeErrorMessage = Object.freeze({
  type: PREVIEW_ERROR_TYPE,
  code: APP_RUNTIME_ERROR_CODE,
});

/** Fixed message `type` for the app-mounted timing signal. */
export const APP_MOUNTED_TYPE = "app-mounted" as const;

/**
 * The app-mounted message shape. `timestamp` is the only dynamic field —
 * a `Date.now()` clock reading, not customer content — filled in by the
 * sender at call time, which is why this is a type rather than a single
 * frozen constant like `APP_RUNTIME_ERROR_MESSAGE`.
 */
export interface AppMountedMessage {
  readonly type: typeof APP_MOUNTED_TYPE;
  readonly timestamp: number;
}

/** Fixed message `type` for the app-view snapshot. */
export const APP_VIEW_TYPE = "app-view" as const;

/** Wire version of the app-view envelope; bump on any field change. */
export const APP_VIEW_PROTOCOL_VERSION = 1 as const;

/** Maximum character count for the serialized accessible-name tree. */
export const APP_VIEW_MAX_TREE_CHARS = 24_000;

/** Hard cap on the matched route pattern, in characters. */
export const APP_VIEW_MAX_ROUTE_CHARS = 512;

/**
 * What the serialiser hands the sender. `route` is a route PATTERN from the
 * routes manifest (never the pathname, never params); `tree` is the
 * accessible-name tree cut at whole lines; `truncated` says whether any cap
 * cut it.
 */
export interface AppViewSnapshot {
  readonly route: string;
  readonly tree: string;
  readonly truncated: boolean;
}

/**
 * The app-view message shape. `timestamp` is the sender's `Date.now()` value
 * and is advisory because sender and receiver clocks may differ. The envelope
 * contains no app id.
 */
export interface AppViewMessage {
  readonly type: typeof APP_VIEW_TYPE;
  readonly v: typeof APP_VIEW_PROTOCOL_VERSION;
  readonly timestamp: number;
  readonly route: string;
  readonly tree: string;
  readonly truncated: boolean;
}

/** Union of the fixed message types this app can send to an embedding host. */
export type AppMessage = AppRuntimeErrorMessage | AppMountedMessage | AppViewMessage;
