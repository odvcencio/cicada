'use strict';
// Annotated screenshot of a running Studio, for pull request evidence.
// usage: node annotate.cjs <studio-url> <out.png> <WxH> <search> [options]
//   --panes LC          library/code panes cookie, two digits (default 10)
//   --click <selector>  click before capturing (repeatable, in order)
//   --step <kind:arg>   click:<sel> | select:<sel>=<value> | fill:<sel>=<text> | key:<key> | hover:<sel> | wait:<ms> | waitfor:<sel>, run in order after the clicks
//   --scroll <selector> scroll this element to the centre before marking (repeatable)
//   --wait <ms>         extra settle time (default 700)
//   --title <text>      caption, drawn with the numbered legend in a band under the capture
//   --mark "<selector>|<label>"  outline the first match and number it (repeatable)
// Marks that match nothing are reported on stderr and skipped.
const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
(async () => {
  const [base, out, size, search, ...rest] = process.argv.slice(2);
  const [w, h] = size.split('x').map(Number);
  const opt = {panes: '10', click: [], step: [], scroll: [], wait: 700, title: '', mark: [], clip: ''};
  for (let i = 0; i < rest.length; i += 2) {
    const k = rest[i].replace(/^--/, ''), v = rest[i + 1];
    if (Array.isArray(opt[k])) opt[k].push(v); else opt[k] = k === 'wait' ? Number(v) : v;
  }
  const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH || undefined, args: ['--mute-audio']});
  const context = await browser.newContext({viewport: {width: w, height: h}});
  const startup = new URL(base);
  if (startup.pathname.startsWith('/__local/')) { const p = await context.newPage(); await p.goto(startup.href); await p.close(); }
  await context.addCookies([{name: 'cicada_panes', value: opt.panes, url: startup.origin}]);
  const page = await context.newPage();
  await page.addInitScript(open => { try { localStorage.setItem('studio:layout:codeOpen', JSON.stringify(open)); } catch {} }, opt.panes[1] === '1');
  const target = new URL('/studio', startup); target.search = search;
  await page.goto(target.href);
  await page.locator('#studio-shell[data-shell-actions="ready"]').waitFor({timeout: 20000}).catch(() => console.error('shell not ready'));
  await page.waitForTimeout(opt.wait);
  for (const sel of opt.click) { try { await page.locator(sel).first().click({timeout: 5000}); await page.waitForTimeout(400); } catch (e) { console.error('click failed: ' + sel); } }
  for (const st of opt.step) {
    const i = st.indexOf(':'), k = st.slice(0, i), v = st.slice(i + 1), j = v.lastIndexOf('=');
    try {
      if (k === 'click') await page.locator(v).first().click({timeout: 5000});
      else if (k === 'select') await page.locator(v.slice(0, j)).first().selectOption(v.slice(j + 1));
      else if (k === 'fill') await page.locator(v.slice(0, j)).first().fill(v.slice(j + 1));
      else if (k === 'key') await page.keyboard.press(v);
      else if (k === 'hover') await page.locator(v).first().hover();
      else if (k === 'wait') await page.waitForTimeout(Number(v));
      else if (k === 'waitfor') await page.locator(v).first().waitFor({timeout: 10000});
      await page.waitForTimeout(250);
    } catch (e) { console.error('step failed: ' + st + ' — ' + e.message.split('\n')[0]); }
  }
  for (const sel of opt.scroll) { try { await page.locator(sel).first().evaluate(el => el.scrollIntoView({block: 'center', inline: 'center'})); await page.waitForTimeout(250); } catch (e) { console.error('scroll failed: ' + sel); } }
  const marks = opt.mark.map(m => { const i = m.indexOf('|'); return {selector: m.slice(0, i), label: m.slice(i + 1)}; });
  const {missing, legend} = await page.evaluate(({marks, title}) => {
    const missing = [], legend = [];
    const layer = document.createElement('div');
    layer.style.cssText = 'position:fixed;inset:0;pointer-events:none;z-index:2147483647;font:600 12px system-ui,sans-serif';
    let n = 0;
    for (const m of marks) {
      const el = document.querySelector(m.selector);
      if (!el) { missing.push(m.selector); continue; }
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) { missing.push(m.selector + ' (no box)'); continue; }
      if (r.bottom < 0 || r.top > innerHeight || r.right < 0 || r.left > innerWidth) { missing.push(m.selector + ' (off screen)'); continue; }
      n++;
      const box = document.createElement('div');
      box.style.cssText = `position:fixed;left:${Math.max(1, r.left - 3)}px;top:${Math.max(1, r.top - 3)}px;width:${Math.min(innerWidth - 2, r.width + 6)}px;height:${Math.min(innerHeight - 2, r.height + 6)}px;border:2px solid #ff4d8d;border-radius:5px;box-shadow:0 0 0 1px rgba(0,0,0,.6)`;
      const badge = document.createElement('div');
      badge.textContent = String(n);
      badge.style.cssText = `position:fixed;left:${Math.max(1, Math.min(innerWidth - 24, r.right - 15))}px;top:${Math.max(1, r.top - 12)}px;min-width:18px;height:18px;line-height:18px;text-align:center;background:#ff4d8d;color:#10131a;border-radius:9px;padding:0 4px`;
      layer.append(box, badge);
      legend.push(n + '  ' + m.label);
    }
    document.body.append(layer);
    return {missing, legend};
  }, {marks, title: opt.title});
  for (const m of missing) console.error('no match: ' + m);
  await page.waitForTimeout(150);
  // The legend goes in a band under the capture, so it never covers the UI.
  const shot = (await page.screenshot()).toString('base64');
  const sheet = await context.newPage();
  await sheet.setViewportSize({width: w, height: 200});
  const esc = t => t.replace(/&/g, '&amp;').replace(/</g, '&lt;');
  const rows = legend.map(l => { const i = l.indexOf('  '); return `<div class="row"><span class="n">${l.slice(0, i)}</span><span>${esc(l.slice(i + 2))}</span></div>`; }).join('');
  await sheet.setContent(`<!doctype html><meta charset="utf-8"><style>
    body{margin:0;background:#10131a;font:500 ${w < 600 ? 12 : 13}px/1.45 system-ui,sans-serif;color:#f4f6fa}
    img{display:block;width:${w}px}
    .legend{border-top:3px solid #ff4d8d;padding:8px 12px 10px}
    .title{font-weight:700;color:#ffd9e6;margin-bottom:4px}
    .row{display:flex;gap:8px;align-items:baseline;margin-top:3px}
    .n{flex:none;min-width:18px;height:18px;line-height:18px;text-align:center;background:#ff4d8d;color:#10131a;border-radius:9px;font-weight:700;font-size:12px;padding:0 4px;box-sizing:border-box}
  </style><img src="data:image/png;base64,${shot}"><div class="legend"><div class="title">${esc(opt.title)}</div>${rows}</div>`);
  await sheet.locator('img').evaluate(img => img.decode());
  await sheet.screenshot({path: out, fullPage: true});
  await browser.close();
})().catch(e => { console.error(e.message); process.exit(1); });
