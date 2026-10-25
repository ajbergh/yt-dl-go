import type { ServiceConnection } from "./downloader";

type DesktopBridge = {
  Connection(): Promise<ServiceConnection>;
  Save(path: string): Promise<boolean>;
  UIReady?(): Promise<void>; // Available only in the e2e-tagged native smoke build.
};

declare global {
  interface Window {
    go?: { main?: { DesktopApp?: DesktopBridge } };
  }
}

let connection: ServiceConnection | undefined;

export async function initializeDesktop(): Promise<void> {
  connection = undefined;
  const bridge = window.go?.main?.DesktopApp;
  if (!bridge) return;
  const result = await bridge.Connection();
  // Fail closed if bootstrap is incomplete. Never fall back to an unauthenticated
  // HTTP service or persist the per-launch capability in browser storage.
  if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(result.base) || !/^[a-f0-9]{64}$/.test(result.token)) {
    throw new Error("Could not connect to the private desktop service.");
  }
  connection = result;
}

export function desktopConnection(): ServiceConnection | undefined {
  return connection;
}

export function desktopBridge(): DesktopBridge | undefined {
  return window.go?.main?.DesktopApp;
}
