export const baseURL = 'https://peerdrive.pages.dev';

export const tests = [
  {
    name: '1. 三列布局加载',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL + '/create', { waitUntil: 'networkidle' });
      await page.waitForTimeout(1000);

      // 左侧面板——检查四个来源 tab
      const bodyText = await page.textContent('body');
      const hasAllTabs = bodyText.includes('所有文件') && bodyText.includes('已注册') &&
                         bodyText.includes('本地电脑') && bodyText.includes('合集');
      ok('左侧面板-四标签栏', hasAllTabs);

      // 右侧编辑器存在 (合集名称输入框)
      const nameInput = await page.$('input[placeholder="合集名称"]');
      ok('右侧编辑器-合集名称', !!nameInput);

      // 保存按钮
      ok('保存按钮', bodyText.includes('保存'));
    }
  },
  {
    name: '2. 四个来源Tab',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL + '/create', { waitUntil: 'networkidle' });
      await page.waitForTimeout(800);

      const bodyText = await page.textContent('body');
      ok('所有文件 tab', bodyText.includes('所有文件'));
      ok('已注册 tab', bodyText.includes('已注册'));
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
    name: '4. 已注册 tab 目录分组',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL + '/create', { waitUntil: 'networkidle' });
      await page.waitForTimeout(800);

      // 点击"已注册" tab
      const regBtn = await page.locator('button', { hasText: '已注册' });
      await regBtn.click();
      await page.waitForTimeout(800);

      // 应显示文件夹或"无匹配文件"
      const bodyText = await page.textContent('body');
      const hasDirOrEmpty = bodyText.includes('文件夹') || bodyText.includes('无匹配文件');
      ok('已注册目录视图', hasDirOrEmpty);
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
        ok('新建文件夹按钮出现', false, '无法添加条目，跳过新建文件夹测试');
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
