// Label rendering + page layout (free packing, sticker sheets, label printers) + printing.

import { h, app, money, catPath, fieldValueText } from './util.js';
import { renderSVG, minWidthMm } from './barcode.js';

export const PAPERS = {
  letter: { label: 'Letter (8.5 × 11 in)', w: 215.9, h: 279.4 },
  a4: { label: 'A4 (210 × 297 mm)', w: 210, h: 297 },
  legal: { label: 'Legal (8.5 × 14 in)', w: 215.9, h: 355.6 },
  a5: { label: 'A5 (148 × 210 mm)', w: 148, h: 210 },
  '4x6': { label: '4 × 6 in', w: 101.6, h: 152.4 },
};

const IN = 25.4;
// cols, rows, label w/h, left/top margin, horizontal/vertical pitch (all mm)
export const SHEETS = {
  avery5160: { label: 'Avery 5160 / 8160 — 30 per sheet (2⅝ × 1 in)', paper: 'letter', cols: 3, rows: 10, w: 2.625 * IN, h: 1 * IN, left: 0.1875 * IN, top: 0.5 * IN, hp: 2.75 * IN, vp: 1 * IN },
  avery5167: { label: 'Avery 5167 / 8167 — 80 per sheet (1¾ × ½ in)', paper: 'letter', cols: 4, rows: 20, w: 1.75 * IN, h: 0.5 * IN, left: 0.3 * IN, top: 0.5 * IN, hp: 2.05 * IN, vp: 0.5 * IN },
  avery5163: { label: 'Avery 5163 / 8163 — 10 per sheet (4 × 2 in)', paper: 'letter', cols: 2, rows: 5, w: 4 * IN, h: 2 * IN, left: 0.15625 * IN, top: 0.5 * IN, hp: 4.1875 * IN, vp: 2 * IN },
  avery5164: { label: 'Avery 5164 — 6 per sheet (4 × 3⅓ in)', paper: 'letter', cols: 2, rows: 3, w: 4 * IN, h: 3.333 * IN, left: 0.15625 * IN, top: 0.5 * IN, hp: 4.1875 * IN, vp: 3.333 * IN },
  l7160: { label: 'Avery L7160 — 21 per sheet (63.5 × 38.1 mm)', paper: 'a4', cols: 3, rows: 7, w: 63.5, h: 38.1, left: 7.25, top: 15.15, hp: 66.0, vp: 38.1 },
  l7651: { label: 'Avery L7651 — 65 per sheet (38.1 × 21.2 mm)', paper: 'a4', cols: 5, rows: 13, w: 38.1, h: 21.2, left: 4.75, top: 10.7, hp: 40.6, vp: 21.2 },
  custom: { label: 'Custom grid…', paper: 'letter', cols: 3, rows: 10, w: 66.7, h: 25.4, left: 4.8, top: 12.7, hp: 69.85, vp: 25.4 },
};

export const ROLLS = {
  label: { label: 'Same size as each label design' },
  dymo30334: { label: 'Dymo 30334 — 2¼ × 1¼ in', w: 57.15, h: 31.75 },
  dymo30252: { label: 'Dymo 30252 — 3½ × 1⅛ in', w: 88.9, h: 28.575 },
  zebra2x1: { label: '2 × 1 in (Zebra, Rollo…)', w: 50.8, h: 25.4 },
  zebra4x6: { label: '4 × 6 in shipping', w: 101.6, h: 152.4 },
  brother62: { label: 'Brother 62 mm × 29 mm', w: 62, h: 29 },
};

export const TEXT_SOURCES = [
  ['name', 'Item name'], ['price', 'Price'], ['code', 'Code (text)'], ['category', 'Category'], ['store', 'Store name (Settings)'], ['static', 'Fixed text…'],
];

export function labelById(id) {
  return app.labels.find(l => l.id === Number(id)) || null;
}

export function defaultLabelFor(categoryId) {
  let c = categoryId ? app.categories.find(x => x.id === categoryId) : null;
  let guard = 0;
  while (c && guard++ < 20) {
    if (c.label_template_id && labelById(c.label_template_id)) return labelById(c.label_template_id);
    c = c.parent_id ? app.categories.find(x => x.id === c.parent_id) : null;
  }
  return labelById(app.settings.default_label_id) || app.labels.find(l => l.name.startsWith('Medium')) || app.labels[0] || null;
}

// Build the data a label needs from an item (or a raw code line).
export function labelData(line, item) {
  if (item) {
    return {
      code: item.code, format: line.format || item.barcode_format || 'code128', name: item.name, price: item.price,
      category: item.category_id ? catPath(item.category_id).split(' › ').pop() : '', custom: item.custom || {}, item,
      thumb: item.thumb || '',
    };
  }
  return { code: line.code, format: line.format || 'code128', name: line.text || '', price: line.price ?? null, category: '', custom: {}, item: null, thumb: '' };
}

function textFor(el, d) {
  let s = '';
  switch (el.source) {
    case 'name': s = d.name; break;
    case 'price': s = d.price != null ? money(d.price) : ''; break;
    case 'code': s = d.code; break;
    case 'category': s = d.category; break;
    case 'store': s = app.settings.store_name || ''; break;
    case 'static': s = el.text || ''; break;
    default:
      if (el.source?.startsWith('field:')) {
        const key = el.source.slice(6);
        const f = app.fields.find(x => x.key === key);
        s = f ? fieldValueText(f, d.custom[key], d.item) : (d.custom[key] ?? '');
      }
  }
  if (s === '' || s == null) return '';
  return (el.prefix || '') + s + (el.suffix || '');
}

// Render one label element. w/h in mm. Returns { el, warnings: [] }
export function renderLabel(tpl, d, w, hgt, { cut = false, preview = false } = {}) {
  const t = tpl?.data || { elements: [{ type: 'barcode', show_text: true }] };
  const pad = t.pad ?? 1.5;
  const warnings = [];
  const box = h('div', { class: 'label' + (cut ? ' cut' : ''), style: { width: w + 'mm', height: hgt + 'mm', padding: pad + 'mm' } });
  let hasBarcode = false;
  for (const el of t.elements || []) {
    if (el.type === 'text') {
      const s = textFor(el, d);
      if (!s) continue;
      const lines = Math.max(1, el.lines || 1);
      const node = h('div', { class: 'l-text' + (lines === 1 ? ' one' : ''), style: {
        fontSize: (el.size || 8) + 'pt', fontWeight: el.bold ? '700' : '400', textAlign: el.align || 'center',
        ...(lines > 1 ? { display: '-webkit-box', WebkitLineClamp: String(lines), WebkitBoxOrient: 'vertical', maxHeight: `calc(${lines} * 1.15em)` } : {}),
      } }, s);
      box.appendChild(node);
    } else if (el.type === 'image') {
      if (!d.thumb) continue;
      box.appendChild(h('div', { class: 'l-img', style: { height: (el.height || 10) + 'mm' } }, h('img', { src: d.thumb, alt: '' })));
    } else if (el.type === 'barcode') {
      hasBarcode = true;
      const fmt = el.format || t.format || d.format;
      const r = renderSVG(fmt, d.code);
      const bc = h('div', { class: 'l-bc' });
      if (!r.svg) {
        bc.appendChild(h('div', { class: 'bc-err' }, r.error || 'Cannot draw'));
        warnings.push(`${d.code}: ${r.error}`);
      } else {
        const holder = document.createElement('div');
        holder.innerHTML = r.svg;
        const svg = holder.firstElementChild;
        const quiet = r.d2 ? 0.5 : 2; // mm of white space each side so scanners find the edges
        svg.style.padding = `0 ${quiet}mm`;
        svg.style.boxSizing = 'border-box';
        bc.appendChild(svg);
        if (el.show_text !== false) {
          const fs = Math.max(4.5, Math.min(9, hgt * 0.28));
          bc.appendChild(h('div', { class: 'hr', style: { fontSize: fs + 'pt' } }, d.code));
        }
        const need = minWidthMm(r);
        const avail = w - 2 * pad;
        if (need && avail < need) {
          warnings.push(`${d.code} needs about ${Math.ceil(need)} mm width to scan reliably (label has ${Math.floor(avail)} mm).`);
          if (preview) box.classList.add('warn-small');
        }
        if (r.fallback && r.error) warnings.push(`${d.code}: ${r.error} Printed as ${r.format === 'qrcode' ? 'QR Code' : 'Code 128'}.`);
      }
      box.appendChild(bc);
    }
  }
  if (!hasBarcode && !(t.elements || []).length) box.appendChild(h('div', { class: 'muted' }, 'Empty label'));
  return { el: box, warnings };
}

// labels: [{ w, h, tpl, data, key }]
// page: { mode: 'free'|'sheet'|'roll', paper, orientation, margin, gap, sheet, custom, start, roll, pack }
export function layout(labels, page) {
  const pages = [];
  const warnings = [];
  if (page.mode === 'roll') {
    const roll = ROLLS[page.roll] || ROLLS.label;
    for (const l of labels) {
      const w = roll.w || l.w, hh = roll.h || l.h;
      pages.push({ w, h: hh, items: [{ ...l, x: 0, y: 0, w, h: hh }] });
    }
    return { pages, warnings };
  }
  if (page.mode === 'sheet') {
    const s = page.sheet === 'custom' ? { ...SHEETS.custom, ...(page.custom || {}) } : SHEETS[page.sheet] || SHEETS.avery5160;
    const paper = PAPERS[s.paper] || PAPERS.letter;
    const per = s.cols * s.rows;
    let idx = Math.max(0, Math.min(per - 1, page.start || 0));
    let cur = null;
    const newPage = () => { cur = { w: paper.w, h: paper.h, items: [], sheet: s, startCell: pages.length === 0 ? (page.start || 0) : 0 }; pages.push(cur); };
    newPage();
    for (const l of labels) {
      if (idx >= per) { newPage(); idx = 0; }
      const col = idx % s.cols, row = Math.floor(idx / s.cols);
      cur.items.push({ ...l, x: s.left + col * s.hp, y: s.top + row * s.vp, w: s.w, h: s.h });
      idx++;
    }
    return { pages, warnings };
  }
  // free mode: shelf packing, mixed sizes
  let paper = PAPERS[page.paper] || PAPERS.letter;
  let pw = paper.w, ph = paper.h;
  if (page.orientation === 'landscape') [pw, ph] = [ph, pw];
  const m = Number(page.margin ?? 6), gap = Number(page.gap ?? 2);
  const list = page.pack ? [...labels].sort((a, b) => b.h - a.h || b.w - a.w) : labels;
  let cur = null, x = 0, y = 0, rowH = 0;
  const newPage = () => { cur = { w: pw, h: ph, items: [] }; pages.push(cur); x = m; y = m; rowH = 0; };
  newPage();
  for (const l of list) {
    if (l.w > pw - 2 * m || l.h > ph - 2 * m) warnings.push(`A ${l.w}×${l.h} mm label is larger than the printable area of the page.`);
    if (x > m && x + l.w > pw - m + 0.01) { x = m; y += rowH + gap; rowH = 0; }
    if (y > m && y + l.h > ph - m + 0.01) newPage();
    cur.items.push({ ...l, x, y });
    x += l.w + gap;
    rowH = Math.max(rowH, l.h);
  }
  return { pages, warnings: [...new Set(warnings)] };
}

// Build the DOM for pages (used for both preview and print).
export function renderPages(pages, { cut = false, preview = false, onCell = null, calibration = null } = {}) {
  const warnings = [];
  const out = pages.map((p, pi) => {
    const sheet = h('div', { class: 'sheet', style: { width: p.w + 'mm', height: p.h + 'mm' } });
    const inner = h('div', { style: { position: 'absolute', inset: '0', transformOrigin: '0 0' } });
    if (calibration && (calibration.x || calibration.y || (calibration.scale && calibration.scale !== 100))) {
      inner.style.transform = `translate(${calibration.x || 0}mm, ${calibration.y || 0}mm) scale(${(calibration.scale || 100) / 100})`;
    }
    sheet.appendChild(inner);
    if (preview && p.sheet) {
      const s = p.sheet;
      for (let i = 0; i < s.cols * s.rows; i++) {
        const col = i % s.cols, row = Math.floor(i / s.cols);
        inner.appendChild(h('div', { class: 'cell-ghost' + (pi === 0 && i < p.startCell ? ' used' : ''), title: pi === 0 ? 'Click: start printing at this label' : '',
          style: { left: s.left + col * s.hp + 'mm', top: s.top + row * s.vp + 'mm', width: s.w + 'mm', height: s.h + 'mm' },
          onclick: pi === 0 && onCell ? () => onCell(i) : null }));
      }
    }
    for (const it of p.items) {
      const r = renderLabel(it.tpl, it.data, it.w, it.h, { cut, preview });
      r.el.style.left = it.x + 'mm';
      r.el.style.top = it.y + 'mm';
      if (p.sheet) r.el.style.pointerEvents = 'none';
      inner.appendChild(r.el);
      warnings.push(...r.warnings);
    }
    return sheet;
  });
  return { sheets: out, warnings: [...new Set(warnings)] };
}

let pageStyle = null;
export function printPages(pages, opts = {}) {
  const root = document.getElementById('print-root');
  root.className = '';
  root.replaceChildren(...renderPages(pages, opts).sheets);
  // All pages of one job share a size; roll jobs may differ — use the first page's size.
  const first = pages[0];
  pageStyle?.remove();
  pageStyle = h('style', null, `@page { size: ${first.w}mm ${first.h}mm; margin: 0; }`);
  document.head.appendChild(pageStyle);
  return new Promise(resolve => {
    // Let images (photos on labels) load before the print dialog opens.
    const imgs = [...root.querySelectorAll('img')].filter(i => !i.complete);
    Promise.all(imgs.map(i => new Promise(r => { i.onload = i.onerror = r; }))).then(() => {
      setTimeout(() => {
        window.print();
        setTimeout(() => { root.replaceChildren(); resolve(); }, 500);
      }, 50);
    });
  });
}

export function printHTML(node, pageCSS = '@page { margin: 12mm; }') {
  const root = document.getElementById('print-root');
  root.className = 'catalog';
  root.replaceChildren(node);
  pageStyle?.remove();
  pageStyle = h('style', null, pageCSS);
  document.head.appendChild(pageStyle);
  setTimeout(() => { window.print(); setTimeout(() => root.replaceChildren(), 500); }, 50);
}

export function calibrationFor(key) {
  return (app.settings.calibration || {})[key] || { x: 0, y: 0, scale: 100 };
}
