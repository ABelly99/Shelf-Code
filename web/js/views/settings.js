import { h, app, clear, toast, confirmDialog, dialog, fmtDate, money, download } from '../util.js';
import { api } from '../api.js';
import { FORMATS } from '../barcode.js';
import { PAPERS, SHEETS, calibrationFor } from '../labelkit.js';
import { refreshMeta, go } from '../main.js';

const TABS = [['general', 'General'], ['agents', 'AI agents'], ['scanner', 'Scanner'], ['codes', 'Codes & lookup'], ['printing', 'Printing'], ['items', 'Items & statuses'], ['network', 'Network'], ['backup', 'Backup'], ['about', 'About']];

export default async function ({ main, parts }) {
  const tab = parts[1] || 'general';
  await refreshMeta(['settings']);
  const s = app.settings;
  main.append(h('div', { class: 'page-head' }, h('h1', null, 'Settings')),
    h('div', { class: 'tabs' }, TABS.map(([k, l]) => h('a', { href: `#/settings/${k}`, class: k === tab ? 'on' : '' }, l))));
  const body = h('div', { style: { maxWidth: '760px' } });
  main.appendChild(body);

  const save = async (patch, msg = 'Saved') => {
    try { app.settings = await api.put('/api/settings', patch); toast(msg); }
    catch (e) { toast(e.message, { error: true }); }
  };
  const text = (key, opts = {}) => { const i = h('input', { type: 'text', value: s[key] ?? '', ...opts, onchange: () => save({ [key]: i.value }) }); return i; };
  const numb = (key, opts = {}) => { const i = h('input', { type: 'number', value: s[key] ?? '', style: { width: '100px' }, ...opts, onchange: () => save({ [key]: i.value === '' ? null : Number(i.value) }) }); return i; };
  const bool = (key, label) => h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: !!s[key], onchange: (e) => save({ [key]: e.target.checked }) }), label);
  const select = (key, options) => { const el = h('select', { onchange: () => save({ [key]: el.value === 'true' ? true : el.value === 'false' ? false : /^\d+$/.test(el.value) && typeof options[0][0] === 'number' ? Number(el.value) : el.value }) }, options.map(([v, l]) => h('option', { value: v, selected: String(s[key]) === String(v) }, l))); return el; };
  const field = (label, input, help) => h('div', { class: 'field' }, h('label', null, label), input, help ? h('div', { class: 'help' }, help) : null);
  const section = (title, ...kids) => h('div', { class: 'panel' }, title ? h('h2', null, title) : null, h('div', { class: 'col', style: { gap: '12px' } }, kids));

  if (tab === 'general') {
    body.append(section(null,
      field('Store name', text('store_name', { placeholder: 'Shown on Home and on labels that include it' })),
      h('div', { class: 'grid3' },
        field('Currency symbol', text('currency_symbol', { style: { width: '80px' } })),
        field('Symbol position', select('currency_after', [[false, 'Before ($12.00)'], [true, 'After (12,00 €)']])),
        field('Number format', (() => { const el = h('select', { onchange: () => { const [d, t] = el.value.split('|'); save({ decimal_sep: d, thousands_sep: t }); } },
          [['.|,', '1,234.56'], [',|.', '1.234,56'], [',| ', '1 234,56'], ['.| ', '1 234.56']].map(([v, l]) => h('option', { value: v, selected: v === `${s.decimal_sep}|${s.thousands_sep}` }, l))); return el; })())),
      h('div', { class: 'grid3' },
        field('Theme', (() => { const el = select('theme', [['system', 'Same as Windows'], ['light', 'Light'], ['dark', 'Dark']]); el.addEventListener('change', () => setTimeout(() => refreshMeta(['settings']), 100)); return el; })()),
        field('List density', (() => { const el = select('density', [['compact', 'Compact'], ['comfortable', 'Comfortable (bigger photos)']]); el.addEventListener('change', () => setTimeout(() => refreshMeta(['settings']), 100)); return el; })())),
      h('p', { class: 'muted small' }, 'Example price: ', money(123456))));
  }

  if (tab === 'agents') {
    const info = await api.get('/api/agents');
    const copyBtn = (text, label = 'Copy') => h('button', { class: 'btn small', onclick: async () => {
      try { await navigator.clipboard.writeText(text); toast('Copied'); } catch { toast('Select the text and copy it with Ctrl+C', { error: true }); }
    } }, label);
    const codeLine = (text) => h('div', { class: 'row nowrap', style: { gap: '6px' } },
      h('code', { style: { flex: 1, minWidth: 0, overflowX: 'auto', whiteSpace: 'nowrap', padding: '6px 8px', background: 'var(--surface-2)', border: '1px solid var(--border)', borderRadius: '4px', fontSize: '12px' } }, text), copyBtn(text));

    body.append(section('Run SHELFCODE from your AI assistant',
      h('p', null, 'Connect an AI assistant and just ask. No buttons or forms needed. For example:'),
      h('ul', { class: 'small', style: { margin: '0 0 4px', paddingLeft: '18px', lineHeight: 1.7 } },
        ['“Add 12 black Northline tees, S to XL, $19.99, and print hang tags for all of them.”',
         '“What phones under $300 do we have in grade A?”',
         '“Sold the iPhone with IMEI 356789104455661 for $420.”',
         '“Make me 50 blank stickers on Avery 5160 sheets.”',
         '“Undo the last price change on the denim jacket.”'].map(t => h('li', null, t))),
      h('p', { class: 'muted small' }, 'Changes show up here as they happen, and every change is saved in the item’s history so it can be undone.')));

    if (info.local) {
      const list = h('div', { class: 'col', style: { gap: '0' } });
      const allBox = h('div');
      const render = (agents) => {
        const todo = agents.filter(a => a.detected && !a.connected);
        allBox.replaceChildren(todo.length > 1 ? h('button', { class: 'btn primary', onclick: async () => { for (const a of todo) await act(a, false); } }, `Connect all ${todo.length}`) : '');
        list.replaceChildren(...agents.map(a => {
          const badge = a.connected ? h('span', { class: 'badge success' }, 'Connected') : a.detected ? h('span', { class: 'badge accent' }, 'Found on this PC') : h('span', { class: 'badge' }, 'Not installed');
          const btn = a.connected
            ? h('button', { class: 'btn small', onclick: () => act(a, true) }, 'Disconnect')
            : h('button', { class: 'btn small primary', disabled: !a.detected, onclick: () => act(a, false) }, 'Connect');
          return h('div', { class: 'row nowrap', style: { padding: '8px 0', borderBottom: '1px solid var(--border)' } },
            h('strong', { class: 'grow' }, a.name), badge, btn);
        }));
      };
      const act = async (a, remove) => {
        try {
          const r = await api.post('/api/agents/connect', { id: a.id, remove });
          toast(remove ? `${a.name} disconnected` : `${a.name} connected. ${r.after}`, { ms: 7000 });
        } catch (e) { toast(e.message, { error: true }); }
        render((await api.get('/api/agents')).agents);
      };
      render(info.agents);
      body.append(section('Connect an assistant on this PC',
        info.exe_error ? h('p', { class: 'warn-text' }, info.exe_error) : h('p', { class: 'muted small' }, 'One click: SHELFCODE adds itself to the assistant’s settings. Nothing to copy or paste.'),
        list,
        allBox));
    }

    const notes = h('textarea', { rows: 5, value: info.notes || '', placeholder: 'e.g.\n- Prices end in .99\n- Tag new arrivals with “new”\n- Phones: always fill in battery health' , style: { width: '100%' } });
    body.append(section('Store notes',
      h('p', { class: 'muted small' }, 'Your assistant reads these before every task and follows them. It adds to them when you tell it “always…” or “from now on…”.'),
      notes,
      h('div', null, h('button', { class: 'btn', onclick: () => save({ agent_notes: notes.value }, 'Store notes saved') }, 'Save notes'))));

    const auth = 'Bearer ' + info.token;
    const httpCfg = { url: info.url, headers: { Authorization: auth } };
    const b64 = btoa(unescape(encodeURIComponent(JSON.stringify(httpCfg))));
    const cursorLink = `cursor://anysphere.cursor-deeplink/mcp/install?name=shelfcode&config=${encodeURIComponent(b64)}`;
    const vscodeLink = 'vscode:mcp/install?' + encodeURIComponent(JSON.stringify({ name: 'shelfcode', type: 'http', ...httpCfg }));
    const tokenBox = h('code', { class: 'mono small' }, info.token.slice(0, 6) + '••••••••••••');
    body.append(section(info.local ? 'Connect from another computer' : 'Connect your AI assistant',
      h('p', { class: 'muted small' }, info.local ? 'For an assistant on a different PC on your network.' : 'SHELFCODE runs on this server; connect the assistant on your own computer.'),
      h('div', { class: 'row' },
        h('a', { class: 'btn primary', href: cursorLink }, 'Add to Cursor'),
        h('a', { class: 'btn primary', href: vscodeLink }, 'Add to VS Code')),
      field('Claude Code', codeLine(`claude mcp add --transport http shelfcode ${info.url} --header "Authorization: ${auth}"`)),
      field('Gemini CLI', codeLine(`gemini mcp add --transport http shelfcode ${info.url} --header "Authorization: ${auth}"`)),
      field('Any other MCP app (JSON)', codeLine(JSON.stringify({ mcpServers: { shelfcode: { type: 'http', ...httpCfg } } }))),
      h('div', { class: 'row small muted' }, 'Address ', h('code', null, info.url), ' · token ', tokenBox,
        h('button', { class: 'btn small ghost', onclick: () => { tokenBox.textContent = info.token; } }, 'Show'),
        h('button', { class: 'btn small ghost', onclick: async () => {
          if (!(await confirmDialog('Make a new token?', 'Assistants connected from other computers stop working until you connect them again. Assistants on this PC are not affected.', { okLabel: 'New token' }))) return;
          await api.post('/api/agents/token'); go('#/settings/agents?r=' + Date.now());
        } }, 'New token')),
      h('p', { class: 'muted small' }, 'The token is like a password: anyone with it can change your inventory. Keep it private.')));
  }

  if (tab === 'scanner') {
    const status = h('div');
    const site = h('input', { type: 'text', value: s.scanner_relay_url || '', style: { width: '100%' } });
    const code = h('input', { type: 'text', class: 'code-input', placeholder: 'XXXX-XXXX-XXXX', value: s.scanner_code ? s.scanner_code.replace(/(.{4})(.{4})(.{4})/, '$1-$2-$3') : '', style: { width: '200px', textTransform: 'uppercase', fontSize: '16px', letterSpacing: '.05em' } });
    const renderStatus = (st) => {
      const color = st.status === 'connected' ? (st.phones ? 'var(--success)' : 'var(--accent)') : st.status === 'error' ? 'var(--danger)' : st.status === 'connecting' ? 'var(--warning)' : 'var(--border-strong)';
      clear(status).append(h('div', { class: 'row' }, h('span', { class: 'dot', style: { background: color, width: '10px', height: '10px' } }),
        h('strong', null, { connected: st.phones ? 'Ready' : 'Connected', connecting: 'Connecting…', error: 'Problem', off: 'Off' }[st.status] || st.status),
        h('span', { class: 'muted' }, st.message || ''), st.code ? h('span', { class: 'mono small muted' }, st.code) : null),
        st.last_scan ? h('div', { class: 'small muted' }, `Last scan ${st.last_at}: `, h('span', { class: 'mono' }, st.last_scan)) : null);
    };
    renderStatus(app.scanner || await api.get('/api/scanner'));
    const onStatus = (e) => renderStatus(e.detail);
    document.addEventListener('scanner-status', onStatus);
    const testIn = h('input', { type: 'text', placeholder: 'Click here and scan with a USB scanner', style: { width: '100%' } });
    const testOut = h('div', { class: 'small muted' });
    let t0 = 0, times = [];
    testIn.addEventListener('keydown', (e) => {
      const n = performance.now();
      if (t0) times.push(n - t0);
      t0 = n;
      if (e.key === 'Enter') {
        e.preventDefault();
        const avg = times.length ? times.reduce((a, b) => a + b, 0) / times.length : 0;
        testOut.textContent = `Received “${testIn.value}” (${testIn.value.length} characters, about ${avg.toFixed(0)} ms between keys). ${avg && avg <= (s.scan_threshold_ms || 35) ? 'This counts as a scanner ✓' : 'Too slow to count as a scanner — raise the threshold below if this was a scanner.'}`;
        testIn.value = ''; t0 = 0; times = [];
      }
    });
    body.append(
      section('Phone scanner (Barcode Scanner website)',
        h('p', { class: 'muted' }, 'Scans from your phone arrive here directly, no Barcode Typer needed. On the phone, open the scanner website, tap Scan, and open the Drawer to see the connecting code. Close Barcode Typer on this PC while SHELFCODE is connected (otherwise every scan is also typed).'),
        status,
        field('Connecting code (from the phone’s Drawer)', h('div', { class: 'row' }, code,
          h('button', { class: 'btn primary', onclick: async () => {
            try { const st = await api.post('/api/scanner/connect', { site: site.value.trim(), code: code.value }); renderStatus(st); await refreshMeta(['settings']); toast('Connecting…'); }
            catch (e) { toast(e.message, { error: true }); }
          } }, 'Connect'),
          h('button', { class: 'btn', onclick: async () => { renderStatus(await api.post('/api/scanner/disconnect')); await refreshMeta(['settings']); } }, 'Disconnect'))),
        h('details', null, h('summary', { class: 'muted small', style: { cursor: 'pointer' } }, 'Scanner website address'), h('div', { style: { marginTop: '8px' } }, field('Website', site, 'Your Barcode Scanner site. Saved when you press Connect.'))),
        h('p', { class: 'muted small' }, 'Scans go to the SHELFCODE page you used last. If a text box on an item form has the cursor, the scan is typed there instead (like Barcode Typer). SHELFCODE reconnects by itself when it starts.')),
      section('USB / Bluetooth scanners',
        h('p', { class: 'muted' }, 'Any scanner that types like a keyboard works anywhere in SHELFCODE, no setup needed. Test it here:'),
        testIn, testOut,
        h('div', { class: 'grid3' },
          field('Scanner speed threshold (ms between keys)', numb('scan_threshold_ms', { min: 5, max: 200 }), 'Faster than this = a scanner. Default 35.'),
          field('Minimum code length', numb('scan_min_length', { min: 1, max: 50 })),
          field('Sound', bool('scan_beep', 'Beep on each scan')))));
    return { destroy: () => document.removeEventListener('scanner-status', onStatus) };
  }

  if (tab === 'codes') {
    const PRESETS = [
      ['{RANDNUM:10}', 'Random number', '4829107365'],
      ['{RANDNUM:8}', 'Random number (shorter)', '48291073'],
      ['{RANDNUM:12}', 'Random number (longer)', '482910736519'],
      ['{PREFIX}{RANDNUM:8}', 'Category prefix + random number', 'PH-48291073'],
      ['{PREFIX}{SEQ:6}', 'Category prefix + counting up', 'PH-000001, PH-000002…'],
    ];
    const cur = s.code_pattern || '{RANDNUM:10}';
    const isPreset = PRESETS.some(([v]) => v === cur);
    const custom = h('input', { type: 'text', class: 'code-input', value: cur, style: { width: '100%' }, onchange: () => save({ code_pattern: custom.value.trim() || '{RANDNUM:10}' }) });
    const customBox = h('div', { class: 'field', hidden: isPreset }, h('label', null, 'Custom pattern'), custom,
      h('div', { class: 'help' }, '{RANDNUM:10} random digits · {RAND:4} random letters+digits · {PREFIX} category prefix · {SEQ:6} counting up · {YYYY} {YY} {MM} {DD} date · {CHECK} check digit'));
    const style = h('select', { onchange: () => {
      customBox.hidden = style.value !== 'custom';
      if (style.value !== 'custom') { custom.value = style.value; save({ code_pattern: style.value }, 'New codes will look like this from now on'); }
    } }, PRESETS.map(([v, l, ex]) => h('option', { value: v, selected: v === cur }, `${l} — ${ex}`)), h('option', { value: 'custom', selected: !isPreset }, 'Custom pattern…'));
    const prefix = text('global_prefix', { class: 'code-input', style: { width: '120px' } });
    body.append(
      section('New codes',
        h('p', { class: 'muted' }, 'When you add an item, SHELFCODE makes its code for you (you can always type or scan your own instead). Existing items keep their codes.'),
        field('Generated codes look like', style),
        customBox,
        h('div', { class: 'grid2' },
          field('Default barcode format', select('default_format', Object.entries(FORMATS).map(([k, f]) => [k, f.label])), 'Categories can choose their own.'),
          field('Prefix (when the category has none)', prefix, 'Only used by patterns with a prefix.')),
        h('p', { class: 'muted small' }, 'With EAN-13, UPC-A or EAN-8 as the format, generated codes are valid in-store numbers (EAN-13 starting with 2, UPC-A starting with 4) with a correct check digit, so any retail scanner reads them. Codes are always checked so two items never share one.')),
      section('Code lookup (the box at the top)',
        h('p', { class: 'muted' }, 'A code opens its item only when it matches exactly. When nothing matches, nothing happens.'),
        bool('lookup_ignore_case', 'Ignore upper/lower case (abc = ABC)'),
        bool('lookup_keep_text', 'Keep the text in the box when nothing matches'),
        field('When nothing matches', select('lookup_nomatch', [['none', 'Do nothing (default)'], ['sound', 'Low beep'], ['flash', 'Flash the box red']])),
        field('Unknown codes', select('lookup_unknown', [['nothing', 'Do nothing (default)'], ['offer', 'Offer to create an item with that code']]))));
  }

  if (tab === 'printing') {
    const cal = { ...(s.calibration || {}) };
    const keySel = h('select', null, [...Object.entries(PAPERS).map(([k, p]) => [`paper:${k}`, `Free mode · ${p.label}`]), ...Object.entries(SHEETS).map(([k, sh]) => [`sheet:${k}`, `Sheet · ${sh.label}`]), ['roll', 'Label printer']].map(([v, l]) => h('option', { value: v }, l)));
    const cx = h('input', { type: 'number', step: '0.1', style: { width: '80px' } });
    const cy = h('input', { type: 'number', step: '0.1', style: { width: '80px' } });
    const cs = h('input', { type: 'number', step: '0.1', style: { width: '80px' } });
    const load = () => { const c = cal[keySel.value] || { x: 0, y: 0, scale: 100 }; cx.value = c.x; cy.value = c.y; cs.value = c.scale; };
    keySel.addEventListener('change', load);
    load();
    body.append(
      section('Defaults',
        h('div', { class: 'grid3' },
          field('Paper', select('paper', Object.entries(PAPERS).map(([k, p]) => [k, p.label]))),
          field('Page margins (mm)', numb('page_margin_mm', { step: 0.5 })),
          field('Gap between labels (mm)', numb('page_gap_mm', { step: 0.5 }))),
        bool('cut_lines', 'Show cut lines in free mode'),
        field('Default label design', select('default_label_id', [[0, '(Medium 50×30)'], ...app.labels.map(l => [l.id, l.name])]))),
      section('Printer calibration',
        h('p', { class: 'muted' }, 'If labels come out shifted or slightly too big/small, print the test page, measure it with a ruler, and correct here. Saved per paper / sheet.'),
        field('For', keySel),
        h('div', { class: 'row' }, h('label', null, 'Move right (mm) '), cx, h('label', null, 'Move down (mm) '), cy, h('label', null, 'Scale (%) '), cs),
        h('div', { class: 'row' },
          h('button', { class: 'btn primary', onclick: () => { cal[keySel.value] = { x: Number(cx.value) || 0, y: Number(cy.value) || 0, scale: Number(cs.value) || 100 }; save({ calibration: cal }); } }, 'Save correction'),
          h('button', { class: 'btn', onclick: () => printTestPage(keySel.value) }, 'Print test page'))));
  }

  if (tab === 'items') {
    const statuses = (s.statuses || []).map(x => ({ ...x }));
    const list = h('div', { class: 'col' });
    const render = () => {
      clear(list).append(...statuses.map((st, i) => h('div', { class: 'row nowrap' },
        h('input', { type: 'text', value: st.label, oninput: (e) => { st.label = e.target.value; } }),
        h('span', { class: 'mono small muted', style: { width: '90px' } }, st.key),
        h('select', { onchange: (e) => { st.color = e.target.value; } }, ['success', 'warning', 'danger', 'accent', 'muted'].map(c => h('option', { value: c, selected: st.color === c }, c))),
        ['in_stock', 'sold'].includes(st.key) ? h('span', { class: 'muted small' }, 'built in') : h('button', { class: 'btn small ghost', onclick: () => { statuses.splice(i, 1); render(); } }, 'Remove'))),
      h('div', { class: 'row' }, h('button', { class: 'btn small', onclick: () => {
        const label = prompt('New status name');
        if (!label) return;
        statuses.push({ key: label.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '') || 'status', label, color: 'accent' });
        render();
      } }, '+ Status'), h('button', { class: 'btn small primary', onclick: () => save({ statuses }) }, 'Save statuses')));
    };
    render();
    body.append(
      section('New items',
        h('div', { class: 'grid3' },
          field('Default quantity', numb('default_quantity', { min: 0 })),
          field('Default low stock alert', numb('default_low_stock', { min: 0, placeholder: 'none' })))),
      section('Statuses', list),
      section('Privacy & clean-up',
        bool('hide_cost', 'Hide cost and margin on item pages and Home (for shop-floor screens)'),
        field('Empty Trash after (days)', numb('trash_days', { min: 0 }), '0 = never empty automatically.')));
  }

  if (tab === 'network') {
    const info = await api.get('/api/info');
    const local = info.local;
    const pin = h('input', { type: 'text', value: s.lan_pin || '', placeholder: 'No PIN', inputmode: 'numeric', style: { width: '160px' }, disabled: !local });
    const pinField = field(info.server_mode ? 'PIN' : 'PIN for other devices', h('div', { class: 'row' }, pin, h('button', { class: 'btn', disabled: !local, onclick: () => save({ lan_pin: pin.value.trim() }, pin.value.trim() ? 'PIN set' : 'PIN removed') }, 'Save PIN')),
      info.server_mode ? 'Everyone who opens SHELFCODE must enter this PIN once (remembered for 30 days on that device).' : 'Other devices must enter this PIN once (remembered for 30 days). This PC never needs it.');
    if (info.server_mode) {
      body.append(section('Server',
        h('p', null, 'SHELFCODE is running as a server. Open it from any device that can reach:'),
        h('div', { class: 'mono', style: { fontSize: '16px' } }, location.origin),
        h('p', { class: 'muted small' }, `Version ${info.version} · data folder ${info.data_dir}`),
        s.lan_pin ? null : h('p', { class: 'warn-text small' }, 'No PIN is set: anyone who can reach this address can use SHELFCODE.'),
        pinField));
    } else {
      body.append(section('Use SHELFCODE on other devices',
        h('p', null, 'Phones, tablets and other PCs on the same Wi-Fi / network can open:'),
        info.urls.lan.length ? h('div', { class: 'col', style: { gap: '4px' } }, info.urls.lan.map(u => h('div', null, h('span', { class: 'mono', style: { fontSize: '16px' } }, u.url), h('span', { class: 'muted small' }, `  (${u.iface})`))))
          : h('p', { class: 'warn-text' }, 'No network connection found on this PC.'),
        h('p', { class: 'muted small' }, `On this PC: ${info.urls.local}. The first time, Windows may ask whether to allow SHELFCODE on private networks — choose Allow.`),
        local ? null : h('p', { class: 'warn-text small' }, 'These settings can only be changed on the main PC.'),
        h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: s.lan_enabled !== false, disabled: !local, onchange: (e) => save({ lan_enabled: e.target.checked }) }), 'Allow other devices'),
        pinField));
    }
  }

  if (tab === 'backup') {
    const list = h('div');
    const load = async () => {
      const r = await api.get('/api/backups');
      clear(list).append(h('p', { class: 'muted small' }, 'Folder: ', h('span', { class: 'mono' }, r.dir)),
        r.backups.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
          h('thead', null, h('tr', null, h('th', null, 'Backup'), h('th', null, 'Made'), h('th', { class: 'num' }, 'Size'), h('th', null, ''))),
          h('tbody', null, r.backups.map(b => h('tr', null, h('td', null, b.reason), h('td', null, b.created), h('td', { class: 'num' }, (b.size / 1048576).toFixed(1) + ' MB'),
            h('td', { class: 'row nowrap', style: { justifyContent: 'flex-end' } },
              h('a', { class: 'btn small', href: `/backup-file/${encodeURIComponent(b.name)}`, download: b.name }, 'Download'),
              h('button', { class: 'btn small danger', onclick: () => restore(b.name) }, 'Restore'))))))) : h('p', { class: 'muted' }, 'No backups yet.'));
    };
    const restore = async (name) => {
      if (!(await confirmDialog('Restore this backup?', 'All current items, photos and settings are replaced with the ones in this backup. Your current data is backed up first, so you can undo this by restoring that backup.', { okLabel: 'Restore', danger: true }))) return;
      try { const r = await api.post('/api/backups/restore', { name }); toast(`Restored: ${r.items} items`); setTimeout(() => location.reload(), 800); }
      catch (e) { toast(e.message, { error: true }); }
    };
    const upload = h('input', { type: 'file', accept: '.zip', hidden: true, onchange: async () => {
      const f = upload.files[0];
      if (!f) return;
      if (!(await confirmDialog('Restore from this file?', `${f.name}: all current data will be replaced. Your current data is backed up first.`, { okLabel: 'Restore', danger: true }))) return;
      const fd = new FormData(); fd.append('file', f);
      try { const r = await api.upload('/api/backups/upload', fd); toast(`Restored: ${r.items} items`); setTimeout(() => location.reload(), 800); }
      catch (e) { toast(e.message, { error: true }); }
    } });
    body.append(
      section('Backups',
        h('p', { class: 'muted' }, 'A backup is one .zip file with the database and all photos. SHELFCODE makes one automatically every day (and before imports, restores and upgrades). To move to another PC, copy the whole SHELFCODE folder, or restore a backup there.'),
        h('div', { class: 'row' },
          h('button', { class: 'btn primary', onclick: async () => { try { const r = await api.post('/api/backups'); toast('Backup saved: ' + r.name); load(); } catch (e) { toast(e.message, { error: true }); } } }, 'Back up now'),
          h('button', { class: 'btn', onclick: () => upload.click() }, 'Restore from a file…'), upload),
        h('div', { class: 'grid2' },
          field('Keep daily backups', numb('backup_keep', { min: 1, max: 365 })),
          field('Also copy backups to (folder on this PC)', text('backup_extra_dir', { placeholder: 'e.g. D:\\Backups or a OneDrive folder', disabled: !app.info?.local }))),
        list));
    await load();
  }

  if (tab === 'about') {
    const info = await api.get('/api/info');
    body.append(section('SHELFCODE',
      h('dl', { class: 'kv' },
        h('dt', null, 'Version'), h('dd', null, info.version),
        h('dt', null, 'Updates'), h('dd', null, info.update),
        h('dt', null, 'Data folder'), h('dd', { class: 'mono' }, info.data_dir),
        h('dt', null, 'Items'), h('dd', null, info.items),
        h('dt', null, 'Database size'), h('dd', null, (info.db_bytes / 1048576).toFixed(2) + ' MB'),
        h('dt', null, 'Running since'), h('dd', null, fmtDate(info.started_at, true))),
      h('p', { class: 'muted small' }, 'Barcodes are drawn with bwip-js (MIT licence).')),
      section('Automatic updates',
        bool('auto_update', 'Install new versions automatically'),
        h('p', { class: 'muted small' }, info.install === 'package'
          ? 'Installed as a .deb/.rpm package: update with apt or dnf.'
          : 'Each time SHELFCODE starts (or restarts), it checks GitHub for a newer release, checks the download, installs it and starts the new version. Your data is kept and backed up before any database change. To update now, restart SHELFCODE.')));
  }
  return {};
}

function printTestPage(key) {
  const paper = key.startsWith('paper:') ? PAPERS[key.slice(6)] : key.startsWith('sheet:') ? PAPERS[SHEETS[key.slice(6)].paper] : PAPERS.letter;
  const c = calibrationFor(key);
  const root = document.getElementById('print-root');
  const sheet = h('div', { class: 'sheet', style: { width: paper.w + 'mm', height: paper.h + 'mm' } });
  const inner = h('div', { style: { position: 'absolute', inset: '0', transformOrigin: '0 0', transform: `translate(${c.x || 0}mm, ${c.y || 0}mm) scale(${(c.scale || 100) / 100})` } },
    h('div', { style: { position: 'absolute', left: '20mm', top: '20mm', width: '100mm', height: '100mm', border: '0.25mm solid #000', fontFamily: 'Arial', fontSize: '9pt', color: '#000', padding: '3mm', boxSizing: 'border-box' } },
      h('div', null, 'SHELFCODE test page'),
      h('div', null, 'This square should be exactly 100 × 100 mm.'),
      h('div', null, 'Its top-left corner should be 20 mm from the top and left edges of the paper.'),
      h('div', null, 'If it is off, enter the difference in Settings → Printing.')),
    ...Array.from({ length: 11 }, (_, i) => h('div', { style: { position: 'absolute', left: 20 + i * 10 + 'mm', top: '15mm', height: '5mm', borderLeft: '0.25mm solid #000' } })),
    ...Array.from({ length: 11 }, (_, i) => h('div', { style: { position: 'absolute', top: 20 + i * 10 + 'mm', left: '15mm', width: '5mm', borderTop: '0.25mm solid #000' } })));
  sheet.appendChild(inner);
  root.className = '';
  root.replaceChildren(sheet);
  const st = h('style', null, `@page { size: ${paper.w}mm ${paper.h}mm; margin: 0; }`);
  document.head.appendChild(st);
  setTimeout(() => { window.print(); setTimeout(() => { root.replaceChildren(); st.remove(); }, 500); }, 50);
}
