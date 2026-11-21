// Talks to the SHELFCODE server. Every write sends X-Shelfcode (the server refuses writes without it).

export class ApiError extends Error {
  constructor(status, data) {
    super(data?.error || `Request failed (${status})`);
    this.status = status;
    this.data = data || {};
  }
}

let pending = 0;
let bar;
function progress(delta) {
  pending += delta;
  if (!bar) { bar = document.createElement('div'); bar.className = 'progress'; document.body.appendChild(bar); }
  clearTimeout(progress.t);
  if (pending > 0) {
    progress.t = setTimeout(() => { bar.style.width = '70%'; bar.hidden = false; }, 250);
  } else {
    bar.style.width = '100%';
    progress.t = setTimeout(() => { bar.hidden = true; bar.style.width = '0'; }, 200);
  }
}

export let onAuthNeeded = () => {};
export let onOffline = () => {};
export let onOnline = () => {};
export function setHandlers(h) {
  if (h.auth) onAuthNeeded = h.auth;
  if (h.offline) onOffline = h.offline;
  if (h.online) onOnline = h.online;
}

async function request(method, url, body, { form = false, quiet = false } = {}) {
  const opts = { method, headers: { 'X-Shelfcode': '1' }, credentials: 'same-origin' };
  if (body !== undefined) {
    if (form) opts.body = body;
    else { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
  }
  if (!quiet) progress(1);
  let res;
  try {
    res = await fetch(url, opts);
  } catch (e) {
    onOffline();
    throw new ApiError(0, { error: "Can't reach the SHELFCODE server. Is its window still open?" });
  } finally {
    if (!quiet) progress(-1);
  }
  onOnline();
  let data = null;
  const ct = res.headers.get('Content-Type') || '';
  if (ct.includes('application/json')) data = await res.json();
  else data = { text: await res.text() };
  if (res.status === 401 && data?.error === 'pin required') {
    onAuthNeeded();
  }
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}

export const api = {
  get: (url, opts) => request('GET', url, undefined, opts),
  post: (url, body, opts) => request('POST', url, body ?? {}, opts),
  put: (url, body, opts) => request('PUT', url, body ?? {}, opts),
  del: (url, opts) => request('DELETE', url, undefined, opts),
  upload: (url, formData) => request('POST', url, formData, { form: true }),
};

export function qs(params) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v == null || v === '' || (Array.isArray(v) && !v.length)) continue;
    p.set(k, typeof v === 'object' ? JSON.stringify(v) : String(v));
  }
  const s = p.toString();
  return s ? '?' + s : '';
}
