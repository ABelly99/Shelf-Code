// App shell: router, global lookup, scanner routing, keyboard shortcuts, first-run setup.

import { app, h, $, $$, clear, toast, dialog, dialogOpen, isTyping, beep, catPath } from './util.js';
import { api, setHandlers, ApiError } from './api.js';
import { onScan, onScannerStatus, startEvents, wedgeKey } from './scan.js';

const routes = {
  home: () => import('./views/home.js'),
  items: () => import('./views/items.js'),
  item: () => import('./views/item.js'),
  new: () => import('./views/item.js'),
  print: () => import('./views/print.js'),
  labels: () => import('./views/labels.js'),
  stock: () => import('./views/stock.js'),
  categories: () => import('./views/categories.js'),
  reports: () => import('./views/reports.js'),
  import: () => import('./views/importexport.js'),
  trash: () => import('./views/trash.js'),
  settings: () => import('./views/settings.js'),
};

let current = null;       // { onScan, canLeave, destroy, shortcuts }
let currentHash = '';
let ignoreHash = false;
let renderSeq = 0;

export async function refreshMeta(parts = ['settings', 'categories', 'fields', 'labels', 'views']) {
  const jobs = [];
  if (parts.includes('settings')) jobs.push(api.get('/api/settings', { quiet: true }).then(s => { app.settings = s; applyLook(); }));
  if (parts.includes('categories')) jobs.push(api.get('/api/categories', { quiet: true }).then(r => { app.categories = r.categories; app.uncategorized = r.uncategorized; }));
  if (parts.includes('fields')) jobs.push(api.get('/api/fields', { quiet: true }).then(r => { app.fields = r.fields; }));
  if (parts.includes('labels')) jobs.push(api.get('/api/docs/label', { quiet: true }).then(r => { app.labels = r.docs; }));
  if (parts.includes('views')) jobs.push(api.get('/api/docs/view', { quiet: true }).then(r => { app.views = r.docs; renderSavedViews(); }));
  await Promise.all(jobs);
}

function applyLook() {
  const root = document.documentElement;
  const t = app.settings.theme;
  if (t === 'light' || t === 'dark') root.dataset.theme = t; else delete root.dataset.theme;
  root.dataset.density = app.settings.density || 'compact';
}

function renderSavedViews() {
  const box = $('#saved-views');
  clear(box);
  if (!app.views.length) { box.appendChild(h('div', { class: 'muted small', style: { padding: '2px 10px' } }, 'None yet. Filter the item list, then “Save view”.')); return; }
  for (const v of app.views) box.appendChild(h('a', { href: `#/items?view=${v.id}`, dataset: { view: v.id } }, h('span', { class: 'name' }, v.name)));
  highlightNav();
}

export async function refreshCounts() {
  try {
    const [low, trash] = await Promise.all([
      api.get('/api/items?low=1&limit=1', { quiet: true }),
      api.get('/api/items?trash=1&limit=1', { quiet: true }),
    ]);
    $('#low-link').hidden = !low.total;
    $('#low-count').textContent = low.total || '';
    $('#trash-count').textContent = trash.total || '';
  } catch { /* offline */ }
}

// ---------- router ----------

function parseHash() {
  const raw = location.hash.replace(/^#\/?/, '') || 'home';
  const [path, q = ''] = raw.split('?');
  const parts = path.split('/').filter(Boolean).map(decodeURIComponent);
  return { name: parts[0] || 'home', parts, query: new URLSearchParams(q), hash: location.hash || '#/home' };
}

function highlightNav() {
  const { name, query } = parseHash();
  const navName = name === 'item' || name === 'new' ? 'items' : name === 'labels' ? 'print' : name;
  $$('#sidebar a').forEach(a => a.classList.remove('active'));
  if (name === 'items' && query.get('view')) {
    $(`#sidebar a[data-view="${query.get('view')}"]`)?.classList.add('active');
  } else {
    $(`#sidebar a[data-nav="${navName}"]`)?.classList.add('active');
  }
}

async function route() {
  if (ignoreHash) { ignoreHash = false; return; }
  if (current?.canLeave && !(await current.canLeave())) {
    ignoreHash = true;
    location.hash = currentHash;
    return;
  }
  const r = parseHash();
  currentHash = r.hash;
  $('#sidebar').classList.remove('open');
  highlightNav();
  const loader = routes[r.name];
  const main = $('#main');
  const seq = ++renderSeq;
  try { current?.destroy?.(); } catch { /* ignore */ }
  current = null;
  if (!loader) { clear(main).appendChild(h('div', { class: 'empty' }, h('h2', null, 'Page not found'), h('a', { href: '#/home' }, 'Go home'))); return; }
  try {
    const mod = await loader();
    if (seq !== renderSeq) return;
    clear(main);
    main.scrollTop = 0;
    const ctx = { main, parts: r.parts, query: r.query, name: r.name };
    const v = await mod.default(ctx);
    if (seq !== renderSeq) { v?.destroy?.(); return; }
    current = v || {};
    if (!main.contains(document.activeElement) || document.activeElement === document.body) {
      if (!main.querySelector('[autofocus]')) main.focus({ preventScroll: true });
    }
  } catch (e) {
    if (seq !== renderSeq) return;
    console.error(e);
    clear(main).appendChild(h('div', { class: 'empty' },
      h('h2', null, e instanceof ApiError && e.status === 404 ? 'Not found' : 'Something went wrong'),
      h('p', null, e.message),
      h('div', { class: 'row' }, h('a', { class: 'btn', href: '#/home' }, 'Go home'), h('button', { class: 'btn', onclick: () => route() }, 'Try again'))));
  }
  refreshCounts();
}

export function go(hash) {
  if (location.hash === hash) route(); else location.hash = hash;
}

// ---------- the strict code lookup ----------

const lookupInput = $('#lookup');

export async function lookup(code) {
  code = (code ?? '').trim();
  if (!code) return;
  let r;
  try { r = await api.get('/api/lookup?code=' + encodeURIComponent(code), { quiet: true }); }
  catch (e) { toast(e.message, { error: true }); return; }
  if (r.item_id) {
    lookupInput.value = '';
    lookupInput.blur();
    go('#/item/' + r.item_id);
    return;
  }
  if (r.reserved) {
    // A blank code printed earlier: assign it to a new (or existing) item.
    lookupInput.value = '';
    lookupInput.blur();
    go('#/new?blank=1&code=' + encodeURIComponent(code));
    return;
  }
  // No match: do nothing (by default). Optional quiet signal from Settings.
  if (!app.settings.lookup_keep_text) lookupInput.value = '';
  const sig = app.settings.lookup_nomatch;
  if (sig === 'sound') beep(false);
  if (sig === 'flash') {
    const form = $('#lookup-form');
    form.classList.add('flash');
    setTimeout(() => form.classList.remove('flash'), 900);
  }
  if (app.settings.lookup_unknown === 'offer') {
    toast(`No item has the code ${code}.`, { action: () => go('#/new?code=' + encodeURIComponent(code)), actionLabel: 'Create item with this code' });
  }
}

$('#lookup-form').addEventListener('submit', (e) => { e.preventDefault(); lookup(lookupInput.value); });

onScan(async (code, source) => {
  if (dialogOpen()) return;
  if (current?.onScan) {
    const handled = await current.onScan(code, source);
    if (handled) return;
  }
  lookup(code);
});

// ---------- scanner status dot ----------

onScannerStatus((s) => {
  app.scanner = s;
  const dot = $('#scan-dot');
  dot.className = 'scan-dot';
  let title = 'Phone scanner: off (Settings → Scanner)';
  if (s.status === 'connecting') { dot.classList.add('connecting'); title = 'Phone scanner: connecting…'; }
  else if (s.status === 'connected') { dot.classList.add(s.phones > 0 ? 'ready' : 'connected'); title = 'Phone scanner: ' + s.message; }
  else if (s.status === 'error') { dot.classList.add('error'); title = 'Phone scanner: ' + s.message; }
  dot.title = title;
  document.dispatchEvent(new CustomEvent('scanner-status', { detail: s }));
});

// ---------- keyboard ----------

let pendingShortcut = null;

document.addEventListener('keydown', (e) => {
  if (dialogOpen()) return;
  const typing = isTyping(e.target);
  if (!typing) {
    // A fast burst of keys is a scanner, not a person: hold back single-key shortcuts.
    const inBurst = wedgeKey(e);
    if (e.defaultPrevented) { clearTimeout(pendingShortcut); return; }
    if (inBurst) { clearTimeout(pendingShortcut); pendingShortcut = null; return; }
  }
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    if (current?.shortcuts?.save) { e.preventDefault(); current.shortcuts.save(e.shiftKey); }
    return;
  }
  if (e.key === 'F2') { e.preventDefault(); lookupInput.focus(); lookupInput.select(); return; }
  if (e.key === 'Escape' && e.target === lookupInput) { lookupInput.value = ''; lookupInput.blur(); return; }
  if (typing || e.ctrlKey || e.metaKey || e.altKey) return;
  const k = e.key;
  const run = (fn) => {
    e.preventDefault();
    clearTimeout(pendingShortcut);
    pendingShortcut = setTimeout(() => { pendingShortcut = null; fn(); }, 60);
  };
  if (k === '/') return run(() => { lookupInput.focus(); lookupInput.select(); });
  if (k === '?') return run(showShortcuts);
  if (current?.shortcuts?.[k]) return run(current.shortcuts[k]);
  if (k === 'n' || k === 'N') return run(() => go('#/new' + (current?.newQuery || '')));
});

function showShortcuts() {
  const rows = [
    ['/  or  F2', 'Scan or type a code (lookup box)'],
    ['N', 'New item'],
    ['E', 'Edit this item'],
    ['P', 'Print label for this item'],
    ['D', 'Duplicate this item'],
    ['S', 'Sell / stock out 1'],
    ['[  ]', 'Previous / next item in the list'],
    ['Ctrl+S', 'Save'],
    ['Ctrl+Shift+S', 'Save and start a new item'],
    ['Esc', 'Cancel / close'],
    ['Ctrl+F', 'Browser find; on Items, use the Filter box'],
    ['?', 'This help'],
  ];
  dialog({
    title: 'Keyboard shortcuts',
    body: h('table', { class: 't' }, h('tbody', null, rows.map(([k, d]) => h('tr', null, h('td', null, h('span', { class: 'kbd' }, k)), h('td', null, d))))),
    cancelLabel: 'Close',
  });
}
$('#shortcuts-btn').addEventListener('click', showShortcuts);
$('#menu-btn').addEventListener('click', () => $('#sidebar').classList.toggle('open'));
$('#new-btn').addEventListener('click', (e) => {
  if (current?.newQuery) { e.preventDefault(); go('#/new' + current.newQuery); }
});

// ---------- offline banner ----------

setHandlers({
  offline: () => {
    const b = $('#banner');
    if (!b.hidden) return;
    clear(b).append(h('span', null, "Can't reach the SHELFCODE server. Is the SHELFCODE window still open on the main PC?"), h('button', { onclick: () => location.reload() }, 'Retry'));
    b.hidden = false;
  },
  online: () => { $('#banner').hidden = true; },
  auth: () => showPin(),
});

// ---------- PIN (other devices on the network) ----------

function showPin() {
  if ($('.pin-screen')) return;
  const input = h('input', { type: 'password', inputmode: 'numeric', autocomplete: 'current-password', autofocus: true });
  const err = h('div', { class: 'danger-text small' });
  const screen = h('div', { class: 'pin-screen' }, h('form', {
    class: 'panel', onsubmit: async (e) => {
      e.preventDefault();
      try { await api.post('/api/pin', { pin: input.value }); location.reload(); }
      catch (x) { err.textContent = x.message; input.select(); }
    },
  }, h('div', { class: 'logo', style: { width: 'auto' } }, 'SHELFCODE'), h('p', { class: 'muted', style: { marginTop: '8px' } }, 'Enter the PIN to use SHELFCODE on this device.'), input, err, h('button', { class: 'btn primary', style: { width: '100%' } }, 'Unlock')));
  document.body.appendChild(screen);
  setTimeout(() => input.focus(), 0);
}

// ---------- first run ----------

async function onboarding() {
  const { templates } = await api.get('/api/templates');
  const picked = new Set();
  const cards = templates.map(t => {
    const card = h('div', { class: 'template-card', tabindex: 0, onclick: () => toggle(), onkeydown: (e) => { if (e.key === ' ') { e.preventDefault(); toggle(); } } },
      h('input', { type: 'checkbox', tabindex: -1 }),
      h('div', null, h('div', { class: 't-name' }, t.name), h('div', { class: 't-desc' }, t.description)));
    function toggle() {
      if (picked.has(t.id)) picked.delete(t.id); else picked.add(t.id);
      card.classList.toggle('on', picked.has(t.id));
      card.querySelector('input').checked = picked.has(t.id);
    }
    return card;
  });
  const r = await dialog({
    title: 'Welcome to SHELFCODE',
    xwide: true,
    body: h('div', null,
      h('p', null, 'Pick what you sell. Each choice adds a category with ready-made fields (you can change everything later in Categories & fields).'),
      h('div', { class: 'template-grid', style: { margin: '12px 0' } }, cards),
      h('p', { class: 'muted small' }, 'Already have data? You can also ', h('a', { href: '#/import' }, 'import a CSV file'), ' or ', h('a', { href: '#/settings/backup' }, 'restore a backup'), '.')),
    cancelLabel: 'Skip — start blank',
    actions: [{ label: 'Start', primary: true, onClick: async () => { if (picked.size) await api.post('/api/templates/apply', { ids: [...picked] }); return 'ok'; } }],
  });
  await api.put('/api/settings', { onboarded: true });
  await refreshMeta();
  if (r === 'ok') toast('Ready. Add your first item with “+ New item”.');
  route();
}

// ---------- boot ----------

async function boot() {
  try {
    const pin = await api.get('/api/pin-required', { quiet: true });
    if (pin.required) { showPin(); return; }
    await refreshMeta();
    app.info = await api.get('/api/info', { quiet: true });
  } catch (e) {
    clear($('#main')).appendChild(h('div', { class: 'empty' }, h('h2', null, "Can't load SHELFCODE"), h('p', null, e.message), h('button', { class: 'btn', onclick: () => location.reload() }, 'Retry')));
    return;
  }
  startEvents();
  window.addEventListener('hashchange', route);
  window.addEventListener('beforeunload', (e) => {
    if (current?.dirty?.()) { e.preventDefault(); e.returnValue = ''; }
  });
  if (!app.settings.onboarded && !app.categories.length) {
    await route();
    onboarding();
    return;
  }
  route();
}

export { catPath };
boot();
