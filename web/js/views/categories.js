// Categories & custom fields: the part that makes SHELFCODE fit any business.

import { h, app, clear, toast, dialog, confirmDialog, promptDialog, catById, catPath, catAncestors, fieldsFor, FIELD_TYPES, download, sortableList } from '../util.js';
import { api } from '../api.js';
import { FORMATS } from '../barcode.js';
import { fieldInput, categoryOptions } from '../components.js';
import { go, refreshMeta } from '../main.js';

const BUILTINS = [['price', 'Price'], ['cost', 'Cost'], ['quantity', 'Quantity'], ['low_stock_at', 'Low stock alert'], ['status', 'Status'], ['location', 'Location'], ['supplier', 'Supplier'], ['tags', 'Tags'], ['description', 'Description']];

export default async function ({ main, parts }) {
  await refreshMeta(['categories', 'fields', 'labels']);
  let selId = parts[1] === 'global' ? 'global' : Number(parts[1]) || app.categories[0]?.id || 'global';

  const treeBox = h('div', { class: 'panel flush' });
  const fieldsBox = h('div');
  const previewBox = h('div', { class: 'panel preview-col' });
  main.append(
    h('div', { class: 'page-head' }, h('h1', null, 'Categories & fields'),
      h('button', { class: 'btn', onclick: fromTemplate }, '+ From template'),
      h('button', { class: 'btn primary', onclick: () => editCategory(null) }, '+ Category')),
    h('div', { class: 'cat-layout' }, treeBox, fieldsBox, previewBox));

  function select(id) { selId = id; history.replaceState(null, '', `#/categories/${id}`); render(); }

  function renderTree() {
    clear(treeBox);
    const tree = h('div', { class: 'cat-tree', style: { padding: '6px' } });
    const add = (parent, depth) => {
      for (const c of app.categories.filter(c => (c.parent_id || null) === parent).sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name))) {
        tree.appendChild(h('a', { href: '#', class: selId === c.id ? 'on' : '', style: { paddingLeft: 6 + depth * 14 + 'px' }, onclick: (e) => { e.preventDefault(); select(c.id); } },
          h('span', { class: 'dot', style: { background: c.color || 'var(--border-strong)' } }), h('span', { class: 'name' }, c.name), h('span', { class: 'count' }, c.count || '')));
        add(c.id, depth + 1);
      }
    };
    add(null, 0);
    tree.appendChild(h('a', { href: '#', class: selId === 'global' ? 'on' : '', style: { marginTop: '8px', borderTop: '1px solid var(--border)', paddingTop: '8px' }, onclick: (e) => { e.preventDefault(); select('global'); } },
      h('span', { class: 'name' }, 'Fields on every item'), h('span', { class: 'count' }, app.fields.filter(f => f.category_id == null).length || '')));
    treeBox.append(h('div', { class: 'panel-head' }, h('h2', null, 'Categories')), tree);
  }

  function renderFields() {
    clear(fieldsBox);
    const isGlobal = selId === 'global';
    const cat = isGlobal ? null : catById(selId);
    if (!isGlobal && !cat) {
      fieldsBox.appendChild(h('div', { class: 'panel empty' }, h('h2', null, 'No categories yet'), h('p', null, 'Start from a template (Clothing, Phones…) or make your own.'),
        h('div', { class: 'row' }, h('button', { class: 'btn primary', onclick: fromTemplate }, '+ From template'), h('button', { class: 'btn', onclick: () => editCategory(null) }, '+ Category'))));
      return;
    }
    const own = app.fields.filter(f => isGlobal ? f.category_id == null : f.category_id === cat.id).sort((a, b) => a.sort_order - b.sort_order || a.id - b.id);
    const inherited = isGlobal ? [] : fieldsFor(cat.id).filter(f => f.category_id !== cat.id);
    const list = h('ul', { class: 'field-list' });
    for (const f of inherited) {
      list.appendChild(h('li', { class: 'inherited', title: 'Comes from ' + (f.category_id ? catPath(f.category_id) : 'Fields on every item') },
        h('span', { class: 'handle' }, ' '), h('span', { class: 'fname' }, f.label), h('span', { class: 'ftype' }, FIELD_TYPES[f.type]), h('span', { class: 'small' }, 'from ' + (f.category_id ? catById(f.category_id)?.name : 'all'))));
    }
    for (const f of own) {
      const flags = [f.required && 'required', f.as_code && 'scannable', f.unique && !f.as_code && 'unique', f.show_in_list && 'in list', f.show_on_label && 'on labels'].filter(Boolean);
      list.appendChild(h('li', { draggable: 'true', dataset: { id: f.id } },
        h('span', { class: 'handle', title: 'Drag to reorder' }, '⋮⋮'),
        h('span', { class: 'fname' }, f.label, f.group ? h('span', { class: 'muted', style: { fontWeight: 400 } }, ` · ${f.group}`) : null),
        h('span', { class: 'ftype' }, FIELD_TYPES[f.type] + (f.options?.unit ? ` (${f.options.unit})` : '')),
        flags.length ? h('span', { class: 'small muted' }, flags.join(', ')) : null,
        h('button', { class: 'btn small', onclick: () => editField(f) }, 'Edit')));
    }
    sortableList(list, 'li[draggable]', async (els) => {
      const ids = els.map(el => Number(el.dataset.id));
      await api.post('/api/fields/reorder', { ids });
      await refreshMeta(['fields']);
      renderPreview();
    });

    const head = isGlobal
      ? h('div', { class: 'panel-head' }, h('h2', null, 'Fields on every item'), h('button', { class: 'btn small primary', onclick: () => editField(null) }, '+ Add field'))
      : h('div', { class: 'panel-head' },
        h('span', { class: 'dot', style: { background: cat.color || 'var(--border-strong)', width: '12px', height: '12px' } }),
        h('h2', null, catPath(cat.id)),
        h('button', { class: 'btn small', onclick: () => editCategory(cat) }, 'Settings'),
        h('button', { class: 'btn small', onclick: () => download(`/api/categories/${cat.id}/export`), title: 'Save this category and its fields as a template file' }, 'Export'),
        h('button', { class: 'btn small primary', onclick: () => editField(null) }, '+ Add field'));
    const info = isGlobal ? h('p', { class: 'muted small', style: { padding: '0 16px' } }, 'These fields show on every item, in every category.') : h('div', { class: 'small muted', style: { padding: '10px 16px 0' } },
      [(app.settings.code_pattern || '{RANDNUM:10}').includes('{PREFIX}') && cat.code_prefix ? `Codes: ${cat.code_prefix}…` : null,
        cat.default_format ? `Barcode: ${FORMATS[cat.default_format]?.label}` : null,
        cat.unique_items ? 'One-of-a-kind items (quantity 1)' : null,
        cat.label_template_id ? `Label: ${app.labels.find(l => l.id === cat.label_template_id)?.name || '?'}` : null,
        cat.hidden_builtins?.length ? `Hidden: ${cat.hidden_builtins.join(', ')}` : null].filter(Boolean).join(' · '));
    fieldsBox.append(h('div', { class: 'panel flush' }, head, info,
      h('div', { style: { padding: '12px 16px 16px' } }, own.length || inherited.length ? list : h('p', { class: 'muted' }, 'No fields yet. Add fields like Brand, Size, Colour, RAM…'))));
  }

  function renderPreview() {
    clear(previewBox);
    const catId = selId === 'global' ? null : selId;
    previewBox.appendChild(h('h3', null, 'Form preview'));
    const fs = fieldsFor(catId).filter(f => selId !== 'global' || f.category_id == null);
    if (!fs.length) { previewBox.appendChild(h('p', { class: 'muted small' }, 'Fields you add appear here as they will on the item form.')); return; }
    const groups = new Map();
    for (const f of fs) { const g = f.group || 'Details'; if (!groups.has(g)) groups.set(g, []); groups.get(g).push(fieldInput(f, f.default_value || undefined, { custom: {} }).el); }
    for (const [g, els] of groups) previewBox.append(h('div', { style: { marginBottom: '14px' } }, h('h3', { style: { fontSize: '11px' } }, g), h('div', { class: 'col' }, els)));
  }

  function render() { renderTree(); renderFields(); renderPreview(); }

  // ---------- category dialog ----------
  async function editCategory(cat) {
    const isNew = !cat;
    const c = cat ? structuredClone(cat) : { name: '', parent_id: selId !== 'global' && selId ? null : null, color: '', code_prefix: '', next_seq: 1, default_format: '', unique_items: false, label_template_id: null, hidden_builtins: [], variant_fields: [] };
    const name = h('input', { type: 'text', value: c.name, autofocus: true });
    const parent = h('select', null, categoryOptions(c.parent_id, { none: '(top level)', exclude: cat ? [cat.id] : [] }));
    const color = h('input', { type: 'color', value: c.color || '#1F5FBF', style: { width: '48px', height: '32px', padding: '2px' } });
    const prefix = h('input', { type: 'text', value: c.code_prefix, placeholder: 'e.g. PH-', class: 'code-input' });
    const seq = h('input', { type: 'number', min: 1, value: c.next_seq });
    const fmt = h('select', null, h('option', { value: '' }, '(app default)'), Object.entries(FORMATS).map(([k, f]) => h('option', { value: k, selected: c.default_format === k }, f.label)));
    const label = h('select', null, h('option', { value: '' }, '(app default)'), app.labels.map(l => h('option', { value: l.id, selected: c.label_template_id === l.id }, l.name)));
    const unique = h('input', { type: 'checkbox', checked: c.unique_items });
    const hidden = BUILTINS.map(([k, l]) => ({ k, cb: h('input', { type: 'checkbox', checked: c.hidden_builtins.includes(k) }), l }));
    const variantChoices = (cat ? fieldsFor(cat.id) : []).filter(f => ['select', 'size', 'color', 'text'].includes(f.type));
    const variants = variantChoices.map(f => ({ k: f.key, cb: h('input', { type: 'checkbox', checked: c.variant_fields.includes(f.key) }), l: f.label }));
    const r = await dialog({
      title: isNew ? 'New category' : `Category settings: ${c.name}`, wide: true,
      body: h('div', { class: 'col', style: { gap: '14px' } },
        h('div', { class: 'grid2' },
          h('div', { class: 'field' }, h('label', null, 'Name'), name),
          h('div', { class: 'field' }, h('label', null, 'Inside'), parent),
          h('div', { class: 'field' }, h('label', null, 'Code prefix'), prefix, h('div', { class: 'help' }, 'Only used when Settings → Codes is set to a pattern with a prefix (e.g. PH-48291073).')),
          h('div', { class: 'field' }, h('label', null, 'Next number'), seq, h('div', { class: 'help' }, 'Only for counting-up codes.')),
          h('div', { class: 'field' }, h('label', null, 'Barcode format'), fmt),
          h('div', { class: 'field' }, h('label', null, 'Label design'), label),
          h('div', { class: 'field' }, h('label', null, 'Colour'), color),
          h('div', { class: 'field' }, h('label', null, 'Items'), h('label', { class: 'check', style: { height: '32px' } }, unique, 'One-of-a-kind (always quantity 1, e.g. phones)'))),
        h('div', null, h('h3', null, 'Hide these built-in fields'), h('div', { class: 'row', style: { gap: '6px 14px' } }, hidden.map(x => h('label', { class: 'check' }, x.cb, x.l)))),
        variantChoices.length ? h('div', null, h('h3', null, 'Variant fields (pre-selected in “Make variants”)'), h('div', { class: 'row', style: { gap: '6px 14px' } }, variants.map(x => h('label', { class: 'check' }, x.cb, x.l)))) : null),
      actions: [
        ...(isNew ? [] : [{ label: 'Delete category', danger: true, onClick: async (close) => { close(); await deleteCategory(cat); return false; } }]),
        { label: isNew ? 'Create' : 'Save', primary: true, onClick: async () => {
          if (!name.value.trim()) { toast('Enter a name.', { error: true }); return false; }
          const body = { name: name.value.trim(), parent_id: Number(parent.value) || null, color: color.value, code_prefix: prefix.value.trim(), next_seq: Number(seq.value) || 1,
            default_format: fmt.value, unique_items: unique.checked, label_template_id: Number(label.value) || null,
            hidden_builtins: hidden.filter(x => x.cb.checked).map(x => x.k), variant_fields: variants.filter(x => x.cb.checked).map(x => x.k) };
          try { return isNew ? await api.post('/api/categories', body) : await api.put(`/api/categories/${cat.id}`, body); }
          catch (e) { toast(e.message, { error: true }); return false; }
        } }],
    });
    if (r?.id) { await refreshMeta(['categories']); select(r.id); toast(isNew ? 'Category created. Now add its fields.' : 'Saved'); }
  }

  async function deleteCategory(cat) {
    const n = cat.count;
    if (!(await confirmDialog(`Delete “${cat.name}”?`, `${n ? `Its ${n} item(s) move to ${cat.parent_id ? 'the parent category' : '“No category”'}. ` : ''}Sub-categories move up one level. Its fields are removed, but values already saved in items are kept.`, { okLabel: 'Delete', danger: true }))) return;
    await api.del(`/api/categories/${cat.id}`);
    await refreshMeta(['categories', 'fields']);
    select(app.categories[0]?.id || 'global');
  }

  // ---------- field dialog ----------
  async function editField(f) {
    const isNew = !f;
    const x = f ? structuredClone(f) : { label: '', key: '', type: 'text', options: {}, required: false, unique: false, as_code: false, show_in_list: true, show_on_label: false, group: '', default_value: '', help_text: '', category_id: selId === 'global' ? null : selId };
    const label = h('input', { type: 'text', value: x.label, autofocus: true, placeholder: 'e.g. Brand, Size, RAM' });
    const key = h('input', { type: 'text', class: 'code-input', value: x.key, placeholder: 'made from the label' });
    const type = h('select', null, Object.entries(FIELD_TYPES).map(([k, l]) => h('option', { value: k, selected: x.type === k }, l)));
    const group = h('input', { type: 'text', value: x.group, list: 'group-list', placeholder: 'e.g. Specs, Condition' });
    const groupList = h('datalist', { id: 'group-list' }, [...new Set(app.fields.map(f => f.group).filter(Boolean))].map(g => h('option', { value: g })));
    const help = h('input', { type: 'text', value: x.help_text, placeholder: 'Small hint under the input' });
    const def = h('input', { type: 'text', value: x.default_value, placeholder: 'Value for new items' });
    const required = h('input', { type: 'checkbox', checked: x.required });
    const inList = h('input', { type: 'checkbox', checked: x.show_in_list });
    const onLabel = h('input', { type: 'checkbox', checked: x.show_on_label });
    const unique = h('input', { type: 'checkbox', checked: x.unique });
    const asCode = h('input', { type: 'checkbox', checked: x.as_code });
    const typeBox = h('div', { class: 'col' });
    const choices = h('textarea', { rows: 6, value: (x.options.choices || []).join('\n'), placeholder: 'One choice per line' });
    const allowNew = h('input', { type: 'checkbox', checked: x.options.allow_new !== false && (isNew || x.options.allow_new) });
    const unit = h('input', { type: 'text', value: x.options.unit || '', placeholder: 'GB, %, g, cm…', style: { width: '120px' } });
    const decimals = h('input', { type: 'number', min: 0, max: 6, value: x.options.decimals ?? '', style: { width: '80px' } });
    const min = h('input', { type: 'number', value: x.options.min ?? '', style: { width: '100px' } });
    const max = h('input', { type: 'number', value: x.options.max ?? '', style: { width: '100px' } });
    const ageFrom = h('select', null, fieldsFor(x.category_id).filter(ff => ff.type === 'date').map(ff => h('option', { value: ff.key, selected: x.options.from === ff.key }, ff.label)));
    const renderType = () => {
      clear(typeBox);
      const t = type.value;
      if (['select', 'size', 'multi_select', 'color'].includes(t)) {
        typeBox.append(h('div', { class: 'field' }, h('label', null, t === 'size' ? 'Choices, in size order (smallest first — lists sort this way)' : 'Choices'), choices),
          h('label', { class: 'check' }, allowNew, 'Allow typing a value that is not in the list'));
      }
      if (t === 'number') typeBox.append(h('div', { class: 'row' }, h('label', { class: 'row nowrap' }, 'Unit ', unit), h('label', { class: 'row nowrap' }, 'Decimals ', decimals), h('label', { class: 'row nowrap' }, 'Min ', min), h('label', { class: 'row nowrap' }, 'Max ', max)));
      if (t === 'age') typeBox.append(ageFrom.options.length ? h('div', { class: 'field' }, h('label', null, 'Count the age from this date field'), ageFrom) : h('p', { class: 'warn-text small' }, 'Add a Date field (e.g. “Purchase date”) to this category first.'));
      const codeOk = t === 'identifier' || t === 'text';
      asCode.disabled = !codeOk;
      if (!codeOk) asCode.checked = false;
    };
    type.addEventListener('change', renderType);
    renderType();
    const r = await dialog({
      title: isNew ? 'Add field' : `Edit field: ${x.label}`, wide: true,
      body: h('div', { class: 'col', style: { gap: '14px' } },
        h('div', { class: 'grid2' },
          h('div', { class: 'field' }, h('label', null, 'Label'), label),
          h('div', { class: 'field' }, h('label', null, 'Type'), type),
          h('div', { class: 'field' }, h('label', null, 'Section on the form'), group, groupList),
          h('div', { class: 'field' }, h('label', null, 'Key (used in CSV files)'), key)),
        typeBox,
        h('div', { class: 'grid2' },
          h('div', { class: 'field' }, h('label', null, 'Help text'), help),
          h('div', { class: 'field' }, h('label', null, 'Default value'), def)),
        h('div', { class: 'col', style: { gap: '6px' } },
          h('label', { class: 'check' }, required, 'Required'),
          h('label', { class: 'check' }, inList, 'Show as a column in the item list'),
          h('label', { class: 'check' }, onLabel, 'Offer on labels'),
          h('label', { class: 'check' }, unique, 'Must be unique (no two items with the same value)'),
          h('label', { class: 'check' }, asCode, h('span', null, h('strong', null, 'Scannable: '), 'scanning this value opens the item (IMEI, serial, ISBN…). Always unique.')))),
      actions: [
        ...(isNew ? [] : [{ label: 'Delete field', danger: true, onClick: async (close) => { close(); await deleteField(f); return false; } }]),
        { label: isNew ? 'Add' : 'Save', primary: true, onClick: async () => {
          if (!label.value.trim()) { toast('Enter a label.', { error: true }); return false; }
          const t = type.value;
          const options = {};
          if (['select', 'size', 'multi_select', 'color'].includes(t)) { options.choices = choices.value.split('\n').map(s => s.trim()).filter(Boolean); options.allow_new = allowNew.checked; }
          if (t === 'number') { if (unit.value.trim()) options.unit = unit.value.trim(); if (decimals.value !== '') options.decimals = Number(decimals.value); if (min.value !== '') options.min = Number(min.value); if (max.value !== '') options.max = Number(max.value); }
          if (t === 'age') { if (!ageFrom.value) { toast('Pick a date field.', { error: true }); return false; } options.from = ageFrom.value; }
          const body = { category_id: x.category_id, label: label.value.trim(), key: key.value.trim(), type: t, options, group: group.value.trim(), help_text: help.value.trim(), default_value: def.value,
            required: required.checked, show_in_list: inList.checked, show_on_label: onLabel.checked, unique: unique.checked || asCode.checked, as_code: asCode.checked };
          if (!isNew && f.type !== t) {
            const ok = await confirmDialog('Change the field type?', 'Values already saved stay as they are. Values that don’t fit the new type (for example text in a Number field) will need fixing when those items are next edited.', { okLabel: 'Change type' });
            if (!ok) return false;
          }
          try { return isNew ? await api.post('/api/fields', body) : await api.put(`/api/fields/${f.id}`, body); }
          catch (e) { toast(e.message, { error: true }); return false; }
        } }],
    });
    if (r?.id) { await refreshMeta(['fields']); render(); toast('Field saved'); }
  }

  async function deleteField(f) {
    const purge = h('input', { type: 'checkbox' });
    const r = await dialog({
      title: `Delete field “${f.label}”?`,
      body: h('div', { class: 'col' }, h('p', null, 'The field is removed from forms and lists. Values already saved in items are kept (hidden) unless you tick the box.'),
        h('label', { class: 'check' }, purge, 'Also delete the saved values from all items')),
      actions: [{ label: 'Delete', danger: true }],
    });
    if (!r) return;
    await api.del(`/api/fields/${f.id}${purge.checked ? '?purge=1' : ''}`);
    await refreshMeta(['fields']);
    render();
  }

  // ---------- templates ----------
  async function fromTemplate() {
    const { templates } = await api.get('/api/templates');
    const picked = new Set();
    let custom = null;
    const fileInput = h('input', { type: 'file', accept: '.json,application/json', hidden: true, onchange: async () => {
      try { custom = JSON.parse(await fileInput.files[0].text()); fileLabel.textContent = `Loaded: ${custom.name}`; }
      catch { toast('That file is not a SHELFCODE template.', { error: true }); }
    } });
    const fileLabel = h('span', { class: 'muted small' });
    const r = await dialog({
      title: 'Add categories from templates', xwide: true,
      body: h('div', null,
        h('div', { class: 'template-grid' }, templates.map(t => {
          const card = h('div', { class: 'template-card', onclick: () => { picked.has(t.id) ? picked.delete(t.id) : picked.add(t.id); card.classList.toggle('on'); card.querySelector('input').checked = picked.has(t.id); } },
            h('input', { type: 'checkbox', tabindex: -1 }), h('div', null, h('div', { class: 't-name' }, t.name), h('div', { class: 't-desc' }, t.description)));
          return card;
        })),
        h('div', { class: 'row', style: { marginTop: '12px' } }, h('button', { class: 'btn small', onclick: () => fileInput.click() }, 'Import a template file…'), fileLabel, fileInput)),
      actions: [{ label: 'Add', primary: true, onClick: async () => {
        if (!picked.size && !custom) return false;
        return api.post('/api/templates/apply', { ids: [...picked], template: custom });
      } }],
    });
    if (r?.created) { await refreshMeta(['categories', 'fields']); select(r.created[0]); toast(`${r.created.length} categor${r.created.length === 1 ? 'y' : 'ies'} added`); }
  }

  render();
  return {};
}
