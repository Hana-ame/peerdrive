# Auth Module Docs

认证/注册服务（regserver）相关设计与评审。**当前状态：设计提案阶段，代码尚未落地**
（除节点侧鉴权中间件与 relay 登记/心跳已实现，见下表）。

- `API-DESIGN.md` — 注册服务器端点（§1）、节点侧鉴权端点（§2）、鉴权中间件（§3）、
  relay 登记（§4）、测试账号（§5）；**§6-§10 为 2026-09-19 新增的设计提案**：
  用户↔节点目录、传输量统计与防谎报、注册用户加密信道、relay 在 node 内的定位、
  与现有代码的衔接点及落地顺序。
- `USER-ROLES.md` — 四个角色（匿名访客 / 认证用户 / 匿名+节点 / 认证+节点）的
  权限矩阵与前端角色判定逻辑。
- `SECURITY-REVIEW.md` — 安全审计结果。

## 与 peerdrive 主仓的衔接

| 已实现 | 位置 |
|---|---|
| 节点侧鉴权中间件（`AuthOptional`/`AuthRequired`，token 转发给 regserver 校验） | `back/internal/...`（`PEERDRIVE_REG_SERVER` 配置） |
| relay 登记 / 心跳 / 列表 | 见 `API-DESIGN.md` §4 |
| 合集 `Owner` 语义（`visibility` 三档的 owner 字段） | `doc/REFACTOR.md` §3.16 |

| 未实现（提案） | 见 |
|---|---|
| 用户 ↔ 节点目录 | `API-DESIGN.md` §6 |
| 传输量统计 + 防谎报（会签/中继兜底/抽样挑战/漂移检测） | `API-DESIGN.md` §7 |
| 注册用户的应用层加密信道 + 账号身份绑定 | `API-DESIGN.md` §8 |
| relay 流量统计回传 | `API-DESIGN.md` §7 + §9 |
