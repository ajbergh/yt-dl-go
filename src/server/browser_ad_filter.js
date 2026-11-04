// Targeted player-data filtering, independently implemented for yt-dl-go.
// Method references: uBlockOrigin/uAssets filters/filters.txt and
// gorhill/uBlock src/js/resources/json-prune.js. No upstream code is bundled.
(() => {
  if (!/^(www\.|m\.)?youtube\.com$/.test(location.hostname)) return;
  if (window.__ytdlPlayerDataFilter) return;
  window.__ytdlPlayerDataFilter = true;
  const parse = JSON.parse;
  const stringify = JSON.stringify;
  const adKeys = ['adPlacements', 'playerAds', 'adSlots'];
  const strip = value => {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
    for (const key of adKeys) {
      if (Object.prototype.hasOwnProperty.call(value, key)) {
        delete value[key];
        window.__ytdlFilteredAdFields = (window.__ytdlFilteredAdFields || 0) + 1;
      }
    }
    return value;
  };
  const clean = value => {
    if (!value || typeof value !== 'object') return value;
    if (Array.isArray(value)) {
      for (const entry of value) clean(entry);
      return value;
    }
    // JSON.parse is shared with unrelated page data. Only recognize player
    // envelopes; do not recursively delete similarly named content fields.
    if (value.videoDetails || value.streamingData || value.playabilityStatus) strip(value);
    strip(value.playerResponse);
    strip(value.endpoint?.watchEndpoint?.unserializedPrefetchData?.playerResponse);
    return value;
  };
  const matchingURL = raw => {
    try {
      const url = new URL(raw, location.href);
      return url.origin === location.origin && /^\/youtubei\/v1\/(player|get_watch|next|resolve_url)\/?$/.test(url.pathname);
    } catch (_) { return false; }
  };
  for (const name of ['ytInitialPlayerResponse', 'playerResponse']) {
    const descriptor = Object.getOwnPropertyDescriptor(window, name);
    if (descriptor && (!descriptor.configurable || descriptor.get || descriptor.set || descriptor.writable === false)) continue;
    let value = strip(window[name]);
    Object.defineProperty(window, name, {
      configurable: true, enumerable: descriptor?.enumerable ?? true,
      get: () => value,
      set: next => { value = strip(next); },
    });
  }
  JSON.parse = new Proxy(parse, {
    apply(target, receiver, args) { return clean(Reflect.apply(target, receiver, args)); },
  });
  if (typeof window.fetch === 'function') {
    window.fetch = new Proxy(window.fetch, {
      apply(target, receiver, args) {
        const result = Reflect.apply(target, receiver, args);
        const raw = typeof args[0] === 'string' ? args[0] : args[0]?.url;
        if (!matchingURL(raw)) return result;
        return result.then(async response => {
          if (!response.ok) return response;
          try {
            const original = await response.clone().text();
            const filtered = stringify(clean(parse(original)));
            if (filtered === original) return response;
            const headers = new Headers(response.headers);
            headers.delete('content-length');
            headers.delete('content-encoding');
            const replacement = new Response(filtered, {status: response.status, statusText: response.statusText, headers});
            for (const key of ['url', 'type', 'redirected']) Object.defineProperty(replacement, key, {value: response[key]});
            return replacement;
          } catch (_) { return response; }
        });
      },
    });
  }
  if (typeof window.XMLHttpRequest === 'function') {
    const OriginalXHR = window.XMLHttpRequest;
    const matches = new WeakSet();
    const text = value => {
      try { return stringify(clean(parse(value))); } catch (_) { return value; }
    };
    window.XMLHttpRequest = class extends OriginalXHR {
      open(method, url, ...args) {
        matches.delete(this);
        if (matchingURL(url)) matches.add(this);
        return super.open(method, url, ...args);
      }
      get response() {
        const value = super.response;
        if (!matches.has(this) || this.readyState !== 4 || this.status < 200 || this.status >= 300) return value;
        if (this.responseType === 'json') return clean(value);
        return typeof value === 'string' ? text(value) : value;
      }
      get responseText() {
        const value = super.responseText;
        return matches.has(this) && this.readyState === 4 && this.status >= 200 && this.status < 300 ? text(value) : value;
      }
    };
  }
})();
