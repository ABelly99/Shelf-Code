// Print designer: any mix of codes, copies and label sizes on one page.

import { h, app, $, clear, money, toast, dialog, confirmDialog, promptDialog, debounce, catPath, fmtDate } from '../util.js';
import { api, qs } from '../api.js';
import { FORMATS, guessFormat, formatLabel } from '../barcode.js';
import { PAPERS, SHEETS, ROLLS, layout, renderPages, printPages, labelById, defaultLabelFor, labelData, calibrationFor } from '../labelkit.js';
import { formatSelect } from '../components.js';
import { go, refreshMeta } from '../main.js';

const STORE_KEY = 'print.state.v1';

function defaultState() {
  return {
    lines: [], // { id, item_id?, code?, text?, format?, label_id, qty }
    page: {
      mode: 'free', paper: app.settings.paper || 'letter', orientation: 'portrait',
      margin: app.settings.page_margin_mm ?? 6, gap: app.settings.page_gap_mm ?? 2,
      sheet: 'avery5160', custom: { ...SHEETS.custom }, start: 0, roll: 'label',
      pack: false, cut: app.settings.cut_lines !== false,
    },
  };
}

function loadState() {
  try { const s = JSON.parse(localStorage.getItem(STORE_KEY)); if (s?.lines) { const d = defaultState(); return { lines: s.lines, page: { ...d.page, ...s.page } }; } } catch { /* */ }
  return defaultState();
}
function saveState(st) { try { localStorage.setItem(STORE_KEY, JSON.stringify(st)); } catch { /* */ } }

export default async function ({ main, query }) {
  const st = loadState();
  const items = new Map(); // item_id -> item
  let lid = Date.now();
  const nextId = () => ++lid;

  async function fetchItems(ids) {
    const need = ids.filter(id => !items.has(id));
    for (let i = 0; i < need.length; i += 400) {
      const chunk = need.slice(i, i + 400);
      const r = await api.get('/api/items' + qs({ ids: chunk.join(','), limit: 5000 }));
      r.items.forEach(it => items.set(it.id, it));
    }
  }

  async function addItems(ids, qty = 1) {
    await fetchItems(ids);
    for (const id of ids) {
      const it = items.get(id);
      if (!it) continue;
      const existing = st.lines.find(l => l.item_id === id);
      if (existing) existing.qty += qty;
      else st.lines.push({ id: nextId(), item_id: id, label_id: defaultLabelFor(it.category_id)?.id ?? null, qty, format: '' });
    }
  }

  // ?job=ID: a print sheet prepared by an AI agent (or any saved layout)
  const job = query.get('job');
  if (job) {
    try {
      const { docs } = await api.get('/api/docs/printjob');
      const d = docs.find(x => String(x.id) === job);
      if (d?.data) {
        st.lines = (d.data.lines || []).map(l => ({ ...l, id: nextId() }));
        Object.assign(st.page, d.data.page || {});
        saveState(st);
      }
    } catch { /* keep the current list */ }
    history.replaceState(null, '', '#/print');
  }

  // ?add=1,2,3 from the item page / list
  const add = query.get('add');
  if (add) {
    const ids = add.split(',').map(Number).filter(Boolean);
    await addItems(ids);
    history.replaceState(null, '', '#/print');
  }
  await fetchItems(st.lines.filter(l => l.item_id).map(l => l.item_id));
  st.lines = st.lines.filter(l => !l.item_id || items.has(l.item_id));

  // ---------- page settings ----------
  const P = st.page;
  const ctl = {};
  const sel = (key, options, onchange) => {
    const s = h('select', { onchange: () => { P[key] = s.value; onchange?.(); changed(); } }, Object.entries(options).map(([k, v]) => h('option', { value: k, selected: String(P[key]) === k }, typeof v === 'string' ? v : v.label)));
    ctl[key] = s;
    return s;
  };
  const numIn = (key, width = '64px', obj = P) => {
    const i = h('input', { type: 'number', step: '0.1', value: obj[key], style: { width }, onchange: () => { obj[key] = Number(i.value) || 0; changed(); } });
    return i;
  };
  const chk = (key, label) => h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: !!P[key], onchange: (e) => { P[key] = e.target.checked; changed(); } }), label);

  const modeBtns = h('div', { class: 'btn-group' });
  const settingsBox = h('div', { class: 'row', style: { gap: '10px 16px' } });
  const renderSettings = () => {
    modeBtns.replaceChildren(...[['free', 'Free (mixed sizes)'], ['sheet', 'Sticker sheet'], ['roll', 'Label printer']].map(([k, l]) =>
      h('button', { class: 'btn small' + (P.mode === k ? ' on' : ''), onclick: () => { P.mode = k; renderSettings(); changed(); } }, l)));
    clear(settingsBox);
    if (P.mode === 'free') {
      settingsBox.append(
        h('label', { class: 'row nowrap' }, 'Paper ', sel('paper', PAPERS)),
        h('label', { class: 'row nowrap' }, sel('orientation', { portrait: 'Portrait', landscape: 'Landscape' })),
        h('label', { class: 'row nowrap' }, 'Margins ', numIn('margin', '56px'), ' mm'),
        h('label', { class: 'row nowrap' }, 'Gap ', numIn('gap', '56px'), ' mm'),
        chk('pack', 'Pack tightly'),
        chk('cut', 'Cut lines'));
    } else if (P.mode === 'sheet') {
      settingsBox.append(h('label', { class: 'row nowrap' }, 'Sheet ', sel('sheet', SHEETS, () => { P.start = 0; renderSettings(); })),
        h('label', { class: 'row nowrap' }, 'Start at label ', (() => { const i = h('input', { type: 'number', min: 1, value: (P.start || 0) + 1, style: { width: '64px' }, onchange: () => { P.start = Math.max(0, (Number(i.value) || 1) - 1); changed(); } }); return i; })()),
        h('span', { class: 'muted small' }, 'Tip: click a cell in the preview to skip used labels.'),
        chk('cut', 'Outline (preview)'));
      if (P.sheet === 'custom') {
        const c = P.custom;
        settingsBox.append(h('div', { class: 'row', style: { width: '100%', gap: '8px' } },
          h('label', { class: 'row nowrap' }, 'Paper ', (() => { const s = h('select', { onchange: () => { c.paper = s.value; changed(); } }, Object.entries(PAPERS).map(([k, v]) => h('option', { value: k, selected: c.paper === k }, v.label))); return s; })()),
          ...[['cols', 'Columns'], ['rows', 'Rows'], ['w', 'Label W'], ['h', 'Label H'], ['left', 'Left'], ['top', 'Top'], ['hp', 'Across pitch'], ['vp', 'Down pitch']].map(([k, l]) =>
            h('label', { class: 'row nowrap small' }, l + ' ', numIn(k, '62px', c))),
          h('span', { class: 'muted small' }, 'mm. Pitch = distance from one label’s left (top) edge to the next.')));
      }
    } else {
      settingsBox.append(h('label', { class: 'row nowrap' }, 'Label size ', sel('roll', ROLLS)),
        h('span', { class: 'muted small' }, 'One label per page. In the print dialog, pick your label printer and the same paper size.'));
    }
  };

  // ---------- lines ----------
  const linesBox = h('div');
  const addInput = h('input', { type: 'text', placeholder: 'Scan or type a code, then Enter', spellcheck: 'false', autocomplete: 'off', 'aria-label': 'Add by code' });
  const addMsg = h('div', { class: 'small', style: { minHeight: '18px', marginBottom: '6px' } });

  async function addByCode(code) {
    code = code.trim();
    if (!code) return;
    addMsg.replaceChildren();
    const r = await api.get('/api/lookup?code=' + encodeURIComponent(code), { quiet: true });
    if (r.item_id) {
      await addItems([r.item_id]);
      const it = items.get(r.item_id);
      addMsg.replaceChildren(h('span', { class: 'ok-text' }, `+1 ${it.name}`));
      addInput.value = '';
      changed(true);
    } else {
      addMsg.replaceChildren(h('span', { class: 'muted' }, `${code} is not in the database. `),
        h('button', { class: 'btn small', onclick: () => { addRaw(code); addMsg.replaceChildren(); } }, 'Add it as a code-only label'));
      addInput.value = '';
    }
  }
  addInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); addByCode(addInput.value); } });

  async function addRaw(code = '') {
    const codeIn = h('input', { type: 'text', class: 'code-input', value: code, autofocus: !code, spellcheck: 'false' });
    const textIn = h('input', { type: 'text', placeholder: 'Optional text, e.g. “Shelf B3”', autofocus: !!code });
    const fmt = formatSelect(code ? guessFormat(code) : 'code128');
    const r = await dialog({
      title: 'Add a code that is not an item',
      body: h('div', { class: 'col' }, h('p', { class: 'muted small' }, 'For shelf and bin labels, or one-off tags. It is not saved as an item.'),
        h('div', { class: 'field' }, h('label', null, 'Code'), codeIn), h('div', { class: 'field' }, h('label', null, 'Text (shown as the name)'), textIn),
        h('div', { class: 'field' }, h('label', null, 'Barcode format'), fmt)),
      actions: [{ label: 'Add', primary: true, onClick: () => codeIn.value.trim() ? { code: codeIn.value.trim(), text: textIn.value.trim(), format: fmt.value } : false }],
    });
    if (!r) return;
    st.lines.push({ id: nextId(), code: r.code, text: r.text, format: r.format, label_id: defaultLabelFor(null)?.id ?? null, qty: 1 });
    changed(true);
  }

  // Blank codes: unique random codes printed now and given to items later (scan one → new item).
  async function addBlank() {
    const defLabel = app.labels.find(l => l.name.startsWith('Small'))?.id ?? defaultLabelFor(null)?.id;
    const fmt = formatSelect(app.settings.default_format || 'code128');
    const rowsBox = h('div', { class: 'col', style: { gap: '6px' } });
    const summary = h('div', { class: 'small', style: { fontWeight: 600 } });
    const err = h('div', { class: 'danger-text small' });
    const rows = [];

    const update = () => {
      const codes = rows.reduce((a, r) => a + (Number(r.codes.value) || 0), 0);
      const labels = rows.reduce((a, r) => a + (Number(r.codes.value) || 0) * (Number(r.copies.value) || 0), 0);
      summary.textContent = `${codes} new code${codes === 1 ? '' : 's'} · ${labels} label${labels === 1 ? '' : 's'}`;
    };
    const addRow = (codes = 1, copies = 1, label = defLabel) => {
      const r = {};
      r.codes = h('input', { type: 'number', min: 1, max: 1000, value: codes, style: { width: '72px' }, oninput: update, 'aria-label': 'How many codes' });
      r.copies = h('input', { type: 'number', min: 1, max: 500, value: copies, style: { width: '72px' }, oninput: update, 'aria-label': 'Copies of each' });
      r.label = h('select', { style: { maxWidth: '180px' }, 'aria-label': 'Label design' }, labelOptions(label));
      r.el = h('div', { class: 'row nowrap', style: { gap: '6px' } },
        r.codes, h('span', { class: 'muted small nowrap' }, 'code(s) ×'), r.copies, h('span', { class: 'muted small nowrap' }, 'copies'), r.label,
        h('button', { class: 'btn small ghost', type: 'button', 'aria-label': 'Remove row', onclick: () => { if (rows.length > 1) { rows.splice(rows.indexOf(r), 1); r.el.remove(); update(); } } }, '×'));
      rows.push(r);
      rowsBox.appendChild(r.el);
      update();
      return r;
    };
    addRow();

    // Quick entry: "2, 6, 12, 1" → four different codes printed 2, 6, 12 and 1 times.
    const quick = h('input', { type: 'text', placeholder: 'e.g. 2, 6, 12, 1', style: { width: '100%' }, autofocus: true,
      oninput: () => {
        const nums = quick.value.split(/[\s,;x×]+/).map(Number).filter(n => Number.isInteger(n) && n > 0);
        if (!nums.length) return;
        rows.splice(0).forEach(r => r.el.remove());
        nums.forEach(n => addRow(1, n));
      } });

    const r = await dialog({
      title: 'Print blank codes', wide: true,
      body: h('div', { class: 'col', style: { gap: '12px' } },
        h('p', { class: 'muted small', style: { margin: 0 } }, 'New random codes that no item uses yet. Stick them on things now; later, scan one and SHELFCODE opens a new item with that code (or attaches it to an existing item).'),
        h('div', { class: 'field' }, h('label', null, 'Quick: copies for each new code, separated by commas'), quick,
          h('div', { class: 'help' }, '“2, 6, 12, 1” = one code printed 2 times, another 6 times, another 12 times, another once.')),
        h('div', { class: 'field' }, h('label', null, 'Or build it row by row'), rowsBox,
          h('div', null, h('button', { class: 'btn small', type: 'button', onclick: () => { const last = rows[rows.length - 1]; addRow(1, 1, last ? Number(last.label.value) : defLabel).codes.focus(); } }, '+ Add row'))),
        h('div', { class: 'row' }, h('label', { class: 'row nowrap' }, 'Barcode format ', fmt), h('span', { class: 'spacer' }), summary),
        err),
      actions: [{ label: 'Add to print list', primary: true, onClick: async () => {
        const groups = rows.map(r => ({ codes: Math.round(Number(r.codes.value)), copies: Math.round(Number(r.copies.value)), label: Number(r.label.value) }));
        if (groups.some(g => !(g.codes >= 1) || !(g.copies >= 1))) { err.textContent = 'Every row needs at least 1 code and 1 copy.'; return false; }
        const total = groups.reduce((a, g) => a + g.codes, 0);
        if (total > 1000) { err.textContent = 'At most 1000 new codes at once.'; return false; }
        try {
          const res = await api.post('/api/codes/reserve', { count: total, format: fmt.value });
          let i = 0;
          for (const g of groups) {
            st.lines.push({ id: nextId(), batch: res.codes.slice(i, i + g.codes), format: res.format, label_id: g.label, qty: g.copies });
            i += g.codes;
          }
          return { codes: total };
        } catch (e) { err.textContent = e.message; return false; }
      } }],
    });
    if (!r?.codes) return;
    changed(true);
    toast(`${r.codes} new blank code${r.codes === 1 ? '' : 's'} added. They're saved, so they stay unique even if you don't print them now.`);
  }

  const labelOptions = (selected) => app.labels.map(l => h('option', { value: l.id, selected: l.id === selected }, `${l.name}`));

  function renderLines() {
    clear(linesBox);
    if (!st.lines.length) {
      linesBox.appendChild(h('div', { class: 'panel empty', style: { padding: '28px 16px' } },
        h('p', null, 'Nothing to print yet.'),
        h('p', { class: 'muted small' }, 'Scan codes into the box above, or select items in the ', h('a', { href: '#/items' }, 'item list'), ' and choose “Print labels”.'),
        h('button', { class: 'btn', onclick: () => addBlank() }, '+ Print blank codes to assign later')));
      return;
    }
    const totalLabels = st.lines.reduce((a, l) => a + (Number(l.qty) || 0) * (l.batch ? l.batch.length : 1), 0);
    linesBox.appendChild(h('div', { class: 'table-wrap' }, h('table', { class: 't print-lines' },
      h('thead', null, h('tr', null, h('th', null, 'Item / code'), h('th', null, 'Label design / format'), h('th', { class: 'num' }, 'Copies'), h('th', null, ''))),
      h('tbody', null, st.lines.map((l, i) => {
        const it = l.item_id ? items.get(l.item_id) : null;
        const qty = h('input', { type: 'number', min: 0, value: l.qty, 'aria-label': 'Copies', onchange: () => { l.qty = Math.max(0, Math.round(Number(qty.value) || 0)); changed(); } });
        const lsel = h('select', { onchange: () => { l.label_id = Number(lsel.value); changed(); } }, labelOptions(l.label_id));
        const fsel = h('select', { onchange: () => { l.format = fsel.value; changed(); } }, h('option', { value: '' }, it ? `Item’s (${formatLabel(it.barcode_format)})` : 'Default'),
          Object.entries(FORMATS).map(([k, f]) => h('option', { value: k, selected: l.format === k }, f.label)));
        return h('tr', { class: 'stack' },
          l.batch ? h('td', { class: 'wrap' },
            h('div', null, h('strong', null, l.batch.length === 1 ? 'Blank code' : `${l.batch.length} blank codes`), h('span', { class: 'muted small' }, l.batch.length === 1 ? ' (not assigned yet)' : ` × ${l.qty} copies each`)),
            h('div', { class: 'mono small muted' }, l.batch.length > 1 ? `${l.batch[0]} … ${l.batch[l.batch.length - 1]}` : l.batch[0]))
          : h('td', { class: 'wrap' },
            h('div', null, it ? h('a', { href: `#/item/${it.id}` }, it.name) : h('span', { class: 'muted' }, l.text || '(code only)')),
            h('div', { class: 'mono small muted' }, it ? it.code : l.code)),
          h('td', null, h('div', { class: 'col', style: { gap: '4px' } }, lsel, fsel)),
          h('td', { class: 'num qty' }, h('div', { class: 'row nowrap', style: { justifyContent: 'flex-end', gap: '2px' } },
            h('button', { class: 'btn small ghost', 'aria-label': 'One less', onclick: () => { l.qty = Math.max(0, l.qty - 1); qty.value = l.qty; changed(); } }, '−'), qty,
            h('button', { class: 'btn small ghost', 'aria-label': 'One more', onclick: () => { l.qty++; qty.value = l.qty; changed(); } }, '+'))),
          h('td', null, h('button', { class: 'btn small ghost', 'aria-label': 'Remove', onclick: () => { st.lines.splice(i, 1); changed(true); } }, '×')));
      })))),
    h('div', { class: 'row', style: { marginTop: '8px' } },
      h('span', { class: 'muted small' }, `${st.lines.length} line(s) · ${totalLabels} label(s)`),
      h('span', { class: 'spacer' }),
      h('button', { class: 'btn small', onclick: () => { st.lines.forEach(l => { const it = items.get(l.item_id); if (it) l.qty = Math.max(0, it.quantity); }); changed(true); }, title: 'One label per unit in stock' }, 'Copies = quantity in stock'),
      h('button', { class: 'btn small', onclick: async () => {
        const lsel = h('select', null, labelOptions(st.lines[0]?.label_id));
        const r = await dialog({ title: 'Use one label design for all lines', body: lsel, actions: [{ label: 'Apply', primary: true, onClick: () => Number(lsel.value) }] });
        if (r) { st.lines.forEach(l => { l.label_id = r; }); changed(true); }
      } }, 'Same design for all')));
  }

  // ---------- preview ----------
  const previewArea = h('div', { class: 'preview-area' });
  const summary = h('div', { class: 'small muted' });
  const warnBox = h('div', { class: 'small warn-text' });
  let lastLayout = null;

  function buildLabels() {
    const out = [];
    for (const l of st.lines) {
      const it = l.item_id ? items.get(l.item_id) : null;
      const tpl = labelById(l.label_id) || defaultLabelFor(it?.category_id);
      const w = tpl?.data?.w || 50, hh = tpl?.data?.h || 30;
      if (l.batch) {
        for (const code of l.batch) {
          const d = labelData({ code, format: l.format, text: '' }, null);
          for (let i = 0; i < (Number(l.qty) || 0); i++) out.push({ w, h: hh, tpl, data: d });
        }
        continue;
      }
      const d = labelData(l, it);
      for (let i = 0; i < (Number(l.qty) || 0); i++) out.push({ w, h: hh, tpl, data: d, item_id: l.item_id });
    }
    return out;
  }

  const renderPreview = debounce(() => {
    const labels = buildLabels();
    lastLayout = layout(labels, P);
    const { sheets, warnings } = renderPages(lastLayout.pages, { cut: P.cut, preview: true, onCell: (i) => { P.start = i; renderSettings(); changed(); } });
    clear(previewArea);
    if (!labels.length) { previewArea.appendChild(h('div', { class: 'empty' }, 'The preview appears here.')); summary.textContent = ''; warnBox.textContent = ''; return; }
    const avail = previewArea.clientWidth - 34;
    sheets.forEach((s, i) => {
      const p = lastLayout.pages[i];
      const pxPerMm = 96 / 25.4;
      const scale = Math.min(1.6, avail / (p.w * pxPerMm));
      s.style.transform = `scale(${scale})`;
      const wrap = h('div', { class: 'sheet-wrap', style: { width: p.w * pxPerMm * scale + 'px', height: p.h * pxPerMm * scale + 'px' } }, s);
      previewArea.append(wrap, h('div', { class: 'page-no' }, `Page ${i + 1} of ${sheets.length}`));
    });
    summary.textContent = `${labels.length} label(s) on ${lastLayout.pages.length} page(s)`;
    const all = [...lastLayout.warnings, ...warnings];
    warnBox.replaceChildren(...all.slice(0, 6).map(w => h('div', null, '⚠ ' + w)));
  }, 80);

  function changed(relines = false) {
    saveState(st);
    if (relines) renderLines();
    renderPreview();
  }

  // ---------- print ----------
  async function doPrint() {
    if (!lastLayout || !lastLayout.pages.length || !buildLabels().length) { toast('Add some labels first.', { error: true }); return; }
    const key = P.mode === 'sheet' ? 'sheet:' + P.sheet : P.mode === 'roll' ? 'roll' : 'paper:' + P.paper;
    await printPages(lastLayout.pages, { cut: P.cut && P.mode === 'free', calibration: calibrationFor(key) });
    const counts = {};
    for (const l of st.lines) if (l.item_id && l.qty > 0) counts[l.item_id] = (counts[l.item_id] || 0) + l.qty;
    api.post('/api/printed', { items: counts }, { quiet: true }).catch(() => {});
    api.put('/api/settings', { last_print_job: st }, { quiet: true }).then(s => { app.settings = s; }).catch(() => {});
  }

  async function saveLayout() {
    const name = await promptDialog('Save print layout', 'Name', '', { placeholder: 'e.g. Weekly restock tags', okLabel: 'Save' });
    if (!name) return;
    await api.post('/api/docs/printjob', { name, data: st });
    toast('Layout saved');
  }

  async function loadLayout() {
    const { docs } = await api.get('/api/docs/printjob');
    const lastJob = app.settings.last_print_job;
    if (!docs.length && !lastJob) { toast('No saved layouts yet.'); return; }
    const pick = await dialog({
      title: 'Load a print layout', wide: true,
      body: (close) => h('ul', { class: 'list-plain' },
        lastJob ? h('li', null, h('strong', { class: 'grow' }, 'Last printed job'), h('span', { class: 'muted small' }, `${lastJob.lines?.length || 0} line(s)`), h('button', { class: 'btn small primary', onclick: () => close({ data: lastJob }) }, 'Load')) : null,
        docs.map(d => h('li', null, h('span', { class: 'grow' }, d.name), h('span', { class: 'muted small' }, fmtDate(d.updated_at)),
          h('button', { class: 'btn small primary', onclick: () => close(d) }, 'Load'),
          h('button', { class: 'btn small ghost danger', onclick: async (e) => { if (await confirmDialog('Delete layout?', d.name, { okLabel: 'Delete', danger: true })) { await api.del(`/api/docs/printjob/${d.id}`); e.target.closest('li').remove(); } } }, 'Delete')))),
      cancelLabel: 'Close',
    });
    if (!pick?.data) return;
    st.lines = (pick.data.lines || []).map(l => ({ ...l, id: nextId() }));
    Object.assign(P, pick.data.page || {});
    await fetchItems(st.lines.filter(l => l.item_id).map(l => l.item_id));
    st.lines = st.lines.filter(l => !l.item_id || items.has(l.item_id));
    renderSettings();
    changed(true);
    toast('Layout loaded. Change copies if you need to.');
  }

  main.append(
    h('div', { class: 'page-head' }, h('h1', null, 'Print labels'),
      h('a', { class: 'btn', href: '#/labels' }, 'Label designs'),
      h('button', { class: 'btn', onclick: loadLayout }, 'Load…'),
      h('button', { class: 'btn', onclick: saveLayout, disabled: false }, 'Save layout'),
      h('button', { class: 'btn ghost', onclick: async () => { if (!st.lines.length || await confirmDialog('Clear the list?', 'All lines will be removed from this print job.', { okLabel: 'Clear' })) { st.lines = []; P.start = 0; renderSettings(); changed(true); } } }, 'Clear')),
    h('div', { class: 'panel', style: { marginBottom: '16px', padding: '12px 16px' } }, h('div', { class: 'row', style: { gap: '12px' } }, modeBtns), h('div', { style: { marginTop: '10px' } }, settingsBox)),
    h('div', { class: 'print-layout' },
      h('div', null,
        h('div', { class: 'addbox' }, addInput, h('button', { class: 'btn', onclick: () => addByCode(addInput.value) }, 'Add'), h('button', { class: 'btn', onclick: () => addRaw() }, '+ Code only'), h('button', { class: 'btn', onclick: () => addBlank(), title: 'Random codes to assign to items later' }, '+ Blank codes')),
        addMsg,
        linesBox),
      h('div', null,
        h('div', { class: 'row', style: { marginBottom: '8px' } }, summary, h('span', { class: 'spacer' }),
          h('button', { class: 'btn primary', onclick: doPrint }, 'Print…')),
        h('div', { class: 'small muted', style: { marginBottom: '8px' } }, 'In the print dialog: Scale ', h('strong', null, '100% / Actual size'), ', Margins ', h('strong', null, 'None'), '. Turn off “Fit to page”. Choose “Save as PDF” to make a PDF.'),
        warnBox,
        previewArea)));

  renderSettings();
  renderLines();
  setTimeout(renderPreview, 0);
  const onResize = debounce(renderPreview, 150);
  window.addEventListener('resize', onResize);
  setTimeout(() => addInput.focus(), 0);

  return {
    onScan: async (code) => { await addByCode(code); return true; },
    shortcuts: { save: () => saveLayout() },
    destroy: () => window.removeEventListener('resize', onResize),
  };
}
