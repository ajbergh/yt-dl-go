import { afterEach, describe, expect, test } from "bun:test";
import { api, formatBytes, parseYouTubeURL, serviceURL } from "./lib/downloader";
import { confirmAction, desktopConnection, initializeDesktop } from "./lib/desktop";

describe("desktop bootstrap", () => {
  const originalWindow = globalThis.window;
  afterEach(async () => {
    globalThis.window = {};
    await initializeDesktop();
    if (originalWindow === undefined) delete globalThis.window;
    else globalThis.window = originalWindow;
  });
  test("uses native confirmations without opening a WebView dialog", async () => {
    const calls = [];
    globalThis.window = {
      confirm: () => { throw new Error("WebView confirmation must not be used"); },
      go: { main: { DesktopApp: { Confirm: async (title, message) => { calls.push([title, message]); return false; } } } },
    };
    expect(await confirmAction("Remove history?")).toBe(false);
    expect(calls).toEqual([["YouTube Vault", "Remove history?"]]);
    globalThis.window.go.main.DesktopApp.Confirm = async () => true;
    expect(await confirmAction("Remove history?")).toBe(true);
  });
  test("propagates native dialog errors instead of falling back to WebView confirmation", async () => {
    globalThis.window = {
      confirm: () => { throw new Error("Unexpected fallback"); },
      go: { main: { DesktopApp: { Confirm: async () => { throw new Error("Native dialog unavailable"); } } } },
    };
    await expect(confirmAction("Delete files?")).rejects.toThrow("Native dialog unavailable");
  });
  test("keeps confirmations available in ordinary browser builds", async () => {
    let asked;
    globalThis.window = { confirm: message => { asked = message; return true; } };
    expect(await confirmAction("Cancel downloads?")).toBe(true);
    expect(asked).toBe("Cancel downloads?");
  });
  test("uses the private connection supplied by Wails before mounting the UI", async () => {
    const connection = { base: "http://127.0.0.1:49152", token: "a".repeat(64) };
    globalThis.window = { go: { main: { DesktopApp: { Connection: async () => connection } } } };
    await initializeDesktop();
    expect(desktopConnection()).toEqual(connection);
  });
  test("does not turn ordinary browser builds into desktop clients", async () => {
    globalThis.window = {};
    await initializeDesktop();
    expect(desktopConnection()).toBeUndefined();
  });
  test("rejects an incomplete bridge connection without falling back to HTTP", async () => {
    globalThis.window = { go: { main: { DesktopApp: { Connection: async () => ({ base: "http://remote.invalid", token: "" }) } } } };
    await expect(initializeDesktop()).rejects.toThrow("Please close it and try again");
    expect(desktopConnection()).toBeUndefined();
  });
});

describe("YouTube URLs", () => {
  test.each([
    "https://www.youtube.com/watch?v=abcdefghijk",
    "https://youtu.be/abcdefghijk?t=10",
    "https://m.youtube.com/shorts/abcdefghijk",
    "https://youtube.com/live/abcdefghijk",
    "https://youtube.com/embed/abcdefghijk",
  ])("normalizes a video: %s", input => {
    expect(parseYouTubeURL(input)).toEqual({ canonical: "https://www.youtube.com/watch?v=abcdefghijk", kind: "video" });
  });
  test.each([
    "https://youtube.com/playlist?list=PL_example",
    "https://youtube.com/watch?v=abcdefghijk&list=PL_example&index=4",
    "https://youtu.be/abcdefghijk?list=PL_example",
    "https://www.youtube.com/show/VLPLrpqCP1_qdKhShZPqyWk9xJp0HIqRKc-t?sbp=Kgs5NllzeXJJNEt2OEAB",
  ])("selects the ENTIRE playlist: %s", input => {
    const expected = input.includes("/show/")
      ? "https://www.youtube.com/playlist?list=PLrpqCP1_qdKhShZPqyWk9xJp0HIqRKc-t"
      : "https://www.youtube.com/playlist?list=PL_example";
    expect(parseYouTubeURL(input)).toEqual({ canonical: expected, kind: "playlist" });
  });
  test.each([
    "http://youtube.com/watch?v=abcdefghijk",
    "https://youtube.com.evil.invalid/watch?v=abcdefghijk",
    "https://user:pass@youtube.com/watch?v=abcdefghijk",
    "https://youtube.com:444/watch?v=abcdefghijk",
    "https://youtube.com/redirect?list=PL_example",
    "https://youtube.com/playlist?list=bad%20id",
    "https://youtube.com/watch?v=short",
    "file:///etc/passwd",
    "--exec malicious",
    "https://youtube.com/@channel",
    "https://www.youtube.com/show/PLrpqCP1_qdKhShZPqyWk9xJp0HIqRKc-t",
  ])("rejects unsupported input: %s", input => expect(() => parseYouTubeURL(input)).toThrow());
});

describe("Download service", () => {
  test("allows local HTTP and remote HTTPS", () => {
    expect(serviceURL("http://127.0.0.1:8080/")).toBe("http://127.0.0.1:8080");
    expect(serviceURL("https://downloads.example.invalid/")).toBe("https://downloads.example.invalid");
  });
  test.each(["http://downloads.example.invalid", "https://user:pass@example.invalid", "https://example.invalid/api", "https://example.invalid?token=secret"])("rejects unsafe service origin: %s", input => {
    expect(() => serviceURL(input)).toThrow();
  });
  const originalFetch = globalThis.fetch;
  afterEach(() => { globalThis.fetch = originalFetch; });
  test("sends token only in the authorization header", async () => {
    globalThis.fetch = async (url, init) => {
      expect(url).toBe("https://service.example.invalid/api/jobs");
      expect(init.headers.get("Authorization")).toBe("Bearer test-token");
      expect(init.credentials).toBe("omit");
      return Response.json({ jobs: [] });
    };
    expect(await api({ base: "https://service.example.invalid", token: "test-token" }, "/api/jobs")).toEqual({ jobs: [] });
  });
  test("surfaces service errors", async () => {
    globalThis.fetch = async () => Response.json({ error: "Queue is full." }, { status: 429 });
    await expect(api({ base: "https://service.example.invalid", token: "" }, "/api/jobs")).rejects.toThrow("Queue is full.");
  });
  test("rejects non-JSON success", async () => {
    globalThis.fetch = async () => new Response("<html>Wrong server</html>");
    await expect(api({ base: "https://service.example.invalid", token: "" }, "/api/jobs")).rejects.toThrow("Please try again");
  });
});

test("human-readable file sizes", () => {
  expect(formatBytes(1024)).toBe("1.0 KB");
  expect(formatBytes(1048576)).toBe("1.0 MB");
});
