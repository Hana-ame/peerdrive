export const baseURL = 'https://peerdrive.pages.dev';

export const tests = [
  // ── P2P Panel ──
  {
    name: 'P2P Panel — dual-stack page loads',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/p2p`, { waitUntil: 'networkidle' });
      const text = await page.evaluate(() => document.body.innerText);
      ok('page renders', text.length > 100, `${text.length} chars`);
      ok('has P2P content', text.includes('P2P') || text.includes('IPFS') || text.includes('BT') || text.includes('节点'));
    }
  },
  {
    name: 'P2P Panel — IPFS sub-page loads',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/p2p/ipfs`, { waitUntil: 'networkidle' });
      const text = await page.evaluate(() => document.body.innerText);
      ok('IPFS page renders', text.includes('IPFS') || text.includes('libp2p') || text.includes('Peer') || text.includes('节点') || text.includes('地址'));
    }
  },
  {
    name: 'P2P Panel — BT DHT sub-page loads',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/p2p/bt`, { waitUntil: 'networkidle' });
      const text = await page.evaluate(() => document.body.innerText);
      ok('BT page renders', text.includes('BT') || text.includes('DHT') || text.includes('BitTorrent') || text.includes('节点'));
    }
  },
  // ── P2P Dashboard (legacy) ──
  {
    name: 'P2P Dashboard — legacy page loads',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/p2p/legacy`, { waitUntil: 'networkidle' });
      const text = await page.evaluate(() => document.body.innerText);
      ok('legacy dashboard renders', text.includes('P2P') || text.includes('仪表') || text.includes('节点'));
    }
  },
  // ── Navbar P2P dropdown ──
  {
    name: 'Navbar — P2P dropdown exists',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}`, { waitUntil: 'networkidle' });
      const navbar = await page.evaluate(() => {
        const links = Array.from(document.querySelectorAll('a, button'));
        return links.filter(l => l.textContent.includes('P2P') || l.textContent.includes('IPFS') || l.textContent.includes('BT')).length;
      });
      ok('has P2P nav links', navbar > 0, `${navbar} P2P links`);
    }
  },
  // ── Mobile PWA ──
  {
    name: 'PWA — manifest exists',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}`, { waitUntil: 'networkidle' });
      const manifest = await page.evaluate(() => {
        const link = document.querySelector('link[rel="manifest"]');
        return link ? link.href : null;
      });
      ok('has manifest link', manifest !== null, manifest);
    }
  },
  {
    name: 'PWA — mobile nav visible on small screen',
    fn: async ({ page, ok }) => {
      await page.setViewportSize({ width: 375, height: 812 });
      await page.goto(`${baseURL}`, { waitUntil: 'networkidle' });
      const hasMobileNav = await page.evaluate(() => {
        return document.body.innerText.includes('Home') || document.body.innerText.includes('首页') || document.body.innerText.includes('文件');
      });
      ok('mobile layout works', hasMobileNav);
    }
  },
  // ── WebRTC Transfer component ──
  {
    name: 'WebRTC — share button in FileManager',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/files`, { waitUntil: 'networkidle' });
      await page.waitForTimeout(1500);
      const hasWebRTC = await page.evaluate(() => {
        return document.body.innerText.includes('WebRTC') || document.body.innerText.includes('📡');
      });
      ok('has WebRTC button or text', hasWebRTC);
    }
  },
  // ── Share functionality ──
  {
    name: 'Share — P2P announce button per file',
    fn: async ({ page, ok }) => {
      await page.goto(`${baseURL}/files`, { waitUntil: 'networkidle' });
      await page.waitForTimeout(1500);
      const hasP2PShare = await page.evaluate(() => {
        return document.body.innerText.includes('🧲');
      });
      ok('has P2P share icon', hasP2PShare);
    }
  },
];
