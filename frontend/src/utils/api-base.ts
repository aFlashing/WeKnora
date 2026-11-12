/**
 * Origin the axios instance should talk to.
 *
 * The desktop shell injects `window.__WEKNORA_API_BASE__` — the loopback URL of
 * its embedded backend, e.g. `http://127.0.0.1:8080/api/v1`. Desktop API calls
 * have to use it instead of the page origin (`wails.localhost`): that origin is
 * served by the WebView's custom-protocol layer, which does not hand request
 * bodies to the Go reverse proxy, so every upload arrives as a zero-byte file.
 * The value is injected on DomReady, after this module loads, so callers resolve
 * the base per request rather than caching it here.
 */
export function getApiBaseUrl(): string {
  const injected = (globalThis as { __WEKNORA_API_BASE__?: string }).__WEKNORA_API_BASE__
  if (typeof injected === 'string' && injected.trim()) {
    // The injection carries the full API root; axios paths already start with
    // /api/v1, so keep only the origin.
    return injected.trim().replace(/\/api\/v1\/?$/, '').replace(/\/+$/, '')
  }
  // LocalHub plugin patch (2026-04-29): respect vite's BASE_URL so that
  // axios calls work at `/app/weknora/` (LocalHub reverse proxy). Without
  // this · axios hits `/api/v1/...` at LocalHub root · gets 404 "Cannot
  // POST". Strip trailing slash so axios doesn't produce `/app/weknora//api/v1/...`.
  // See: plugins/weknora/patches/api-base-baseurl.patch
  const base = (import.meta.env?.BASE_URL || '/').replace(/\/+$/, '');
  return base;
}
