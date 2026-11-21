// Reusable inputs: custom field editors, category picker, tags, extra codes, images.

import { h, app, money, moneyInput, parseMoney, catPath, ageFrom, FIELD_TYPES, toast, resizeImage, sortableList, lightbox, confirmDialog } from './util.js';
import { FORMATS } from './barcode.js';
import { api } from './api.js';

// A field input returns { el, get(), focus(), error(msg) }
export function fieldInput(f, value, item = null) {
  const id = 'f_' + f.key + '_' + Math.random().toString(36).slice(2, 7);
  const err = h('div', { class: 'err' });
  const opts = f.options || {};
  let input, get;
  const choices = Array.isArray(opts.choices) ? opts.choices.map(String) : [];
  switch (f.type) {
    case 'long_text':
      input = h('textarea', { id, rows: 3, value: value ?? '' });
      get = () => input.value;
      break;
    case 'number':
    case 'rating': {
      const n = h('input', { id, type: 'text', inputmode: 'decimal', value: value ?? '', placeholder: f.type === 'rating' ? '1–5' : '' });
      input = opts.unit ? h('div', { class: 'input-unit' }, n, h('span', { class: 'unit' }, opts.unit)) : n;
      get = () => {
        const s = n.value.trim().replace(',', '.');
        if (!s) return null;
        const v = Number(s);
        if (Number.isNaN(v)) throw new Error(`${f.label}: enter a number`);
        if (opts.min != null && v < opts.min) throw new Error(`${f.label}: at least ${opts.min}`);
        if (opts.max != null && v > opts.max) throw new Error(`${f.label}: at most ${opts.max}`);
        return v;
      };
      input.focusTarget = n;
      break;
    }
    case 'money': {
      const n = h('input', { id, type: 'text', inputmode: 'decimal', value: moneyInput(value) });
      input = moneyWrap(n);
      get = () => {
        const c = parseMoney(n.value);
        if (Number.isNaN(c)) throw new Error(`${f.label}: enter an amount`);
        return c;
      };
      input.focusTarget = n;
      break;
    }
    case 'select':
    case 'size':
    case 'color': {
      const listId = id + '_list';
      if (opts.allow_new || f.type === 'color') {
        // Free text with suggestions: pick from the list or type a new value.
        const txt = h('input', { id, type: 'text', list: listId, value: value ?? '', placeholder: choices.length ? 'Pick or type…' : '' });
        const dl = h('datalist', { id: listId }, choices.map(c => h('option', { value: c })));
        const wrap = h('div', null, txt, dl);
        get = () => txt.value.trim() || null;
        wrap.focusTarget = txt;
        input = wrap;
      } else {
        const sel = h('select', { id }, h('option', { value: '' }, '—'), choices.map(c => h('option', { value: c, selected: c === value }, c)));
        if (value && !choices.includes(String(value))) sel.appendChild(h('option', { value, selected: true }, value));
        input = sel;
        get = () => sel.value || null;
      }
      break;
    }
    case 'multi_select': {
      const cur = new Set(Array.isArray(value) ? value.map(String) : value ? [String(value)] : []);
      const all = [...new Set([...choices, ...cur])];
      const box = h('div', { class: 'chips' });
      const render = () => {
        box.replaceChildren(...all.map(c => h('span', { class: 'chip pick' + (cur.has(c) ? ' on' : ''), tabindex: 0, role: 'checkbox', 'aria-checked': cur.has(c) ? 'true' : 'false',
          onclick: () => { cur.has(c) ? cur.delete(c) : cur.add(c); render(); },
          onkeydown: (e) => { if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); cur.has(c) ? cur.delete(c) : cur.add(c); render(); box.querySelectorAll('.chip')[all.indexOf(c)]?.focus(); } } }, c)),
        opts.allow_new !== false ? h('button', { type: 'button', class: 'btn small ghost', onclick: () => {
          const v = prompt('New choice');
          if (v && v.trim()) { const t = v.trim(); if (!all.includes(t)) all.push(t); cur.add(t); render(); }
        } }, '+ other') : null);
      };
      render();
      input = box;
      get = () => all.filter(c => cur.has(c));
      break;
    }
    case 'boolean': {
      const cb = h('input', { id, type: 'checkbox', checked: !!value });
      input = h('label', { class: 'check', style: { height: '32px' } }, cb, h('span', null, 'Yes'));
      get = () => cb.checked;
      input.focusTarget = cb;
      break;
    }
    case 'date':
      input = h('input', { id, type: 'date', value: value ?? '' });
      get = () => input.value || null;
      break;
    case 'url':
      input = h('input', { id, type: 'url', value: value ?? '', placeholder: 'https://…' });
      get = () => input.value.trim() || null;
      break;
    case 'identifier':
      input = h('input', { id, type: 'text', class: 'code-input', value: value ?? '', spellcheck: 'false', autocomplete: 'off' });
      get = () => input.value.trim() || null;
      break;
    case 'age': {
      input = h('div', { class: 'muted', style: { height: '32px', display: 'flex', alignItems: 'center' } }, ageFrom(item?.custom?.[opts.from]) || `(from ${opts.from || 'a date field'})`);
      get = () => undefined;
      break;
    }
    default:
      input = h('input', { id, type: 'text', value: value ?? '' });
      get = () => input.value.trim() || null;
  }
  const label = h('label', { for: id }, f.label, f.required ? h('span', { class: 'req' }, ' *') : null,
    f.as_code ? h('span', { class: 'muted', title: 'Scanning this value opens the item' }, ' · scannable') : null);
  const el = h('div', { class: 'field' + (f.type === 'long_text' || f.type === 'multi_select' ? ' span2' : '') }, label, input,
    f.help_text ? h('div', { class: 'help' }, f.help_text) : null, err);
  return {
    el, key: f.key, field: f,
    get,
    focus: () => (input.focusTarget || input).focus?.(),
    error: (msg) => { err.textContent = msg || ''; (input.focusTarget || input).classList?.toggle('invalid', !!msg); },
  };
}

export function moneyWrap(input) {
  const s = app.settings;
  const sym = h('span', { class: 'unit' + (s.currency_after ? '' : ' before') }, s.currency_symbol || '$');
  return s.currency_after ? h('div', { class: 'input-unit' }, input, sym) : h('div', { class: 'input-unit' }, sym, input);
}

export function categoryOptions(selected, { none = '(no category)', exclude = [] } = {}) {
  const out = [h('option', { value: '' }, none)];
  const add = (parentId, depth) => {
    for (const c of app.categories.filter(c => (c.parent_id || null) === parentId).sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name))) {
      if (exclude.includes(c.id)) continue;
      out.push(h('option', { value: c.id, selected: c.id === selected }, '  '.repeat(depth) + c.name));
      add(c.id, depth + 1);
    }
  };
  add(null, 0);
  return out;
}

export function categorySelect(selected, opts) {
  return h('select', null, categoryOptions(selected, opts));
}

export function statusSelect(value) {
  const list = app.settings.statuses || [];
  const sel = h('select', null, list.map(s => h('option', { value: s.key, selected: s.key === value }, s.label)));
  if (value && !list.some(s => s.key === value)) sel.appendChild(h('option', { value, selected: true }, value));
  return sel;
}

export function formatSelect(value, { allowDefault = false } = {}) {
  return h('select', null,
    allowDefault ? h('option', { value: '' }, '(use item format)') : null,
    Object.entries(FORMATS).map(([k, f]) => h('option', { value: k, selected: k === value, title: f.hint }, f.label)));
}

export function tagInput(tags = []) {
  const cur = [...tags];
  const box = h('div', { class: 'chips', style: { alignItems: 'center' } });
  const input = h('input', { type: 'text', placeholder: 'Add tag…', style: { width: '120px', height: '26px' },
    onkeydown: (e) => {
      if ((e.key === 'Enter' || e.key === ',') && input.value.trim()) { e.preventDefault(); add(input.value); }
      else if (e.key === 'Backspace' && !input.value && cur.length) { cur.pop(); render(); }
    },
    onblur: () => { if (input.value.trim()) add(input.value); } });
  const add = (v) => { v = v.trim().replace(/,$/, ''); if (v && !cur.some(t => t.toLowerCase() === v.toLowerCase())) cur.push(v); input.value = ''; render(); input.focus(); };
  const render = () => box.replaceChildren(...cur.map((t, i) => h('span', { class: 'chip' }, t, h('button', { type: 'button', 'aria-label': 'Remove ' + t, onclick: () => { cur.splice(i, 1); render(); } }, '×'))), input);
  render();
  return { el: box, get: () => { if (input.value.trim()) add(input.value); return [...cur]; } };
}

const CODE_KINDS = { upc: 'UPC / EAN', imei: 'IMEI', serial: 'Serial', sku: 'SKU', isbn: 'ISBN', other: 'Other' };

export function extraCodesEditor(list = []) {
  const rows = [];
  const box = h('div', { class: 'extra-codes accept-scan' });
  const addBtn = h('button', { type: 'button', class: 'btn small', onclick: () => { addRow({ kind: 'upc', code: '' }); rows[rows.length - 1].input.focus(); } }, '+ Add code');
  const addRow = (e) => {
    const kind = h('select', null, Object.entries(CODE_KINDS).map(([k, l]) => h('option', { value: k, selected: k === e.kind }, l)));
    const input = h('input', { type: 'text', class: 'code-input', value: e.code, placeholder: 'Scan or type', spellcheck: 'false', autocomplete: 'off' });
    const row = h('div', { class: 'ec' }, kind, input, h('button', { type: 'button', class: 'btn icon ghost', 'aria-label': 'Remove code', onclick: () => { row.remove(); rows.splice(rows.indexOf(r), 1); } }, '×'));
    const r = { row, kind, input };
    rows.push(r);
    box.insertBefore(row, addBtn);
  };
  box.appendChild(addBtn);
  list.forEach(addRow);
  return { el: box, get: () => rows.map(r => ({ kind: r.kind.value, code: r.input.value.trim() })).filter(e => e.code) };
}

// Image editor. For a saved item it uploads immediately; for a new item it queues files until save.
export function imageEditor(itemId, images = []) {
  let list = images.map(i => ({ ...i }));
  const queued = [];
  const fileInput = h('input', { type: 'file', accept: 'image/*', multiple: true, hidden: true, onchange: () => { addFiles([...fileInput.files]); fileInput.value = ''; } });
  const box = h('div', { class: 'img-edit' });
  const drop = h('div', { class: 'drop', tabindex: 0, onclick: () => fileInput.click(), onkeydown: (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); fileInput.click(); } } },
    h('div', null, '+ Photo'), h('div', null, 'drop, paste or click'));
  ['dragenter', 'dragover'].forEach(ev => drop.addEventListener(ev, (e) => { if (e.dataTransfer?.types?.includes('Files')) { e.preventDefault(); drop.classList.add('over'); } }));
  ['dragleave', 'drop'].forEach(ev => drop.addEventListener(ev, () => drop.classList.remove('over')));
  drop.addEventListener('drop', (e) => { e.preventDefault(); addFiles([...e.dataTransfer.files]); });

  const render = () => {
    box.replaceChildren(
      ...list.map((im, i) => h('div', { class: 'im', draggable: 'true', dataset: { id: im.id ?? '', q: im.q ?? '' } },
        h('img', { src: im.thumb, alt: '', onclick: () => lightbox(im.url) }),
        i === 0 ? h('span', { class: 'star' }, 'Main') : null,
        h('button', { type: 'button', class: 'x', 'aria-label': 'Remove photo', onclick: (e) => { e.stopPropagation(); remove(im); } }, '×'))),
      drop, fileInput);
  };
  sortableList(box, '.im', async (els) => {
    const order = els.map(el => list.find(im => String(im.id ?? '') === el.dataset.id && String(im.q ?? '') === el.dataset.q)).filter(Boolean);
    list = order;
    render();
    if (itemId) {
      const ids = list.filter(i => i.id).map(i => i.id);
      await api.put(`/api/items/${itemId}/images`, { ids }).catch(e => toast(e.message, { error: true }));
    }
  });

  async function addFiles(files) {
    for (const file of files) {
      if (!file.type.startsWith('image/')) { toast(`${file.name} is not an image.`, { error: true }); continue; }
      try {
        const { full, thumb } = await resizeImage(file);
        if (itemId) {
          const fd = new FormData();
          fd.append('full', full, 'full.jpg');
          fd.append('thumb', thumb, 'thumb.jpg');
          const r = await api.upload(`/api/items/${itemId}/images`, fd);
          list.push(r);
        } else {
          const q = Math.random().toString(36).slice(2);
          queued.push({ q, full, thumb });
          list.push({ q, url: URL.createObjectURL(full), thumb: URL.createObjectURL(thumb) });
        }
        render();
      } catch (e) { toast(e.message, { error: true }); }
    }
  }

  async function remove(im) {
    if (im.id) {
      if (!(await confirmDialog('Remove photo?', 'This photo will be deleted.', { okLabel: 'Remove', danger: true }))) return;
      await api.del(`/api/images/${im.id}`).catch(e => toast(e.message, { error: true }));
    } else {
      const i = queued.findIndex(x => x.q === im.q);
      if (i >= 0) queued.splice(i, 1);
    }
    list = list.filter(x => x !== im);
    render();
  }

  // Paste an image from the clipboard (Ctrl+V) while the editor is on screen.
  const onPaste = (e) => {
    if (!box.isConnected) { document.removeEventListener('paste', onPaste); return; }
    const files = [...(e.clipboardData?.files || [])].filter(f => f.type.startsWith('image/'));
    if (files.length) { e.preventDefault(); addFiles(files); }
  };
  document.addEventListener('paste', onPaste);

  render();
  return {
    el: box,
    hasQueued: () => queued.length > 0,
    // Upload queued photos (for a new item) in the order shown.
    async flush(newId) {
      for (const im of list) {
        const q = queued.find(x => x.q === im.q);
        if (!q) continue;
        const fd = new FormData();
        fd.append('full', q.full, 'full.jpg');
        fd.append('thumb', q.thumb, 'thumb.jpg');
        await api.upload(`/api/items/${newId}/images`, fd);
      }
      queued.length = 0;
    },
    destroy: () => document.removeEventListener('paste', onPaste),
  };
}

export function fieldTypeName(t) { return FIELD_TYPES[t] || t; }
export { money, catPath };

// A "More ▾" style button with a small menu. items: [{label, onClick, danger}]
export function menuButton(label, items) {
  const wrap = h('div', { class: 'menu' });
  let list = null;
  const close = () => { list?.remove(); list = null; document.removeEventListener('mousedown', outside, true); document.removeEventListener('keydown', onKey, true); };
  const outside = (e) => { if (!wrap.contains(e.target)) close(); };
  const onKey = (e) => {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); btn.focus(); }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const bs = [...list.querySelectorAll('button')];
      const i = bs.indexOf(document.activeElement);
      bs[(i + (e.key === 'ArrowDown' ? 1 : bs.length - 1)) % bs.length]?.focus();
    }
  };
  const btn = h('button', { class: 'btn', type: 'button', 'aria-haspopup': 'menu', onclick: () => {
    if (list) { close(); return; }
    list = h('div', { class: 'menu-list', role: 'menu' }, items.filter(Boolean).map(it => h('button', { type: 'button', role: 'menuitem', class: it.danger ? 'danger' : '', onclick: () => { close(); it.onClick(); } }, it.label)));
    wrap.appendChild(list);
    document.addEventListener('mousedown', outside, true);
    document.addEventListener('keydown', onKey, true);
    list.querySelector('button')?.focus();
  } }, label);
  wrap.appendChild(btn);
  return wrap;
}
