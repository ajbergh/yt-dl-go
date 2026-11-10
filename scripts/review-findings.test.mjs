import assert from "node:assert/strict";
import { readFileSync, existsSync } from "node:fs";
import { registerHooks } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import test from "node:test";
import ts from "typescript";
import { Window } from "happy-dom";

// Load the real TSX components without a browser server or generated files.
registerHooks({
  resolve(specifier, context, next) {
    if (specifier.startsWith(".") && context.parentURL?.startsWith("file:")) {
      const target = new URL(specifier, context.parentURL);
      for (const extension of [".ts", ".tsx"]) {
        const candidate = fileURLToPath(target) + extension;
        if (existsSync(candidate)) return next(pathToFileURL(candidate).href, context);
      }
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url.startsWith("file:") && /\.tsx?$/.test(url) && !url.includes("node_modules")) {
      let source = readFileSync(fileURLToPath(url), "utf8");
      if (url.endsWith("/pages/queue.tsx")) source = source.replace("  const itemActive =", "  globalThis.__reviewRows.push(job.id + ':' + item.index); const itemActive =");
      return { format: "module", shortCircuit: true, source: ts.transpileModule(source, { compilerOptions: { jsx: ts.JsxEmit.ReactJSX, module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText };
    }
    return next(url, context);
  },
});
const window = new Window({ url: "http://localhost:5173" });
for (const name of ["window", "document", "HTMLElement", "Element", "Node", "Event", "MouseEvent", "KeyboardEvent", "MutationObserver", "getComputedStyle"]) {
  Object.defineProperty(globalThis, name, { value: window[name], configurable: true });
}
Object.defineProperty(globalThis, "navigator", { value: window.navigator, configurable: true });
globalThis.IS_REACT_ACT_ENVIRONMENT = true;
globalThis.__reviewRows = [];
const { createElement, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { QueuePage } = await import("../src/pages/queue.tsx");
const { mergeSettingsDraft, reconcileQueueJobs } = await import("../src/hooks/use-service.ts");

test("reconnection preserves dirty fields and adopts unrelated server settings", () => {
  const baseline = { defaultQuality: "720", downloadSlots: 4, userCategories: ["General"] };
  const draft = { ...baseline, defaultQuality: "1080", userCategories: ["General", "Mine"] };
  const incoming = { ...baseline, downloadSlots: 8 };
  assert.deepEqual(mergeSettingsDraft(draft, baseline, incoming), { ...draft, downloadSlots: 8 });
  assert.deepEqual(mergeSettingsDraft(incoming, incoming, { ...incoming, defaultQuality: "480" }), { ...incoming, defaultQuality: "480" });
});

test("queue reconciliation keeps completed rows without duplicating incoming IDs", () => {
  const done = { id: "done", status: "completed" }, active = { id: "active", status: "queued" };
  assert.deepEqual(reconcileQueueJobs([done, active], [active]), [active, done]);
  assert.deepEqual(reconcileQueueJobs([done], [done]), [done]);
});

function queueProps(rows) {
  const noop = () => {};
  const props = { url: "", batchMode: false, drafts: [], serviceReady: true, inspecting: false, formError: "", mp3Supported: true, settings: { userCategories: ["General"], maxConcurrentDownloads: 3 }, rightsConfirmed: false, startPaused: false, submitting: false, visibleQueueRows: rows, activeCount: 0, totalCurrentSpeed: 0, completedQueueCount: 0, queuedCount: rows.length, jobs: rows.map(row => row.job), queuedJobsOrdered: [], busyAction: "", queueFilter: "all", search: "", filteredQueue: rows };
  for (const key of ["setUrl", "setBatchMode", "setDrafts", "inspectLinks", "setFormError", "applyMediaTypeToAll", "applyQualityToAll", "applyVideoStrategyToAll", "applyAudioFormatToAll", "applyAudioBitrateToAll", "applyCategoryToAll", "updatePlaylistSelection", "togglePlaylistEntry", "setRightsConfirmed", "setStartPaused", "addDownloads", "batchAction", "clearCompleted", "reorderQueuedJobs", "reorderPlaylistItems", "downloadNext", "setQueueFilter", "setSearch", "retryPlaylistItem", "retryAllFailedItems", "updateJobConfiguration", "bulkJobAction", "saveFile", "jobAction"]) props[key] = noop;
  return props;
}
function row(id, index = 1, kind = "video") {
  const item = { index, title: id, status: "queued", downloadedBytes: 0, totalBytes: 100, speedBytesPerSec: 0, etaSeconds: 0 };
  return { job: { id, title: id, url: "https://www.youtube.com/watch?v=abcdefghijk", kind, quality: "best", mediaType: "video", status: "queued", files: [], items: [item] }, item };
}

test("progress rerenders only its row and callbacks stay current after memo skips", async () => {
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  try {
    const rows = [row("first"), row("second")];
    const props = queueProps(rows);
    await act(async () => { root.render(createElement(QueuePage, props)); });
    globalThis.__reviewRows.length = 0;
    const changedItem = { ...rows[0].item, downloadedBytes: 10 };
    const nextRows = [{ ...rows[0], item: changedItem }, rows[1]];
    let command = "";
    const next = { ...props, filteredQueue: nextRows, visibleQueueRows: nextRows, jobAction: job => { command = job.id; } };
    await act(async () => { root.render(createElement(QueuePage, next)); });
    assert.deepEqual(globalThis.__reviewRows, ["first:1"]);
    await act(async () => { container.querySelector('[aria-label="Pause second"]').click(); });
    assert.equal(command, "second");
  } finally { await act(async () => root.unmount()); container.remove(); }
});

test("playlist controls remain visible when the first item is filtered out", async () => {
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  try {
    await act(async () => root.render(createElement(QueuePage, queueProps([row("playlist", 2, "playlist")]))));
    assert.ok(container.querySelector('[aria-label="Pause playlist playlist"]'));
    assert.ok(container.querySelector('[aria-label="Select playlist"]'));
  } finally { await act(async () => root.unmount()); container.remove(); }
});

test("bulk delete sends only selected jobs still visible", async () => {
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  try {
    const rows = [row("first"), row("second")]; const props = queueProps(rows); let deleted;
    props.bulkJobAction = async (action, ids) => { deleted = { action, ids }; return ids; };
    await act(async () => root.render(createElement(QueuePage, props)));
    await act(async () => { container.querySelector('[aria-label="Select first"]').click(); container.querySelector('[aria-label="Select second"]').click(); });
    await act(async () => root.render(createElement(QueuePage, { ...props, filteredQueue: [rows[0]] })));
    await act(async () => container.querySelector('[aria-label="Delete selected jobs"]').click());
    assert.deepEqual(deleted, { action: "delete", ids: ["first"] });
  } finally { await act(async () => root.unmount()); container.remove(); }
});

test("playlist progress does not render unchanged sibling rows", async () => {
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  try {
    const first = row("playlist", 1, "playlist"), second = row("playlist", 2, "playlist");
    const job = { ...first.job, items: [first.item, second.item] };
    const rows = [{ job, item: first.item }, { job, item: second.item }];
    const props = queueProps(rows);
    await act(async () => root.render(createElement(QueuePage, props)));
    globalThis.__reviewRows.length = 0;
    const changed = { ...first.item, downloadedBytes: 20 };
    const updatedJob = { ...job, downloadedBytes: 20, currentItem: "changed", items: [changed, second.item] };
    const nextRows = [{ job: updatedJob, item: changed }, { job: updatedJob, item: second.item }];
    await act(async () => root.render(createElement(QueuePage, { ...props, filteredQueue: nextRows, visibleQueueRows: nextRows })));
    assert.deepEqual(globalThis.__reviewRows, ["playlist:1"]);
  } finally { await act(async () => root.unmount()); container.remove(); }
});
