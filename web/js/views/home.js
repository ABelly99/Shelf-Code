import { h, app, money, relTime } from '../util.js';
import { api } from '../api.js';
import { reasonLabel } from './item.js';

export default async function ({ main }) {
  const d = await api.get('/api/dashboard');
  const s = d.scanner || {};
  const scannerText = s.status === 'connected' ? (s.phones > 0 ? `Phone scanner ready (${s.phones} phone${s.phones > 1 ? 's' : ''})` : 'Scanner connected, waiting for phone')
    : s.status === 'connecting' ? 'Phone scanner connecting…' : s.status === 'error' ? 'Phone scanner: ' + s.message : 'Phone scanner not connected';
  const urls = app.info?.urls;

  main.append(
    h('div', { class: 'page-head' }, h('h1', null, app.settings.store_name || 'Home'),
      h('a', { class: 'btn', href: '#/print' }, 'Print labels'), h('a', { class: 'btn primary', href: '#/new' }, '+ New item')),
    h('div', { class: 'stat-row' },
      h('a', { class: 'stat', href: '#/items' }, h('div', { class: 'v' }, d.items), h('div', { class: 'l' }, 'Items')),
      h('div', { class: 'stat' }, h('div', { class: 'v' }, d.units), h('div', { class: 'l' }, 'Units in stock')),
      h('div', { class: 'stat' }, h('div', { class: 'v' }, money(d.retail)), h('div', { class: 'l' }, 'Stock value (retail)')),
      app.settings.hide_cost ? null : h('div', { class: 'stat' }, h('div', { class: 'v' }, money(d.cost)), h('div', { class: 'l' }, 'Stock value (cost)')),
      h('a', { class: 'stat' + (d.low ? ' warn' : ''), href: '#/items?low=1' }, h('div', { class: 'v' }, d.low), h('div', { class: 'l' }, 'Low stock')),
      h('a', { class: 'stat', href: '#/stock/history' }, h('div', { class: 'v' }, d.sold_week), h('div', { class: 'l' }, `Sold this week · ${money(d.sales_week)}`))),
    h('div', { class: 'grid2', style: { alignItems: 'start' } },
      h('div', { class: 'panel' }, h('h2', null, 'Recently added'),
        d.recent.length ? h('ul', { class: 'list-plain' }, d.recent.map(r => h('li', null,
          r.thumb ? h('img', { class: 'thumb', src: '/img/' + r.thumb, alt: '', style: { width: '32px', height: '32px', objectFit: 'cover', borderRadius: '3px' } }) : h('span', { style: { width: '32px', height: '32px', background: 'var(--surface-2)', borderRadius: '3px' } }),
          h('a', { class: 'grow', href: `#/item/${r.id}` }, r.name), h('span', { class: 'mono small muted' }, r.code), h('span', { class: 'num' }, money(r.price)))))
          : h('div', { class: 'empty', style: { padding: '24px' } }, h('p', null, 'No items yet.'), h('a', { class: 'btn primary', href: '#/new' }, '+ New item'))),
      h('div', { class: 'col', style: { gap: '16px' } },
        h('div', { class: 'panel' }, h('h2', null, 'Scanner'),
          h('p', null, h('span', { class: 'dot', style: { background: s.status === 'connected' ? (s.phones ? 'var(--success)' : 'var(--accent)') : s.status === 'error' ? 'var(--danger)' : 'var(--border-strong)', marginRight: '6px' } }), scannerText),
          h('p', { class: 'muted small' }, 'Scan anywhere in SHELFCODE: if the code belongs to an item, it opens. USB scanners work too.'),
          h('a', { class: 'btn small', href: '#/settings/scanner' }, 'Scanner settings')),
        d.low_items.length ? h('div', { class: 'panel' }, h('h2', null, 'Low stock'), h('ul', { class: 'list-plain' }, d.low_items.map(r => h('li', null,
          h('a', { class: 'grow', href: `#/item/${r.id}` }, r.name), h('span', { class: 'badge warning' }, `${r.quantity} left`))))) : null,
        d.recent_sold.length ? h('div', { class: 'panel' }, h('h2', null, 'Recently sold'), h('ul', { class: 'list-plain' }, d.recent_sold.map(r => h('li', null,
          h('a', { class: 'grow', href: `#/item/${r.id}` }, r.name), h('span', { class: 'muted small' }, `${r.qty} × ${money(r.price_each, { blank: '—' })}`), h('span', { class: 'muted small' }, relTime(r.created_at)))))) : null,
        urls ? h('div', { class: 'panel' }, h('h2', null, 'Open on other devices'),
          h('p', { class: 'small muted' }, 'Phones and tablets on the same Wi-Fi can open:'),
          urls.lan.length ? urls.lan.map(u => h('div', { class: 'mono' }, u.url)) : h('p', { class: 'muted small' }, 'No network found.'),
          app.settings.lan_enabled === false ? h('p', { class: 'warn-text small' }, 'Turned off in Settings → Network.') : null) : null)));
  return {};
}
