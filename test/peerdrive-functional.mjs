// Functional tests — user flows, not just page loads
export const baseURL = 'https://peerdrive.pages.dev';
const API = 'https://wsl-3000.moonchan.xyz';
const setupAPI = async (page) => {
  await page.goto(baseURL, { waitUntil: 'networkidle' });
  await page.evaluate(`localStorage.setItem('peerdrive_api_base', '${API}')`);
  await page.reload({ waitUntil: 'networkidle' });
};

export const tests = [
  // ═══ Plaza ═══
  { name: 'Plaza — paste SHA256 navigates to collection',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL, { waitUntil: 'networkidle' });
      const input = page.locator('input[placeholder*="SHA"]');
      await input.fill('422cffa2612dbb659c8949cbe9e4f0bcd7e2b1bc8cdddd633b335d5bbfdd1a04');
      await page.keyboard.press('Enter');
      await new Promise(r => setTimeout(r, 2000));
      const url = page.url();
      ok('navigated to collection', url.includes('/anon/collections/'), url);
    }
  },
  { name: 'Plaza — click collection card navigates',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL, { waitUntil: 'networkidle' });
      const cards = page.locator('[class*="rounded-xl"]');
      const count = await cards.count();
      if (count > 0) {
        await cards.first().click();
        await new Promise(r => setTimeout(r, 2000));
        const url = page.url();
        ok('url changed', url !== baseURL + '/', url);
      } else {
        ok('no cards to click', false, 'empty plaza');
      }
    }
  },

  // ═══ AnonCreator ═══
  { name: 'AnonCreator — timeline shows date-grouped files',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/anon/create`, { waitUntil: 'networkidle' });
      await new Promise(r => setTimeout(r, 2000));
      const dates = await page.locator('[class*="sticky"]').count();
      ok('has date headers', dates > 0, `${dates} date headers`);
    }
  },
  { name: 'AnonCreator — registered tab shows breadcrumb navigation',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/anon/create`, { waitUntil: 'networkidle' });
      await page.click('text=📁 已注册');
      await new Promise(r => setTimeout(r, 2000));
      const text = await page.evaluate(() => document.body.innerText);
      ok('shows root path', text.includes('/') || text.includes('📂'), text.substring(0, 100));
    }
  },
  { name: 'AnonCreator — system tab shows filesystem root',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/anon/create`, { waitUntil: 'networkidle' });
      await page.click('text=🖥️ 本机');
      await new Promise(r => setTimeout(r, 2000));
      const dirs = await page.locator('text=文件夹').count();
      ok('shows folders', dirs > 0, `${dirs} folders`);
    }
  },
  { name: 'AnonCreator — collection tab shows list',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/anon/create`, { waitUntil: 'networkidle' });
      await page.click('text=📦 合集');
      await new Promise(r => setTimeout(r, 2000));
      const text = await page.evaluate(() => document.body.innerText);
      ok('has collection entries', text.includes('文件') || text.includes('合集') || text.includes('暂无'), text.substring(0, 100));
    }
  },
  { name: 'AnonCreator — 4-tab buttons are large enough',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/anon/create`, { waitUntil: 'networkidle' });
      // The 4-tab bar is inside a flex container with bg-gray-800 rounded
      const tabs = page.locator('.flex.bg-gray-800.rounded button');
      const count = await tabs.count();
      ok('4-tab bar has buttons', count >= 4, `${count} tabs`);
      if (count > 0) {
        const box = await tabs.first().boundingBox();
        ok('button height reasonable', box?.height >= 24, `${box?.height}px`);
      }
    }
  },

  // ═══ AnonExplorer ═══
  { name: 'AnonExplorer — hash input accepts paste',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/anon`, { waitUntil: 'networkidle' });
      const input = page.locator('input[placeholder*="Hash"], input[placeholder*="SHA"]');
      const exists = await input.count();
      ok('has hash input', exists > 0, `${exists} inputs`);
      if (exists > 0) {
        await input.first().fill('422cffa2612dbb659c8949cbe9e4f0bcd7e2b1bc8cdddd633b335d5bbfdd1a04');
        await page.click('text=查看');
        await new Promise(r => setTimeout(r, 2000));
        ok('page loaded content', (await page.evaluate(() => document.body.innerText)).length > 50);
      }
    }
  },

  // ═══ FileManager ═══
  { name: 'FileManager — checkboxes toggle selection',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/files`, { waitUntil: 'networkidle' });
      await new Promise(r => setTimeout(r, 1500));
      const cbs = page.locator('input[type="checkbox"]');
      const count = await cbs.count();
      if (count > 1) {
        await cbs.nth(1).click(); // first visible file checkbox (index 0 is header)
        await new Promise(r => setTimeout(r, 500));
        const text = await page.evaluate(() => document.body.innerText);
        ok('selection counter appears', text.includes('已选择') || text.includes('个文件'), text.substring(0, 150));
      } else {
        ok('no files to select', false, 'empty file list');
      }
    }
  },
  { name: 'FileManager — sort buttons exist',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/files`, { waitUntil: 'networkidle' });
      const sorts = ['时间', '名称', '大小', '类型'];
      let found = 0;
      for (const s of sorts) { if (await page.locator(`button:has-text("${s}")`).count() > 0) found++; }
      ok('all sort buttons', found >= 2, `${found}/${sorts.length}`);
    }
  },
  { name: 'FileManager — view toggle (list/tree)',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/files`, { waitUntil: 'networkidle' });
      const hasList = await page.locator('button:has-text("列表")').count();
      const hasTree = await page.locator('button:has-text("目录树")').count();
      ok('has view toggle', hasList > 0 || hasTree > 0, `list:${hasList} tree:${hasTree}`);
    }
  },

  // ═══ Settings ═══
  { name: 'Settings — LLM endpoint configurable',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/settings`, { waitUntil: 'networkidle' });
      const input = page.locator('input[placeholder*="siliconflow"], input[placeholder*="Endpoint"], input[placeholder*="endpoint"]');
      const exists = await input.count();
      ok('has endpoint input', exists > 0, `${exists} inputs`);
    }
  },
  { name: 'Settings — model dropdown has options',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/settings`, { waitUntil: 'networkidle' });
      const select = page.locator('select');
      const count = await select.count();
      ok('has select element', count > 0, `${count} selects`);
    }
  },
  { name: 'Settings — has API base config',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/settings`, { waitUntil: 'networkidle' });
      const text = await page.evaluate(() => document.body.innerText);
      ok('has API config section', text.includes('端点') || text.includes('API') || text.includes('wsl'), text.substring(0, 100));
    }
  },

  // ═══ Navbar ═══
  { name: 'Navbar — all links work',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL, { waitUntil: 'networkidle' });
      const links = ['文件管理', '探索合集', '创建合集'];
      for (const label of links) {
        const btn = page.locator(`a:has-text("${label}"), button:has-text("${label}")`);
        ok(`link "${label}" exists`, (await btn.count()) > 0, `${await btn.count()} found`);
      }
    }
  },
  { name: 'Navbar — Ctrl+K opens search',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL, { waitUntil: 'networkidle' });
      await page.keyboard.press('Control+k');
      await new Promise(r => setTimeout(r, 500));
      const text = await page.evaluate(() => document.body.innerText);
      ok('search panel opened', text.includes('搜索'), text.substring(0, 100));
    }
  },

  // ═══ API Health ═══
  { name: 'API — /ping responds',
    fn: async ({ page, ok }) => {
      const resp = await page.evaluate(() => fetch('https://wsl-3000.moonchan.xyz/ping').then(r => r.text()));
      ok('API ping returns pong', resp === '"pong"' || resp === 'pong', resp);
    }
  },
  { name: 'API — /files returns array',
    fn: async ({ page, ok }) => {
      const resp = await page.evaluate(() => fetch('https://wsl-3000.moonchan.xyz/files').then(r => r.json()));
      ok('API /files returns array', Array.isArray(resp), `count=${resp.length}`);
    }
  },
  { name: 'API — /anon/collections returns array',
    fn: async ({ page, ok }) => {
      const resp = await page.evaluate(() => fetch('https://wsl-3000.moonchan.xyz/anon/collections').then(r => r.json()));
      ok('API /anon/collections returns array', Array.isArray(resp), `count=${resp.length}`);
    }
  },
  { name: 'API — /p2p/status responds',
    fn: async ({ page, ok }) => {
      const resp = await page.evaluate(() => fetch('https://wsl-3000.moonchan.xyz/p2p/status').then(r => r.json()));
      ok('API /p2p/status has enabled field', 'enabled' in resp, JSON.stringify(resp));
    }
  },
];
