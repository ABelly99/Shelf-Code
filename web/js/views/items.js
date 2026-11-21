// Items list: category tree, filters generated from fields, sorting, columns, grid/table, bulk actions, saved views.

import { h, app, $, clear, money, debounce, catById, catPath, catDescendants, fieldsFor, fieldValueText, statusBadge, statusInfo, qtyBadge,
  toast, dialog, confirmDialog, promptDialog, download, relTime, parseMoney, hiddenBuiltins } from '../util.js';
import { api, qs } from '../api.js';
import { fieldInput, categorySelect, statusSelect, formatSelect, moneyWrap } from '../components.js';
import { go, refreshMeta } from '../main.js';

const LIMIT = 100;

const BUILTIN_COLS = {
  thumb: { label: 'Photo' }, code: { label: 'Code', sort: 'code' }, name: { label: 'Name', sort: 'name' },
  category: { label: 'Category', sort: 'category' }, price: { label: 'Price', sort: 'price', num: true }, cost: { label: 'Cost', sort: 'cost', num: true },
  quantity: { label: 'Qty', sort: 'quantity', num: true }, status: { label: 'Status', sort: 'status' }, location: { label: 'Location', sort: 'location' },
  supplier: { label: 'Supplier', sort: 'supplier' }, tags: { label: 'Tags' }, updated_at: { label: 'Updated', sort: 'updated_at' }, created_at: { label: 'Added', sort: 'created_at' },
};

function loadPref(key, fallback) { try { return JSON.parse(localStorage.getItem(key)) ?? fallback; } catch { return fallback; } }
function savePref(key, v) { try { localStorage.setItem(key, JSON.stringify(v)); } catch { /* private mode */ } }

export default async function ({ main, query }) {
  const view = query.get('view') ? app.views.find(v => String(v.id) === query.get('view')) : null;
  const st = {
    cat: query.get('cat') || '',
    q: query.get('q') || '',
    f: safeJSON(query.get('f'), []),
    sort: safeJSON(query.get('sort'), [{ k: 'updated_at', dir: 'desc' }]),
    page: Number(query.get('page')) || 1,
    low: query.get('low') === '1',
    parent: query.get('parent') || '',
    mode: loadPref('items.mode', 'table'),
    cols: null,
  };
  if (view) Object.assign(st, { cat: '', q: '', f: [], sort: [{ k: 'updated_at', dir: 'desc' }], low: false }, view.data || {});
  const selected = new Set();
  let items = [], total = 0, facets = { counts: {}, ranges: {} };

  const filterBox = h('input', { type: 'search', placeholder: 'Filter list: name, brand, any field…', value: st.q, 'aria-label': 'Filter list' });
  const left = h('aside', { class: 'filters' });
  const right = h('div');
  const head = h('div', { class: 'page-head' });
  main.append(head, h('div', { class: 'items-layout' }, left, right));

  const catId = () => (st.cat && st.cat !== 'none' ? Number(st.cat) : null);
  const fields = () => fieldsFor(catId());

  const columns = () => {
    const key = 'items.cols.' + (st.cat || 'all');
    if (view?.data?.cols && !st.cols) st.cols = view.data.cols;
    if (st.cols) return st.cols;
    const saved = loadPref(key, null);
    if (saved) return saved;
    const hidden = hiddenBuiltins(catId());
    const custom = fields().filter(f => f.show_in_list).slice(0, 5).map(f => 'custom.' + f.key);
    return ['thumb', 'code', 'name', ...(catId() && !app.categories.some(c => c.parent_id === catId()) ? [] : ['category']), ...custom, 'price', ...(hidden.has('quantity') ? [] : ['quantity']), 'status'];
  };

  const syncURL = () => {
    const p = {};
    if (view) p.view = view.id;
    if (st.cat) p.cat = st.cat;
    if (st.q) p.q = st.q;
    if (st.f.length) p.f = st.f;
    if (!(st.sort.length === 1 && st.sort[0].k === 'updated_at' && st.sort[0].dir === 'desc')) p.sort = st.sort;
    if (st.page > 1) p.page = st.page;
    if (st.low) p.low = 1;
    if (st.parent) p.parent = st.parent;
    const hash = '#/items' + qs(p);
    history.replaceState(null, '', hash);
    app.lastItemsHash = hash;
  };

  const params = (withFilters = true) => ({
    category: st.cat, q: st.q, f: withFilters ? st.f : null, low: st.low ? 1 : null, parent: st.parent || null,
  });

  async function load() {
    syncURL();
    const [, list, fc] = await Promise.all([
      refreshMeta(['categories']),
      api.get('/api/items' + qs({ ...params(), sort: st.sort, page: st.page, limit: LIMIT })),
      api.get('/api/facets' + qs(params(false)), { quiet: true }).catch(() => facets),
    ]);
    items = list.items; total = list.total; facets = fc;
    app.lastList = items.map(i => i.id);
    renderHead(); renderLeft(); renderRight();
  }
  const reload = () => { st.page = 1; load(); };

  // ---------- header ----------
  function renderHead() {
    const title = view ? view.name : st.low ? 'Low stock' : st.parent ? 'Variants' : st.cat === 'none' ? 'No category' : catId() ? catPath(catId()) : 'All items';
    clear(head).append(
      h('h1', null, title, h('span', { class: 'muted', style: { fontWeight: 400, fontSize: '15px', marginLeft: '8px' } }, `${total}`)),
      view ? h('button', { class: 'btn ghost small', onclick: deleteView }, 'Delete view') : null,
      view ? h('button', { class: 'btn small', onclick: updateView }, 'Update view') : null,
      h('button', { class: 'btn', onclick: saveView, title: 'Save these filters and columns in the sidebar' }, 'Save view'),
      h('button', { class: 'btn', onclick: () => download('/api/export' + qs({ ...params(), sort: st.sort, format: 'csv' })) }, 'Export CSV'),
      h('a', { class: 'btn primary', href: '#/new' + (catId() ? `?cat=${catId()}` : '') }, '+ New item'));
  }

  // ---------- left: categories + facets ----------
  function renderLeft() {
    clear(left);
    const tree = h('div', { class: 'cat-tree' });
    const link = (label, value, count, depth = 0, color = null) => h('a', {
      class: String(st.cat) === String(value) && !st.low && !view ? 'on' : '', style: { paddingLeft: 6 + depth * 14 + 'px' },
      href: '#', onclick: (e) => { e.preventDefault(); if (view) { go('#/items?cat=' + value); return; } st.cat = String(value); st.f = []; st.cols = null; st.low = false; st.parent = ''; reload(); },
    }, color ? h('span', { class: 'dot', style: { background: color } }) : null, h('span', { class: 'name' }, label), h('span', { class: 'count' }, count ?? ''));
    const totalAll = app.categories.reduce((a, c) => a + c.count, 0) + app.uncategorized;
    tree.appendChild(link('All items', '', totalAll));
    const add = (parent, depth) => {
      for (const c of app.categories.filter(c => (c.parent_id || null) === parent).sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name))) {
        const cnt = catDescendants(c.id).reduce((a, id) => a + (catById(id)?.count || 0), 0);
        tree.appendChild(link(c.name, c.id, cnt, depth, c.color || null));
        add(c.id, depth + 1);
      }
    };
    add(null, 0);
    if (app.uncategorized) tree.appendChild(link('No category', 'none', app.uncategorized));
    const facetsBody = h('div', { class: 'facets-body col', style: { gap: '10px' } });
    const toggleBtn = h('button', { class: 'btn small', style: { display: 'none' }, onclick: () => left.classList.toggle('collapsed') }, 'Filters');
    if (matchMedia('(max-width: 860px)').matches) { toggleBtn.style.display = ''; if (!st.f.length) left.classList.add('collapsed'); }
    left.append(h('div', null, h('h3', null, 'Categories'), tree), toggleBtn, facetsBody);

    const addFacet = (key, label, valueLabel = (v) => v) => {
      const list = facets.counts?.[key];
      if (!list || !list.length || (list.length < 2 && !activeValues(key).length)) return;
      const active = activeValues(key);
      const opts = h('div', { class: 'opts' }, list.slice(0, 60).map(({ value, count }) => h('label', { class: 'check' },
        h('input', { type: 'checkbox', checked: active.includes(value), onchange: () => toggleValue(key, value) }),
        h('span', { class: 'v' }, valueLabel(value) || '(empty)'), h('span', { class: 'count' }, count))));
      facetsBody.appendChild(h('div', { class: 'facet' }, h('h3', null, label), opts));
    };
    const addRange = (key, label, isMoney = false) => {
      const r = facets.ranges?.[key];
      if (!r || r[0] === r[1]) return;
      const cur = st.f.find(x => x.k === key && x.op === 'range')?.v || [null, null];
      const conv = (v) => v == null ? '' : isMoney ? (v / 100).toString() : String(v);
      const lo = h('input', { type: 'text', inputmode: 'decimal', placeholder: conv(r[0]), value: conv(cur[0]) });
      const hi = h('input', { type: 'text', inputmode: 'decimal', placeholder: conv(r[1]), value: conv(cur[1]) });
      const apply = () => {
        const p = (s) => { if (!s.value.trim()) return null; const n = isMoney ? parseMoney(s.value) : Number(s.value.replace(',', '.')); return Number.isNaN(n) ? null : n; };
        const v = [p(lo), p(hi)];
        st.f = st.f.filter(x => x.k !== key);
        if (v[0] != null || v[1] != null) st.f.push({ k: key, op: 'range', v });
        reload();
      };
      lo.addEventListener('change', apply); hi.addEventListener('change', apply);
      facetsBody.appendChild(h('div', { class: 'facet' }, h('h3', null, label), h('div', { class: 'range' }, lo, h('span', { class: 'muted' }, '–'), hi)));
    };
    addFacet('status', 'Status', (v) => statusInfo(v).label);
    for (const f of fields()) {
      if (['select', 'size', 'color', 'multi_select', 'text'].includes(f.type)) addFacet('custom.' + f.key, f.label);
      else if (f.type === 'boolean') addFacet('custom.' + f.key, f.label, (v) => v === 'true' ? 'Yes' : 'No');
      else if (['number', 'money', 'rating'].includes(f.type)) addRange('custom.' + f.key, f.label + (f.options?.unit ? ` (${f.options.unit})` : ''), f.type === 'money');
    }
    addRange('price', 'Price', true);
    addRange('quantity', 'Quantity');
    addFacet('location', 'Location');
    addFacet('tags', 'Tags');
  }

  function activeValues(key) {
    const b = st.f.find(x => x.k === key && x.op === 'bool');
    if (b) return [String(b.v)];
    return st.f.find(x => x.k === key && x.op === 'in')?.v || [];
  }
  function toggleValue(key, value) {
    const fieldType = key.startsWith('custom.') ? app.fields.find(x => x.key === key.slice(7))?.type : null;
    if (fieldType === 'boolean') {
      const had = st.f.find(x => x.k === key && x.op === 'bool');
      st.f = st.f.filter(x => x.k !== key);
      if (!had || String(had.v) !== value) st.f.push({ k: key, op: 'bool', v: value === 'true' });
      reload();
      return;
    }
    let f = st.f.find(x => x.k === key && x.op === 'in');
    if (!f) { f = { k: key, op: 'in', v: [] }; st.f.push(f); }
    f.v = f.v.includes(value) ? f.v.filter(x => x !== value) : [...f.v, value];
    if (!f.v.length) st.f = st.f.filter(x => x !== f);
    reload();
  }

  // ---------- right: toolbar, chips, table/grid ----------
  function renderRight() {
    clear(right);
    const modeBtns = h('div', { class: 'btn-group' },
      h('button', { class: 'btn small' + (st.mode === 'table' ? ' on' : ''), onclick: () => { st.mode = 'table'; savePref('items.mode', 'table'); renderRight(); } }, '☰ Table'),
      h('button', { class: 'btn small' + (st.mode === 'grid' ? ' on' : ''), onclick: () => { st.mode = 'grid'; savePref('items.mode', 'grid'); renderRight(); } }, '▦ Grid'));
    right.appendChild(h('div', { class: 'toolbar' }, filterBox, h('span', { class: 'spacer' }), modeBtns,
      st.mode === 'table' ? h('button', { class: 'btn small', onclick: chooseColumns }, 'Columns') : null,
      sortSelect()));

    const chips = [];
    if (st.low) chips.push(chip('Low stock', () => { st.low = false; reload(); }));
    if (st.parent) chips.push(chip('Variants of one item', () => { st.parent = ''; reload(); }));
    for (const f of st.f) {
      const label = labelForKey(f.k);
      let text;
      if (f.op === 'in') text = `${label}: ${f.v.map(v => f.k === 'status' ? statusInfo(v).label : v || '(empty)').join(', ')}`;
      else if (f.op === 'range') { const isM = f.k === 'price' || app.fields.find(x => 'custom.' + x.key === f.k)?.type === 'money'; const fm = (v) => v == null ? '…' : isM ? money(v) : v; text = `${label}: ${fm(f.v[0])} – ${fm(f.v[1])}`; }
      else if (f.op === 'bool') text = `${label}: ${f.v ? 'Yes' : 'No'}`;
      else text = `${label}: ${f.v}`;
      chips.push(chip(text, () => { st.f = st.f.filter(x => x !== f); reload(); }));
    }
    if (chips.length) right.appendChild(h('div', { class: 'active-filters' }, chips, h('button', { class: 'btn small ghost', onclick: () => { st.f = []; st.low = false; st.parent = ''; st.q = ''; filterBox.value = ''; reload(); } }, 'Clear all')));

    if (!items.length) {
      const anyFilter = st.f.length || st.q || st.low || st.parent;
      right.appendChild(h('div', { class: 'panel empty' },
        anyFilter ? [h('h2', null, 'No items match these filters.'), h('div', { class: 'row' }, h('button', { class: 'btn', onclick: () => { st.f = []; st.q = ''; st.low = false; st.parent = ''; filterBox.value = ''; reload(); } }, 'Clear filters'))]
          : [h('h2', null, 'No items yet.'), h('p', null, 'Add your first item, or import a spreadsheet.'), h('div', { class: 'row' }, h('a', { class: 'btn primary', href: '#/new' + (catId() ? `?cat=${catId()}` : '') }, '+ New item'), h('a', { class: 'btn', href: '#/import' }, 'Import CSV'))]));
      return;
    }
    right.appendChild(st.mode === 'grid' ? grid() : table());
    if (total > LIMIT) {
      const pages = Math.ceil(total / LIMIT);
      right.appendChild(h('div', { class: 'pager' },
        `${(st.page - 1) * LIMIT + 1}–${Math.min(total, st.page * LIMIT)} of ${total}`,
        h('button', { class: 'btn small', disabled: st.page <= 1, onclick: () => { st.page--; load(); } }, '‹ Prev'),
        h('span', null, `Page ${st.page} / ${pages}`),
        h('button', { class: 'btn small', disabled: st.page >= pages, onclick: () => { st.page++; load(); } }, 'Next ›')));
    }
    renderBulk();
  }

  function chip(text, onRemove) { return h('span', { class: 'chip' }, text, h('button', { 'aria-label': 'Remove filter', onclick: onRemove }, '×')); }

  function labelForKey(k) {
    if (k.startsWith('custom.')) return app.fields.find(f => f.key === k.slice(7))?.label || k.slice(7);
    return { status: 'Status', tags: 'Tag', location: 'Location', price: 'Price', quantity: 'Qty' }[k] || k;
  }

  function sortSelect() {
    const opts = [['updated_at:desc', 'Recently updated'], ['created_at:desc', 'Newest first'], ['created_at:asc', 'Oldest first'], ['name:asc', 'Name A–Z'], ['name:desc', 'Name Z–A'],
      ['code:asc', 'Code'], ['price:asc', 'Price low–high'], ['price:desc', 'Price high–low'], ['quantity:asc', 'Quantity low–high']];
    for (const f of fields()) if (f.type !== 'long_text' && f.type !== 'multi_select') opts.push([`custom.${f.key}:asc`, `${f.label}`]);
    const curKey = `${st.sort[0]?.k}:${st.sort[0]?.dir}`;
    if (!opts.some(o => o[0] === curKey)) opts.push([curKey, 'Custom sort']);
    return h('select', { 'aria-label': 'Sort', style: { height: '26px', fontSize: '13px' }, onchange: (e) => { const [k, dir] = e.target.value.split(':'); st.sort = [{ k, dir }]; reload(); } },
      opts.map(([v, l]) => h('option', { value: v, selected: v === curKey }, 'Sort: ' + l)));
  }

  function headerSort(key, e) {
    const i = st.sort.findIndex(s => s.k === key);
    if (e.shiftKey) {
      if (i >= 0) st.sort[i].dir = st.sort[i].dir === 'asc' ? 'desc' : 'asc';
      else st.sort.push({ k: key, dir: 'asc' });
    } else {
      st.sort = [{ k: key, dir: i === 0 && st.sort[0].dir === 'asc' ? 'desc' : 'asc' }];
    }
    reload();
  }

  function cellValue(col, it) {
    if (col === 'thumb') return it.thumb ? h('img', { class: 'thumb', src: it.thumb, alt: '', loading: 'lazy' }) : h('div', { class: 'thumb' });
    if (col === 'code') return h('span', { class: 'mono' }, it.code);
    if (col === 'name') return h('strong', { style: { fontWeight: 500 } }, it.name);
    if (col === 'category') return it.category_id ? catPath(it.category_id) : '';
    if (col === 'price' || col === 'cost') return money(it[col]);
    if (col === 'quantity') return h('span', null, String(it.quantity), ' ', qtyBadge(it));
    if (col === 'status') return statusBadge(it.status);
    if (col === 'tags') return it.tags.join(', ');
    if (col === 'updated_at' || col === 'created_at') return relTime(it[col]);
    if (col.startsWith('custom.')) {
      const key = col.slice(7);
      const f = app.fields.find(x => x.key === key && (x.category_id == null || fieldsFor(it.category_id).includes(x))) || app.fields.find(x => x.key === key);
      return f ? fieldValueText(f, it.custom[key], it) : String(it.custom[key] ?? '');
    }
    return String(it[col] ?? '');
  }

  function colLabel(col) {
    if (col.startsWith('custom.')) return app.fields.find(f => f.key === col.slice(7))?.label || col.slice(7);
    return BUILTIN_COLS[col]?.label || col;
  }
  function colSortKey(col) {
    if (col.startsWith('custom.')) { const f = app.fields.find(x => x.key === col.slice(7)); return f && f.type !== 'multi_select' && f.type !== 'long_text' ? col : null; }
    return BUILTIN_COLS[col]?.sort || null;
  }
  function colIsNum(col) {
    if (col.startsWith('custom.')) return ['number', 'money', 'rating'].includes(app.fields.find(x => x.key === col.slice(7))?.type);
    return !!BUILTIN_COLS[col]?.num;
  }

  function table() {
    const cols = columns();
    const allOnPage = items.length > 0 && items.every(i => selected.has(i.id));
    const tbody = h('tbody', null, items.map(it => {
      const cb = h('input', { type: 'checkbox', checked: selected.has(it.id), 'aria-label': 'Select', onclick: (e) => e.stopPropagation(), onchange: (e) => { e.target.checked ? selected.add(it.id) : selected.delete(it.id); tr.classList.toggle('sel', e.target.checked); renderBulk(); } });
      const tr = h('tr', { class: 'click' + (selected.has(it.id) ? ' sel' : ''), onclick: (e) => {
        if (e.ctrlKey || e.metaKey) { cb.checked = !cb.checked; cb.dispatchEvent(new Event('change')); return; }
        go(`#/item/${it.id}`);
      } }, h('td', { class: 'chk' }, cb), cols.map(c => h('td', { class: colIsNum(c) ? 'num' : '' }, cellValue(c, it))));
      return tr;
    }));
    return h('div', { class: 'table-wrap' }, h('table', { class: 't' },
      h('thead', null, h('tr', null,
        h('th', { class: 'chk' }, h('input', { type: 'checkbox', checked: allOnPage, 'aria-label': 'Select all on this page', onchange: (e) => { items.forEach(i => e.target.checked ? selected.add(i.id) : selected.delete(i.id)); renderRight(); } })),
        cols.map(c => {
          const sk = colSortKey(c);
          const si = st.sort.findIndex(s => s.k === sk);
          return h('th', { class: (sk ? 'sortable ' : '') + (colIsNum(c) ? 'num' : ''), title: sk ? 'Click to sort · Shift+click to add a second sort' : null, onclick: sk ? (e) => headerSort(sk, e) : null },
            c === 'thumb' ? '' : colLabel(c), si >= 0 ? h('span', { class: 'arrow' }, (st.sort[si].dir === 'asc' ? '▲' : '▼') + (st.sort.length > 1 ? si + 1 : '')) : null);
        }))),
      tbody));
  }

  function grid() {
    const fs = fields().filter(f => f.show_in_list).slice(0, 2);
    return h('div', { class: 'grid-view' }, items.map(it => {
      const cb = h('input', { type: 'checkbox', class: 'chk', checked: selected.has(it.id), onclick: (e) => e.stopPropagation(), onchange: (e) => { e.target.checked ? selected.add(it.id) : selected.delete(it.id); tile.classList.toggle('sel', e.target.checked); renderBulk(); } });
      const tile = h('div', { class: 'tile' + (selected.has(it.id) ? ' sel' : ''), onclick: () => go(`#/item/${it.id}`) }, cb,
        h('div', { class: 'img' }, it.thumb ? h('img', { src: it.thumb, alt: '', loading: 'lazy' }) : 'No photo'),
        h('div', { class: 'body' },
          h('div', { class: 'name', title: it.name }, it.name),
          h('div', { class: 'mono small muted' }, it.code),
          fs.length ? h('div', { class: 'small muted' }, fs.map(f => fieldValueText(f, it.custom[f.key], it)).filter(Boolean).join(' · ')) : null,
          h('div', { class: 'row', style: { justifyContent: 'space-between' } }, h('strong', null, money(it.price)), h('span', { class: 'small' }, hiddenBuiltins(it.category_id).has('quantity') ? statusBadge(it.status) : [`Qty ${it.quantity} `, qtyBadge(it)]))));
      return tile;
    }));
  }

  // ---------- bulk ----------
  let bulkEl = null;
  function renderBulk() {
    bulkEl?.remove();
    if (!selected.size) return;
    const ids = [...selected];
    bulkEl = h('div', { class: 'bulkbar' },
      h('strong', null, `${ids.length} selected`),
      h('a', { class: 'btn primary', href: `#/print?add=${ids.join(',')}` }, 'Print labels'),
      h('button', { class: 'btn', onclick: () => bulkEdit(ids) }, 'Edit field…'),
      h('button', { class: 'btn', onclick: () => bulkTag(ids) }, 'Tags…'),
      h('button', { class: 'btn', onclick: () => download('/api/export' + qs({ ids: ids.join(','), format: 'csv' })) }, 'Export'),
      h('button', { class: 'btn danger', onclick: () => bulkTrash(ids) }, 'Move to Trash'),
      h('span', { class: 'spacer' }),
      h('button', { class: 'btn ghost', onclick: () => { selected.clear(); renderRight(); } }, 'Clear selection'));
    right.appendChild(bulkEl);
  }

  async function bulkTrash(ids) {
    await api.post('/api/items/bulk', { ids, action: 'trash' });
    selected.clear();
    toast(`${ids.length} item(s) moved to Trash`, { action: async () => { await api.post('/api/items/bulk', { ids, action: 'restore' }); load(); } });
    refreshMeta(['categories']);
    load();
  }

  async function bulkTag(ids) {
    const input = h('input', { type: 'text', placeholder: 'Tag name', autofocus: true });
    const mode = h('select', null, h('option', { value: 'add_tag' }, 'Add tag'), h('option', { value: 'remove_tag' }, 'Remove tag'));
    const r = await dialog({ title: `Tags for ${ids.length} item(s)`, body: h('div', { class: 'row nowrap' }, mode, input),
      actions: [{ label: 'Apply', primary: true, onClick: async () => { if (!input.value.trim()) return false; return api.post('/api/items/bulk', { ids, action: mode.value, value: input.value.trim() }); } }] });
    if (r) { toast(`${r.changed} item(s) updated`); load(); }
  }

  async function bulkEdit(ids) {
    const options = [['status', 'Status'], ['category_id', 'Category'], ['price', 'Price'], ['cost', 'Cost'], ['quantity', 'Quantity'], ['low_stock_at', 'Low stock alert'],
      ['location', 'Location'], ['supplier', 'Supplier'], ['barcode_format', 'Barcode format']];
    for (const f of fields()) if (f.type !== 'age' && f.type !== 'identifier' && !f.unique) options.push(['custom.' + f.key, f.label]);
    const which = h('select', null, options.map(([k, l]) => h('option', { value: k }, l)));
    const valueBox = h('div');
    let getValue = () => null, getMode = () => 'set';
    const renderValue = () => {
      clear(valueBox);
      const k = which.value;
      getMode = () => 'set';
      if (k === 'status') { const s = statusSelect('in_stock'); valueBox.append(s); getValue = () => s.value; }
      else if (k === 'category_id') { const s = categorySelect(catId()); valueBox.append(s); getValue = () => s.value; }
      else if (k === 'barcode_format') { const s = formatSelect('code128'); valueBox.append(s); getValue = () => s.value; }
      else if (k === 'price' || k === 'cost') {
        const m = h('select', null, h('option', { value: 'set' }, 'Set to'), h('option', { value: 'percent' }, 'Change by %'), h('option', { value: 'add' }, 'Add / subtract amount'));
        const inp = h('input', { type: 'text', inputmode: 'decimal', placeholder: '0' });
        valueBox.append(h('div', { class: 'row nowrap' }, m, inp), h('p', { class: 'muted small', style: { marginTop: '6px' } }, 'Examples: Set to 19.99 · Change by % 10 (or -15) · Add -2.00'));
        getMode = () => m.value;
        getValue = () => { if (m.value === 'percent') return Number(inp.value.replace(',', '.')); const c = parseMoney(inp.value); if (c == null || Number.isNaN(c)) throw new Error('Enter an amount'); return c; };
      } else if (k === 'quantity' || k === 'low_stock_at') { const inp = h('input', { type: 'number' }); valueBox.append(inp); getValue = () => inp.value === '' ? null : Number(inp.value); }
      else if (k === 'location' || k === 'supplier') { const inp = h('input', { type: 'text', style: { width: '100%' } }); valueBox.append(inp); getValue = () => inp.value; }
      else {
        const f = app.fields.find(x => 'custom.' + x.key === k);
        const fi = fieldInput(f, undefined);
        valueBox.append(fi.el);
        getValue = () => fi.get();
      }
    };
    which.addEventListener('change', renderValue);
    renderValue();
    const r = await dialog({
      title: `Edit ${ids.length} item(s)`,
      body: h('div', { class: 'col' }, h('div', { class: 'field' }, h('label', null, 'Field'), which), h('div', { class: 'field' }, h('label', null, 'New value'), valueBox), h('p', { class: 'muted small' }, 'Leave the value empty to clear it.')),
      actions: [{ label: 'Apply to all', primary: true, onClick: async () => {
        try { return await api.post('/api/items/bulk', { ids, action: 'set', key: which.value, value: getValue(), mode: getMode() }); }
        catch (e) { toast(e.message, { error: true }); return false; }
      } }],
    });
    if (r) { toast(`${r.changed} item(s) updated`); refreshMeta(['categories']); load(); }
  }

  // ---------- columns ----------
  async function chooseColumns() {
    const cur = columns();
    const all = [...Object.keys(BUILTIN_COLS), ...fields().map(f => 'custom.' + f.key)];
    const order = [...cur, ...all.filter(c => !cur.includes(c))];
    const boxes = order.map(c => ({ c, cb: h('input', { type: 'checkbox', checked: cur.includes(c) }) }));
    const r = await dialog({
      title: 'Columns',
      body: h('div', { class: 'col', style: { gap: '4px' } }, h('p', { class: 'muted small' }, 'Pick the columns for this category. Saved on this device.'),
        boxes.map(({ c, cb }) => h('label', { class: 'check' }, cb, c === 'thumb' ? 'Photo' : colLabel(c)))),
      actions: [{ label: 'Reset', onClick: () => 'reset' }, { label: 'Apply', primary: true, onClick: () => boxes.filter(b => b.cb.checked).map(b => b.c) }],
    });
    if (!r) return;
    const key = 'items.cols.' + (st.cat || 'all');
    if (r === 'reset') { try { localStorage.removeItem(key); } catch { /* */ } st.cols = null; }
    else { savePref(key, r); st.cols = r; }
    renderRight();
  }

  // ---------- saved views ----------
  const viewData = () => ({ cat: st.cat, q: st.q, f: st.f, sort: st.sort, low: st.low, cols: st.cols || columns() });
  async function saveView() {
    const name = await promptDialog('Save view', 'Name', '', { placeholder: 'e.g. Phones – Grade A under $300', okLabel: 'Save' });
    if (!name) return;
    const r = await api.post('/api/docs/view', { name, data: viewData() });
    await refreshMeta(['views']);
    toast('View saved in the sidebar');
    go(`#/items?view=${r.id}`);
  }
  async function updateView() {
    await api.put(`/api/docs/view/${view.id}`, { name: view.name, data: viewData() });
    await refreshMeta(['views']);
    toast('View updated');
  }
  async function deleteView() {
    if (!(await confirmDialog('Delete view?', `“${view.name}” will be removed from the sidebar. Items are not affected.`, { okLabel: 'Delete', danger: true }))) return;
    await api.del(`/api/docs/view/${view.id}`);
    await refreshMeta(['views']);
    go('#/items');
  }

  filterBox.addEventListener('input', debounce(() => { st.q = filterBox.value.trim(); reload(); }, 250));
  filterBox.addEventListener('keydown', (e) => { if (e.key === 'Escape') { filterBox.value = ''; st.q = ''; reload(); } });

  await load();
  return {
    newQuery: catId() ? `?cat=${catId()}` : '',
    shortcuts: {},
  };
}

function safeJSON(s, fallback) { try { return s ? JSON.parse(s) : fallback; } catch { return fallback; } }
