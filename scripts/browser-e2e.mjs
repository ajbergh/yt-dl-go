import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const serverDir = path.join(root, "src", "server");
const fixtureURL = "https://www.youtube.com/watch?v=E2EVIDEO001";

function findChrome() {
  const explicit = process.env.CHROME_BIN;
  if (explicit && existsSync(explicit)) return explicit;
  for (const command of ["google-chrome", "google-chrome-stable", "chromium", "chromium-browser"]) {
    const result = spawnSync(process.platform === "win32" ? "where" : "which", [command], { encoding: "utf8" });
    if (result.status === 0) {
      const candidate = result.stdout.split(/\r?\n/).find(Boolean)?.trim();
      if (candidate && existsSync(candidate)) return candidate;
    }
  }
  throw new Error("Browser E2E requires Google Chrome or Chromium on PATH (or CHROME_BIN).");
}

async function freePort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  const port = typeof address === "object" && address ? address.port : 0;
  await new Promise(resolve => server.close(resolve));
  return port;
}

function captureProcess(child, label) {
  let output = "";
  const append = chunk => {
    output += chunk.toString();
    if (output.length > 12000) output = output.slice(-12000);
  };
  child.stdout?.on("data", append);
  child.stderr?.on("data", append);
  child.on("error", error => append(`${label} process error: ${error.stack || error}\n`));
  return () => output;
}

async function waitForHTTP(url, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(2000) });
      if (response.ok) return response;
      lastError = new Error(`${response.status} ${response.statusText}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise(resolve => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${url}: ${lastError?.message || "unknown error"}`);
}

async function stopProcess(child) {
  if (!child || child.exitCode !== null) return;
  child.kill("SIGTERM");
  const exited = await Promise.race([
    new Promise(resolve => child.once("exit", () => resolve(true))),
    new Promise(resolve => setTimeout(() => resolve(false), 5000)),
  ]);
  if (!exited && child.exitCode === null) {
    child.kill("SIGKILL");
    await new Promise(resolve => child.once("exit", resolve));
  }
}

class CDP {
  constructor(url) {
    this.url = url;
    this.nextId = 1;
    this.pending = new Map();
    this.waiters = new Map();
  }

  async connect() {
    this.ws = new WebSocket(this.url);
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("Timed out opening Chrome DevTools WebSocket")), 10000);
      this.ws.addEventListener("open", () => {
        clearTimeout(timer);
        resolve();
      }, { once: true });
      this.ws.addEventListener("error", event => {
        clearTimeout(timer);
        reject(new Error(`Chrome DevTools WebSocket failed: ${event.message || "unknown error"}`));
      }, { once: true });
    });
    this.ws.addEventListener("message", event => {
      const message = JSON.parse(event.data);
      if (message.id) {
        const pending = this.pending.get(message.id);
        if (!pending) return;
        this.pending.delete(message.id);
        clearTimeout(pending.timer);
        if (message.error) pending.reject(new Error(message.error.message));
        else pending.resolve(message.result);
        return;
      }
      const listeners = this.waiters.get(message.method);
      if (!listeners?.length) return;
      for (const waiter of [...listeners]) {
        if (!waiter.predicate || waiter.predicate(message.params)) {
          clearTimeout(waiter.timer);
          listeners.splice(listeners.indexOf(waiter), 1);
          waiter.resolve(message.params);
        }
      }
    });
  }

  send(method, params = {}, timeoutMs = 15000) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`CDP ${method} timed out`));
      }, timeoutMs);
      this.pending.set(id, { resolve, reject, timer });
      this.ws.send(JSON.stringify({ id, method, params }));
    });
  }

  waitEvent(method, predicate = null, timeoutMs = 15000) {
    return new Promise((resolve, reject) => {
      const waiter = {
        predicate,
        resolve,
        reject,
        timer: setTimeout(() => {
          const listeners = this.waiters.get(method) ?? [];
          const index = listeners.indexOf(waiter);
          if (index >= 0) listeners.splice(index, 1);
          reject(new Error(`CDP event ${method} timed out`));
        }, timeoutMs),
      };
      const listeners = this.waiters.get(method) ?? [];
      listeners.push(waiter);
      this.waiters.set(method, listeners);
    });
  }

  async evaluate(expression, timeoutMs = 15000) {
    const result = await this.send("Runtime.evaluate", {
      expression,
      awaitPromise: true,
      returnByValue: true,
    }, timeoutMs);
    if (result.exceptionDetails) {
      throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text || "Browser evaluation failed");
    }
    return result.result?.value;
  }

  close() {
    this.ws?.close();
  }
}

async function waitFor(cdp, expression, label, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  let lastValue;
  while (Date.now() < deadline) {
    try {
      lastValue = await cdp.evaluate(expression);
      if (lastValue) return lastValue;
    } catch {
      // The page may be navigating/reloading. Keep polling until the deadline.
    }
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`Timed out waiting for ${label}; last value: ${JSON.stringify(lastValue)}`);
}

function bodyIncludes(text) {
  return `document.body?.innerText.includes(${JSON.stringify(text)}) === true`;
}

async function clickButton(cdp, label) {
  const result = await cdp.evaluate(`(() => {
    const label = ${JSON.stringify(label)};
    const button = [...document.querySelectorAll("button")].find(item => item.textContent.trim() === label)
      ?? [...document.querySelectorAll("button")].find(item => item.textContent.trim().startsWith(label));
    if (!button) return false;
    button.click();
    return true;
  })()`);
  assert.equal(result, true, `button not found: ${label}`);
}

async function clickSelector(cdp, selector) {
  const result = await cdp.evaluate(`(() => {
    const element = document.querySelector(${JSON.stringify(selector)});
    if (!element) return false;
    element.click();
    return true;
  })()`);
  assert.equal(result, true, `element not found: ${selector}`);
}

async function setValue(cdp, selector, value) {
  const result = await cdp.evaluate(`(() => {
    const element = document.querySelector(${JSON.stringify(selector)});
    if (!element) return false;
    const value = ${JSON.stringify(value)};
    let proto = HTMLInputElement.prototype;
    if (element instanceof HTMLTextAreaElement) proto = HTMLTextAreaElement.prototype;
    else if (element instanceof HTMLSelectElement) proto = HTMLSelectElement.prototype;
    const descriptor = Object.getOwnPropertyDescriptor(proto, "value");
    descriptor.set.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
    element.dispatchEvent(new Event("change", { bubbles: true }));
    return element.value;
  })()`);
  assert.equal(String(result), String(value), `could not set ${selector}`);
}

async function readValue(cdp, selector) {
  return cdp.evaluate(`document.querySelector(${JSON.stringify(selector)})?.value ?? null`);
}

async function getJSON(url) {
  const response = await fetch(url);
  assert.equal(response.ok, true, `GET ${url} failed: ${response.status}`);
  return response.json();
}

async function main() {
  const chromePath = findChrome();
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), "yt-dl-go-browser-e2e-"));
  const dataDir = path.join(tempRoot, "data");
  const chromeData = path.join(tempRoot, "chrome");
  const binary = path.join(tempRoot, process.platform === "win32" ? "yt-dl-go-e2e.exe" : "yt-dl-go-e2e");
  await mkdir(dataDir, { recursive: true, mode: 0o700 });
  await mkdir(chromeData, { recursive: true, mode: 0o700 });

  let service;
  let chrome;
  let cdp;
  let serviceLog = () => "";
  let chromeLog = () => "";

  try {
    const build = spawnSync("go", ["build", "-tags=e2e", "-o", binary, "."], {
      cwd: serverDir,
      encoding: "utf8",
      env: process.env,
    });
    if (build.status !== 0) {
      throw new Error(`E2E fixture server build failed:\n${build.stdout}\n${build.stderr}`);
    }

    const port = await freePort();
    const debugPort = await freePort();
    const baseURL = `http://127.0.0.1:${port}`;

    const startService = async () => {
      service = spawn(binary, [], {
        cwd: serverDir,
        env: {
          ...process.env,
          ADDR: `127.0.0.1:${port}`,
          DATA_DIR: dataDir,
          YT_DL_GO_E2E_FIXTURE: "1",
          NO_BROWSER: "1",
          RETENTION: "1h",
          JOB_TIMEOUT: "2m",
        },
        stdio: ["ignore", "pipe", "pipe"],
      });
      serviceLog = captureProcess(service, "service");
      await waitForHTTP(`${baseURL}/api/health`);
    };

    await startService();

    chrome = spawn(chromePath, [
      "--headless=new",
      "--disable-gpu",
      "--disable-dev-shm-usage",
      "--no-sandbox",
      "--no-first-run",
      "--no-default-browser-check",
      `--remote-debugging-port=${debugPort}`,
      `--user-data-dir=${chromeData}`,
      "about:blank",
    ], { stdio: ["ignore", "pipe", "pipe"] });
    chromeLog = captureProcess(chrome, "chrome");
    await waitForHTTP(`http://127.0.0.1:${debugPort}/json/version`);

    const targets = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`)).json();
    const target = targets.find(item => item.type === "page" && item.webSocketDebuggerUrl);
    assert.ok(target, "Chrome did not expose a debuggable page target");

    cdp = new CDP(target.webSocketDebuggerUrl);
    await cdp.connect();
    await cdp.send("Runtime.enable");
    await cdp.send("Page.enable");
    await cdp.send("Page.navigate", { url: `${baseURL}/` });
    await waitFor(cdp, bodyIncludes("Service connected"), "service connection in browser");

    // 1-3: actual service startup, URL inspection, and queue creation.
    await setValue(cdp, "#video-url", fixtureURL);
    await clickButton(cdp, "Inspect qualities");
    await waitFor(cdp, bodyIncludes("E2E Fixture Video"), "fixture inspection result");
    await clickSelector(cdp, 'input[aria-label="Confirm download rights"]');
    await clickButton(cdp, "Add 1 to queue");
    await waitFor(cdp, bodyIncludes("Downloading"), "active download");

    // 4: pause and resume through the browser UI while the fixture stream is live.
    await clickSelector(cdp, '[aria-label^="Pause E2E Fixture Video"]');
    await waitFor(cdp, bodyIncludes("Paused"), "paused job");
    await clickSelector(cdp, '[aria-label^="Resume E2E Fixture Video"]');

    // 5-6: completion must surface in the queue and Library.
    await waitFor(cdp, bodyIncludes("Completed"), "completed job", 30000);
    await clickButton(cdp, "Library");
    await waitFor(cdp, bodyIncludes("Download library"), "Library page");
    await waitFor(cdp, bodyIncludes("E2E Fixture Video"), "completed Library item");

    const beforeRestart = await getJSON(`${baseURL}/api/jobs`);
    assert.equal(beforeRestart.jobs.length, 1, "completed job was not persisted");
    assert.equal(beforeRestart.jobs[0].status, "completed", "fixture job did not complete");

    // 7: save settings through the real UI/API.
    await clickButton(cdp, "Settings");
    await setValue(cdp, "#naming-pattern", "E2E {title}");
    await setValue(cdp, 'input[aria-label="Maximum concurrent downloads"]', "2");
    await clickButton(cdp, "Save preferences");
    await waitFor(cdp, bodyIncludes("Saved"), "settings save acknowledgement");

    let settings = (await getJSON(`${baseURL}/api/settings`)).settings;
    assert.equal(settings.namingPattern, "E2E {title}");
    assert.equal(settings.maxConcurrentDownloads, 2);

    // 8: restart the service against the same SQLite/data directory, then reload
    // the browser and prove both settings and completed Library state survive.
    await stopProcess(service);
    await startService();
    await cdp.send("Page.reload", { ignoreCache: true });
    await waitFor(cdp, bodyIncludes("Service connected"), "service reconnect after restart", 30000);
    await clickButton(cdp, "Settings");
    await waitFor(cdp, `document.querySelector("#naming-pattern")?.value === "E2E {title}"`, "persisted naming pattern");
    assert.equal(await readValue(cdp, 'input[aria-label="Maximum concurrent downloads"]'), "2");
    await clickButton(cdp, "Library");
    await waitFor(cdp, bodyIncludes("E2E Fixture Video"), "persisted Library item after restart");

    settings = (await getJSON(`${baseURL}/api/settings`)).settings;
    assert.equal(settings.namingPattern, "E2E {title}");
    const afterRestart = await getJSON(`${baseURL}/api/jobs`);
    assert.equal(afterRestart.jobs.length, 1);
    assert.equal(afterRestart.jobs[0].status, "completed");

    // 9: destructive browser actions must surface an explicit confirmation
    // before the API mutation is allowed.
    const dialogPromise = cdp.waitEvent("Page.javascriptDialogOpening");
    const clickPromise = cdp.evaluate(`(() => {
      const button = [...document.querySelectorAll("button")].find(item => item.textContent.trim() === "Delete everywhere");
      if (!button) return false;
      button.click();
      return true;
    })()`, 30000);
    const dialog = await dialogPromise;
    assert.match(dialog.message, /Delete this media everywhere/i);
    await cdp.send("Page.handleJavaScriptDialog", { accept: true });
    assert.equal(await clickPromise, true);
    await waitFor(cdp, bodyIncludes("Your library is empty"), "destructive deletion completion", 20000);
    const afterDelete = await getJSON(`${baseURL}/api/jobs`);
    assert.equal(afterDelete.jobs.length, 0, "confirmed delete-everywhere did not remove Library history");

    console.log("Browser E2E passed: startup, inspect, queue, pause/resume, completion, Library, settings, restart persistence, confirmation.");
  } catch (error) {
    console.error(error?.stack || error);
    console.error("\n--- service log ---\n" + serviceLog());
    console.error("\n--- chrome log ---\n" + chromeLog());
    process.exitCode = 1;
  } finally {
    cdp?.close();
    await stopProcess(service);
    await stopProcess(chrome);
    await rm(tempRoot, { recursive: true, force: true });
  }
}

await main();
