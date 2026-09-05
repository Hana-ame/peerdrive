export const baseURL = 'https://peerdrive.pages.dev';

// 测试期望与线上 peerdrive.pages.dev 当前 UI 对齐（2026-09-05 更新）。
// 旧版四标签栏（所有文件/已注册/本地电脑/合集）已简化为两标签（本地电脑/合集），
// 测试 1-2 同步更新；测试 4 原"已注册 tab 目录分组"因 tab 已删而重写为验证
// 合集 tab 交互。
export const tests = [
  {
    name: '1. 三列布局加载',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL + '/create', { waitUntil: 'networkidle' });
      await page.waitForTimeout(1000);

      // 左侧面板——检查两个来源 tab（本地电脑 + 合集，2026-09-05 UI 简化）
      const bodyText = await page.textContent('body');
      const hasAllTabs = bodyText.includes('本地电脑') && bodyText.includes('合集');
      ok('左侧面板-两标签栏', hasAllTabs);

      // 右侧编辑器存在 (合集名称输入框)
      const nameInput = await page.$('input[placeholder="合集名称"]');
      ok('右侧编辑器-合集名称', !!nameInput);

      // 保存按钮
      ok('保存按钮', bodyText.includes('保存'));
    }
  },
  {
    name: '2. 两个来源Tab',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL + '/create', { waitUntil: 'networkidle' });
      await page.waitForTimeout(800);

      const bodyText = await page.textContent('body');
      ok('本地电脑 tab', bodyText.includes('本地电脑'));
      ok('合集 tab', bodyText.includes('合集'));
    }
  },
  {
    name: '3. 本地电脑 tab 隐藏筛选栏',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL + '/create', { waitUntil: 'networkidle' });
      await page.waitForTimeout(800);

      // 点击"本地电脑" tab
      const localBtn = await page.locator('button', { hasText: '本地电脑' });
      await localBtn.click();
      await page.waitForTimeout(500);

      // 排序/筛选/搜索栏应隐藏
      const sortButtons = await page.locator('button', { hasText: '创建时间' }).count();
      const typeButtons = await page.locator('button', { hasText: '图片' }).count();
      const searchInput = await page.$('input[placeholder="搜索文件名..."]');

      ok('排序栏隐藏', sortButtons === 0);
      ok('类型筛选隐藏', typeButtons === 0);
      ok('搜索框隐藏', !searchInput);
    }
  },
  {
    name: '4. 合集 tab 目录视图',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL + '/create', { waitUntil: 'networkidle' });
      await page.waitForTimeout(800);

      // 点击"合集" tab
      const collBtn = await page.locator('button', { hasText: '合集' });
      await collBtn.click();
      await page.waitForTimeout(800);

      // 应显示合集面板（有内容或空状态提示）
      const bodyText = await page.textContent('body');
      const hasCollPanel = bodyText.includes('合集') &&
                           (bodyText.includes('暂无合集') || bodyText.includes('创建第一个') ||
                            bodyText.includes('文件数'));
      ok('合集目录视图', hasCollPanel);
    }
  },
  {
    name: '5. 编辑器区域完整',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL + '/create', { waitUntil: 'networkidle' });
      await page.waitForTimeout(800);

      const nameInput = await page.$('input[placeholder="合集名称"]');
      ok('合集名称输入框', !!nameInput);

      const bodyText = await page.textContent('body');
      ok('保存按钮存在', bodyText.includes('保存'));

      const dragHint = await page.$('text=拖拽文件到此处');
      ok('FileTree 拖拽区', !!dragHint);
    }
  },
  {
    name: '6. 新建文件夹',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL + '/create', { waitUntil: 'networkidle' });
      await page.waitForTimeout(2000);

      const bodyText = await page.textContent('body');

      // 如果 FileTree 已有条目，直接测试新建文件夹
      let newFolderBtn = await page.locator('button', { hasText: '新建文件夹' });
      if (!(await newFolderBtn.count())) {
        // 从左侧文件列表 hover 行后点击 "+" 按钮添加条目
        // "+" 按钮使用 opacity-0 group-hover:opacity-100，需 hover 父级 .group
        const fileRow = await page.locator('.group').first();
        if (await fileRow.count()) {
          await fileRow.hover();
          await page.waitForTimeout(300);
          // 点击该行内的 "+" 按钮
          const addBtn = fileRow.locator('button');
          const addBtnCount = await addBtn.count();
          ok('找到 + 按钮', addBtnCount > 0);
          if (addBtnCount > 0) {
            await addBtn.first().click({ force: true });
            await page.waitForTimeout(800);
            newFolderBtn = await page.locator('button', { hasText: '新建文件夹' });
          }
        } else {
          ok('找到 .group 行', false);
        }
      }

      if (!(await newFolderBtn.count())) {
        // CORS 阻后端时 FileTree 为空、"+" 点击无法加载条目——属环境问题非测试 bug。
        ok('新建文件夹按钮出现', false, '后端 CORS 或条目加载失败，跳过');
        return;
      }

      ok('新建文件夹按钮可见', true);
      await newFolderBtn.click();
      await page.waitForTimeout(300);

      const folderInput = await page.$('input[placeholder="文件夹名称"]');
      ok('文件夹名称输入框出现', !!folderInput);

      if (folderInput) {
        await folderInput.fill('smoke-test-dir');
        await page.waitForTimeout(200);
        const confirmBtn = await page.locator('button', { hasText: '确定' });
        await confirmBtn.click();
        await page.waitForTimeout(800);

        const txt = await page.textContent('body');
        ok('文件夹创建成功', txt.includes('smoke-test-dir'));
      }
    }
  },
];
