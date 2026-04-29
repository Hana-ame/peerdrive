# sha256
这个阶段，请完成：
1.file层的代码
2.测试，和
3.说明文件。

需要：
实现一个注册方法。
1. 可以注册文件
2. 可以注册文件夹

注册的文件和文件夹是以full path的形式提供的。
full path即实例运行环境的full path。

注册之后的效果
表sha256 - metadata中有文件的metadata
包括size，hash，mime（不要做gzip等操作，这是为后面的功能准备的）

表 sha256 - provider - path 中有相关内容。

在测试脚本中，这部分需要被检查。

注册之后，测试下载，对比文件内容需要一致。

没有约定的内容，都可以自拟。

# 上传

上传功能是可选的，需要在配置文件中设置。
需要设置文件保存位置，如果没有设置，则不开启这个功能（403）

需要：
1. 上传之后保存到被设置的保存位置中。
2. 注册到sha256 - metadata 和 sha256 - provider

如果保存位置中有同一个文件，则直接返回成功或提示已存在（可以有区分）

在测试脚本中，需要检查设置了和没设置，有重复文件和新文件这三种情况。

没有约定的内容，都可以自拟

# 匿名collection stage1

匿名collection不需要任何用户权限，或者说，（sha256）(上传)这两个功能也是暂时未给出任何权限的。

新建一个匿名collection。

这是一个json。主要包含的信息是：path - sha256

json的其他信息至少要包含：version，其他必要字段，可选字段，可以自行进行添加。

请先完成stage1的内容

上传一个collection（有专门的endpoint path）
上传时需要处理的：
当中包含的文件需要注册到sha256 - metadata sha256 - files当中

上传的结果是：返回的信息中需要有这个collection json的sha256内容。
上传时需要检查：
- path应该都是相对path
- 如果有../等危险内容，则返回禁止。

上传之后需要：
1. 查看collection 的 path，是否能够访问到collection的json内容。
2. 查看sha256sum/:hash 是否能够访问到这个collection的json内容。
3. 查看sha256sum/:hash 是否有正确的HEADER（需要联动之前不让修改的，sha256 - metadata表中的扩展内容，需要提示这是一个collection的json,字段自拟）
4. 尝试下载collection/path/to/file，是否能找到正确的文件。

# P2P Stage 2

P2P 节点发现、通信、同步功能。

需要验证：
1. 双节点启动后通过 mDNS 互相发现
2. 手动 connect 对等点
3. 声明/拉取匿名合集（FetchCollection）
4. 从对等点同步文件内容
5. 向对等点推送合集（PushSync）
6. P2P 下载回退：本地→HTTP→P2P

测试脚本：`test/p2p.sh`

# P2P Stage 3 (NAT Traversal + Relay + WS)

中继、NAT 穿透、WebSocket 传输功能。

需要验证：
1. 中继服务器模式启动（relay_mode=server）
2. 客户端连接中继（relay_mode=client + static_relays）
3. STUN 打洞启用（hole_punch=true）
4. 通过中继完成 mDNS 发现和合集传输
5. 文件请求广播（request-file）
6. WebSocket 连接信息（ws/info）

测试脚本：`test/relay.sh`