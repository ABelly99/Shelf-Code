// Stock: receive, quick sell (scan = sell 1), stock count, history.

import { h, app, clear, money, moneyInput, parseMoney, toast, fmtDate, beep, confirmDialog, catPath } from '../util.js';
import { api, qs } from '../api.js';
import { categoryOptions, moneyWrap } from '../components.js';
import { reasonLabel } from './item.js';
import { go } from '../main.js';

export default async function ({ main, parts }) {
  const tab = parts[1] || 'sell';
  const tabs = [['sell', 'Quick sell'], ['in', 'Stock in'], ['count', 'Stock count'], ['history', 'History']];
  main.append(h('div', { class: 'page-head' }, h('h1', null, 'Stock')),
    h('div', { class: 'tabs' }, tabs.map(([k, l]) => h('a', { href: `#/stock/${k}`, class: k === tab ? 'on' : '' }, l))));
  const body = h('div');
  main.appendChild(body);
  if (tab === 'sell' || tab === 'in') return scanSession(body, tab);
  if (tab === 'count') return countSession(body);
  return history(body);
}

async function findItem(code) {
  const r = await api.get('/api/lookup?code=' + encodeURIComponent(code), { quiet: true });
  if (!r.item_id) return null;
  return (await api.get(`/api/items/${r.item_id}`, { quiet: true })).item;
}

// Quick sell / stock in: every scan adds a line; Apply writes them all.
function scanSession(body, mode) {
  const sell = mode === 'sell';
  const key = 'stock.session.' + mode;
  let lines = [];
  try { lines = JSON.parse(sessionStorage.getItem(key)) || []; } catch { /* */ }
  const persist = () => { try { sessionStorage.setItem(key, JSON.stringify(lines)); } catch { /* */ } };
  const input = h('input', { type: 'text', class: 'scanpad', placeholder: sell ? 'Scan items to sell…' : 'Scan items you received…', spellcheck: 'false', autocomplete: 'off', style: { width: '100%' } });
  const msg = h('div', { class: 'small', style: { minHeight: '20px', margin: '6px 0' } });
  const list = h('div');
  const totalBox = h('div');
  const note = h('input', { type: 'text', placeholder: 'Note for all (optional)', style: { width: '260px' } });

  async function add(code) {
    code = code.trim();
    if (!code) return;
    input.value = '';
    const it = await findItem(code);
    if (!it) { msg.replaceChildren(h('span', { class: 'danger-text' }, `${code}: not in the database`)); beep(false); return; }
    if (it.deleted_at) { msg.replaceChildren(h('span', { class: 'danger-text' }, `${it.name} is in the Trash`)); return; }
    const ex = lines.find(l => l.item_id === it.id);
    if (ex) ex.qty++;
    else lines.unshift({ item_id: it.id, code: it.code, name: it.name, qty: 1, price: sell ? it.price : it.cost, stock: it.quantity });
    msg.replaceChildren(h('span', { class: 'ok-text' }, `+1 ${it.name}`));
    persist();
    render();
    list.querySelector('tr')?.classList.add('flash-ok');
  }

  function render() {
    clear(list);
    clear(totalBox);
    if (!lines.length) { list.appendChild(h('p', { class: 'muted' }, sell ? 'Each scan adds one to the list. Nothing changes until you press “Sell all”.' : 'Each scan adds one to the list. Nothing changes until you press “Add to stock”.')); return; }
    list.appendChild(h('div', { class: 'table-wrap' }, h('table', { class: 't' },
      h('thead', null, h('tr', null, h('th', null, 'Code'), h('th', null, 'Item'), h('th', { class: 'num' }, 'In stock'), h('th', { class: 'num' }, 'Qty'), h('th', { class: 'num' }, sell ? 'Price each' : 'Cost each'), h('th', null, ''))),
      h('tbody', null, lines.map((l, i) => {
        const q = h('input', { type: 'number', min: 1, value: l.qty, onchange: () => { l.qty = Math.max(1, Math.round(Number(q.value) || 1)); persist(); render(); } });
        const p = h('input', { type: 'text', inputmode: 'decimal', value: moneyInput(l.price), style: { width: '90px' }, onchange: () => { const c = parseMoney(p.value); l.price = Number.isNaN(c) ? l.price : c; persist(); render(); } });
        return h('tr', null, h('td', { class: 'mono' }, l.code), h('td', null, h('a', { href: `#/item/${l.item_id}` }, l.name)),
          h('td', { class: 'num ' + (sell && l.qty > l.stock ? 'danger-text' : '') }, l.stock), h('td', { class: 'num' }, q), h('td', { class: 'num' }, p),
          h('td', null, h('button', { class: 'btn small ghost', 'aria-label': 'Remove', onclick: () => { lines.splice(i, 1); persist(); render(); } }, '×')));
      })))));
    const units = lines.reduce((a, l) => a + l.qty, 0);
    const total = lines.reduce((a, l) => a + l.qty * (l.price || 0), 0);
    totalBox.append(h('div', { class: 'row', style: { marginTop: '12px', alignItems: 'flex-end' } },
      h('div', null, h('div', { class: 'muted small' }, `${units} unit(s)`), h('div', { class: 'big-total' }, money(total))),
      h('span', { class: 'spacer' }), note,
      h('button', { class: 'btn', onclick: async () => { if (await confirmDialog('Clear the list?', 'Nothing has been changed yet.', { okLabel: 'Clear' })) { lines = []; persist(); render(); } } }, 'Clear'),
      h('button', { class: 'btn primary', onclick: apply }, sell ? 'Sell all' : 'Add to stock')));
  }

  async function apply() {
    try {
      await api.post('/api/stock/batch', { moves: lines.map(l => ({ item_id: l.item_id, change: sell ? -l.qty : l.qty, reason: sell ? 'sold' : 'received', price_each: l.price, note: note.value })) });
      toast(sell ? `Sold ${lines.reduce((a, l) => a + l.qty, 0)} unit(s)` : 'Stock updated');
      lines = []; note.value = ''; persist(); render();
      msg.replaceChildren();
    } catch (e) { toast(e.message, { error: true }); }
  }

  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); add(input.value); } });
  body.append(h('div', { class: 'panel', style: { marginBottom: '16px' } }, input, msg, h('p', { class: 'muted small', style: { margin: 0 } }, 'Scan with a USB scanner or your phone. Codes not in the database are ignored.')), list, totalBox);
  render();
  setTimeout(() => input.focus(), 0);
  return { onScan: async (code) => { await add(code); return true; }, dirty: () => lines.length > 0 };
}

// Stock count: scan everything on the shelf, then compare with what the database expects.
function countSession(body) {
  const key = 'stock.count';
  let st = { cat: '', location: '', counts: {}, started: false, names: {} };
  try { st = { ...st, ...JSON.parse(sessionStorage.getItem(key)) }; } catch { /* */ }
  const persist = () => { try { sessionStorage.setItem(key, JSON.stringify(st)); } catch { /* */ } };

  const catSel = h('select', null, categoryOptions(Number(st.cat) || null, { none: 'All categories' }));
  const locIn = h('input', { type: 'text', placeholder: 'Any location', value: st.location });
  const input = h('input', { type: 'text', class: 'scanpad', placeholder: 'Scan every item…', spellcheck: 'false', autocomplete: 'off', style: { width: '100%' } });
  const msg = h('div', { class: 'small', style: { minHeight: '20px', margin: '6px 0' } });
  const out = h('div');

  async function scan(code) {
    code = code.trim(); input.value = '';
    if (!code) return;
    const it = await findItem(code);
    if (!it) { msg.replaceChildren(h('span', { class: 'danger-text' }, `${code}: not in the database`)); beep(false); return; }
    st.counts[it.id] = (st.counts[it.id] || 0) + 1;
    st.names[it.id] = { name: it.name, code: it.code, qty: it.quantity };
    persist();
    msg.replaceChildren(h('span', { class: 'ok-text' }, `${it.name}: counted ${st.counts[it.id]}`));
    renderCounted();
  }

  function renderCounted() {
    const n = Object.values(st.counts).reduce((a, b) => a + b, 0);
    clear(out).append(h('div', { class: 'row' }, h('strong', null, `${n} scan(s), ${Object.keys(st.counts).length} different item(s)`), h('span', { class: 'spacer' }),
      h('button', { class: 'btn', onclick: async () => { if (await confirmDialog('Start over?', 'All counts in this session will be cleared.', { okLabel: 'Start over' })) { st.counts = {}; st.names = {}; persist(); renderCounted(); } } }, 'Start over'),
      h('button', { class: 'btn primary', onclick: finish }, 'Finish & compare')));
  }

  async function finish() {
    st.cat = catSel.value; st.location = locIn.value.trim(); persist();
    const f = st.location ? [{ k: 'location', op: 'in', v: [st.location] }] : [];
    const expected = (await api.get('/api/items' + qs({ category: st.cat, f, limit: 5000 }))).items;
    const rows = [];
    const seen = new Set();
    for (const it of expected) {
      seen.add(it.id);
      const counted = st.counts[it.id] || 0;
      if (counted !== it.quantity) rows.push({ id: it.id, name: it.name, code: it.code, expected: it.quantity, counted });
    }
    for (const [id, c] of Object.entries(st.counts)) {
      if (seen.has(Number(id))) continue;
      const n = st.names[id];
      rows.push({ id: Number(id), name: n.name, code: n.code, expected: n.qty, counted: c, extra: true });
    }
    clear(out);
    if (!rows.length) { out.appendChild(h('div', { class: 'panel' }, h('h2', { class: 'ok-text' }, 'Everything matches ✓'), h('p', null, `${expected.length} item(s) checked.`))); return; }
    const checks = rows.map(r => ({ r, cb: h('input', { type: 'checkbox', checked: true }) }));
    out.append(h('h2', null, `${rows.length} difference(s)`),
      h('div', { class: 'table-wrap' }, h('table', { class: 't' },
        h('thead', null, h('tr', null, h('th', null, ''), h('th', null, 'Code'), h('th', null, 'Item'), h('th', { class: 'num' }, 'Expected'), h('th', { class: 'num' }, 'Counted'), h('th', null, ''))),
        h('tbody', null, checks.map(({ r, cb }) => h('tr', null, h('td', { class: 'chk' }, cb), h('td', { class: 'mono' }, r.code), h('td', null, h('a', { href: `#/item/${r.id}` }, r.name)),
          h('td', { class: 'num' }, r.expected), h('td', { class: 'num ' + (r.counted < r.expected ? 'danger-text' : 'ok-text') }, r.counted),
          h('td', { class: 'small muted' }, r.counted === 0 ? 'Missing' : r.extra ? 'Not in this category/location' : r.counted > r.expected ? 'Extra' : 'Short')))))),
      h('div', { class: 'row', style: { marginTop: '12px' } }, h('span', { class: 'muted small' }, 'Apply sets the quantity of the ticked items to what you counted.'), h('span', { class: 'spacer' }),
        h('button', { class: 'btn', onclick: renderCounted }, 'Back to counting'),
        h('button', { class: 'btn primary', onclick: async () => {
          const moves = checks.filter(c => c.cb.checked).map(({ r }) => ({ item_id: r.id, set_to: r.counted, reason: 'count', note: 'Stock count' }));
          await api.post('/api/stock/batch', { moves });
          toast(`${moves.length} quantity correction(s) saved`);
          st.counts = {}; st.names = {}; persist(); renderCounted();
        } }, 'Apply corrections')));
  }

  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); scan(input.value); } });
  body.append(h('div', { class: 'panel', style: { marginBottom: '16px' } },
    h('div', { class: 'row', style: { marginBottom: '10px' } }, h('label', null, 'Count: '), catSel, locIn),
    input, msg, h('p', { class: 'muted small', style: { margin: 0 } }, 'Scan each unit once. When done, “Finish & compare” shows what is missing or extra.')), out);
  renderCounted();
  setTimeout(() => input.focus(), 0);
  return { onScan: async (code) => { await scan(code); return true; } };
}

async function history(body) {
  const reason = h('select', { onchange: () => load() }, h('option', { value: '' }, 'All changes'), ['sold', 'received', 'returned', 'damaged', 'adjustment', 'count', 'initial'].map(r => h('option', { value: r }, reasonLabel(r))));
  const box = h('div');
  body.append(h('div', { class: 'toolbar' }, reason), box);
  async function load() {
    const { moves } = await api.get('/api/stock' + qs({ reason: reason.value, limit: 500 }));
    clear(box).appendChild(moves.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
      h('thead', null, h('tr', null, h('th', null, 'When'), h('th', null, 'Code'), h('th', null, 'Item'), h('th', { class: 'num' }, 'Change'), h('th', null, 'Reason'), h('th', { class: 'num' }, 'Price each'), h('th', null, 'Note'))),
      h('tbody', null, moves.map(m => h('tr', { class: 'click', onclick: () => go(`#/item/${m.item_id}`) }, h('td', null, fmtDate(m.created_at, true)), h('td', { class: 'mono' }, m.code), h('td', null, m.name),
        h('td', { class: 'num ' + (m.change > 0 ? 'ok-text' : 'danger-text') }, (m.change > 0 ? '+' : '') + m.change), h('td', null, reasonLabel(m.reason)), h('td', { class: 'num' }, money(m.price_each)), h('td', { class: 'wrap' }, m.note))))))
      : h('p', { class: 'muted' }, 'No stock changes yet.'));
  }
  await load();
  return {};
}
