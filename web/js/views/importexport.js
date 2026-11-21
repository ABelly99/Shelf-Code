import { h, app, clear, toast, download, FIELD_TYPES } from '../util.js';
import { api, qs } from '../api.js';
import { categoryOptions } from '../components.js';
import { refreshMeta } from '../main.js';

const TARGETS = [['', '— ignore —'], ['code', 'Code'], ['name', 'Name'], ['category', 'Category (by name)'], ['price', 'Price'], ['cost', 'Cost'], ['quantity', 'Quantity'],
  ['low_stock_at', 'Low stock alert'], ['status', 'Status'], ['location', 'Location'], ['supplier', 'Supplier'], ['tags', 'Tags (; separated)'], ['description', 'Description'],
  ['notes', 'Notes'], ['extra_codes', 'Other codes (kind:code;…)'], ['barcode_format', 'Barcode format']];

export default async function ({ main }) {
  const catSel = h('select', null, categoryOptions(null, { none: 'All items' }));
  const excel = h('input', { type: 'checkbox', checked: true });
  main.append(
    h('div', { class: 'page-head' }, h('h1', null, 'Import / Export')),
    h('div', { class: 'grid2', style: { alignItems: 'start' } },
      h('div', { class: 'panel' }, h('h2', null, 'Export'),
        h('div', { class: 'col' },
          h('div', { class: 'row' }, h('label', null, 'Items: '), catSel),
          h('label', { class: 'check' }, excel, 'Keep leading zeros when opened in Excel (codes like 062600629487)'),
          h('div', { class: 'row' },
            h('button', { class: 'btn primary', onclick: () => download('/api/export' + qs({ category: catSel.value, format: 'csv', excel: excel.checked ? 1 : 0 })) }, 'Download CSV'),
            h('button', { class: 'btn', onclick: () => download('/api/export' + qs({ category: catSel.value, format: 'json' })) }, 'Download JSON (everything)')),
          h('p', { class: 'muted small' }, 'For a full copy with photos, use Settings → Backup.'))),
      h('div', { class: 'panel' }, h('h2', null, 'Import a CSV file'), importer())));
  return {};
}

function importer() {
  const box = h('div', { class: 'col' });
  const file = h('input', { type: 'file', accept: '.csv,.txt,.tsv,text/csv' });
  const target = h('select', null, categoryOptions(null, { none: '(no category — or use a Category column)' }));
  const mode = h('select', null, h('option', { value: 'both' }, 'Add new items and update existing ones (matched by code)'), h('option', { value: 'create' }, 'Only add new items (skip codes that exist)'), h('option', { value: 'update' }, 'Only update existing items'));
  const step2 = h('div');
  let csv = '', preview = null;

  file.addEventListener('change', async () => {
    const f = file.files[0];
    if (!f) return;
    csv = await f.text();
    try { preview = await api.post('/api/import/preview', { csv }); }
    catch (e) { toast(e.message, { error: true }); return; }
    renderMapping();
  });

  function renderMapping() {
    clear(step2);
    const fieldOpts = [];
    const seen = new Set();
    for (const f of app.fields) { if (seen.has(f.key)) continue; seen.add(f.key); fieldOpts.push(['custom.' + f.key, `Field: ${f.label}`]); }
    const sels = preview.headers.map((hd, i) => {
      const sug = preview.suggest[i];
      const s = h('select', null, TARGETS.map(([v, l]) => h('option', { value: v, selected: sug === v }, l)), fieldOpts.map(([v, l]) => h('option', { value: v, selected: sug === v }, l)),
        h('option', { value: 'new:' + hd, selected: sug.startsWith('new:') }, `New text field “${hd}”`));
      return s;
    });
    const result = h('div');
    step2.append(
      h('p', null, h('strong', null, `${preview.rows} row(s).`), ' Match each column to a field:'),
      h('div', { class: 'table-wrap', style: { maxHeight: '420px' } }, h('table', { class: 't' },
        h('thead', null, h('tr', null, h('th', null, 'Column in your file'), h('th', null, 'Goes into'), h('th', null, 'First values'))),
        h('tbody', null, preview.headers.map((hd, i) => h('tr', null, h('td', null, h('strong', null, hd)), h('td', null, sels[i]),
          h('td', { class: 'muted small', style: { maxWidth: '260px' } }, preview.sample.slice(0, 3).map(r => r[i] ?? '').filter(Boolean).join(' · '))))))),
      h('div', { class: 'grid2', style: { marginTop: '10px' } },
        h('div', { class: 'field' }, h('label', null, 'Put new items in'), target),
        h('div', { class: 'field' }, h('label', null, 'Mode'), mode)),
      h('p', { class: 'muted small' }, 'Rows without a code get a generated one. Codes are kept exactly as written (leading zeros too). A backup is made before importing.'),
      h('div', { class: 'row' },
        h('button', { class: 'btn', onclick: () => run(true) }, 'Check (no changes)'),
        h('button', { class: 'btn primary', onclick: () => run(false) }, 'Import')),
      result);

    async function run(dry) {
      const mapping = sels.map(s => s.value);
      if (!mapping.includes('name') && mode.value !== 'update') { toast('Pick which column is the Name.', { error: true }); return; }
      try {
        const r = await api.post('/api/import/run', { csv, mapping, mode: mode.value, category_id: Number(target.value) || null, dry_run: dry });
        clear(result).append(
          h('div', { class: 'panel', style: { marginTop: '12px', background: r.errors.length ? 'var(--warning-soft)' : 'var(--success-soft)' } },
            h('strong', null, dry ? 'Check result: ' : 'Imported: '), `${r.created} new, ${r.updated} updated, ${r.skipped} skipped, ${r.errors.length} with problems.`,
            r.errors.length ? h('ul', { class: 'small', style: { margin: '8px 0 0', paddingLeft: '18px', maxHeight: '200px', overflow: 'auto' } }, r.errors.map(e => h('li', null, `Row ${e.row}: ${e.error}`))) : null));
        if (!dry) { await refreshMeta(['categories', 'fields']); toast('Import finished'); }
      } catch (e) { toast(e.message, { error: true }); }
    }
  }

  box.append(h('p', { class: 'muted small' }, 'Excel: File → Save As → CSV UTF-8. The first row must be column names.'), file, step2);
  return box;
}
