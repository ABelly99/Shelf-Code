// Label designs: size + a simple stack of elements (text lines, barcode, photo).

import { h, app, clear, toast, confirmDialog, promptDialog, debounce, fieldsFor } from '../util.js';
import { api, qs } from '../api.js';
import { FORMATS } from '../barcode.js';
import { renderLabel, labelData, TEXT_SOURCES } from '../labelkit.js';
import { go, refreshMeta } from '../main.js';

export default async function ({ main, parts }) {
  await refreshMeta(['labels']);
  let current = parts[1] ? app.labels.find(l => l.id === Number(parts[1])) : app.labels[0];
  let draft = current ? structuredClone(current) : null;
  let dirty = false;
  let sample = null;
  try { sample = (await api.get('/api/items' + qs({ limit: 1 }))).items[0] || null; } catch { /* */ }

  const listBox = h('div', { class: 'panel flush' });
  const editor = h('div');
  main.append(
    h('div', { class: 'page-head' }, h('div', { class: 'crumbs' }, h('a', { href: '#/print' }, 'Print labels'), ' › Label designs'), h('span', { class: 'spacer' }),
      h('button', { class: 'btn', onclick: () => create() }, '+ New design')),
    h('div', { class: 'cat-layout', style: { gridTemplateColumns: '240px minmax(0,1fr)' } }, listBox, editor));

  function renderList() {
    clear(listBox).append(h('div', { class: 'panel-head' }, h('h2', null, 'Designs')),
      h('div', { style: { padding: '6px' }, class: 'cat-tree' }, app.labels.map(l => h('a', { href: '#', class: draft?.id === l.id ? 'on' : '', onclick: async (e) => {
        e.preventDefault();
        if (dirty && !(await confirmDialog('Discard changes?', 'Your changes to this design are not saved.', { okLabel: 'Discard', danger: true }))) return;
        current = l; draft = structuredClone(l); dirty = false; renderList(); renderEditor();
      } }, h('span', { class: 'name' }, l.name), h('span', { class: 'count' }, `${l.data.w}×${l.data.h}`)))));
  }

  const markDirty = () => { dirty = true; updatePreview(); };

  const previewBox = h('div', { style: { background: 'var(--surface-2)', padding: '24px', borderRadius: '4px', display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '200px', overflow: 'auto' } });
  const warn = h('div', { class: 'small warn-text' });
  const updatePreview = debounce(() => {
    if (!draft) return;
    const d = sample ? labelData({}, sample) : { code: 'SC-000123', format: 'code128', name: 'Sample item name', price: 1999, category: 'Category', custom: {}, item: null, thumb: '' };
    const scale = Math.min(3, 300 / (draft.data.w * 96 / 25.4));
    const r = renderLabel(draft, d, draft.data.w, draft.data.h, { cut: true, preview: true });
    r.el.style.position = 'relative';
    r.el.style.transform = `scale(${scale})`;
    r.el.style.transformOrigin = 'center';
    r.el.style.boxShadow = '0 1px 4px rgba(0,0,0,.25)';
    r.el.style.margin = `${(draft.data.h * 96 / 25.4) * (scale - 1) / 2}px ${(draft.data.w * 96 / 25.4) * (scale - 1) / 2}px`;
    previewBox.replaceChildren(r.el);
    warn.replaceChildren(...r.warnings.map(w => h('div', null, '⚠ ' + w)));
  }, 50);

  function sourceOptions(selected) {
    const opts = [...TEXT_SOURCES];
    const seen = new Set();
    for (const f of app.fields) {
      if (seen.has(f.key) || f.type === 'long_text') continue;
      seen.add(f.key);
      opts.push([`field:${f.key}`, `Field: ${f.label}`]);
    }
    if (selected && !opts.some(o => o[0] === selected)) opts.push([selected, selected]);
    return opts.map(([v, l]) => h('option', { value: v, selected: v === selected }, l));
  }

  function renderEditor() {
    clear(editor);
    if (!draft) { editor.appendChild(h('div', { class: 'panel empty' }, h('p', null, 'No label designs yet.'), h('button', { class: 'btn primary', onclick: () => create() }, '+ New design'))); return; }
    const d = draft.data;
    d.elements = d.elements || [];
    const name = h('input', { type: 'text', value: draft.name, oninput: () => { draft.name = name.value; dirty = true; } });
    const num = (obj, key, step = 0.5, ph = '') => { const i = h('input', { type: 'number', step, min: 0, value: obj[key] ?? '', placeholder: ph, style: { width: '80px' }, oninput: () => { obj[key] = Number(i.value) || 0; markDirty(); } }); return i; };
    const fmt = h('select', { onchange: () => { d.format = fmt.value; markDirty(); } }, h('option', { value: '' }, 'Each item’s own format'), Object.entries(FORMATS).map(([k, f]) => h('option', { value: k, selected: d.format === k }, f.label)));

    const elList = h('ul', { class: 'field-list el-list' });
    const renderEls = () => {
      clear(elList);
      d.elements.forEach((el, i) => {
        const move = (dir) => { const j = i + dir; if (j < 0 || j >= d.elements.length) return; [d.elements[i], d.elements[j]] = [d.elements[j], d.elements[i]]; renderEls(); markDirty(); };
        const remove = () => { d.elements.splice(i, 1); renderEls(); markDirty(); };
        let body;
        if (el.type === 'text') {
          const src = h('select', { onchange: () => { el.source = src.value; renderEls(); markDirty(); } }, sourceOptions(el.source));
          const size = h('input', { type: 'number', min: 4, max: 72, step: 0.5, value: el.size ?? 8, title: 'Font size (pt)', oninput: () => { el.size = Number(size.value) || 8; markDirty(); } });
          const bold = h('label', { class: 'check small' }, h('input', { type: 'checkbox', checked: !!el.bold, onchange: (e) => { el.bold = e.target.checked; markDirty(); } }), 'Bold');
          const align = h('select', { onchange: () => { el.align = align.value; markDirty(); } }, ['left', 'center', 'right'].map(a => h('option', { value: a, selected: (el.align || 'center') === a }, a[0].toUpperCase() + a.slice(1))));
          const lines = h('select', { title: 'Max lines', onchange: () => { el.lines = Number(lines.value); markDirty(); } }, [1, 2, 3, 4].map(n => h('option', { value: n, selected: (el.lines || 1) === n }, `${n} line${n > 1 ? 's' : ''}`)));
          const prefix = h('input', { type: 'text', placeholder: 'Before', value: el.prefix || '', style: { width: '70px' }, oninput: () => { el.prefix = prefix.value; markDirty(); } });
          const suffix = h('input', { type: 'text', placeholder: 'After', value: el.suffix || '', style: { width: '70px' }, oninput: () => { el.suffix = suffix.value; markDirty(); } });
          const text = el.source === 'static' ? h('input', { type: 'text', placeholder: 'Text', value: el.text || '', oninput: (e) => { el.text = e.target.value; markDirty(); } }) : null;
          body = h('div', null, h('strong', null, 'Text'), h('div', { class: 'el-row' }, src, text, size, h('span', { class: 'small muted' }, 'pt'), bold), h('div', { class: 'el-row' }, align, lines, prefix, suffix));
        } else if (el.type === 'barcode') {
          const showText = h('label', { class: 'check small' }, h('input', { type: 'checkbox', checked: el.show_text !== false, onchange: (e) => { el.show_text = e.target.checked; markDirty(); } }), 'Show code under barcode');
          const f = h('select', { onchange: () => { el.format = f.value; markDirty(); } }, h('option', { value: '' }, 'Format: as above'), Object.entries(FORMATS).map(([k, x]) => h('option', { value: k, selected: el.format === k }, x.label)));
          body = h('div', null, h('strong', null, 'Barcode'), h('span', { class: 'muted small' }, ' — fills the space left over'), h('div', { class: 'el-row' }, showText, f));
        } else {
          const ht = h('input', { type: 'number', min: 3, step: 1, value: el.height ?? 10, oninput: (e) => { el.height = Number(e.target.value) || 10; markDirty(); } });
          body = h('div', null, h('strong', null, 'Photo'), h('div', { class: 'el-row' }, 'Height ', ht, ' mm'));
        }
        elList.appendChild(h('li', null,
          h('div', { class: 'col', style: { gap: '0' } }, h('button', { class: 'btn small ghost', 'aria-label': 'Move up', onclick: () => move(-1) }, '▲'), h('button', { class: 'btn small ghost', 'aria-label': 'Move down', onclick: () => move(1) }, '▼')),
          body,
          h('button', { class: 'btn small ghost', 'aria-label': 'Remove', onclick: remove }, '×')));
      });
    };
    renderEls();

    const presets = [[25, 15], [38, 25], [50, 25], [50, 30], [57, 32], [62, 29], [70, 40], [90, 35], [100, 50], [101.6, 152.4]];
    editor.append(
      h('div', { class: 'label-designer' },
        h('div', { class: 'col', style: { gap: '16px' } },
          h('div', { class: 'panel' },
            h('div', { class: 'grid2' },
              h('div', { class: 'field span2' }, h('label', null, 'Name'), name),
              h('div', { class: 'field' }, h('label', null, 'Size (mm)'), h('div', { class: 'row nowrap' }, num(d, 'w'), '×', num(d, 'h'))),
              h('div', { class: 'field' }, h('label', null, 'Inner padding (mm)'), num(d, 'pad', 0.25, '1.5')),
              h('div', { class: 'field span2' }, h('label', null, 'Common sizes'), h('div', { class: 'chips' }, presets.map(([w, hh]) => h('span', { class: 'chip pick', onclick: () => { d.w = w; d.h = hh; renderEditor(); markDirty(); } }, `${w}×${hh}`)))),
              h('div', { class: 'field span2' }, h('label', null, 'Barcode format'), fmt))),
          h('div', { class: 'panel' },
            h('div', { class: 'row', style: { marginBottom: '8px' } }, h('h2', { style: { margin: 0 } }, 'Contents (top to bottom)'), h('span', { class: 'spacer' }),
              h('button', { class: 'btn small', onclick: () => { d.elements.push({ type: 'text', source: 'name', size: 8, bold: true, lines: 1, align: 'center' }); renderEls(); markDirty(); } }, '+ Text'),
              h('button', { class: 'btn small', onclick: () => { d.elements.push({ type: 'barcode', show_text: true }); renderEls(); markDirty(); } }, '+ Barcode'),
              h('button', { class: 'btn small', onclick: () => { d.elements.push({ type: 'image', height: 10 }); renderEls(); markDirty(); } }, '+ Photo')),
            elList)),
        h('div', { class: 'col' },
          h('h3', null, 'Preview'), previewBox, warn,
          h('p', { class: 'muted small' }, sample ? `Showing: ${sample.name}` : 'Showing sample data.'),
          h('div', { class: 'row' },
            h('button', { class: 'btn primary', onclick: save }, 'Save'),
            h('button', { class: 'btn', onclick: duplicate }, 'Duplicate'),
            h('button', { class: 'btn danger', onclick: remove }, 'Delete')))));
    updatePreview();
  }

  async function save() {
    if (!draft.name.trim()) { toast('Give the design a name.', { error: true }); return; }
    if (!(draft.data.w > 3 && draft.data.h > 3)) { toast('Enter a size of at least 4 × 4 mm.', { error: true }); return; }
    try {
      if (draft.id) await api.put(`/api/docs/label/${draft.id}`, { name: draft.name, data: draft.data });
      else { const r = await api.post('/api/docs/label', { name: draft.name, data: draft.data }); draft.id = r.id; }
      dirty = false;
      await refreshMeta(['labels']);
      toast('Design saved');
      renderList();
    } catch (e) { toast(e.message, { error: true }); }
  }

  async function create(base = null) {
    const name = await promptDialog('New label design', 'Name', base ? base.name + ' copy' : '', { okLabel: 'Create' });
    if (!name) return;
    const data = base ? structuredClone(base.data) : { w: 50, h: 30, pad: 1.5, format: '', elements: [{ type: 'text', source: 'name', size: 8, bold: true, lines: 1, align: 'center' }, { type: 'barcode', show_text: true }, { type: 'text', source: 'price', size: 10, bold: true, lines: 1, align: 'center' }] };
    const r = await api.post('/api/docs/label', { name, data });
    await refreshMeta(['labels']);
    current = app.labels.find(l => l.id === r.id);
    draft = structuredClone(current);
    dirty = false;
    renderList(); renderEditor();
  }
  const duplicate = () => create(draft);

  async function remove() {
    if (!(await confirmDialog('Delete design?', `“${draft.name}” will be deleted. Categories using it go back to the default design.`, { okLabel: 'Delete', danger: true }))) return;
    await api.del(`/api/docs/label/${draft.id}`);
    await refreshMeta(['labels', 'categories']);
    draft = app.labels[0] ? structuredClone(app.labels[0]) : null;
    dirty = false;
    renderList(); renderEditor();
  }

  renderList();
  renderEditor();
  return {
    shortcuts: { save },
    canLeave: async () => !dirty || confirmDialog('Discard changes?', 'Your changes to this label design are not saved.', { okLabel: 'Discard', danger: true }),
  };
}
