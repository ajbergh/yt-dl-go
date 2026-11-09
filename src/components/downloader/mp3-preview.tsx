import { useEffect, useState, type Ref } from "react";

/**
 * The service decodes MP3 previews to PCM/WAV to avoid Chromium MP3 decoder
 * dropouts. Buffer that response locally, retaining its actual audio/wav MIME.
 * The URL and pending transfer belong to this preview's lifetime.
 */
export function MP3Preview({ url, mediaRef }: { url: string; mediaRef: Ref<HTMLAudioElement> }) {
  const [source, setSource] = useState("");
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    let objectURL = "";
    let disposed = false;
    async function prepare() {
      try {
        // The scoped ticket authorizes this request. No bearer or cookies are
        // needed, and the resulting Blob never contains a service credential.
        const response = await fetch(url, { credentials: "omit", signal: controller.signal });
        if (!response.ok) throw new Error("Audio preview could not be loaded. Close it and try again.");
        const data = await response.blob();
        if (!data.size) throw new Error("This audio file is empty.");
        if (disposed) return;
        objectURL = URL.createObjectURL(data);
        setSource(objectURL);
      } catch (failure) {
        if (!disposed) setError(failure instanceof Error ? failure.message : "Audio preview could not be loaded.");
      }
    }
    void prepare();
    return () => {
      disposed = true;
      controller.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [url, attempt]);

  if (error) return <div role="alert" className="space-y-3 text-sm text-amber-200"><p>{error}</p><button type="button" className="rounded-lg border border-neutral-700 px-3 py-2 text-neutral-200" onClick={() => { setSource(""); setError(""); setAttempt(value => value + 1); }}>Retry audio preview</button></div>;
  if (!source) return <p role="status" className="text-sm text-neutral-300">Preparing audio preview…</p>;
  return <audio ref={mediaRef} controls preload="auto" src={source} className="w-full" onError={() => setError("This audio could not be played. Try opening the saved file in your audio player.")}>This audio format cannot be played here.</audio>;
}
