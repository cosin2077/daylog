// Run with the existing Playwright tool against report-demo's report.html and
// failure.html (--failure), served from the same local directory. No packages
// or user data are required. Clipboard success/failure is deliberately mocked.
async (page) => {
  const assert = (ok, message) => { if (!ok) throw new Error(message); };
  const reportURL = new URL('report.html', page.url()).href;
  const failureURL = new URL('failure.html', page.url()).href;
  const errors = [];
  const onError = error => errors.push(error.message);
  page.on('pageerror', onError);
  const results = [];
  // Category filter is a custom listbox: open the button, pick the option by value.
  const pickCategory = async value => { await page.locator('#category-button').click(); await page.locator(`#category-list [role=option][data-value="${value}"]`).click(); };
  const category = () => page.locator('#category-filter').getAttribute('data-value');
  try {
    await page.goto(reportURL);
    assert(await page.locator('#report-data').evaluate(n => JSON.parse(n.textContent).sample === true), 'Only synthetic fixtures may be tested');
    await page.setViewportSize({ width: 1280, height: 900 });
    const synthetic = await page.locator('#report-data').evaluate(n => JSON.parse(n.textContent));
    const daily = synthetic.views.find(v => v.period === 'day');
    assert(daily.work_time.estimated_seconds > daily.estimated_seconds, 'Fixture has no unclassified computer activity');
    assert(daily.work_time.classified_seconds + daily.work_time.app_attributed_seconds + daily.work_time.other_unclassified_seconds === daily.work_time.estimated_seconds, 'Work breakdown does not add up');
    assert((await page.locator('#work-label').innerText()) === '电脑工作估时', 'Headline still labels task estimates as total time');
    assert(await page.locator('#data-explain').evaluate(n => !n.hidden && !n.open), 'Data notes should start as one collapsed line');
    await page.locator('#data-explain > .disc-btn').click();
    assert((await page.locator('#work-breakdown').innerText()).includes('仅应用'), 'Application-only time hidden from the summary');
    await page.locator('#records-summary').click();
    await page.locator('#search').fill('不匹配的查询');
    await pickCategory('写代码');
    await page.locator('#work-unclassified').focus();
    await page.keyboard.press('Enter');
    assert(await page.locator('#search').inputValue() === '' && await category() === '全部', 'Unclassified drilldown retained filters');
    assert((await page.locator('#page-info').innerText()).startsWith((daily.work_time.app_spans.length + (daily.work_time.other_unclassified_spans||[]).length) + '项'), 'Unclassified drilldown lost timing intervals');
    assert((await page.locator('#filters-note').innerText()).includes('已计入工作估时'), 'Unclassified rows do not explain their inclusion');
    assert(await page.locator('#records-summary').evaluate(n => n === document.activeElement), 'Unclassified navigation lost keyboard focus');
    await page.getByRole('tab', { name: '周', exact: true }).click();
    assert(await page.locator('#filters-note').isHidden(), 'Period navigation retained unclassified mode');
    await page.getByRole('tab', { name: '日', exact: true }).click();
    if (await page.locator('#records').evaluate(n => n.open)) await page.locator('#records-summary').click();
    results.push('Work total and breakdown, unclassified keyboard drilldown, filter reset and period reset');
    const taskTitle = '讨论日／周／月报告的展示';
    const task = page.locator('.task-row').filter({ hasText: taskTitle });
    await page.locator('#records-summary').click();
    await pickCategory('写代码');
    await task.click();
    assert(await category() === '全部', 'Task navigation retained category');
    assert((await page.locator('#page-info').innerText()).startsWith('1项'), 'Task navigation lost its record');
    assert((await page.locator('#filters-note').innerText()).includes(taskTitle), 'Selected task is unnamed');
    assert(await page.locator('#records-summary').evaluate(n => n === document.activeElement), 'Keyboard focus did not follow task navigation');
    await page.locator('#search').fill('不匹配的查询');
    assert(await page.locator('#records-empty').isVisible(), 'Manual search did not intersect task');
    await task.click();
    assert(await page.locator('#search').inputValue() === '', 'Task navigation retained search');
    assert((await page.locator('#page-info').innerText()).startsWith('1项'), 'Search reset still gives empty task');
    await pickCategory('写代码');
    assert(await page.locator('#records-empty').isVisible(), 'Manual category did not intersect task');
    assert((await page.locator('#filters-note').innerText()).includes('匹配 0项'), 'Filtered task count missing');
    await page.locator('#clear').click();
    assert(await page.locator('#filters-note').isHidden(), 'Clear retained task state');
    assert((await page.locator('#page-info').innerText()).startsWith('19项'), 'Clear did not restore records');
    await task.click();
    await page.getByRole('tab', { name: '周', exact: true }).click();
    assert(await page.locator('#filters-note').isHidden(), 'Period navigation retained task');
    await page.getByRole('tab', { name: '日', exact: true }).click();
    await task.click();
    await page.locator('#date-prev').click();
    assert(await page.locator('#filters-note').isHidden(), 'Date navigation retained task');
    assert(await page.locator('#preparation-state').isHidden(), 'Quiet period shows unnecessary status warning');
    assert(await page.locator('#show-failures').isHidden(), 'Quiet period shows recovery action');
    await page.locator('#date-next').click();
    results.push('UX-1 category/search reset, intersections, count/name, clear/date/period and keyboard focus');

    assert((await page.locator('#snapshot-time').innerText()).includes('离线快照'), 'Snapshot identity hidden');
    assert((await page.locator('#preparation-text').innerText()).includes('1段待整理'), 'Pending state hidden');
    assert(await page.locator('#show-failures').isHidden(), 'Normal pending report shows failure action');
    await page.goto(failureURL);
    assert(await page.locator('#about').evaluate(n => !n.open), 'About starts expanded');
    assert((await page.locator('#preparation-text').innerText()).includes('其中1段失败'), 'Failure count missing above fold');
    await page.locator('#show-failures').click();
    assert(await page.locator('#about').evaluate(n => n.open), 'Failure jump did not expand about');
    assert(await page.locator('#failures-heading').evaluate(n => n === document.activeElement), 'Failure jump lost keyboard focus');
    const retry = await page.locator('#failures code').innerText();
    assert(/^daylog analyze --batch [A-Za-z0-9._:-]+ --retry$/.test(retry), 'Unsafe or incorrect retry command');
    await page.evaluate(() => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async text => { window.__testCopied = text; } } }));
    await page.getByRole('button', { name: '复制重试命令', exact: true }).click();
    await page.getByText('已复制，尚未执行。', { exact: true }).waitFor();
    assert(await page.evaluate(() => window.__testCopied) === retry, 'Copy payload differs from displayed command');
    await page.evaluate(() => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async () => { throw new Error('fixture clipboard denied'); } } }));
    await page.getByRole('button', { name: '复制重试命令', exact: true }).click();
    await page.getByText('无法自动复制，请选中上方命令手动复制；尚未执行。', { exact: true }).waitFor();
    assert(await page.locator('#failures code').isVisible(), 'Denied clipboard hid selectable command');
    await page.locator('#refresh-guide > .disc-btn').click();
    const generated = await page.locator('#refresh-command code').innerText();
    assert(/^daylog report --date \d{4}-\d{2}-\d{2} --period day$/.test(generated), 'Regeneration lost selected date');
    await page.getByRole('tab', { name: '月', exact: true }).click();
    assert((await page.locator('#refresh-command code').innerText()).endsWith('--period month'), 'Regeneration lost period');
    results.push('UX-2 visible pending/failure/snapshot, focus jump, selectable commands and mocked clipboard success/denial');

    await page.goto(reportURL);
    await page.setViewportSize({ width: 375, height: 812 });
    const disclosure = page.locator('#rhythm-details > .disc-btn');
    await disclosure.focus();
    await page.keyboard.press('Enter');
    assert(await page.locator('#rhythm-details').evaluate(n => n.open), 'Timeline inaccessible from keyboard');
    const list = page.locator('.timeline-list');
    const descriptions = await list.innerText();
    assert(descriptions.includes('09:00–09:12') && descriptions.includes('写代码'), 'Ordinary interval lacks description');
    assert(descriptions.includes('含邻接推断'), 'Inferred interval is not identified');
    assert(descriptions.includes('单帧活动 1') && descriptions.includes('时长未估'), 'Single-frame point lacks equivalent text');
    assert(await list.locator('li').count() === await page.locator('.rhythm-part,.rhythm-point').count(), 'Some timeline marks lack descriptions');
    assert(await page.locator('.rhythm-part,.rhythm-point').evaluateAll(ns => ns.every(n => n.tabIndex < 0)), 'Tiny marks created excessive tab stops');
    await disclosure.click();
    assert(await page.locator('#rhythm-details').evaluate(n => !n.open), 'Touch/click disclosure cannot close');
    await page.evaluate(() => window.dispatchEvent(new Event('beforeprint')));
    await page.emulateMedia({ media: 'print' });
    assert(await page.locator('#rhythm-details').evaluate(n => n.open), 'Print hides timeline descriptions');
    assert(await list.isVisible(), 'Timeline list is invisible in print');
    await page.emulateMedia({ media: 'screen' });
    await page.evaluate(() => window.dispatchEvent(new Event('afterprint')));
    assert(await page.locator('#rhythm-details').evaluate(n => !n.open), 'Print did not restore collapsed state');
    results.push('UX-3 keyboard/click disclosure, ordinary/inferred/single-frame text, complete mark coverage and print restoration');


    // Header: project link is a plain new-tab navigation that carries no report data.
    const gh = await page.locator('#gh-link').evaluate(a => ({ href: a.getAttribute('href'), rel: a.rel, target: a.target, label: a.getAttribute('aria-label') }));
    assert(gh.href === 'https://github.com/cosin2077/daylog' && gh.rel === 'noopener noreferrer' && gh.target === '_blank' && gh.label, 'GitHub link changed: ' + JSON.stringify(gh));
    assert(await page.locator('link[rel=icon]').evaluate(n => n.getAttribute('href').startsWith('data:image/svg+xml')), 'Favicon must be an inline data URI');
    // Share card: spy on canvas text to prove the default card has no task titles or app names.
    await page.evaluate(() => { window.__drawn = []; const f = CanvasRenderingContext2D.prototype.fillText; CanvasRenderingContext2D.prototype.fillText = function (t, ...r) { window.__drawn.push(String(t)); return f.call(this, t, ...r); }; });
    const shareReady = () => page.waitForFunction(() => !document.getElementById('share-save').disabled);
    await page.locator('#share-btn').click();
    await shareReady();
    assert(await page.locator('#share-close').evaluate(n => n === document.activeElement), 'Share dialog did not take focus');
    assert(await page.locator('#share-titles').getAttribute('aria-checked') === 'false' && await page.locator('#share-shots').getAttribute('aria-checked') === 'false', 'Share opt-ins must default to off');
    const secret = synthetic.views.find(v => v.period === 'day');
    const titles = [...new Set([...(secret.activities || []).map(a => a.title), ...(secret.tasks || []).map(t => t.title)].filter(t => t && t.length > 3))];
    const appNames = [...new Set((secret.work_time.app_spans || []).map(s => s.name).filter(n => n && n.length > 2))];
    const drawnDefault = await page.evaluate(() => window.__drawn.join('\n'));
    assert(drawnDefault.includes('github.com/cosin2077/daylog'), 'Share card footer missing');
    assert(!titles.some(t => drawnDefault.includes(t.slice(0, 8))), 'Default share card drew a task title');
    assert(!appNames.some(n => drawnDefault.includes(n)), 'Default share card drew an app name');
    const canvasInfo = () => page.locator('#share-canvas').evaluate(c => new Promise(res => { const x = c.getContext('2d').getImageData(0, 0, c.width, c.height).data; let ink = 0; for (let i = 0; i < x.length; i += 4 * 97) if (x[i] < 200) ink++; c.toBlob(b => res({ w: c.width, h: c.height, size: b ? b.size : 0, ink, url: c.toDataURL().length })); }));
    const plain = await canvasInfo();
    assert(plain.w === 1080 && plain.h > 900 && plain.size > 20000 && plain.ink > 500, 'Share image blank or wrong size: ' + JSON.stringify(plain));
    const [download] = await Promise.all([page.waitForEvent('download'), page.locator('#share-save').click()]);
    assert(/^daylog-\d{4}-\d\d-\d\d\.png$/.test(download.suggestedFilename()), 'Unexpected share file name ' + download.suggestedFilename());
    await page.evaluate(() => { window.__copied = null; Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { write: async items => { window.__copied = items[0].types; } } }); });
    await page.locator('#share-copy').click();
    await page.waitForFunction(() => window.__copied);
    assert((await page.evaluate(() => window.__copied)).includes('image/png'), 'Copy did not write a PNG');
    await page.evaluate(() => { window.__drawn = []; });
    await page.locator('#share-titles').click();
    await page.locator('#share-shots').click();
    await shareReady();
    await page.waitForFunction(() => document.getElementById('share-meta').textContent.includes('×'));
    const optIn = await canvasInfo();
    assert(optIn.h > plain.h && optIn.size > plain.size, 'Opt-in titles/screenshots not drawn: ' + JSON.stringify(optIn));
    const drawnOptIn = await page.evaluate(() => window.__drawn.join('\n'));
    assert(titles.some(t => drawnOptIn.includes(t.slice(0, 6))), 'Title opt-in did not draw any title');
    for (let i = 0; i < 12; i++) await page.keyboard.press('Tab');
    assert(await page.evaluate(() => document.getElementById('share').contains(document.activeElement)), 'Focus escaped the share dialog');
    await page.keyboard.press('Escape');
    assert(await page.locator('#share').isHidden() && await page.locator('#share-btn').evaluate(n => n === document.activeElement), 'Esc did not close share or restore focus');
    await page.locator('#share-btn').click();
    await shareReady();
    assert(await page.locator('#share-titles').getAttribute('aria-checked') === 'false', 'Opt-ins persisted across openings');
    await page.locator('#share-close').click();
    results.push('GitHub link and favicon, share dialog focus/Esc, PNG size and no taint, download name, copy, privacy defaults (no titles/app names drawn), opt-ins');

    for (const width of [375, 320]) {
      await page.setViewportSize({ width, height: 812 });
      const bar = await page.evaluate(() => { const t = document.querySelector('.topbar').getBoundingClientRect(); return { h: t.height, right: Math.max(...[...document.querySelectorAll('.topbar *')].filter(n => n.getClientRects().length).map(n => n.getBoundingClientRect().right)), vw: innerWidth }; });
      assert(bar.h <= 50 && bar.right <= bar.vw - 8, 'Top bar wraps or overflows at ' + width + ': ' + JSON.stringify(bar));
      await page.locator('#share-btn').click();
      await page.waitForFunction(() => !document.getElementById('share-save').disabled);
      await page.waitForTimeout(300); // let the open animation settle before measuring
      const sheet = await page.evaluate(() => ({ w: document.querySelector('.share-dlg').getBoundingClientRect().width, vw: innerWidth, sw: document.documentElement.scrollWidth, btn: Math.min(...[...document.querySelectorAll('.share-actions button:not([hidden]),#share-close')].map(b => b.getBoundingClientRect().height)) }));
      assert(sheet.w === sheet.vw && sheet.sw <= sheet.vw && sheet.btn >= 44, 'Share sheet not full-width or targets too small: ' + JSON.stringify(sheet));
      await page.keyboard.press('Escape');
      await disclosure.click();
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Timeline overflows mobile viewport');
      await disclosure.click();
      await page.goto(failureURL);
      await page.locator('#show-failures').click();
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Failure commands overflow mobile viewport');
      await page.goto(reportURL);
    }
    // Exercise the range-only branch with synthetic data in the same template.
    const sourceHTML = await (await page.request.get(reportURL)).text();
    const dataPattern = /(<script type="application\/json" id="report-data">)([\s\S]*?)(<\/script>)/;
    const rangeData = JSON.parse(sourceHTML.match(dataPattern)[2]);
    const range = { ...rangeData.views.find(v => v.period === 'day'), period: 'range' };
    rangeData.views = [range]; rangeData.calendar = []; rangeData.periods = [];
    const rangeHTML = sourceHTML.replace('data-initial="day"', 'data-initial="range"').replace(dataPattern, (_, a, b, c) => a + JSON.stringify(rangeData).replace(/</g, '\\u003c') + c);
    const rangeURL = new URL('synthetic-range.html', reportURL).href;
    const rangeRoute = route => route.fulfill({ contentType: 'text/html', body: rangeHTML });
    await page.route(rangeURL, rangeRoute);
    try {
      await page.goto(rangeURL);
      await page.locator('#refresh-guide > .disc-btn').click();
      assert(await page.locator('#refresh-command code').innerText() === 'daylog report --from ' + range.start + ' --to ' + range.end, 'Range regeneration lost exact boundaries');
      await page.locator('#rhythm-details > .disc-btn').click();
      assert((await page.locator('.timeline-list').innerText()).includes(range.start.slice(0, 10)), 'Range timeline lacks date context');
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Range command overflows mobile viewport');
    } finally {
      await page.unroute(rangeURL, rangeRoute);
      await page.goto(reportURL);
    }
    results.push('Synthetic range regeneration boundaries and dated timeline descriptions');
    assert(errors.length === 0, errors.join('\n'));
    results.push('375px/320px layout, single-row top bar, full-width share sheet and no page errors');
    return { passed: results, clipboard: 'mocked; no real retry/command executed' };
  } finally {
    await page.emulateMedia({ media: 'screen' });
    page.off('pageerror', onError);
  }
}
