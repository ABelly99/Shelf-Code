import { h, app, clear, money, fmtDate, toast, confirmDialog } from '../util.js';
import { api } from '../api.js';
import { refreshMeta, refreshCounts } from '../main.js';

export default async function ({ main }) {
  const box = h('div');
  main.append(h('div', { class: 'page-head' }, h('h1', null, 'Trash'), h('button', { class: 'btn danger', onclick: emptyAll }, 'Empty Trash')),
    h('p', { class: 'muted' }, `Deleted items stay here for ${app.settings.trash_days || 30} days, then they are removed for good. Their codes stay reserved until then.`), box);
  async function load() {
    const { items } = await api.get('/api/items?trash=1&limit=1000&sort=' + encodeURIComponent(JSON.stringify([{ k: 'deleted_at', dir: 'desc' }])));
    clear(box);
    if (!items.length) { box.appendChild(h('div', { class: 'panel empty' }, h('p', null, 'The Trash is empty.'))); return; }
    box.appendChild(h('div', { class: 'table-wrap' }, h('table', { class: 't' },
      h('thead', null, h('tr', null, h('th', null, 'Code'), h('th', null, 'Name'), h('th', { class: 'num' }, 'Price'), h('th', null, 'Deleted'), h('th', null, ''))),
      h('tbody', null, items.map(it => h('tr', null,
        h('td', { class: 'mono' }, it.code), h('td', null, h('a', { href: `#/item/${it.id}` }, it.name)), h('td', { class: 'num' }, money(it.price)), h('td', null, fmtDate(it.deleted_at, true)),
        h('td', { class: 'row nowrap', style: { justifyContent: 'flex-end' } },
          h('button', { class: 'btn small', onclick: async () => { await api.post(`/api/items/${it.id}/restore`); toast(`Restored “${it.name}”`); refreshMeta(['categories']); refreshCounts(); load(); } }, 'Restore'),
          h('button', { class: 'btn small ghost danger', onclick: async () => {
            if (!(await confirmDialog('Delete forever?', `“${it.name}” and its photos and history will be removed. This can’t be undone.`, { okLabel: 'Delete forever', danger: true }))) return;
            await api.del(`/api/items/${it.id}?permanent=1`); refreshCounts(); load();
          } }, 'Delete forever'))))))));
  }
  async function emptyAll() {
    if (!(await confirmDialog('Empty the Trash?', 'All items in the Trash, with their photos and history, will be removed for good. This can’t be undone.', { okLabel: 'Empty Trash', danger: true }))) return;
    const r = await api.post('/api/trash/empty');
    toast(`${r.purged} item(s) removed`);
    refreshCounts();
    load();
  }
  await load();
  return {};
}
