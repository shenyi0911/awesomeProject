package entity

import (
	"time"

	"awesomeProject/internal/domain/enum"
)

type Algorithm struct {
	ID                  string          `json:"id"` // 主键（服务端生成）
	Name                string          `json:"name"`
	Code                string          `json:"code"` // 算法唯一标识符（仅允许字母/数字 A-Za-z0-9，全局唯一）
	Description         string          `json:"description"`
	Category            string          `json:"category"`
	Author              string          `json:"author"`
	Language            enum.Language   `json:"language"` // 开发语言（枚举：Java/Python/C++/C#/Go）
	Scene               string          `json:"scene"`
	Port                int             `json:"port"` // 算法监听端口（平台指定，全局唯一；运行时绑定到算法进程）
	Stateful            bool            `json:"stateful"`
	UpdateUser          string          `json:"updateUser"`
	Files               []AlgorithmFile `json:"files"`
	LatestVersion       string          `json:"latestVersion"`
	LatestVersionStatus string          `json:"latestVersionStatus"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`
}
