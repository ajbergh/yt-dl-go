import { afterEach, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import { Window } from "happy-dom";
import { act, createElement } from "react";
import { HomePage } from "./pages/home";

const testWindow = new Window({ url: "http://localhost:5173/" });
for (const key of ["window", "document", "HTMLElement", "HTMLInputElement", "Node", "Event", "MouseEvent"]) {
  globalThis[key] = key === "window" ? testWindow : testWindow[key];
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true;
let createRoot;
let root;
let container;
const realFetch = globalThis.fetch;
let requests;
let rows;
let missing;
let healthEngine;

const job = {
  id: "fixture-job", url: "https://www.youtube.com/playlist?list=PL_example",
  kind: "playlist", quality: "1080", status: "queued", title: "Test playlist",
  progress: null, currentItem: "", completedCount: 0, totalCount: null,
  files: [], error: "", createdAt: "2026-01-01T00:00:00Z",
};
const inspection = {
  url: "https://www.youtube.com/playlist?list=PL_example", kind: "playlist",
  title: "Test playlist", author: "Test channel", itemCount: 2,
  items: [{ id: "abcdefghijk", title: "First video" }],
};

beforeAll(async () => { ({ createRoot } = await import("react-dom/client")); });
beforeEach(async () => {
  requests = [];
  rows = [];
  missing = [];
  healthEngine = "native-go";
  globalThis.fetch = async (url, init = {}) => {
    const path = new URL(url).pathname;
    requests.push({ url: String(url), path, ...init });
    if (path === "/api/health") return Response.json({ ready: missing.length === 0, missing, engine: healthEngine, capabilities: { combinedStreamsOnly: false, adaptiveStreamsSupported: true, externalBinariesRequired: false } });
    if (path === "/api/settings" && init.method === "PUT") return Response.json({ settings: JSON.parse(init.body) });
    if (path === "/api/settings") return Response.json({ settings: { defaultQuality: "best" } });
    if (path === "/api/inspect") return Response.json(inspection);
    if (path === "/api/jobs" && init.method === "POST") return Response.json(job, { status: 202 });
    if (path === "/api/jobs") return Response.json({ jobs: rows });
    if (path.endsWith("/cancel")) return Response.json({ ...job, status: "cancelled" });
    if (path.endsWith("/pause")) return Response.json({ ...job, status: "paused" });
    if (path.endsWith("/resume")) return Response.json({ ...job, status: "queued" });
    if (path.endsWith("/retry")) return Response.json({ ...job, id: "retried-job" }, { status: 202 });
    if (path.endsWith("/ticket")) return Response.json({ path: "/api/downloads/test-ticket" });
    if (init.method === "DELETE") return new Response(null, { status: 204 });
    return Response.json({ error: "Not found." }, { status: 404 });
  };
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  await act(async () => {
    root.render(createElement(HomePage));
    await new Promise(resolve => setTimeout(resolve, 0));
  });
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  globalThis.fetch = realFetch;
});

function button(label) {
  return [...container.querySelectorAll("button")].find(item => item.textContent.trim() === label);
}
async function click(element) {
  expect(element).toBeTruthy();
  await act(async () => { element.click(); });
}
async function setInput(element, value) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(testWindow.HTMLInputElement.prototype, "value").set.call(element, value);
    element.dispatchEvent(new testWindow.Event("input", { bubbles: true }));
  });
}
async function connect() {
  expect(container.textContent).toContain("Service connected");
}

describe("Downloader UI and Go API integration", () => {
  test("connects to the built-in Go service automatically", () => {
    expect(container.textContent).toContain("No downloads yet");
    expect(container.textContent).toContain("Service connected");
    expect(container.textContent).toContain("Built-in Go download service");
    expect(requests.some(item => item.path === "/api/health")).toBe(true);
    expect(requests.some(item => item.path === "/api/jobs")).toBe(true);
    expect(requests.some(item => item.path === "/api/settings")).toBe(true);
    expect(requests.every(item => item.url.startsWith("http://127.0.0.1:8080/"))).toBe(true);
  });
  test("does not hide service readiness failures", async () => {
    missing = ["writable storage"];
    await click(button("Settings"));
    expect(container.querySelector('[role="alert"]').textContent).toContain("writable storage");
    expect(container.querySelector("#service-heading")).toBeTruthy();
    expect(requests.some(item => item.path === "/api/jobs")).toBe(false);
  });
  test("rejects an incompatible backend without manual setup", async () => {
    healthEngine = undefined;
    await click(button("Settings"));
    expect(container.querySelector('[role="alert"]').textContent).toContain("older or incompatible");
    expect(requests.some(item => item.path === "/api/jobs")).toBe(false);
  });
  test("inspects a playlist and submits it only after permission confirmation", async () => {
    await connect();
    await setInput(container.querySelector("#video-url"), "https://www.youtube.com/watch?v=abcdefghijk&list=PL_example&index=4");
    await click(button("Inspect qualities"));
    expect(requests.some(item => item.path === "/api/inspect" && JSON.parse(item.body).url === inspection.url)).toBe(true);
    expect(container.textContent).toContain("Test playlist");
    expect(button("Add 1 to queue").disabled).toBe(true);
    await click(container.querySelector('input[type="checkbox"]'));
    await click(button("Add 1 to queue"));
    const create = requests.find(item => item.path === "/api/jobs" && item.method === "POST");
    expect(JSON.parse(create.body)).toEqual({ url: inspection.url, quality: "best", mediaType: "video", audioBitrate: "192k", rightsConfirmed: true });
    expect(container.textContent).toContain("Every exposed video will appear as an individual queue item");
  });
  test("persists only a supported preference through the service API", async () => {
    await click(button("Settings"));
    const quality = container.querySelector('select');
    await act(async () => {
      Object.getOwnPropertyDescriptor(testWindow.HTMLSelectElement.prototype, "value").set.call(quality, "720");
      quality.dispatchEvent(new testWindow.Event("change", { bubbles: true }));
    });
    await click(button("Save preferences"));
    const save = requests.find(item => item.path === "/api/settings" && item.method === "PUT");
    expect(JSON.parse(save.body)).toEqual({ defaultQuality: "720", maxConcurrentDownloads: 3 });
    expect(container.textContent).toContain("Saved to SQLite");
  });
  test("pauses a running job through the backend", async () => {
    rows = [{ ...job, status: "downloading" }];
    await connect();
    await click(button("Pause"));
    expect(requests.some(item => item.path.endsWith("/pause") && item.method === "POST")).toBe(true);
  });
  test("requests an archive ticket and a native browser download", async () => {
    rows = [{ ...job, status: "completed", completedCount: 2, totalCount: 2, files: [
      { id: "file-1", name: "001-test.mp4", size: 1024 },
      { id: "file-2", name: "002-test.mp4", size: 2048 },
    ] }];
    await connect();
    await click(button("Library"));
    let target;
    const original = testWindow.HTMLAnchorElement.prototype.click;
    testWindow.HTMLAnchorElement.prototype.click = function () { target = this.href; };
    try {
      await click(button("Save all as ZIP"));
      expect(target).toBe("http://127.0.0.1:8080/api/downloads/test-ticket");
      expect(requests.some(item => item.path.endsWith("/ticket") && item.body === "{}")).toBe(true);
    } finally { testWindow.HTMLAnchorElement.prototype.click = original; }
  });
});
