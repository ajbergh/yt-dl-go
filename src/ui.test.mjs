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
  title: "Test playlist", author: "Test channel", itemCount: 2, audioOnlyAvailable: true,
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
    if (path === "/api/health") return Response.json({ ready: missing.length === 0, missing, engine: healthEngine, capabilities: { combinedStreamsOnly: false, adaptiveStreamsSupported: true, externalBinariesRequired: false, mp3AudioSupported: true } });
    if (path === "/api/settings" && init.method === "PUT") return Response.json({ settings: JSON.parse(init.body) });
    if (path === "/api/folders/select" && init.method === "POST") return Response.json({ path: "C:\\Media\\YouTube" });
    if (path === "/api/settings") return Response.json({ settings: { defaultQuality: "best", maxConcurrentDownloads: 3, downloadLocation: "C:\\Downloads\\YouTube_Vault", namingPattern: "{channel} - {title} [{resolution}]", subfolderSorting: "channel", defaultCategory: "General", userCategories: ["General", "Music"], storageMode: "managed-published" } });
    if (path === "/api/inspect") return Response.json(inspection);
    if (path === "/api/jobs" && init.method === "POST") return Response.json(job, { status: 202 });
    if (path === "/api/jobs") return Response.json({ jobs: rows });
    if (path.endsWith("/cancel")) return Response.json({ ...job, status: "cancelled" });
    if (path.endsWith("/pause")) return Response.json({ ...job, status: "paused" });
    if (path.endsWith("/resume")) return Response.json({ ...job, status: "queued" });
    if (path.endsWith("/retry")) return Response.json({ ...job, id: "retried-job" }, { status: 202 });
    if (path.endsWith("/ticket")) return Response.json({ path: "/api/downloads/test-ticket" });
    if (path.endsWith("/thumbnail") && init.method !== "POST") return new Response(new Blob(["local-thumbnail"], { type: "image/jpeg" }), { status: 200, headers: { "Content-Type": "image/jpeg" } });
    if (path.endsWith("/filesystem") && init.method === "POST") return Response.json({ path: "C:\\Media\\test.mp4" });
    if (init.method === "DELETE" && path.endsWith("/published")) {
      const current = rows[0] ?? job;
      return Response.json({ ...current, files: (current.files ?? []).map(file => ({ ...file, publishedAvailable: false })) });
    }
    if (init.method === "DELETE" && path.endsWith("/managed")) {
      const current = rows[0] ?? job;
      return Response.json({ ...current, files: (current.files ?? []).map(file => ({ ...file, managedAvailable: false })) });
    }
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
  const buttons = [...container.querySelectorAll("button")];
  return buttons.find(item => item.textContent.trim() === label)
    ?? buttons.find(item => item.textContent.trim().startsWith(label));
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
async function setTextarea(element, value) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(testWindow.HTMLTextAreaElement.prototype, "value").set.call(element, value);
    element.dispatchEvent(new testWindow.Event("input", { bubbles: true }));
  });
}
async function setSelect(element, value) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(testWindow.HTMLSelectElement.prototype, "value").set.call(element, value);
    element.dispatchEvent(new testWindow.Event("change", { bubbles: true }));
  });
}
async function connect() {
  expect(container.textContent).toContain("Service connected");
}
async function remount() {
  requests = [];
  await act(async () => root.unmount());
  root = createRoot(container);
  await act(async () => {
    root.render(createElement(HomePage));
    await new Promise(resolve => setTimeout(resolve, 10));
  });
}

describe("Downloader UI and Go API integration", () => {
  test("connects to the built-in Go service automatically", () => {
    expect(container.textContent).toContain("No downloads yet");
    expect(container.textContent).toContain("Service connected");
    expect(requests.some(item => item.path === "/api/health")).toBe(true);
    expect(requests.some(item => item.path === "/api/jobs")).toBe(true);
    expect(requests.some(item => item.path === "/api/settings")).toBe(true);
    expect(requests.every(item => item.url.startsWith("http://127.0.0.1:8080/"))).toBe(true);
  });
  test("does not hide service readiness failures", async () => {
    missing = ["writable storage"];
    await remount();
    await click(button("Settings"));
    expect(container.querySelector('[role="alert"]').textContent).toContain("writable storage");
    expect(container.querySelector("#service-heading")).toBeTruthy();
    expect(requests.some(item => item.path === "/api/jobs")).toBe(false);
  });
  test("rejects an incompatible backend without manual setup", async () => {
    healthEngine = undefined;
    await remount();
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
    const category = container.querySelector('select[aria-label^="Category for"]');
    await setSelect(category, "Music");
    await click(container.querySelector('input[type="checkbox"]'));
    await click(button("Add 1 to queue"));
    const create = requests.find(item => item.path === "/api/jobs" && item.method === "POST");
    expect(JSON.parse(create.body)).toEqual({ url: inspection.url, quality: "best", mediaType: "video", category: "Music", rightsConfirmed: true });
    expect(container.textContent).toContain("Every exposed video will appear as an individual queue item");
  });
  test("applies batch inspection settings without removing per-item controls", async () => {
    await connect();
    await click(button("Batch URLs"));
    await setTextarea(container.querySelector("#video-url"), "https://www.youtube.com/watch?v=abcdefghijk\nhttps://www.youtube.com/watch?v=lmnopqrstuv");
    await click(button("Inspect qualities"));
    expect(container.querySelector('select[aria-label="Apply media type to all"]')).toBeTruthy();
    expect(container.querySelector('select[aria-label="Apply MP3 bitrate to all"]')).toBeTruthy();

    await setSelect(container.querySelector('select[aria-label="Apply quality to all"]'), "720");
    await setSelect(container.querySelector('select[aria-label="Apply category to all"]'), "Music");

    const qualities = [...container.querySelectorAll('select[aria-label^="Quality for"]')];
    const categories = [...container.querySelectorAll('select[aria-label^="Category for"]')];
    expect(qualities).toHaveLength(2);
    expect(categories).toHaveLength(2);
    expect(qualities.every(item => item.value === "720")).toBe(true);
    expect(categories.every(item => item.value === "Music")).toBe(true);

    await setSelect(categories[0], "General");
    expect(categories[0].value).toBe("General");
    expect(categories[1].value).toBe("Music");
  });
  test("queues original M4A audio without an MP3 bitrate", async () => {
    await connect();
    await setInput(container.querySelector("#video-url"), "https://www.youtube.com/watch?v=abcdefghijk&list=PL_example&index=4");
    await click(button("Inspect qualities"));
    await setSelect(container.querySelector('select[aria-label^="Media type for"]'), "audio");
    await setSelect(container.querySelector('select[aria-label^="Audio format for"]'), "m4a");
    expect(container.querySelector('select[aria-label^="MP3 bitrate for"]')).toBeFalsy();
    await click(container.querySelector('input[type="checkbox"]'));
    await click(button("Add 1 to queue"));
    const creates = requests.filter(item => item.path === "/api/jobs" && item.method === "POST");
    const body = JSON.parse(creates.at(-1).body);
    expect(body.audioFormat).toBe("m4a");
    expect(body.audioBitrate).toBeUndefined();
    expect(body.mediaType).toBe("audio");
  });

  test("persists only a supported preference through the service API", async () => {
    await click(button("Settings"));
    const quality = container.querySelector('select[aria-label="Default maximum video quality"]');
    await act(async () => {
      Object.getOwnPropertyDescriptor(testWindow.HTMLSelectElement.prototype, "value").set.call(quality, "720");
      quality.dispatchEvent(new testWindow.Event("change", { bubbles: true }));
    });
    await click(button("Save preferences"));
    const save = requests.find(item => item.path === "/api/settings" && item.method === "PUT");
    expect(JSON.parse(save.body)).toEqual({ defaultQuality: "720", maxConcurrentDownloads: 3, downloadLocation: "C:\\Downloads\\YouTube_Vault", namingPattern: "{channel} - {title} [{resolution}]", subfolderSorting: "channel", defaultCategory: "General", userCategories: ["General", "Music"], storageMode: "managed-published" });
    expect(container.textContent).toContain("Saved to SQLite");
  });
  test("uses the native folder picker for download location", async () => {
    await click(button("Settings"));
    await click(button("Browse"));
    expect(requests.some(item => item.path === "/api/folders/select" && item.method === "POST")).toBe(true);
    expect(container.querySelector("#download-location").value).toBe("C:\\Media\\YouTube");
    expect(container.textContent).toContain("Download folder selected");
  });
  test("persists the selected storage policy for new jobs", async () => {
    await click(button("Settings"));
    const publishedOnly = container.querySelector('input[name="storage-mode"][value="published-only"]');
    await click(publishedOnly);
    await click(button("Save preferences"));
    const saves = requests.filter(item => item.path === "/api/settings" && item.method === "PUT");
    expect(JSON.parse(saves.at(-1).body).storageMode).toBe("published-only");
    expect(container.textContent).toContain("Published only");
  });
  test("pauses a running job through the backend", async () => {
    rows = [{ ...job, status: "downloading" }];
    await remount();
    await connect();
    await click(button("Pause batch"));
    expect(requests.some(item => item.path.endsWith("/pause") && item.method === "POST")).toBe(true);
  });
  test("filters the Library by category and channel and persists layout preference", async () => {
    rows = [
      { ...job, id: "music-job", status: "completed", title: "Road Trip", category: "Music", mediaType: "audio", audioFormat: "m4a", completedCount: 1, totalCount: 1, files: [
        { id: "music-file", name: "001-song.m4a", title: "Song", author: "Channel A", category: "Music", size: 1024, managedAvailable: true, publishedAvailable: true, outputRelativePath: "Channel A/Song.m4a" },
      ] },
      { ...job, id: "coding-job", status: "completed", title: "Go Tutorial", category: "Coding", mediaType: "video", completedCount: 1, totalCount: 1, files: [
        { id: "coding-file", name: "001-code.mp4", title: "Pointers", author: "Channel B", category: "Coding", size: 2048, managedAvailable: true, publishedAvailable: false },
      ] },
    ];
    await remount();
    await connect();
    await click(button("Library"));
    expect(container.textContent).toContain("2 of 2 jobs visible");
    expect(container.textContent).toContain("Managed copies");
    expect(container.textContent).toContain("Published copies");

    await setSelect(container.querySelector('select[aria-label="Filter library by category"]'), "Music");
    expect(container.querySelectorAll('section[aria-labelledby="library-heading"] article')).toHaveLength(1);
    expect(container.textContent).toContain("Road Trip");

    await setSelect(container.querySelector('select[aria-label="Filter library by channel"]'), "Channel A");
    expect(container.querySelectorAll('section[aria-labelledby="library-heading"] article')).toHaveLength(1);

    await click(button("List"));
    expect(window.localStorage.getItem("yt-dl-go:library-layout")).toBe("list");
    await remount();
    await click(button("Library"));
    expect(button("List").getAttribute("aria-pressed")).toBe("true");
    window.localStorage.removeItem("yt-dl-go:library-layout");
  });

  test("prefers authenticated local thumbnails and retains remote artwork as fallback metadata", async () => {
    rows = [{ ...job, status: "completed", completedCount: 1, totalCount: 1, files: [
      { id: "file-thumb", name: "001-test.mp4", size: 1024, thumbnailUrl: "https://i.ytimg.com/vi/fixture/hqdefault.jpg", thumbnailLocalAvailable: true, thumbnailMimeType: "image/jpeg", managedAvailable: true, publishedAvailable: false },
    ] }];
    await remount();
    await connect();
    await click(button("Library"));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); });
    expect(requests.some(item => item.path.endsWith("/thumbnail") && item.url.includes("fileId=file-thumb"))).toBe(true);
    expect(container.querySelector('img[data-local-thumbnail="preferred"]')).toBeTruthy();
  });

  test("separates Library removal from managed and published media deletion", async () => {
    rows = [{ ...job, status: "completed", completedCount: 1, totalCount: 1, files: [
      { id: "file-1", name: "001-test.mp4", size: 1024, outputRelativePath: "Fixture channel/test.mp4", managedAvailable: true, publishedAvailable: true },
    ] }];
    await remount();
    await connect();
    await click(button("Library"));
    expect(button("Remove")).toBeTruthy();
    expect(button("Managed copy")).toBeTruthy();
    expect(button("Published copy")).toBeTruthy();
    expect(button("Delete everywhere")).toBeTruthy();
    expect(button("Reveal")).toBeTruthy();
    expect(button("Folder")).toBeTruthy();
    expect(button("Path")).toBeTruthy();

    await click(button("Reveal"));
    const filesystemRequest = requests.find(item => item.path.endsWith("/filesystem") && item.method === "POST");
    expect(JSON.parse(filesystemRequest.body)).toEqual({ fileId: "file-1", action: "reveal" });

    const originalConfirm = testWindow.confirm;
    testWindow.confirm = () => true;
    try {
      await click(button("Published copy"));
      expect(requests.some(item => item.path.endsWith("/published") && item.method === "DELETE")).toBe(true);
      expect(container.textContent).toContain("Published output files deleted");

      await click(button("Managed copy"));
      expect(requests.some(item => item.path.endsWith("/managed") && item.method === "DELETE")).toBe(true);
      expect(container.textContent).toContain("App-managed media copies deleted");
    } finally {
      testWindow.confirm = originalConfirm;
    }
  });

  test("previews managed media through an inline scoped ticket without autoplay", async () => {
    rows = [{ ...job, status: "completed", completedCount: 1, totalCount: 1, files: [
      { id: "preview-file", name: "001-preview.mp4", title: "Preview clip", size: 1024, mimeType: "video/mp4", managedAvailable: true, publishedAvailable: false },
    ] }];
    await remount();
    await connect();
    await click(button("Library"));
    await click(button("Preview"));
    const ticketRequest = requests.find(item => item.path.endsWith("/ticket") && item.method === "POST");
    expect(JSON.parse(ticketRequest.body)).toEqual({ fileId: "preview-file", inline: true });
    const dialog = container.querySelector('[role="dialog"]');
    expect(dialog).toBeTruthy();
    expect(dialog.textContent).toContain("Preview clip");
    const video = dialog.querySelector("video");
    expect(video).toBeTruthy();
    expect(video.autoplay).toBe(false);
    expect(video.getAttribute("src")).toBe("http://127.0.0.1:8080/api/downloads/test-ticket");
    await click(button("Close"));
    expect(container.querySelector('[role="dialog"]')).toBeFalsy();
  });

  test("requests an archive ticket and a native browser download", async () => {
    rows = [{ ...job, status: "completed", completedCount: 2, totalCount: 2, files: [
      { id: "file-1", name: "001-test.mp4", size: 1024 },
      { id: "file-2", name: "002-test.mp4", size: 2048 },
    ] }];
    await remount();
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
