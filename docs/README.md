# Peerdrive 文档目录

```
docs/
├── specs/              ← 功能规格说明（开发者阅读）
│   ├── design.md             系统设计总览
│   ├── database.md           数据库表结构
│   ├── register.md           文件注册层
│   ├── upload.md             文件上传层
│   ├── sha256-download.md    SHA256 下载层
│   └── anon-collection.md    匿名合集层
│
├── testing/            ← 测试说明（测试者阅读）
│   ├── index.md              测试环境与总览
│   ├── register.md           注册/下载测试说明
│   └── test-case-spec.md     测试用例规范（原始需求）
│
└── notes/             ← Agent 记录与杂项（维护者阅读）
    ├── changelog.md           开发日志
    ├── refactor-report.md     重构报告
    ├── memo.md                环境配置备忘
    ├── backend-reference.md   后端设计参考
    └── references/            参考资料
```

## 快速引导

| 如果你想… | 阅读 |
|-----------|------|
| 了解系统整体架构 | `docs/specs/design.md` |
| 数据库怎么设计的 | `docs/specs/database.md` |
| 注册本地文件的 API | `docs/specs/register.md` |
| 上传文件的 API | `docs/specs/upload.md` |
| 匿名合集怎么用 | `docs/specs/anon-collection.md` |
| 怎么启动和测试 | `docs/testing/index.md` |
| 最近的改动记录 | `docs/notes/changelog.md` |

## 测试脚本

```
test/
├── register.sh         注册 → 下载全链路测试
├── upload.sh           上传功能测试
└── anon-collection.sh  匿名合集测试
```
