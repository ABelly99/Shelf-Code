// Scan input from two sources:
//  1. Keyboard-wedge scanners (USB/Bluetooth, or Barcode Typer): very fast keystrokes ending with Enter.
//  2. The phone scanner relay: the server pushes scans to this page over Server-Sent Events.
// A scan typed into a text field stays in that field (like normal typing). Otherwise it is routed
// to the current page (print list, stock, quick sell…) or, by default, to the strict code lookup.

import { app, uid, isTyping, beep } from './util.js';
import { api } from './api.js';

const clientId = uid();
let handler = null; // (code, source) => void
let statusHandler = null;

export function onScan(fn) { handler = fn; }
export function onScannerStatus(fn) { statusHandler = fn; }

// ---------- keyboard wedge ----------

let buf = '';
let last = 0;
let fastCount = 0;

// Returns true if this keydown should be treated as part of a scan burst (so shortcuts wait).
export function wedgeKey(e) {
  if (e.ctrlKey || e.altKey || e.metaKey) { buf = ''; return false; }
  const t = performance.now();
  const gap = t - last;
  last = t;
  const threshold = Number(app.settings.scan_threshold_ms) || 35;
  if (gap > threshold * 3) { buf = ''; fastCount = 0; }
  else if (gap <= threshold) fastCount++;
  if (e.key === 'Enter' || e.key === 'Tab') {
    const minLen = Number(app.settings.scan_min_length) || 4;
    const code = buf;
    const burst = fastCount >= Math.max(2, code.length - 2);
    buf = ''; fastCount = 0;
    if (code.length >= minLen && burst) {
      e.preventDefault();
      e.stopPropagation();
      deliver(code, 'keyboard');
      return true;
    }
    return false;
  }
  if (e.key.length === 1) { buf += e.key; return buf.length > 1 && gap <= threshold; }
  return false;
}

function deliver(code, source) {
  code = code.trim();
  if (!code) return;
  if (app.settings.scan_beep) beep(true);
  handler?.(code, source);
}

// ---------- phone relay via SSE ----------

let es = null;
let retry = 1000;

export function startEvents() {
  if (es) es.close();
  es = new EventSource(`/api/events?client=${encodeURIComponent(clientId)}`);
  es.addEventListener('open', () => { retry = 1000; reportFocus(); });
  es.addEventListener('scan', (ev) => {
    const d = JSON.parse(ev.data);
    const el = document.activeElement;
    // Like Barcode Typer: if the cursor is in a field that accepts scans, type it there.
    if (isTyping(el) && el.tagName === 'INPUT' && el.closest('.accept-scan') && !el.closest('.lookup')) {
      const start = el.selectionStart ?? el.value.length, end = el.selectionEnd ?? el.value.length;
      el.value = el.value.slice(0, start) + d.code + el.value.slice(end);
      el.dispatchEvent(new Event('input', { bubbles: true }));
      el.dispatchEvent(new Event('change', { bubbles: true }));
      if (app.settings.scan_beep) beep(true);
      return;
    }
    deliver(d.code, d.source || 'phone');
  });
  es.addEventListener('scanner', (ev) => statusHandler?.(JSON.parse(ev.data)));
  es.onerror = () => {
    if (es.readyState === EventSource.CLOSED) {
      setTimeout(startEvents, retry);
      retry = Math.min(retry * 2, 15000);
    }
  };
}

function reportFocus() {
  api.post('/api/focus', { client: clientId, visible: document.visibilityState === 'visible' && document.hasFocus() }, { quiet: true }).catch(() => {});
}

window.addEventListener('focus', reportFocus);
window.addEventListener('blur', reportFocus);
document.addEventListener('visibilitychange', reportFocus);
let lastPing = 0;
document.addEventListener('pointerdown', () => { if (Date.now() - lastPing > 5000) { lastPing = Date.now(); reportFocus(); } }, true);
