// Barcode formats, validation, check digits, SVG rendering (bwip-js, stored locally).

export const FORMATS = {
  code128: { label: 'Code 128', hint: 'Letters, numbers, symbols. Best general choice.', d2: false },
  code39: { label: 'Code 39', hint: 'A–Z, 0–9, - . $ / + % space. For older scanners.', d2: false },
  code93: { label: 'Code 93', hint: 'Like Code 39 but more compact.', d2: false },
  ean13: { label: 'EAN-13', hint: '13 digits (12 + check digit). Retail, international.', d2: false, digits: 13 },
  upca: { label: 'UPC-A', hint: '12 digits (11 + check digit). Retail, North America.', d2: false, digits: 12 },
  ean8: { label: 'EAN-8', hint: '8 digits (7 + check digit). Small retail items.', d2: false, digits: 8 },
  itf14: { label: 'ITF-14', hint: '14 digits (13 + check digit). Cartons and cases.', d2: false, digits: 14 },
  qrcode: { label: 'QR Code', hint: 'Anything, including long text. Phones read it well.', d2: true },
  datamatrix: { label: 'Data Matrix', hint: 'Anything. Very small labels (jewelry, parts).', d2: true },
};

export function formatLabel(f) { return FORMATS[f]?.label || f || 'Code 128'; }

// GS1 mod-10 check digit for a string of digits (without the check digit).
export function gs1Check(body) {
  let sum = 0;
  for (let i = body.length - 1, w = 3; i >= 0; i--, w = w === 3 ? 1 : 3) sum += Number(body[i]) * w;
  return (10 - (sum % 10)) % 10;
}

// validate(format, code) -> { ok, error?, fix? }   fix = suggested corrected code (only offered, never applied silently)
export function validate(format, code) {
  if (!code) return { ok: false, error: 'Enter a code.' };
  const f = FORMATS[format] ? format : 'code128';
  const spec = FORMATS[f];
  if (spec.digits) {
    if (!/^\d+$/.test(code)) return { ok: false, error: `${spec.label} can only contain digits.` };
    if (code.length === spec.digits - 1) {
      const fixed = code + gs1Check(code);
      return { ok: false, error: `${spec.label} needs ${spec.digits} digits (you have ${code.length}). The check digit would be ${fixed.slice(-1)}.`, fix: fixed };
    }
    if (code.length !== spec.digits) return { ok: false, error: `${spec.label} needs ${spec.digits} digits. This code has ${code.length}.` };
    const want = gs1Check(code.slice(0, -1));
    if (Number(code.slice(-1)) !== want) return { ok: false, error: `The last digit (check digit) of this ${spec.label} should be ${want}, not ${code.slice(-1)}.`, fix: code.slice(0, -1) + want };
    return { ok: true };
  }
  if (f === 'code39' && !/^[0-9A-Z\-. $/+%]+$/.test(code)) return { ok: false, error: 'Code 39 allows only A–Z (capitals), 0–9 and - . $ / + % space.' };
  if ((f === 'code128' || f === 'code93') && !/^[\x20-\x7E]+$/.test(code)) return { ok: false, error: `${spec.label} allows only plain letters, numbers and symbols (no accents or emoji). Use QR Code instead.` };
  if (code.length > 2000 && !spec.d2) return { ok: false, error: 'Too long for a 1D barcode. Use QR Code.' };
  return { ok: true };
}

// Suggest a format for a code that came from outside (scan/import), WITHOUT changing the code.
export function guessFormat(code) {
  if (/^\d{13}$/.test(code) && validate('ean13', code).ok) return 'ean13';
  if (/^\d{12}$/.test(code) && validate('upca', code).ok) return 'upca';
  if (/^\d{8}$/.test(code) && validate('ean8', code).ok) return 'ean8';
  if (/^[\x20-\x7E]+$/.test(code)) return 'code128';
  return 'qrcode';
}

const cache = new Map();

// Returns { svg: string, modules: number, format, fallback: bool, error? }
export function renderSVG(format, code) {
  let f = FORMATS[format] ? format : 'code128';
  const key = f + '\u0000' + code;
  if (cache.has(key)) return cache.get(key);
  let fallback = false;
  const v = validate(f, code);
  if (!v.ok) {
    // Never change the code: draw it as Code 128 (or QR if it can't be) and flag it.
    fallback = true;
    f = /^[\x20-\x7E]+$/.test(code) ? 'code128' : 'qrcode';
  }
  let out;
  try {
    const opts = { bcid: f, text: code, scale: 1, includetext: false, paddingwidth: 0, paddingheight: 0 };
    if (!FORMATS[f].d2) opts.height = 10;
    if (f === 'ean13' || f === 'upca' || f === 'ean8') opts.guardwhitespace = false;
    let svg = window.bwipjs.toSVG(opts);
    const vb = /viewBox="0 0 ([\d.]+) ([\d.]+)"/.exec(svg);
    const modules = vb ? Number(vb[1]) : 0;
    svg = svg.replace(/<svg ([^>]*?)width="[^"]*"\s*height="[^"]*"/, '<svg $1')
      .replace('<svg ', `<svg preserveAspectRatio="${FORMATS[f].d2 ? 'xMidYMid meet' : 'none'}" shape-rendering="crispEdges" `);
    out = { svg, modules, format: f, fallback, error: fallback ? v.error : null, d2: FORMATS[f].d2 };
  } catch (e) {
    out = { svg: '', modules: 0, format: f, fallback: true, error: String(e.message || e).replace(/^bwipp\.\w+#\d+:\s*/, '') };
  }
  if (cache.size > 2000) cache.clear();
  cache.set(key, out);
  return out;
}

// Minimum width (mm) for a 1D barcode to scan reliably at ~0.19 mm per bar module, plus quiet zones.
export function minWidthMm(r) {
  if (!r || r.d2 || !r.modules) return 0;
  return r.modules * 0.19 + 4;
}

// A ready-to-insert element for screen previews.
export function barcodeEl(format, code, { height = 56, showText = true } = {}) {
  const wrap = document.createElement('div');
  wrap.className = 'bc-preview';
  const r = renderSVG(format, code);
  if (!r.svg) {
    wrap.innerHTML = `<div class="danger-text small">${escapeHtml(r.error || 'Cannot draw this code')}</div>`;
    return wrap;
  }
  const holder = document.createElement('div');
  holder.innerHTML = r.svg;
  const svg = holder.firstElementChild;
  if (r.d2) { svg.style.width = height * 1.6 + 'px'; svg.style.height = height * 1.6 + 'px'; }
  else { svg.style.width = Math.min(420, Math.max(140, r.modules * 1.6)) + 'px'; svg.style.height = height + 'px'; }
  wrap.appendChild(svg);
  if (showText) {
    const t = document.createElement('div');
    t.className = 'txt';
    t.textContent = code;
    wrap.appendChild(t);
  }
  if (r.fallback && r.error) {
    const w = document.createElement('div');
    w.className = 'small warn-text';
    w.style.maxWidth = '420px';
    w.textContent = `Shown as ${FORMATS[r.format].label}: ${r.error}`;
    wrap.appendChild(w);
  }
  return wrap;
}

export function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}
