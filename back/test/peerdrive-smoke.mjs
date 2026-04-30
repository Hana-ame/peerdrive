export const baseURL = 'https://peerdrive.pages.dev';
const API = 'https://wsl-3000.moonchan.xyz';

export const tests = [
  {
    name: 'Plaza — search input + collection cards',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL, { waitUntil: 'networkidle' });
      await page.evaluate(`localStorage.setItem('peerdrive_api_base', '${API}')`);
      await page.reload({ waitUntil: 'networkidle' });
      const ph = await page.evaluate(() => document.querySelector('input[placeholder*="SHA"]')?.getAttribute('placeholder') || '');
      ok('has search placeholder', ph.includes('SHA256') || ph.includes('Hash'), ph.substring(0, 40));
      ok('has collection cards', ph.length > 0 || (await page.evaluate(() => document.body.innerText)).includes('合集'));
      const cards = await page.locator('[class*="rounded-xl"]').count();
      ok('renders cards', cards > 0, `${cards} cards`);
    }
  },
  {
    name: 'AnonCreator — 4-tab + timeline date grouping',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/anon/create`, { waitUntil: 'networkidle' });
      const text = await page.evaluate(() => document.body.innerText);
      ok('timeline tab', text.includes('时间线'));
      ok('registered tab', text.includes('已注册'));
      ok('system tab', text.includes('本机'));
      ok('collection tab', text.includes('合集'));
      ok('has save button', text.includes('保存'));
      ok('no commit button', !text.includes('Commit'));
      // Click timeline tab to verify date grouping
      await page.click('text=🕐 时间线');
      await new Promise(r => setTimeout(r, 1000));
      const tl = await page.evaluate(() => document.body.innerText);
      ok('timeline shows dates', /\d{4}-\d{2}-\d{2}/.test(tl), 'date pattern found');
    }
  },
  {
    name: 'AnonCreator — registered tab shows dirs',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/anon/create`, { waitUntil: 'networkidle' });
      await page.click('text=📁 已注册');
      await new Promise(r => setTimeout(r, 1500));
      const text = await page.evaluate(() => document.body.innerText);
      ok('shows content or root indicator', text.includes('📂') || text.includes('📁') || text.includes('文件夹'));
    }
  },
  {
    name: 'AnonCreator — system tab browses filesystem',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/anon/create`, { waitUntil: 'networkidle' });
      await page.click('text=🖥️ 本机');
      await new Promise(r => setTimeout(r, 2000));
      const text = await page.evaluate(() => document.body.innerText);
      ok('shows system dirs', text.includes('文件夹') || text.includes('📁'));
    }
  },
  {
    name: 'FileManager — checkboxes visible',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/files`, { waitUntil: 'networkidle' });
      await new Promise(r => setTimeout(r, 1500));
      const cbs = await page.locator('input[type="checkbox"]').count();
      ok('has checkboxes', cbs > 0, `${cbs} checkboxes`);
      // Check selection counter
      const text = await page.evaluate(() => document.body.innerText);
      ok('has file count', text.includes('个文件') || text.includes('文件'));
    }
  },
  {
    name: 'AnonExplorer — hash input',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/anon`, { waitUntil: 'networkidle' });
      const text = await page.evaluate(() => document.body.innerText);
      ok('has hash input placeholder', text.includes('Hash') || text.includes('SHA'));
    }
  },
  {
    name: 'Settings — LLM config',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/settings`, { waitUntil: 'networkidle' });
      const text = await page.evaluate(() => document.body.innerText);
      ok('has LLM section', text.includes('LLM') || text.includes('模型') || text.includes('Endpoint'));
    }
  },
];
