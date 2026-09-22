// node_share.go：本节点共享范围的管理端点（doc/NETDISK.md M2.6 / §12.6）。
//
// 语义：回答并修改「我这个节点对外提供什么、给谁」：
//   - GET  /peerjs/share         当前共享范围 + 可选文件清单（带是否已共享/级别）
//   - PUT  /peerjs/share         局部更新（enable/dirs/files/collections/friends）
//   - POST /peerjs/share/files   勾选/取消若干文件 {hashes:[], shared:bool, level:string}
//
// 为什么要这套端点：共享范围原本只能靠 PEERDRIVE_SHARE_* 环境变量在启动时定，
// 想改就得重启节点。而"我愿意把哪些文件给出去、给谁看"是随手的决定——新上传
// 一个文件想立刻共享、某个目录不想给了。要求重启等于逼运营者要么长期共享一个
// 过宽的目录，要么干脆不开共享（见 service/nodeshare.go 文件头）。
//
// 级别三档（model.Level*）：public 列出且可下载 / unlisted 不列出但可下载 /
// private 只给自己与好友。同一内容被多条来源命中时取最宽松的那条。
//
// 为什么挂 auth（AuthRequired）：GET 会列出本机文件的名字与大小，写端点更是
// 直接决定对外公开什么。未配注册服务器时 AuthRequired 内部放行（单机模式），
// 语义与 /peerjs/nodes/join 一致。
//
// 与 /peerjs/nodes/:peer/shares 的区别（不要混淆命名）：
//   - /peerjs/nodes/:peer/shares = 去**问对端**它的共享清单（share 帧）
//   - /peerjs/share              = 管理**自己**的共享范围（本地状态）

package controller

import (
	"net/http"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

// nodeShareSvc 由 main 注入（同 InitNodeDirectory 模式）。
var nodeShareSvc *service.NodeShare

// InitNodeShareController 注入共享范围服务（nil = 该组端点 503）。
func InitNodeShareController(s *service.NodeShare) {
	log.LogDebug("ctrl-node-share: InitNodeShareController")
	nodeShareSvc = s
}

// GetNodeShare 处理 GET /peerjs/share：
// 返回当前共享范围 + 可选文件清单（每行带 shared/by_dir/level），管理台据此
// 渲染勾选框与级别选择。
func GetNodeShare(c *gin.Context) {
	if nodeShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "share scope not enabled (peerjs disabled)"})
		return
	}
	scope := nodeShareSvc.Scope()
	files := nodeShareSvc.CandidateFiles()
	if files == nil {
		files = []service.ShareFileItem{} // 保持 JSON 为 []
	}
	summary := model.NodeShares{}
	if nodeShareSvc.Enabled() {
		summary = nodeShareSvc.Summary()
	}
	c.JSON(http.StatusOK, gin.H{
		"enable":      scope.Enable,
		"dirs":        scope.Dirs,
		"files":       files,
		"collections": scope.Collections,
		// 勾选了但当前不在 file_index 里的 hash 也要回给前端：否则用户看不到
		// "我勾过它"（文件被删除后勾选残留），会以为系统把他的选择弄丢了。
		"selected": scope.Files,
		// friends：private 级别的放行名单（peer id）。前端直接编辑它。
		"friends": scope.Friends,
		"levels":  []string{model.LevelPublic, model.LevelUnlisted, model.LevelPrivate},
		"summary": summary,
	})
}

// PutNodeShare 处理 PUT /peerjs/share（局部更新，未传的项保持不变）。
func PutNodeShare(c *gin.Context) {
	if nodeShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "share scope not enabled (peerjs disabled)"})
		return
	}
	var patch service.ScopePatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	scope, err := nodeShareSvc.Update(patch)
	if err != nil {
		// 校验失败（卷根目录 / 非法 hash / 非法级别）是用户输入问题，400 而不是 500
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enable":      scope.Enable,
		"dirs":        scope.Dirs,
		"files":       scope.Files,
		"collections": scope.Collections,
		"friends":     scope.Friends,
	})
}

// PostNodeShareFiles 处理 POST /peerjs/share/files
// {hashes:[...], shared:bool, level:"public"|"unlisted"|"private"}。
//
// 单独一条而不是让前端每次 PUT 全量：勾选框一次改一行，全量 PUT 需要前端先把
// 整份范围读回来再拼，并发点两下就会互相覆盖。
//
// level 省略时沿用已有级别（没有就 public）——只切"共享/不共享"的界面不该被迫
// 知道当前级别。
func PostNodeShareFiles(c *gin.Context) {
	if nodeShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "share scope not enabled (peerjs disabled)"})
		return
	}
	var body struct {
		Hashes []string `json:"hashes"`
		Shared bool     `json:"shared"`
		Level  string   `json:"level"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	scope, err := nodeShareSvc.SetFilesShared(body.Hashes, body.Shared, body.Level)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enable":   scope.Enable,
		"files":    scope.Files,
		"selected": scope.Files,
	})
}
