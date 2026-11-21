import { h, app, clear, money, fmtDate, statusInfo, catPath } from '../util.js';
import { api, qs } from '../api.js';
import { describeDetails } from './item.js';
import { printHTML } from '../labelkit.js';

export default async function ({ main, parts }) {
  const tab = parts[1] || 'overview';
  main.append(h('div', { class: 'page-head' }, h('h1', null, 'Reports'), tab === 'overview' ? h('button', { class: 'btn', onclick: printCatalog }, 'Print price list…') : null),
    h('div', { class: 'tabs' }, [['overview', 'Overview'], ['activity', 'Activity log']].map(([k, l]) => h('a', { href: `#/reports/${k}`, class: k === tab ? 'on' : '' }, l))));
  const body = h('div');
  main.appendChild(body);
  if (tab === 'activity') return activity(body);

  const days = h('select', { onchange: () => load() }, [7, 30, 90, 365].map(d => h('option', { value: d, selected: d === 30 }, `Last ${d} days`)));
  body.append(h('div', { class: 'toolbar' }, days));
  const out = h('div');
  body.append(out);
  async function load() {
    const r = await api.get('/api/reports?days=' + days.value);
    const totalRev = r.sales_by_day.reduce((a, d) => a + d.revenue, 0);
    const totalUnits = r.sales_by_day.reduce((a, d) => a + d.units, 0);
    const maxRev = Math.max(1, ...r.sales_by_day.map(d => d.revenue));
    const tbl = (head, rows) => h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, head.map(([l, n]) => h('th', { class: n ? 'num' : '' }, l)))), h('tbody', null, rows)));
    clear(out).append(
      h('div', { class: 'stat-row' },
        h('div', { class: 'stat' }, h('div', { class: 'v' }, totalUnits), h('div', { class: 'l' }, 'Units sold')),
        h('div', { class: 'stat' }, h('div', { class: 'v' }, money(totalRev)), h('div', { class: 'l' }, 'Sales (where a price was entered)'))),
      r.sales_by_day.length ? h('div', { class: 'panel', style: { marginBottom: '16px' } }, h('h2', null, 'Sales by day'),
        h('div', { style: { display: 'flex', alignItems: 'flex-end', gap: '2px', height: '120px', borderBottom: '1px solid var(--border)' } },
          r.sales_by_day.map(d => h('div', { title: `${fmtDate(d.day)}: ${d.units} unit(s), ${money(d.revenue)}`, style: { flex: '1', maxWidth: '28px', background: 'var(--accent)', height: Math.max(2, d.revenue / maxRev * 116) + 'px', borderRadius: '2px 2px 0 0' } })))) : null,
      h('div', { class: 'grid2', style: { alignItems: 'start' } },
        h('div', null, h('h2', null, 'By category'), tbl([['Category'], ['Items', 1], ['Units', 1], ['Retail value', 1], ...(app.settings.hide_cost ? [] : [['Cost value', 1]])],
          r.by_category.map(c => h('tr', null, h('td', null, c.category), h('td', { class: 'num' }, c.items), h('td', { class: 'num' }, c.units), h('td', { class: 'num' }, money(c.retail)), app.settings.hide_cost ? null : h('td', { class: 'num' }, money(c.cost)))))),
        h('div', null, h('h2', null, 'Top sellers'), r.top_sellers.length ? tbl([['Item'], ['Sold', 1], ['Revenue', 1]],
          r.top_sellers.map(t => h('tr', null, h('td', null, h('a', { href: `#/item/${t.id}` }, t.name)), h('td', { class: 'num' }, t.sold), h('td', { class: 'num' }, money(t.revenue))))) : h('p', { class: 'muted' }, 'No sales in this period.')),
        h('div', null, h('h2', null, 'By status'), tbl([['Status'], ['Items', 1]], r.by_status.map(s => h('tr', null, h('td', null, statusInfo(s.status).label), h('td', { class: 'num' }, s.items))))),
        h('div', null, h('h2', null, 'Oldest unsold stock'), r.aging.length ? tbl([['Item'], ['Added'], ['Qty', 1], ['Price', 1]],
          r.aging.map(a => h('tr', null, h('td', null, h('a', { href: `#/item/${a.id}` }, a.name)), h('td', null, fmtDate(a.created_at)), h('td', { class: 'num' }, a.quantity), h('td', { class: 'num' }, money(a.price))))) : h('p', { class: 'muted' }, 'Nothing in stock.'))));
  }
  await load();
  return {};
}

async function activity(body) {
  const { activity } = await api.get('/api/activity?limit=500');
  body.appendChild(activity.length ? h('ul', { class: 'list-plain panel' }, activity.map(a => h('li', { style: { alignItems: 'flex-start' } },
    h('span', { class: 'muted small nowrap', style: { width: '150px', flexShrink: 0 } }, fmtDate(a.created_at, true)),
    h('div', { class: 'grow' }, h('strong', null, a.action[0].toUpperCase() + a.action.slice(1)), ' ',
      a.item_id ? h('a', { href: `#/item/${a.item_id}` }, a.name || a.code || `#${a.item_id}`) : a.action === 'imported' ? h('span', { class: 'muted' }, `${a.details.created} new, ${a.details.updated} updated`) : null,
      describeDetails(a)))))
    : h('p', { class: 'muted' }, 'Nothing yet.'));
  return {};
}

async function printCatalog() {
  const { items } = await api.get('/api/items' + qs({ limit: 5000, sort: [{ k: 'category', dir: 'asc' }, { k: 'name', dir: 'asc' }] }));
  const node = h('div', null,
    h('h1', { style: { fontSize: '16pt' } }, (app.settings.store_name || 'Price list') + ' — ' + fmtDate(new Date().toISOString())),
    h('table', { style: { width: '100%', borderCollapse: 'collapse', fontSize: '9pt' } },
      h('thead', null, h('tr', null, ['', 'Code', 'Name', 'Category', 'Qty', 'Price'].map(t => h('th', { style: { textAlign: 'left', borderBottom: '1px solid #000', padding: '3px' } }, t)))),
      h('tbody', null, items.map(it => h('tr', null,
        h('td', { style: { padding: '2px 3px', borderBottom: '1px solid #ddd', width: '12mm' } }, it.thumb ? h('img', { src: it.thumb, style: { width: '10mm', height: '10mm', objectFit: 'cover' } }) : ''),
        h('td', { style: { padding: '2px 3px', borderBottom: '1px solid #ddd', fontFamily: 'monospace' } }, it.code),
        h('td', { style: { padding: '2px 3px', borderBottom: '1px solid #ddd' } }, it.name),
        h('td', { style: { padding: '2px 3px', borderBottom: '1px solid #ddd' } }, it.category_id ? catPath(it.category_id) : ''),
        h('td', { style: { padding: '2px 3px', borderBottom: '1px solid #ddd', textAlign: 'right' } }, it.quantity),
        h('td', { style: { padding: '2px 3px', borderBottom: '1px solid #ddd', textAlign: 'right' } }, money(it.price)))))));
  printHTML(node);
}
