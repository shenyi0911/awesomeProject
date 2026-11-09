package handler

import (
	"awesomeProject/internal/domain/session"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/service"
	"awesomeProject/internal/runtime"
	"awesomeProject/pkg/response"
)

// CalculateHandler 算法执行层 HTTP 接口（合并进 awesomeProject 后由本处理器承接）。
// 路由前缀（在 router 中绑定到 /api/v1）；对外统一为「算法唯一标识符/方法名」。
// 注意：方法名由各算法自行定义，平台不假定存在 calculate；调用方须显式给出方法名：
//
//	POST /{code}/{method}              直调该算法的任意方法（如 calculate、calculateGTMaxAllowableDiff）
//	GET  /{code}/metadata             代理算法自描述 /metadata（含该算法可用的方法名列表）
//
// 会话管理：每次调用解析 sessionId（请求头 X-Session-Id）；无则平台 mint 新会话并在响应头/响应体
// 回写，有则校验同 code + 未过期并续期；透传给算法进程的同样是 X-Session-Id 头。
type CalculateHandler struct {
	rt      *runtime.Manager
	algoSvc service.AlgorithmService // 取平台注册的算法名（真相源）
	sessSvc *session.SessionService  // 会话编排（mint / 校验 / 续期）
}

func NewCalculateHandler(rt *runtime.Manager, algoSvc service.AlgorithmService, sessSvc *session.SessionService) *CalculateHandler {
	return &CalculateHandler{rt: rt, algoSvc: algoSvc, sessSvc: sessSvc}
}

// RegisterRoutes 注册路由（rg 为 /api/v1 分组）。
// 对外统一为「算法唯一标识符/方法名」：方法名必须显式给出（平台不假定存在 calculate 等固定方法）。
func (h *CalculateHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/:code/:method", h.InvokeGeneric)
	rg.GET("/:code/metadata", h.Metadata) // 算法自描述（调用方据此发现该算法的方法名/单位）
}

// InvokeGeneric 通用转发：/api/v1/{code}/{method} -> Dapr invoke {code}/method/{method}。
// 方法名由各算法自行定义，平台只负责透传，不假定存在 calculate。
// 只读的 /metadata 走专门的 GET /{code}/metadata，这里拦截误用。
func (h *CalculateHandler) InvokeGeneric(c *gin.Context) {
	code := c.Param("code")
	method := c.Param("method")
	if code == "" {
		response.Error(c, response.CodeBadRequest, "缺少算法 code，用法: POST /api/v1/{code}/{method}")
		return
	}
	if method == "" {
		response.Error(c, response.CodeBadRequest, "缺少方法名，用法: POST /api/v1/{code}/{method}（方法名由各算法自行定义）")
		return
	}
	if method == "metadata" {
		response.Error(c, response.CodeBadRequest, "获取元数据请用 GET /api/v1/{code}/metadata")
		return
	}
	inst, ok := h.rt.GetInstance(code)
	if !ok {
		response.Error(c, response.CodeNotFound, "未知算法 code: "+code)
		return
	}
	// 发布状态闸门（与主计算一致，fail-closed）
	if ok, _, msg := h.rt.ReleaseCheck(code); !ok {
		response.Error(c, response.CodeForbidden, msg)
		return
	}

	// 仅「有状态算法」需要会话管理：首次请求 mint sessionId，后续请求携带同一 sessionId 复用上下文。
	// 无状态算法不创建/校验会话，也不回写 X-Session-Id（会话管理对无状态算法无意义）。
	var sess *entity.Session
	if h.isStateful(code) {
		var err error
		sess, _, err = h.sessSvc.Resolve(c.Request.Context(), code, c.GetHeader("X-Session-Id"))
		if err != nil {
			switch {
			case errors.Is(err, entity.ErrNotFound):
				response.Error(c, response.CodeUnauthorized, "会话不存在或已失效，请重新发起首次请求以获取新的 sessionId")
			case errors.Is(err, entity.ErrExpired):
				response.Error(c, response.CodeUnauthorized, "会话已过期，请重新发起首次请求以获取新的 sessionId")
			case errors.Is(err, entity.ErrCodeMismatch):
				response.Error(c, response.CodeUnauthorized, "sessionId 与算法 code 不匹配（会话只能被同一算法复用）")
			case errors.Is(err, entity.ErrInactive):
				response.Error(c, response.CodeUnauthorized, "会话已关闭")
			default:
				response.Error(c, response.CodeInternalError, "会话解析失败: "+err.Error())
			}
			return
		}
	}

	// 会话存在时，把 sessionId 写回响应头（调用方据此续用），并透传给算法进程（Dapr invoke 带 X-Session-Id 头）
	hdr := http.Header{}
	if sess != nil {
		c.Header("X-Session-Id", sess.SessionID)
		hdr.Set("X-Session-Id", sess.SessionID)
	}

	body, _ := io.ReadAll(c.Request.Body)
	respBody, status, err := h.rt.Invoke(inst, method, http.MethodPost, body, hdr)
	if err != nil {
		if status >= 400 {
			response.Error(c, response.Code(status), "调用算法失败: "+err.Error())
		} else {
			response.Error(c, response.CodeBadGateway, "调用算法失败: "+err.Error())
		}
		return
	}
	// 有会话时把 sessionId 注入响应信封顶层（与响应头二选一即可）；无状态算法不注入
	if sess != nil {
		respBody = withSession(respBody, sess.SessionID)
	}
	// 仅主计算（calculate）需要平台覆盖 algorithm 元信息；其余方法原样透传
	if method == "calculate" {
		c.Data(status, "application/json; charset=utf-8", h.enrichAlgorithmMeta(code, inst, respBody))
		return
	}
	c.Data(status, "application/json; charset=utf-8", respBody)
}

// withSession 把 sessionId 注入算法响应信封的顶层字段（算法返回非 JSON 时原样返回，不影响解算结果）。
func withSession(body []byte, sessionID string) []byte {
	var env map[string]interface{}
	if err := json.Unmarshal(body, &env); err != nil {
		return body
	}
	env["sessionId"] = sessionID
	out, err := json.Marshal(env)
	if err != nil {
		return body
	}
	return out
}

// isStateful 判断该算法是否声明为有状态（需要会话管理）。
// 以平台注册的算法元信息为准；查询失败或不存在时按无状态处理（不创建会话）。
func (h *CalculateHandler) isStateful(code string) bool {
	a, err := h.algoSvc.GetByCode(code)
	if err != nil || a == nil {
		return false
	}
	return a.Stateful
}

// enrichAlgorithmMeta 用平台真相覆盖算法响应里的 algorithm 元信息块。
// 平台是真相源：C# 服务硬编码的 name/version 不可信（实测返回 "Survey Type-D Calculator"/"1.0.0"），
// 这里统一注入 code(唯一标识)、name(平台注册名)、version(已发布版本号)、versionId(已发布版本ID)。
func (h *CalculateHandler) enrichAlgorithmMeta(code string, inst *runtime.AlgoInstance, body []byte) []byte {
	var env map[string]interface{}
	if err := json.Unmarshal(body, &env); err != nil {
		return body // 解析失败则原样返回，不影响解算结果
	}
	data, ok := env["data"].(map[string]interface{})
	if !ok {
		return body
	}
	meta := map[string]interface{}{
		"code": code, // 算法唯一标识符
	}
	if a, err := h.algoSvc.GetByCode(code); err == nil && a.Name != "" {
		meta["name"] = a.Name
	} else {
		meta["name"] = code
	}
	meta["version"] = inst.Version     // 当前拉起的已发布版本号
	meta["versionId"] = inst.VersionID // 当前拉起版本的版本ID
	data["algorithm"] = meta
	out, err := json.Marshal(env)
	if err != nil {
		return body
	}
	return out
}

// Metadata 代理算法自描述端点（算法进程已由运行时启动即拉起）。
func (h *CalculateHandler) Metadata(c *gin.Context) {
	code := c.Param("code")
	inst, ok := h.rt.GetInstance(code)
	if !ok {
		response.Error(c, response.CodeNotFound, "未知算法 code: "+code)
		return
	}
	respBody, status, err := h.rt.Invoke(inst, "metadata", http.MethodGet, nil, nil)
	if err != nil {
		response.Error(c, response.CodeBadGateway, "获取算法元数据失败: "+err.Error())
		return
	}
	c.Data(status, "application/json; charset=utf-8", respBody)
}
