package runtime

import (
	"errors"
	"strconv"
	"strings"
)

// manifest 解析错误
var (
	// ErrMissingCode manifest 缺少 code（保留兼容：code 由平台提供，ParseManifest 已不再因此报错，
	// 运行时一律以平台 code 覆盖）。
	ErrMissingCode = errors.New("manifest 缺少 code")
)

func atoiSafe(v string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

// Manifest 算法接入描述（algorithms/<code>/algorithm.yaml 的受限子集，零依赖解析）。
// 平台编排器扫描该文件即可知道如何拉起与调用本算法。
// 平台已提供的信息不必写入 yaml：code/name/version/health 由平台提供，监听端口由平台在拉起时动态分配，均无需在 yaml 声明；
// 运行时以平台注册值覆盖（code=唯一标识符、health 默认 /health、transport 默认 dapr），监听端口由平台动态分配。
type Manifest struct {
	Code         string
	Name         string
	Version      string
	Language     string
	Entry        string
	Health       string
	Transport    string
	Port         int
	Units        map[string][]string
	Dependencies []string // 第三方依赖（Python 等）。运行时据此建 venv 并安装；为空则跳过，零影响。
}

// ParseManifest 解析受限 YAML 子集（不引入第三方 YAML 库）。导出供发布校验等复用。
func ParseManifest(data []byte) (*Manifest, error) {
	m := &Manifest{Health: "/health", Transport: "dapr", Units: map[string][]string{}}
	inSection := ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		// 去掉行内注释（# 之后）
		if ci := strings.Index(line, "#"); ci >= 0 {
			line = strings.TrimSpace(line[:ci])
		}
		if line == "" {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if indent == 0 {
			if strings.HasSuffix(line, ":") {
				inSection = strings.TrimSpace(strings.TrimSuffix(line, ":"))
				continue
			}
			k, v := splitKV(line)
			inSection = ""
			switch k {
			case "code":
				m.Code = v
			case "name":
				m.Name = v
			case "version":
				m.Version = v
			case "language":
				m.Language = v
			case "entry":
				m.Entry = v
			case "health":
				m.Health = v
			case "transport":
				m.Transport = v
			case "port":
				m.Port = atoiSafe(v)
			case "dependencies":
				// 行内写法：dependencies: numpy, scipy  或  dependencies: [numpy, scipy]
				s := strings.TrimSpace(v)
				s = strings.TrimPrefix(s, "[")
				s = strings.TrimSuffix(s, "]")
				for _, d := range strings.Split(s, ",") {
					d = strings.TrimSpace(d)
					if d != "" {
						m.Dependencies = append(m.Dependencies, d)
					}
				}
			}
		} else if inSection == "units" {
			k, v := splitKV(line)
			m.Units[k] = parseList(v)
		} else if inSection == "dependencies" {
			// 子节写法（YAML 列表）：
			//   dependencies:
			//     - numpy
			//     - scipy
			item := strings.TrimSpace(strings.TrimPrefix(line, "-"))
			if item != "" {
				m.Dependencies = append(m.Dependencies, item)
			}
		}
	}
	// code/port 可省略：平台提供 code（唯一标识符）与 port（新建/编辑算法时指定），
	// 运行时一律以平台值覆盖/回退；yaml 只需声明平台不掌握的启动方式（entry 等）。
	return m, nil
}

func splitKV(line string) (string, string) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return line, ""
	}
	k := strings.TrimSpace(line[:idx])
	v := strings.TrimSpace(line[idx+1:])
	v = strings.Trim(v, "\"'")
	return k, v
}

func parseList(v string) []string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") {
		v = strings.TrimPrefix(v, "[")
		v = strings.TrimSuffix(v, "]")
		out := []string{}
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			p = strings.Trim(p, "\"'")
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	if v == "" {
		return nil
	}
	return []string{v}
}
