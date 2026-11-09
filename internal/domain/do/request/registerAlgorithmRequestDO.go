package request

// RegisterAlgorithmRequest 注册算法请求
type RegisterAlgorithmRequest struct {
	Name        string `json:"name" binding:"required"`     // 算法名称
	Code        string `json:"code" binding:"required"`     // 算法唯一标识符（仅允许字母/数字 A-Za-z0-9，全局唯一）
	Description string `json:"description"`                 // 算法描述
	Category    string `json:"category" binding:"required"` // 算法分类
	Author      string `json:"author" binding:"required"`   // 算法作者
	Language    string `json:"language" binding:"required"` // 开发语言（枚举：Java/Python/C++/C#/Go）
	Scene       string `json:"scene"`                       // 应用场景
	Stateful    bool   `json:"stateful"`                    // 是否是有状态算法
	Port        int    `json:"port"`                        // 监听端口：由平台在拉起时按主机动态分配，前端无需填写（保留字段仅用于回显）
}
