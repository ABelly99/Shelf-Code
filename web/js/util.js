// Small DOM + formatting helpers shared by all views.

// Let views write `el.append(cond ? node : null)`: skip null/false instead of printing "null".
for (const name of ['append', 'prepend', 'replaceChildren']) {
  const orig = Element.prototype[name];
  Element.prototype[name] = function (...nodes) { return orig.apply(this, nodes.flat().filter(n => n != null && n !== false)); };
}

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
      else if (k === 'dataset') Object.assign(el.dataset, v);
      else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2).toLowerCase(), v);
      else if (k === 'html') el.innerHTML = v;
      else if (k === 'value') el.value = v;
      else if (k === 'checked' || k === 'selected' || k === 'disabled' || k === 'hidden' || k === 'required' || k === 'multiple' || k === 'autofocus' || k === 'readOnly') el[k] = !!v;
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, v);
    }
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children) {
    if (c == null || c === false) continue;
    if (Array.isArray(c)) append(el, c);
    else if (c instanceof Node) el.appendChild(c);
    else el.appendChild(document.createTextNode(String(c)));
  }
}

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

export function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); return el; }

export function debounce(fn, ms = 250) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

// ---------- settings-aware formatting ----------

export const app = {
  settings: {},
  categories: [],
  uncategorized: 0,
  fields: [],
  labels: [],
  views: [],
  info: {},
};

export function money(cents, { blank = '' } = {}) {
  if (cents == null || cents === '' || Number.isNaN(Number(cents))) return blank;
  const s = app.settings;
  const neg = cents < 0;
  let v = Math.abs(Math.round(Number(cents)));
  const whole = Math.floor(v / 100).toString().replace(/\B(?=(\d{3})+(?!\d))/g, s.thousands_sep ?? ',');
  const frac = String(v % 100).padStart(2, '0');
  const num = `${whole}${s.decimal_sep ?? '.'}${frac}`;
  const sym = s.currency_symbol ?? '$';
  const out = s.currency_after ? `${num} ${sym}` : `${sym}${num}`;
  return neg ? '-' + out : out;
}

// "12.50", "12,50", "$1,234.5" -> cents (or null for empty, NaN for junk)
export function parseMoney(str) {
  if (str == null) return null;
  let s = String(str).trim();
  if (!s) return null;
  s = s.replace(/[^\d.,-]/g, '');
  const dec = app.settings.decimal_sep ?? '.';
  if (dec === ',') s = s.replace(/\./g, '').replace(',', '.');
  else if (!s.includes('.') && /^-?\d+,\d{2}$/.test(s)) s = s.replace(',', '.');
  else s = s.replace(/,/g, '');
  const f = parseFloat(s);
  if (Number.isNaN(f)) return NaN;
  return Math.round(f * 100);
}

export function moneyInput(cents) {
  if (cents == null) return '';
  const v = (Number(cents) / 100).toFixed(2);
  return (app.settings.decimal_sep ?? '.') === ',' ? v.replace('.', ',') : v;
}

export function num(n, decimals) {
  if (n == null || n === '') return '';
  const v = Number(n);
  if (Number.isNaN(v)) return String(n);
  const s = decimals != null ? v.toFixed(decimals) : String(Math.round(v * 1000) / 1000);
  return (app.settings.decimal_sep ?? '.') === ',' ? s.replace('.', ',') : s;
}

export function fmtDate(s, withTime = false) {
  if (!s) return '';
  const d = new Date(s.length <= 10 ? s + 'T00:00:00' : s);
  if (Number.isNaN(d.getTime())) return s;
  const opts = { year: 'numeric', month: 'short', day: 'numeric' };
  if (withTime) Object.assign(opts, { hour: '2-digit', minute: '2-digit' });
  return d.toLocaleString(undefined, opts);
}

export function relTime(s) {
  if (!s) return '';
  const d = new Date(s);
  const sec = (Date.now() - d.getTime()) / 1000;
  if (sec < 60) return 'just now';
  if (sec < 3600) return `${Math.floor(sec / 60)} min ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)} h ago`;
  if (sec < 86400 * 7) return `${Math.floor(sec / 86400)} d ago`;
  return fmtDate(s);
}

// Age from a YYYY-MM-DD date: "2 y 3 m", "5 m", "12 d"
export function ageFrom(dateStr) {
  if (!dateStr) return '';
  const d = new Date(dateStr + 'T00:00:00');
  if (Number.isNaN(d.getTime())) return '';
  const now = new Date();
  let months = (now.getFullYear() - d.getFullYear()) * 12 + (now.getMonth() - d.getMonth());
  if (now.getDate() < d.getDate()) months--;
  if (months < 0) return '';
  if (months === 0) return `${Math.max(0, Math.floor((now - d) / 86400000))} d`;
  const y = Math.floor(months / 12), m = months % 12;
  return y ? (m ? `${y} y ${m} m` : `${y} y`) : `${m} m`;
}

export function statusInfo(key) {
  const list = app.settings.statuses || [];
  return list.find(s => s.key === key) || { key, label: key ? key.replace(/_/g, ' ') : '', color: 'muted' };
}

export function statusBadge(key) {
  const s = statusInfo(key);
  const cls = { success: 'success', warning: 'warning', danger: 'danger', accent: 'accent' }[s.color] || '';
  return h('span', { class: 'badge ' + cls }, s.label);
}

export function qtyBadge(item) {
  if (item.quantity <= 0) return h('span', { class: 'badge danger' }, 'Out');
  if (item.low_stock_at != null && item.quantity <= item.low_stock_at) return h('span', { class: 'badge warning' }, 'Low');
  return null;
}

// ---------- categories & fields ----------

export function catById(id) { return app.categories.find(c => c.id === id); }

export function catPath(id) {
  const parts = [];
  let c = catById(id), guard = 0;
  while (c && guard++ < 20) { parts.unshift(c.name); c = c.parent_id ? catById(c.parent_id) : null; }
  return parts.join(' › ');
}

export function catAncestors(id) {
  const out = [];
  let c = catById(id), guard = 0;
  while (c && guard++ < 20) { out.unshift(c.id); c = c.parent_id ? catById(c.parent_id) : null; }
  return out;
}

export function catDescendants(id) {
  const out = [id];
  let changed = true;
  while (changed) {
    changed = false;
    for (const c of app.categories) if (c.parent_id && out.includes(c.parent_id) && !out.includes(c.id)) { out.push(c.id); changed = true; }
  }
  return out;
}

// Effective category settings walk up to the parent when a value is empty.
export function catSetting(id, key) {
  for (const cid of catAncestors(id).reverse()) {
    const c = catById(cid);
    const v = c?.[key];
    if (v != null && v !== '' && !(Array.isArray(v) && !v.length) && v !== false) return v;
  }
  return null;
}

export function fieldsFor(catId) {
  const chain = catId ? catAncestors(catId) : [];
  return app.fields
    .filter(f => f.category_id == null || chain.includes(f.category_id))
    .sort((a, b) => {
      const da = a.category_id == null ? -1 : chain.indexOf(a.category_id);
      const db = b.category_id == null ? -1 : chain.indexOf(b.category_id);
      return da - db || a.sort_order - b.sort_order || a.id - b.id;
    });
}

export function hiddenBuiltins(catId) {
  const set = new Set();
  for (const id of (catId ? catAncestors(catId) : [])) for (const k of (catById(id)?.hidden_builtins || [])) set.add(k);
  return set;
}

export const FIELD_TYPES = {
  text: 'Text', long_text: 'Long text', number: 'Number', money: 'Money', select: 'Dropdown', size: 'Size (ordered list)',
  multi_select: 'Multiple choice', boolean: 'Yes / no', date: 'Date', rating: 'Rating', color: 'Colour',
  url: 'Link', identifier: 'Identifier (IMEI, serial…)', age: 'Age (computed from a date)',
};

export function fieldValueText(f, v, item) {
  if (f.type === 'age') return ageFrom(item?.custom?.[f.options?.from]);
  if (v == null || v === '') return '';
  switch (f.type) {
    case 'money': return money(v);
    case 'number': { const s = num(v, f.options?.decimals); return f.options?.unit ? `${s} ${f.options.unit}` : s; }
    case 'rating': return '★'.repeat(Math.round(v)) + ` (${v})`;
    case 'boolean': return v ? 'Yes' : 'No';
    case 'multi_select': return Array.isArray(v) ? v.join(', ') : String(v);
    case 'date': return fmtDate(v);
    default: return String(v);
  }
}

// ---------- toasts, dialogs ----------

export function toast(msg, { error = false, action = null, actionLabel = 'Undo', ms = 3000 } = {}) {
  const box = document.getElementById('toasts');
  const t = h('div', { class: 'toast' + (error ? ' error' : ''), role: error ? 'alert' : 'status' }, h('span', null, msg));
  let timer;
  const close = () => { clearTimeout(timer); t.remove(); };
  if (action) {
    t.appendChild(h('button', { onclick: () => { close(); action(); } }, actionLabel));
    ms = Math.max(ms, 8000);
  }
  box.appendChild(t);
  timer = setTimeout(close, error ? Math.max(ms, 6000) : ms);
  return close;
}

let openDialogs = 0;
export const dialogOpen = () => openDialogs > 0;

// dialog({title, body: Node|fn(close), actions:[{label, primary, danger, onClick(close) -> false keeps open}], wide})
export function dialog({ title, body, actions = [], wide = false, xwide = false, onClose = null, cancelLabel = 'Cancel', noCancel = false }) {
  return new Promise(resolve => {
    let done = false;
    const close = (result) => {
      if (done) return;
      done = true;
      openDialogs--;
      overlay.remove();
      document.removeEventListener('keydown', onKey, true);
      onClose?.(result);
      resolve(result);
      prevFocus?.focus?.();
    };
    const prevFocus = document.activeElement;
    const content = typeof body === 'function' ? body(close) : body;
    const foot = h('div', { class: 'd-foot' },
      noCancel ? null : h('button', { class: 'btn', type: 'button', onclick: () => close(undefined) }, cancelLabel),
      actions.map(a => h('button', {
        class: 'btn' + (a.primary ? ' primary' : '') + (a.danger ? ' danger solid' : ''), type: 'button',
        onclick: async (e) => {
          const btn = e.currentTarget;
          btn.disabled = true;
          try {
            const r = await a.onClick?.(close);
            if (r !== false && !done) close(r === undefined ? a.value ?? true : r);
          } finally { btn.disabled = false; }
        },
      }, a.label)));
    const dlg = h('div', { class: 'dialog' + (wide ? ' wide' : '') + (xwide ? ' xwide' : ''), role: 'dialog', 'aria-modal': 'true' },
      title ? h('div', { class: 'd-head' }, h('h2', null, title)) : null,
      h('div', { class: 'd-body' }, content),
      (actions.length || !noCancel) ? foot : null);
    const overlay = h('div', { class: 'overlay', onmousedown: (e) => { if (e.target === overlay) close(undefined); } }, dlg);
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(undefined); }
      else if (e.key === 'Enter' && !e.shiftKey && e.target.tagName !== 'TEXTAREA' && e.target.tagName !== 'BUTTON' && e.target.tagName !== 'SELECT') {
        const primary = foot.querySelector('.btn.primary, .btn.danger.solid');
        if (primary) { e.preventDefault(); primary.click(); }
      }
    };
    document.addEventListener('keydown', onKey, true);
    document.body.appendChild(overlay);
    openDialogs++;
    setTimeout(() => (dlg.querySelector('[autofocus], input:not([type=checkbox]), select, textarea') || foot.querySelector('.primary') || dlg).focus?.(), 0);
  });
}

export async function confirmDialog(title, message, { okLabel = 'OK', danger = false } = {}) {
  const r = await dialog({ title, body: h('p', null, message), actions: [{ label: okLabel, primary: !danger, danger, value: true }] });
  return r === true;
}

export async function promptDialog(title, label, value = '', { placeholder = '', okLabel = 'OK', type = 'text' } = {}) {
  const input = h('input', { type, value, placeholder, style: { width: '100%' }, autofocus: true });
  const r = await dialog({
    title,
    body: h('div', { class: 'field' }, label ? h('label', null, label) : null, input),
    actions: [{ label: okLabel, primary: true, onClick: () => input.value }],
  });
  return r === undefined ? null : r;
}

export function lightbox(src) {
  const lb = h('div', { class: 'lightbox', onclick: () => lb.remove() }, h('img', { src, alt: '' }));
  const onKey = (e) => { if (e.key === 'Escape') { lb.remove(); document.removeEventListener('keydown', onKey, true); e.stopPropagation(); } };
  document.addEventListener('keydown', onKey, true);
  document.body.appendChild(lb);
}

export function download(url) {
  const a = h('a', { href: url, download: '' });
  document.body.appendChild(a);
  a.click();
  a.remove();
}

export function isTyping(el = document.activeElement) {
  if (!el) return false;
  const t = el.tagName;
  if (t === 'TEXTAREA' || t === 'SELECT' || el.isContentEditable) return true;
  if (t === 'INPUT') return !['checkbox', 'radio', 'button', 'submit', 'range', 'file'].includes(el.type);
  return false;
}

// Resize an image file in the browser: returns {full: Blob, thumb: Blob}
export async function resizeImage(file, max = 1600, thumbMax = 320) {
  const bmp = await loadBitmap(file);
  const make = async (limit, quality) => {
    const scale = Math.min(1, limit / Math.max(bmp.width, bmp.height));
    const w = Math.max(1, Math.round(bmp.width * scale)), hgt = Math.max(1, Math.round(bmp.height * scale));
    const c = document.createElement('canvas');
    c.width = w; c.height = hgt;
    const ctx = c.getContext('2d');
    ctx.fillStyle = '#fff';
    ctx.fillRect(0, 0, w, hgt);
    ctx.drawImage(bmp, 0, 0, w, hgt);
    return new Promise(res => c.toBlob(res, 'image/jpeg', quality));
  };
  return { full: await make(max, 0.85), thumb: await make(thumbMax, 0.8) };
}

async function loadBitmap(file) {
  if (window.createImageBitmap) {
    try { return await createImageBitmap(file, { imageOrientation: 'from-image' }); } catch { /* fall through */ }
  }
  return new Promise((res, rej) => {
    const img = new Image();
    img.onload = () => res(img);
    img.onerror = () => rej(new Error('This image could not be read. Use JPG, PNG or WebP.'));
    img.src = URL.createObjectURL(file);
  });
}

export function uid() {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

export function beep(ok = true) {
  try {
    const ctx = new (window.AudioContext || window.webkitAudioContext)();
    const o = ctx.createOscillator(), g = ctx.createGain();
    o.frequency.value = ok ? 1400 : 300;
    o.type = ok ? 'sine' : 'square';
    g.gain.value = 0.06;
    o.connect(g); g.connect(ctx.destination);
    o.start();
    o.stop(ctx.currentTime + (ok ? 0.08 : 0.18));
    setTimeout(() => ctx.close(), 400);
  } catch { /* no audio */ }
}

export function sortableList(container, itemSelector, onDrop) {
  // Simple drag-and-drop reordering using HTML5 DnD.
  let dragEl = null;
  container.addEventListener('dragstart', (e) => {
    const it = e.target.closest(itemSelector);
    if (!it || !container.contains(it)) return;
    dragEl = it;
    it.classList.add('dragging');
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', '');
  });
  container.addEventListener('dragend', () => {
    if (dragEl) dragEl.classList.remove('dragging');
    $$('.over', container).forEach(x => x.classList.remove('over'));
    if (dragEl) onDrop([...container.querySelectorAll(itemSelector)]);
    dragEl = null;
  });
  container.addEventListener('dragover', (e) => {
    if (!dragEl) return;
    e.preventDefault();
    const over = e.target.closest(itemSelector);
    if (!over || over === dragEl || !container.contains(over)) return;
    const r = over.getBoundingClientRect();
    const after = (r.width > r.height * 2) ? (e.clientY > r.top + r.height / 2) : (e.clientX > r.left + r.width / 2);
    over.parentNode.insertBefore(dragEl, after ? over.nextSibling : over);
  });
}
