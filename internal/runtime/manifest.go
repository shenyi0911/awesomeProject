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
// methods 段为「方法级契约」：借鉴 CNPC AlgorithmInterface 的 functionName+params+result+示例，
// 使平台在注册期即可获知每个方法的入参/出参 schema，用于网关校验、前端表单生成与示例联调（无需仅靠运行时 /metadata 探测）。
type Manifest struct {
	Code         string
	Name         string
	Version      string
	Language     string
	Entry        string
	Health       string
	Transport    string
	Port         int
	Units        map[string][]string // 旧式「方法→单位」声明，保留兼容；优先使用 methods 段
	Methods      []MethodSpec        // 方法级契约（对应 CNPC AlgorithmInterface）
	Dependencies []string            // 第三方依赖（Python 等）。运行时据此建 venv 并安装；为空则跳过，零影响。
}

// ParamSpec 单个参数/结果字段定义（对应 CNPC AlgorithmInterface.params / result 中的一行）。
type ParamSpec struct {
	Key      string   `json:"key"`      // 参数路径，如 unit.gravity / params.gravity.x
	Type     string   `json:"type"`     // string|number|boolean|object|array
	Unit     string   `json:"unit"`     // 物理量单位（可选），如 m/s2 / deg
	Required bool     `json:"required"` // 是否必填
	Enum     []string `json:"enum"`     // 枚举约束（可选），如 [g, mps2]
	Desc     string   `json:"desc"`     // 说明
}

// ExampleSpec 调用示例（对应 CNPC paramsDemo / resultDemo）。
type ExampleSpec struct {
	Request  string `json:"request"`  // 请求体样例（JSON 字符串）
	Response string `json:"response"` // 响应体样例（JSON 字符串）
}

// MethodSpec 算法暴露的一个方法/端点（对应 CNPC AlgorithmInterface 的 functionName+params+result）。
// 路由固定为 /api/v1/{code}/{Name}，verb 默认 POST，input 默认 json。
type MethodSpec struct {
	Name    string      `json:"name"`    // 方法名，对应路由末段（= CNPC functionName）
	Verb    string      `json:"verb"`    // HTTP 方法，默认 POST
	Input   string      `json:"input"`   // 入参形态：json | file，默认 json（= CNPC paramType）
	Params  []ParamSpec `json:"params"`  // 入参定义（= CNPC params）
	Result  []ParamSpec `json:"result"`  // 出参定义（= CNPC result）
	Example ExampleSpec `json:"example"` // 调用示例（= CNPC paramsDemo/resultDemo）
}

// ParseManifest 解析受限 YAML 子集（不引入第三方 YAML 库）。导出供发布校验等复用。
func ParseManifest(data []byte) (*Manifest, error) {
	m := &Manifest{Health: "/health", Transport: "dapr", Units: map[string][]string{}}
	inSection := ""
	curMethodIdx := -1 // methods 段内当前方法在 m.Methods 中的下标；-1 表示尚未进入方法体
	methodSub := ""    // methods 段内子节："" | params | result | example
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
				curMethodIdx = -1
				methodSub = ""
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
			continue
		}
		switch inSection {
		case "units":
			k, v := splitKV(line)
			m.Units[k] = parseList(v)
		case "dependencies":
			// 子节写法（YAML 列表）：
			//   dependencies:
			//     - numpy
			//     - scipy
			item := strings.TrimSpace(strings.TrimPrefix(line, "-"))
			if item != "" {
				m.Dependencies = append(m.Dependencies, item)
			}
		case "methods":
			// 方法列表，结构：
			//   methods:
			//     - name: calculate        # indent 2，以 "- " 开头开启新方法
			//       verb: POST             # indent 4
			//       input: json            # json | file
			//       params:                # indent 4 子节
			//         - key|type|unit|required|desc|enum   # indent 6，以 "- " 开头
			//       result:
			//         - key|type|unit|required|desc
			//       example:
			//         request: '{...}'     # indent 6
			//         response: '{...}'
			if indent <= 2 && strings.HasPrefix(line, "-") {
				rest := strings.TrimSpace(strings.TrimPrefix(line, "-"))
				spec := MethodSpec{}
				if k, v := splitKV(rest); k == "name" {
					spec.Name = v
				}
				m.Methods = append(m.Methods, spec)
				curMethodIdx = len(m.Methods) - 1
				methodSub = ""
				continue
			}
			if curMethodIdx < 0 {
				continue
			}
			if indent <= 4 && strings.HasSuffix(line, ":") && !strings.Contains(line, ": ") {
				methodSub = strings.TrimSpace(strings.TrimSuffix(line, ":"))
				continue
			}
			ms := &m.Methods[curMethodIdx]
			k, v := splitKV(line)
			switch methodSub {
			case "":
				switch k {
				case "verb":
					ms.Verb = v
				case "input":
					ms.Input = v
				case "name":
					ms.Name = v
				}
			case "params", "result":
				if strings.HasPrefix(line, "-") {
					ps := parseParamSpec(strings.TrimSpace(strings.TrimPrefix(line, "-")))
					if methodSub == "params" {
						ms.Params = append(ms.Params, ps)
					} else {
						ms.Result = append(ms.Result, ps)
					}
				}
			case "example":
				if k == "request" {
					ms.Example.Request = v
				} else if k == "response" {
					ms.Example.Response = v
				}
			}
		}
	}
	// 方法级默认值
	for i := range m.Methods {
		if m.Methods[i].Verb == "" {
			m.Methods[i].Verb = "POST"
		}
		if m.Methods[i].Input == "" {
			m.Methods[i].Input = "json"
		}
	}
	// code/port 可省略：平台提供 code（唯一标识符）与 port（新建/编辑算法时指定），
	// 运行时一律以平台值覆盖/回退；yaml 只需声明平台不掌握的启动方式（entry 等）。
	return m, nil
}

// parseParamSpec 解析参数/结果行：key|type|unit|required|desc|enum
// 各段以竖线分隔，required 为 true 时表示必填，enum 为逗号分隔的枚举（可选）。
func parseParamSpec(s string) ParamSpec {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, "|")
	get := func(i int) string {
		if i < len(parts) {
			return strings.TrimSpace(parts[i])
		}
		return ""
	}
	ps := ParamSpec{Key: get(0), Type: get(1), Unit: get(2)}
	if strings.EqualFold(get(3), "true") {
		ps.Required = true
	}
	ps.Desc = get(4)
	if e := get(5); e != "" {
		for _, v := range strings.Split(e, ",") {
			v = strings.TrimSpace(v)
			if v != "" {
				ps.Enum = append(ps.Enum, v)
			}
		}
	}
	return ps
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
