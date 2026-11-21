// Item page: view, edit, and new item.

import { h, app, $, clear, money, moneyInput, parseMoney, fmtDate, relTime, statusBadge, qtyBadge, catById, catPath, catSetting,
  fieldsFor, hiddenBuiltins, fieldValueText, toast, dialog, confirmDialog, lightbox, debounce, statusInfo } from '../util.js';
import { api, ApiError } from '../api.js';
import { barcodeEl, validate, FORMATS, formatLabel, guessFormat } from '../barcode.js';
import { fieldInput, moneyWrap, categorySelect, statusSelect, formatSelect, tagInput, extraCodesEditor, imageEditor, menuButton } from '../components.js';
import { go, refreshMeta } from '../main.js';

export default async function (ctx) {
  const { parts, name, query } = ctx;
  if (name === 'new') return editView(ctx, null, query);
  const id = Number(parts[1]);
  if (parts[2] === 'edit') return editView(ctx, id, query);
  return itemView(ctx, id);
}

// ======================= VIEW =======================

async function itemView({ main }, id) {
  const data = await api.get(`/api/items/${id}`);
  const it = data.item;
  const fields = fieldsFor(it.category_id);
  const hidden = hiddenBuiltins(it.category_id);
  const images = it.images || [];
  const list = app.lastList || [];
  const idx = list.indexOf(id);
  const prevId = idx > 0 ? list[idx - 1] : null;
  const nextId = idx >= 0 && idx < list.length - 1 ? list[idx + 1] : null;

  // gallery
  let cur = 0;
  const mainImg = h('div', { class: 'main-img', onclick: () => images[cur] && lightbox(images[cur].url) },
    images.length ? h('img', { src: images[0].url, alt: it.name }) : h('span', null, 'No photo'));
  const strip = h('div', { class: 'strip' }, images.length > 1 ? images.map((im, i) => h('img', {
    src: im.thumb, alt: '', class: i === 0 ? 'on' : '',
    onclick: (e) => { cur = i; mainImg.replaceChildren(h('img', { src: im.url, alt: it.name })); strip.querySelectorAll('img').forEach(x => x.classList.remove('on')); e.target.classList.add('on'); },
  })) : null);

  // custom fields grouped
  const groups = new Map();
  for (const f of fields) {
    const v = it.custom[f.key];
    const text = fieldValueText(f, v, it);
    if (text === '' || text == null) continue;
    const g = f.group || 'Details';
    if (!groups.has(g)) groups.set(g, []);
    let valueEl = text;
    if (f.type === 'url') valueEl = h('a', { href: v, target: '_blank', rel: 'noopener' }, v);
    else if (f.type === 'identifier') valueEl = h('span', { class: 'mono' }, text);
    groups.get(g).push([f.label, valueEl]);
  }
  // Values of fields that no longer belong to this category (kept so nothing is lost).
  const known = new Set(fields.map(f => f.key));
  const orphan = Object.entries(it.custom).filter(([k]) => !known.has(k));
  if (orphan.length) groups.set('Other values', orphan.map(([k, v]) => [k, Array.isArray(v) ? v.join(', ') : String(v)]));

  const margin = it.price != null && it.cost != null && it.price > 0 ? Math.round(((it.price - it.cost) / it.price) * 100) : null;
  const deleted = !!it.deleted_at;

  const basics = [];
  if (!hidden.has('quantity')) basics.push(['Quantity', h('span', { class: 'row', style: { gap: '6px' } }, h('span', { class: 'num' }, String(it.quantity)), qtyBadge(it))]);
  basics.push(['Status', statusBadge(it.status)]);
  if (it.location && !hidden.has('location')) basics.push(['Location', it.location]);
  if (it.supplier && !hidden.has('supplier')) basics.push(['Supplier', it.supplier]);
  if (it.low_stock_at != null && !hidden.has('low_stock_at')) basics.push(['Low stock at', String(it.low_stock_at)]);
  if (it.tags.length) basics.push(['Tags', h('span', { class: 'chips' }, it.tags.map(t => h('a', { class: 'chip', href: `#/items?f=${encodeURIComponent(JSON.stringify([{ k: 'tags', op: 'in', v: [t] }]))}` }, t)))]);
  if (it.extra_codes.length) basics.push(['Other codes', h('div', null, it.extra_codes.map(e => h('div', null, h('span', { class: 'muted small' }, e.kind.toUpperCase() + ' '), h('span', { class: 'mono' }, e.code))))]);
  if (data.parent) basics.push(['Variant of', h('a', { href: `#/item/${data.parent.id}` }, data.parent.name)]);
  basics.push(['Added', fmtDate(it.created_at)]);
  basics.push(['Updated', relTime(it.updated_at)]);

  const copyCode = async () => {
    try { await navigator.clipboard.writeText(it.code); toast('Code copied'); }
    catch { toast('Copy failed (the browser blocked the clipboard).', { error: true }); }
  };

  const actions = {
    e: () => go(`#/item/${id}/edit`),
    p: () => go(`#/print?add=${id}`),
    d: () => duplicate(it),
    s: () => stockDialog(it, 'sold', () => go(`#/item/${id}`)),
    '[': () => prevId && go(`#/item/${prevId}`),
    ']': () => nextId && go(`#/item/${nextId}`),
  };

  const moreMenu = menuButton('More ▾', [
    { label: 'Make variants (size × colour…)', onClick: () => variantsDialog(it) },
    { label: 'Correct quantity…', onClick: () => stockDialog(it, 'adjustment', () => go(`#/item/${id}`)) },
    { label: 'Copy code', onClick: copyCode },
    { label: 'Copy link to this page', onClick: () => navigator.clipboard?.writeText(location.href).then(() => toast('Link copied')) },
    { label: 'Move to Trash', danger: true, onClick: () => trash(it) },
  ]);

  const tabBody = h('div');
  const tabs = h('div', { class: 'tabs' });
  const tabDefs = [
    ['history', 'Stock history', () => stockHistory(id)],
    ['activity', 'Activity', () => activityList(id)],
  ];
  if (data.variants.length) tabDefs.unshift(['variants', `Variants (${data.variants.length})`, () => variantsTable(data.variants, it)]);
  const showTab = async (key) => {
    tabs.querySelectorAll('button').forEach(b => b.classList.toggle('on', b.dataset.k === key));
    tabBody.replaceChildren(h('div', { class: 'muted' }, 'Loading…'));
    const def = tabDefs.find(t => t[0] === key);
    tabBody.replaceChildren(await def[2]());
  };
  tabDefs.forEach(([k, l]) => tabs.appendChild(h('button', { dataset: { k }, onclick: () => showTab(k) }, l)));

  main.append(
    deleted ? h('div', { class: 'panel', style: { background: 'var(--warning-soft)', borderColor: 'var(--warning)', marginBottom: '16px' } },
      h('div', { class: 'row' }, h('strong', null, 'This item is in the Trash.'), h('span', { class: 'muted' }, 'Its code still belongs to it, but scanning it does nothing.'), h('span', { class: 'spacer' }),
        h('button', { class: 'btn', onclick: async () => { await api.post(`/api/items/${id}/restore`); toast('Restored'); go(`#/item/${id}`); } }, 'Restore'))) : null,
    h('div', { class: 'page-head' },
      h('div', { class: 'crumbs' }, h('a', { href: '#/items' }, 'Items'), it.category_id ? [' › ', h('a', { href: `#/items?cat=${it.category_id}` }, catPath(it.category_id))] : null),
      h('span', { class: 'spacer' }),
      h('button', { class: 'btn ghost', disabled: !prevId, title: 'Previous ( [ )', onclick: actions['['] }, '‹ Prev'),
      h('button', { class: 'btn ghost', disabled: !nextId, title: 'Next ( ] )', onclick: actions[']'] }, 'Next ›')),
    h('div', { class: 'item-layout' },
      h('div', { class: 'gallery' }, mainImg, strip),
      h('div', null,
        h('h1', { class: 'item-title' }, it.name),
        h('div', { class: 'item-code' }, h('span', { class: 'code mono' }, it.code), h('button', { class: 'btn small ghost', onclick: copyCode }, 'Copy'), h('span', { class: 'muted small' }, formatLabel(it.barcode_format))),
        barcodeEl(it.barcode_format, it.code, { height: 54 }),
        h('div', { class: 'row', style: { alignItems: 'baseline', gap: '16px', margin: '4px 0 12px' } },
          !hidden.has('price') ? h('span', { class: 'price-big' }, it.price != null ? money(it.price) : h('span', { class: 'muted', style: { fontSize: '16px' } }, 'No price')) : null,
          !app.settings.hide_cost && it.cost != null && !hidden.has('cost') ? h('span', { class: 'muted' }, `cost ${money(it.cost)}`, margin != null ? ` · margin ${margin}%` : '') : null),
        h('dl', { class: 'kv' }, basics.map(([k, v]) => [h('dt', null, k), h('dd', null, v)])),
        h('div', { class: 'actionbar' },
          h('button', { class: 'btn primary', onclick: actions.e, disabled: deleted }, 'Edit', h('span', { class: 'kbd' }, 'E')),
          h('button', { class: 'btn', onclick: actions.p }, 'Print label', h('span', { class: 'kbd' }, 'P')),
          h('button', { class: 'btn', onclick: actions.d, disabled: deleted }, 'Duplicate', h('span', { class: 'kbd' }, 'D')),
          h('button', { class: 'btn', onclick: actions.s, disabled: deleted || it.quantity <= 0 }, 'Sell', h('span', { class: 'kbd' }, 'S')),
          h('button', { class: 'btn', onclick: () => stockDialog(it, 'received', () => go(`#/item/${id}`)), disabled: deleted }, 'Stock in'),
          deleted ? null : moreMenu),
        groups.size ? h('div', { class: 'groups' }, [...groups].map(([g, rows]) => h('div', null, h('h3', null, g), h('dl', { class: 'kv' }, rows.map(([k, v]) => [h('dt', null, k), h('dd', null, v)]))))) : null,
        it.description ? h('div', { style: { marginTop: '20px' } }, h('h3', null, 'Description'), h('p', { style: { whiteSpace: 'pre-wrap' } }, it.description)) : null,
        it.notes ? h('div', { style: { marginTop: '16px' } }, h('h3', null, 'Private notes'), h('p', { style: { whiteSpace: 'pre-wrap' }, class: 'muted' }, it.notes)) : null)),
    h('div', { style: { marginTop: '28px' } }, tabs, tabBody));
  showTab(tabDefs[0][0]);

  return { shortcuts: deleted ? { p: actions.p, '[': actions['['], ']': actions[']'] } : actions, newQuery: it.category_id ? `?cat=${it.category_id}` : '' };
}

async function stockHistory(id) {
  const { moves } = await api.get(`/api/stock?item_id=${id}`);
  if (!moves.length) return h('p', { class: 'muted' }, 'No stock changes yet.');
  return h('div', { class: 'table-wrap' }, h('table', { class: 't' },
    h('thead', null, h('tr', null, h('th', null, 'When'), h('th', null, 'Change'), h('th', null, 'Reason'), h('th', null, 'Price each'), h('th', null, 'Note'))),
    h('tbody', null, moves.map(m => h('tr', null,
      h('td', null, fmtDate(m.created_at, true)),
      h('td', { class: 'num ' + (m.change > 0 ? 'ok-text' : 'danger-text') }, (m.change > 0 ? '+' : '') + m.change),
      h('td', null, reasonLabel(m.reason)),
      h('td', { class: 'num' }, money(m.price_each)),
      h('td', { class: 'wrap' }, m.note))))));
}

export function reasonLabel(r) {
  return { received: 'Received', sold: 'Sold', returned: 'Returned', damaged: 'Damaged', adjustment: 'Correction', count: 'Stock count', lost: 'Lost', initial: 'Added' }[r] || r;
}

async function activityList(id) {
  const { activity } = await api.get(`/api/activity?item_id=${id}`);
  if (!activity.length) return h('p', { class: 'muted' }, 'No activity yet.');
  return h('ul', { class: 'list-plain' }, activity.map(a => h('li', { style: { alignItems: 'flex-start' } },
    h('span', { class: 'muted small nowrap', style: { width: '150px', flexShrink: 0 } }, fmtDate(a.created_at, true)),
    h('div', { class: 'grow' }, h('strong', null, a.action[0].toUpperCase() + a.action.slice(1)), ' ', describeDetails(a)))));
}

export function describeDetails(a) {
  const d = a.details || {};
  if (a.action === 'printed') return h('span', { class: 'muted' }, `${d.labels} label(s)`);
  if (a.action !== 'updated') return null;
  const parts = Object.entries(d).map(([k, [o, n]]) => {
    const fmt = (v) => {
      if (v == null || v === '') return '—';
      if (k === 'price' || k === 'cost') return money(v);
      if (k === 'category_id') return catPath(v) || '—';
      if (k === 'status') return statusInfo(v).label;
      if (Array.isArray(v)) return v.map(x => typeof x === 'object' ? x.code : x).join(', ') || '—';
      return String(v);
    };
    const label = k.startsWith('custom.') ? (app.fields.find(f => f.key === k.slice(7))?.label || k.slice(7)) : k.replace(/_/g, ' ');
    return h('div', { class: 'small' }, h('span', { class: 'muted' }, label + ': '), fmt(o), ' → ', fmt(n));
  });
  return h('div', null, parts);
}

function variantsTable(variants, parent) {
  const ids = variants.map(v => v.id);
  return h('div', null,
    h('div', { class: 'row', style: { marginBottom: '8px' } },
      h('a', { class: 'btn', href: `#/print?add=${ids.join(',')}` }, `Print labels for all ${ids.length}`),
      h('a', { class: 'btn ghost', href: `#/items?parent=${parent.id}` }, 'Open as list')),
    h('div', { class: 'table-wrap' }, h('table', { class: 't' },
      h('thead', null, h('tr', null, h('th', null, 'Code'), h('th', null, 'Name'), h('th', { class: 'num' }, 'Qty'), h('th', { class: 'num' }, 'Price'))),
      h('tbody', null, variants.map(v => h('tr', { class: 'click', onclick: () => { app.lastList = ids; go(`#/item/${v.id}`); } },
        h('td', { class: 'mono' }, v.code), h('td', null, v.name), h('td', { class: 'num' }, v.quantity), h('td', { class: 'num' }, money(v.price))))))));
}

async function duplicate(it) {
  const copyImgs = h('input', { type: 'checkbox', checked: (it.images || []).length > 0 });
  const r = await dialog({
    title: 'Duplicate item',
    body: h('div', { class: 'col' },
      h('p', null, 'Makes a copy with a new code. IMEI, serial numbers and other unique values are not copied.'),
      (it.images || []).length ? h('label', { class: 'check' }, copyImgs, 'Copy photos too') : null),
    actions: [{ label: 'Duplicate', primary: true, onClick: async () => (await api.post(`/api/items/${it.id}/duplicate`, { copy_images: copyImgs.checked })).id }],
  });
  if (r) { toast('Copy created. Check the name and details.'); go(`#/item/${r}/edit`); }
}

async function trash(it) {
  await api.del(`/api/items/${it.id}`);
  toast(`“${it.name}” moved to Trash`, { action: async () => { await api.post(`/api/items/${it.id}/restore`); go(`#/item/${it.id}`); } });
  go('#/items');
}

// reason: sold | received | adjustment
export async function stockDialog(it, reason, after) {
  const isSell = reason === 'sold', isIn = reason === 'received', isAdj = reason === 'adjustment';
  const qty = h('input', { type: 'number', value: isAdj ? it.quantity : 1, min: isAdj ? undefined : 1, style: { width: '100px' }, autofocus: true });
  const price = h('input', { type: 'text', inputmode: 'decimal', value: moneyInput(isSell ? it.price : it.cost) });
  const note = h('input', { type: 'text', placeholder: 'Optional' });
  const err = h('div', { class: 'danger-text small' });
  const r = await dialog({
    title: isSell ? `Sell: ${it.name}` : isIn ? `Stock in: ${it.name}` : `Correct quantity: ${it.name}`,
    body: h('div', { class: 'col' },
      h('div', { class: 'grid2' },
        h('div', { class: 'field' }, h('label', null, isAdj ? 'Actual quantity' : 'Quantity'), qty),
        isAdj ? null : h('div', { class: 'field' }, h('label', null, isSell ? 'Price each' : 'Cost each'), moneyWrap(price))),
      h('div', { class: 'field' }, h('label', null, 'Note'), note),
      h('p', { class: 'muted small' }, `In stock now: ${it.quantity}`),
      err),
    actions: [{ label: isSell ? 'Sell' : isIn ? 'Add stock' : 'Save', primary: true, onClick: async () => {
      const n = Number(qty.value);
      if (!Number.isFinite(n) || (!isAdj && n <= 0)) { err.textContent = 'Enter a quantity.'; return false; }
      const p = parseMoney(price.value);
      if (Number.isNaN(p)) { err.textContent = 'Enter a valid amount.'; return false; }
      try {
        const body = { item_id: it.id, reason, note: note.value, price_each: isAdj ? null : p };
        if (isAdj) body.set_to = Math.round(n); else body.change = isSell ? -Math.round(n) : Math.round(n);
        return await api.post('/api/stock', body);
      } catch (e) { err.textContent = e.message; return false; }
    } }],
  });
  if (r) {
    toast(isSell ? `Sold ${-r.change} · ${r.quantity} left` : isIn ? `Added ${r.change} · now ${r.quantity}` : `Quantity set to ${r.quantity}`);
    after?.(r);
  }
}

async function variantsDialog(it) {
  const fields = fieldsFor(it.category_id).filter(f => ['select', 'size', 'color', 'text', 'multi_select'].includes(f.type) && f.type !== 'multi_select');
  if (!fields.length) {
    await dialog({ title: 'Make variants', body: h('p', null, 'This category has no dropdown fields (like Size or Colour) to make variants from. Add one in Categories & fields first.'), cancelLabel: 'Close' });
    return;
  }
  const preferred = catSetting(it.category_id, 'variant_fields') || [];
  const axes = [];
  const box = h('div', { class: 'col' });
  const count = h('strong');
  const update = () => {
    const n = axes.filter(a => a.enabled.checked && a.values.size).reduce((acc, a) => acc * a.values.size, 1);
    const any = axes.some(a => a.enabled.checked && a.values.size);
    count.textContent = any ? `${n} variant${n === 1 ? '' : 's'} will be created` : 'Pick some values';
  };
  for (const f of fields) {
    const choices = (f.options?.choices || []).map(String);
    const values = new Set();
    const enabled = h('input', { type: 'checkbox', checked: preferred.includes(f.key), onchange: update });
    const chips = h('div', { class: 'chips' });
    const extra = h('input', { type: 'text', placeholder: 'Other value + Enter', style: { width: '160px', height: '26px' }, onkeydown: (e) => {
      if (e.key === 'Enter' && extra.value.trim()) { e.preventDefault(); const v = extra.value.trim(); if (!choices.includes(v)) choices.push(v); values.add(v); enabled.checked = true; extra.value = ''; render(); } } });
    const render = () => {
      chips.replaceChildren(...choices.map(c => h('span', { class: 'chip pick' + (values.has(c) ? ' on' : ''), onclick: () => { values.has(c) ? values.delete(c) : values.add(c); if (values.size) enabled.checked = true; render(); } }, c)), extra);
      update();
    };
    render();
    axes.push({ key: f.key, label: f.label, enabled, values });
    box.appendChild(h('div', { class: 'panel', style: { padding: '10px' } }, h('label', { class: 'check', style: { fontWeight: 600, marginBottom: '6px' } }, enabled, f.label), chips));
  }
  const qty = h('input', { type: 'number', value: 1, min: 0, style: { width: '90px' } });
  const copyImgs = h('input', { type: 'checkbox', checked: true });
  const r = await dialog({
    title: `Make variants of “${it.name}”`, wide: true,
    body: h('div', { class: 'col' },
      h('p', { class: 'muted' }, 'Each combination becomes its own item with its own code, linked to this one. Pick values below (e.g. sizes S, M, L × colours Black, White).'),
      box,
      h('div', { class: 'row' }, h('label', null, 'Quantity of each: '), qty, h('label', { class: 'check' }, copyImgs, 'Copy photos')),
      count),
    actions: [{ label: 'Create variants', primary: true, onClick: async () => {
      const sel = axes.filter(a => a.enabled.checked && a.values.size).map(a => ({ key: a.key, label: a.label, values: [...a.values] }));
      if (!sel.length) { toast('Pick at least one value.', { error: true }); return false; }
      try { return await api.post(`/api/items/${it.id}/variants`, { axes: sel, quantity: Number(qty.value) || 0, copy_images: copyImgs.checked }); }
      catch (e) { toast(e.message, { error: true }); return false; }
    } }],
  });
  if (r?.created) {
    toast(`${r.created.length} variants created`, { action: () => go(`#/print?add=${r.created.join(',')}`), actionLabel: 'Print their labels' });
    go(`#/item/${it.id}`);
  }
}

// ======================= EDIT / NEW =======================

async function editView({ main }, id, query) {
  const isNew = !id;
  let it, genSeq = null;
  if (isNew) {
    const catQ = Number(query.get('cat')) || (app.lastCategory && catById(app.lastCategory) ? app.lastCategory : null);
    it = { id: 0, code: query.get('code') || '', barcode_format: '', name: '', category_id: catQ || null, description: '', price: null, cost: null,
      quantity: Number(app.settings.default_quantity ?? 1), low_stock_at: app.settings.default_low_stock ?? null, status: 'in_stock', location: '', supplier: '',
      tags: [], custom: {}, extra_codes: [], parent_id: null, notes: '', images: [] };
    if (app.draftFromSaveNew) { Object.assign(it, app.draftFromSaveNew); app.draftFromSaveNew = null; }
    if (it.code) it.barcode_format = guessFormat(it.code);
  } else {
    it = (await api.get(`/api/items/${id}`)).item;
  }

  const err = {};
  const codeInput = h('input', { type: 'text', class: 'code-input', value: it.code, spellcheck: 'false', autocomplete: 'off', placeholder: 'Scan, type, or Generate' });
  const fmtSel = formatSelect(it.barcode_format || catSetting(it.category_id, 'default_format') || app.settings.default_format || 'code128');
  const codeMsg = h('div', { class: 'small' });
  const preview = h('div');
  const nameInput = h('input', { type: 'text', value: it.name, autofocus: !!it.code || !isNew });
  const catSel = categorySelect(it.category_id);
  const priceInput = h('input', { type: 'text', inputmode: 'decimal', value: moneyInput(it.price) });
  const costInput = h('input', { type: 'text', inputmode: 'decimal', value: moneyInput(it.cost) });
  const qtyInput = h('input', { type: 'number', value: it.quantity, step: 1 });
  const lowInput = h('input', { type: 'number', value: it.low_stock_at ?? '', min: 0, placeholder: 'No alert' });
  const statusSel = statusSelect(it.status);
  const locInput = h('input', { type: 'text', value: it.location, list: 'loc-list' });
  const supInput = h('input', { type: 'text', value: it.supplier });
  const descInput = h('textarea', { rows: 4, value: it.description });
  const notesInput = h('textarea', { rows: 2, value: it.notes, placeholder: 'Only visible here, never printed' });
  const tags = tagInput(it.tags);
  const extra = extraCodesEditor(it.extra_codes);
  const imgs = imageEditor(isNew ? null : id, it.images || []);
  const customBox = h('div');
  let customInputs = [];
  let generated = null; // the code we generated, so a category change can replace it

  const builtinFields = {};
  const fld = (key, label, input, { req = false, help = null, span = false } = {}) => {
    const e = h('div', { class: 'err' });
    err[key] = (msg) => { e.textContent = msg || ''; };
    const el = h('div', { class: 'field' + (span ? ' span2' : ''), dataset: { builtin: key } }, h('label', null, label, req ? h('span', { class: 'req' }, ' *') : null), input, help ? h('div', { class: 'help' }, help) : null, e);
    builtinFields[key] = el;
    return el;
  };

  // --- code checks ---
  const checkCode = debounce(async () => {
    const code = codeInput.value.trim();
    preview.replaceChildren();
    codeMsg.replaceChildren();
    codeInput.classList.remove('invalid');
    if (!code) return;
    preview.appendChild(barcodeEl(fmtSel.value, code, { height: 44 }));
    const v = validate(fmtSel.value, code);
    if (!v.ok) {
      codeMsg.append(h('span', { class: 'warn-text' }, v.error + ' '));
      if (v.fix) codeMsg.append(h('button', { type: 'button', class: 'btn small', onclick: () => { codeInput.value = v.fix; checkCode(); } }, `Use ${v.fix}`));
      const g = guessFormat(code);
      if (g !== fmtSel.value) codeMsg.append(' ', h('button', { type: 'button', class: 'btn small', onclick: () => { fmtSel.value = g; checkCode(); } }, `Switch to ${formatLabel(g)}`));
    }
    try {
      const r = await api.get(`/api/codes/check?code=${encodeURIComponent(code)}&exclude=${id || 0}`, { quiet: true });
      if (codeInput.value.trim() !== code) return;
      if (!r.free) {
        codeInput.classList.add('invalid');
        codeMsg.append(h('div', { class: 'danger-text' }, 'This code already belongs to ', h('a', { href: `#/item/${r.item_id}` }, r.item_name), '.'));
      }
    } catch { /* offline: server re-checks on save */ }
  }, 200);
  codeInput.addEventListener('input', () => { if (codeInput.value.trim() !== generated) { generated = null; genSeq = null; } checkCode(); });
  fmtSel.addEventListener('change', checkCode);
  // Scanners press Enter after the code: don't submit the form, go on to the name.
  codeInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); nameInput.focus(); } });

  const generate = async () => {
    const r = await api.get(`/api/codes/next?category=${catSel.value || ''}`);
    codeInput.value = r.code;
    generated = r.code;
    genSeq = r.seq;
    fmtSel.value = r.format || fmtSel.value;
    checkCode();
  };

  // --- custom fields per category ---
  const renderCustom = () => {
    const current = collectCustom(true);
    const catId = Number(catSel.value) || null;
    const fields = fieldsFor(catId);
    const hidden = hiddenBuiltins(catId);
    for (const [k, el] of Object.entries(builtinFields)) el.hidden = hidden.has(k);
    const unique = !!catSetting(catId, 'unique_items');
    if (unique && isNew) qtyInput.value = 1;
    customInputs = [];
    clear(customBox);
    const groups = new Map();
    for (const f of fields) {
      const fi = fieldInput(f, current[f.key] ?? it.custom[f.key] ?? (isNew && f.default_value ? f.default_value : undefined), it);
      customInputs.push(fi);
      const g = f.group || 'Details';
      if (!groups.has(g)) groups.set(g, []);
      groups.get(g).push(fi.el);
    }
    for (const [g, els] of groups) customBox.appendChild(h('div', { class: 'section accept-scan' }, h('h3', null, g), h('div', { class: 'grid3' }, els)));
  };
  const collectCustom = (lenient = false) => {
    const out = { ...it.custom };
    for (const fi of customInputs) {
      try {
        const v = fi.get();
        fi.error('');
        if (v === undefined) continue;
        if (v == null || v === '' || (Array.isArray(v) && !v.length)) delete out[fi.key];
        else out[fi.key] = v;
      } catch (e) {
        if (!lenient) { fi.error(e.message.replace(/^[^:]+: /, '')); throw Object.assign(e, { focus: fi.focus }); }
      }
    }
    return out;
  };
  catSel.addEventListener('change', async () => {
    renderCustom();
    if (isNew && (generated || !codeInput.value.trim())) await generate();
    else if (!isNew) {
      const f = catSetting(Number(catSel.value) || null, 'default_format');
      if (f && !codeInput.value) fmtSel.value = f;
    }
  });

  // --- collect & save ---
  const collect = (quiet = false) => {
    const setErr = (k, m) => { if (!quiet) err[k](m); };
    Object.values(err).forEach(fn => fn(''));
    const out = {
      code: codeInput.value.trim(), barcode_format: fmtSel.value, name: nameInput.value.trim(),
      category_id: Number(catSel.value) || null, description: descInput.value, notes: notesInput.value,
      quantity: Math.round(Number(qtyInput.value) || 0), status: statusSel.value, location: locInput.value.trim(), supplier: supInput.value.trim(),
      tags: tags.get(), extra_codes: extra.get(), parent_id: it.parent_id,
    };
    const p = parseMoney(priceInput.value), c = parseMoney(costInput.value);
    if (Number.isNaN(p)) { setErr('price', 'Enter an amount, like 12.50'); if (!quiet) throw Object.assign(new Error('Price is not a valid amount'), { focus: () => priceInput.focus() }); }
    if (Number.isNaN(c)) { setErr('cost', 'Enter an amount, like 12.50'); if (!quiet) throw Object.assign(new Error('Cost is not a valid amount'), { focus: () => costInput.focus() }); }
    out.price = p; out.cost = c;
    out.low_stock_at = lowInput.value === '' ? null : Math.round(Number(lowInput.value));
    if (!out.code) { setErr('code', 'Enter or generate a code'); if (!quiet) throw Object.assign(new Error('Code is required'), { focus: () => codeInput.focus() }); }
    if (!out.name) { setErr('name', 'Give the item a name'); if (!quiet) throw Object.assign(new Error('Name is required'), { focus: () => nameInput.focus() }); }
    out.custom = collectCustom(quiet);
    for (const fi of customInputs) {
      if (quiet) break;
      if (fi.field.required && fi.field.type !== 'boolean' && fi.field.type !== 'age' && (out.custom[fi.key] == null || out.custom[fi.key] === '')) {
        fi.error('Required');
        throw Object.assign(new Error(`${fi.field.label} is required`), { focus: fi.focus });
      }
    }
    return out;
  };

  let saving = false;
  let snapshot = '';
  const snap = () => { try { return JSON.stringify({ ...collect(true), imgs: imgs.hasQueued() }); } catch { return 'invalid' + Math.random(); } };

  const save = async (andNew = false) => {
    if (saving) return;
    let body;
    try { body = collect(); }
    catch (e) { toast(e.message, { error: true }); e.focus?.(); return; }
    if (genSeq != null && body.code === generated) body.gen_seq = genSeq;
    saving = true;
    setSaving(true);
    try {
      let r;
      if (isNew) r = await api.post('/api/items', body);
      else r = await api.put(`/api/items/${id}`, { ...body, expect_updated_at: it.updated_at });
      const newId = r.item.id;
      if (isNew && imgs.hasQueued()) await imgs.flush(newId);
      app.lastCategory = body.category_id;
      snapshot = snap();
      toast('Saved ✓');
      refreshMeta(['categories']);
      if (andNew) {
        // Save & new: keep category and most details for fast batch entry.
        const keep = { ...body.custom };
        for (const f of fieldsFor(body.category_id)) if (f.type === 'identifier' || f.unique || f.as_code) delete keep[f.key];
        app.draftFromSaveNew = { category_id: body.category_id, price: body.price, cost: body.cost, location: body.location, supplier: body.supplier, status: body.status, tags: body.tags, custom: keep, quantity: body.quantity, low_stock_at: body.low_stock_at, barcode_format: '' };
        go('#/new?cat=' + (body.category_id || '') + '&t=' + Date.now());
      } else {
        go(`#/item/${newId}`);
      }
    } catch (e) {
      if (e instanceof ApiError && e.data?.stale) {
        const choice = await dialog({ title: 'Changed somewhere else', body: h('p', null, e.message + ' Overwrite their changes with yours, or reload to see them?'),
          actions: [{ label: 'Reload', value: 'reload' }, { label: 'Overwrite', danger: true, value: 'force' }] });
        if (choice === 'reload') { snapshot = snap(); go(`#/item/${id}/edit?r=${Date.now()}`); }
        if (choice === 'force') { it.updated_at = ''; saving = false; setSaving(false); return save(andNew); }
      } else if (e instanceof ApiError && e.data?.conflict) {
        const c = e.data.conflict;
        toast(e.message, { error: true });
        if (c.code === body.code || !c.code) { err.code(''); codeMsg.replaceChildren(h('div', { class: 'danger-text' }, 'This code already belongs to ', h('a', { href: `#/item/${c.item_id}` }, c.item_name), '.')); codeInput.focus(); }
      } else {
        toast(e.message, { error: true });
      }
    } finally {
      saving = false;
      setSaving(false);
    }
  };

  const cancel = () => go(isNew ? (it.category_id ? `#/items?cat=${it.category_id}` : '#/items') : `#/item/${id}`);
  const saveLabel = isNew ? 'Create item' : 'Save changes';
  const saveBtn = h('button', { class: 'btn primary big', type: 'button', onclick: () => save(false) }, saveLabel);
  const topSaveBtn = h('button', { class: 'btn primary', type: 'button', onclick: () => save(false), title: 'Ctrl+S' }, saveLabel);
  const setSaving = (on) => { for (const b of [saveBtn, topSaveBtn]) { b.disabled = on; b.textContent = on ? 'Saving…' : saveLabel; } };
  const locList = h('datalist', { id: 'loc-list' });
  api.get('/api/facets', { quiet: true }).then(r => (r.counts.location || []).forEach(l => locList.appendChild(h('option', { value: l.value })))).catch(() => {});

  const moreOpen = !!(it.cost != null || it.supplier || it.low_stock_at != null || it.notes || it.extra_codes.length);
  const form = h('form', { class: 'edit-form', autocomplete: 'off', onsubmit: (e) => { e.preventDefault(); save(false); } },
    h('div', { class: 'page-head' },
      h('div', { class: 'crumbs' }, h('a', { href: '#/items' }, 'Items'), ' › ', isNew ? 'New item' : h('a', { href: `#/item/${id}` }, it.name)),
      h('span', { class: 'spacer' }),
      h('button', { class: 'btn ghost', type: 'button', onclick: () => cancel() }, 'Cancel'),
      topSaveBtn,
      h('h1', { style: { width: '100%', order: 2 } }, isNew ? 'New item' : 'Edit item')),
    h('div', { class: 'item-layout' },
      h('div', null, h('h3', null, 'Photos'), imgs.el, h('p', { class: 'muted small', style: { marginTop: '6px' } }, 'Drag to reorder. The first photo is the main one. You can paste a photo with Ctrl+V.')),
      h('div', null,
        h('div', { class: 'section accept-scan' },
          h('h3', null, 'Basics'),
          h('div', { class: 'grid2' },
            h('div', { class: 'field span2' },
              h('label', null, 'Code ', h('span', { class: 'req' }, '*')),
              h('div', { class: 'row nowrap' }, h('div', { class: 'grow' }, codeInput), fmtSel, h('button', { type: 'button', class: 'btn', onclick: generate, title: 'Make the next free code' }, 'Generate')),
              codeMsg, (() => { const e = h('div', { class: 'err' }); err.code = (m) => { e.textContent = m || ''; }; return e; })(),
              preview),
            fld('name', 'Name', nameInput, { req: true, span: true }),
            fld('category', 'Category', catSel),
            fld('status', 'Status', statusSel),
            fld('price', 'Price', moneyWrap(priceInput)),
            fld('quantity', 'Quantity', qtyInput),
            fld('location', 'Location', h('div', null, locInput, locList)),
            fld('tags', 'Tags', tags.el),
            fld('description', 'Description', descInput, { span: true }))),
        customBox,
        h('details', { class: 'section', open: moreOpen },
          h('summary', { style: { cursor: 'pointer', marginBottom: '12px' } }, h('h3', { style: { display: 'inline' } }, 'More details')),
          h('div', { class: 'grid2 accept-scan' },
            fld('cost', 'Cost (what you paid)', moneyWrap(costInput)),
            fld('supplier', 'Supplier', supInput),
            fld('low_stock_at', 'Low stock alert at', lowInput, { help: 'Warn when quantity is at or below this.' }),
            h('div', { class: 'field span2' }, h('label', null, 'Other codes for this item (manufacturer UPC/EAN, serial…) — scanning any of them opens the item'), extra.el),
            fld('notes', 'Private notes', notesInput, { span: true }))))),
    h('div', { class: 'save-bar' },
      saveBtn,
      h('button', { class: 'btn big', type: 'button', onclick: () => save(true), title: 'Ctrl+Shift+S' }, isNew ? 'Create & add another' : 'Save & new item'),
      h('button', { class: 'btn ghost', type: 'button', onclick: () => cancel() }, 'Cancel'),
      h('span', { class: 'muted small', style: { marginLeft: 'auto' } }, 'Ctrl+S to save · Esc to cancel')));
  if (isNew && query.get('blank') && it.code) {
    // A pre-printed blank code was scanned: create its item here, or attach it to one that exists.
    form.insertBefore(h('div', { class: 'panel', style: { background: 'var(--accent-soft)', borderColor: 'var(--accent)', marginBottom: '16px' } },
      h('div', { class: 'row' },
        h('div', { class: 'grow' }, h('strong', null, 'Printed code ', h('span', { class: 'mono' }, it.code), ' is not used yet.'),
          h('div', { class: 'small muted' }, 'Fill in the details below to create its item, or attach the code to an item you already have.')),
        h('button', { class: 'btn', type: 'button', onclick: () => attachToExisting(it.code, () => { snapshot = snap(); }) }, 'Attach to an existing item…'))),
      form.children[1]);
  }
  main.appendChild(form);
  renderCustom();
  if (isNew && !it.code) await generate();
  else checkCode();
  snapshot = snap();
  setTimeout(() => (it.code || !isNew ? nameInput : nameInput).focus(), 0);

  const onEsc = (e) => { if (e.key === 'Escape' && !document.querySelector('.overlay') && main.contains(form)) { e.preventDefault(); cancel(); } };
  document.addEventListener('keydown', onEsc);

  // Scanning while adding an item fills in the Code box (code-type fields like IMEI take
  // the scan themselves when the cursor is in them).
  const onScan = isNew ? (code) => {
    codeInput.value = code;
    generated = null; genSeq = null;
    fmtSel.value = guessFormat(code);
    checkCode();
    codeInput.classList.add('flash-ok');
    setTimeout(() => codeInput.classList.remove('flash-ok'), 700);
    return true;
  } : null;

  return {
    onScan,
    shortcuts: { save: (shift) => save(!!shift) },
    dirty: () => snap() !== snapshot,
    canLeave: async () => snap() === snapshot || await confirmDialog('Leave without saving?', 'Your changes to this item will be lost.', { okLabel: 'Leave', danger: true }),
    destroy: () => { imgs.destroy(); document.removeEventListener('keydown', onEsc); },
  };
}

// Attach a pre-printed code to an item that already exists (as one of its "other codes").
async function attachToExisting(code, done) {
  const input = h('input', { type: 'search', placeholder: 'Type part of a name…', style: { width: '100%' }, autofocus: true });
  const list = h('ul', { class: 'list-plain', style: { maxHeight: '320px', overflowY: 'auto', marginTop: '8px' } });
  let timer;
  const search = () => {
    clearTimeout(timer);
    timer = setTimeout(async () => {
      const r = await api.get('/api/items?limit=30&q=' + encodeURIComponent(input.value.trim()), { quiet: true });
      list.replaceChildren(...r.items.map(it => h('li', null,
        h('span', { class: 'grow' }, it.name), h('span', { class: 'mono small muted' }, it.code),
        h('button', { class: 'btn small primary', onclick: () => attach(it.id) }, 'Attach'))));
      if (!r.items.length) list.replaceChildren(h('li', { class: 'muted' }, 'No items found.'));
    }, 200);
  };
  let closeDlg;
  const attach = async (id) => {
    try {
      const full = (await api.get(`/api/items/${id}`)).item;
      full.extra_codes = [...full.extra_codes, { kind: 'label', code }];
      await api.put(`/api/items/${id}`, { ...full, expect_updated_at: full.updated_at });
      closeDlg?.();
      done?.();
      toast(`Code ${code} now opens “${full.name}”`);
      go(`#/item/${id}`);
    } catch (e) { toast(e.message, { error: true }); }
  };
  input.addEventListener('input', search);
  search();
  await dialog({ title: `Attach ${code} to…`, body: (close) => { closeDlg = close; return h('div', null, input, list); }, cancelLabel: 'Close' });
}
