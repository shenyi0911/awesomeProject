// Package manifest defines the algorithm launch contract (a restricted subset of algorithm.yaml) and its parser.
// It is a domain concept shared by the runtime, version service and algorithm service,
// keeping the domain layer independent of the execution layer.
package manifest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrMissingCode reports a manifest without code. Kept for compatibility: code is provided
// by the platform, and Parse no longer fails on it.
var ErrMissingCode = errors.New("manifest is missing code")

// stringList accepts both a YAML list and a comma-separated scalar.
type stringList []string

// UnmarshalYAML decodes a sequence or a comma-separated scalar into a string slice.
func (s *stringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		raw := strings.TrimSpace(value.Value)
		if raw == "" {
			*s = nil
			return nil
		}
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.Trim(strings.TrimSpace(p), "\"'")
			if p != "" {
				out = append(out, p)
			}
		}
		*s = out
	case yaml.SequenceNode:
		var items []string
		if err := value.Decode(&items); err != nil {
			return err
		}
		*s = items
	default:
		return fmt.Errorf("expected a string or a list")
	}
	return nil
}

// flexInt accepts both an integer and a quoted integer scalar.
type flexInt int

// UnmarshalYAML decodes a scalar into an int.
func (n *flexInt) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("expected an integer scalar")
	}
	v, err := strconv.Atoi(strings.TrimSpace(value.Value))
	if err != nil {
		return fmt.Errorf("invalid integer %q: %w", value.Value, err)
	}
	*n = flexInt(v)
	return nil
}

// rawManifest mirrors the YAML document; parsing is delegated to yaml.v3 so quoted '#' and ':'
// inside values are handled correctly.
type rawManifest struct {
	Code         string                `yaml:"code"`
	Name         string                `yaml:"name"`
	Version      string                `yaml:"version"`
	Language     string                `yaml:"language"`
	Entry        string                `yaml:"entry"`
	Health       string                `yaml:"health"`
	Transport    string                `yaml:"transport"`
	Port         flexInt               `yaml:"port"`
	Units        map[string]stringList `yaml:"units"`
	Methods      []rawMethod           `yaml:"methods"`
	Dependencies stringList            `yaml:"dependencies"`
}

type rawMethod struct {
	Name    string     `yaml:"name"`
	Verb    string     `yaml:"verb"`
	Input   string     `yaml:"input"`
	Params  stringList `yaml:"params"`
	Result  stringList `yaml:"result"`
	Example rawExample `yaml:"example"`
}

type rawExample struct {
	Request  string `yaml:"request"`
	Response string `yaml:"response"`
}

// Parse parses the restricted algorithm.yaml launch contract.
func Parse(data []byte) (*Manifest, error) {
	var raw rawManifest
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse manifest yaml: %w", err)
	}

	m := &Manifest{
		Code:         raw.Code,
		Name:         raw.Name,
		Version:      raw.Version,
		Language:     raw.Language,
		Entry:        raw.Entry,
		Health:       raw.Health,
		Transport:    raw.Transport,
		Port:         int(raw.Port),
		Units:        map[string][]string{},
		Dependencies: []string(raw.Dependencies),
	}
	if m.Health == "" {
		m.Health = "/health"
	}
	if m.Transport == "" {
		m.Transport = "dapr"
	}
	for k, list := range raw.Units {
		m.Units[k] = []string(list)
	}
	for _, rm := range raw.Methods {
		spec := MethodSpec{
			Name:    rm.Name,
			Verb:    rm.Verb,
			Input:   rm.Input,
			Example: ExampleSpec{Request: rm.Example.Request, Response: rm.Example.Response},
		}
		for _, p := range rm.Params {
			spec.Params = append(spec.Params, parseParamSpec(p))
		}
		for _, r := range rm.Result {
			spec.Result = append(spec.Result, parseParamSpec(r))
		}
		if spec.Verb == "" {
			spec.Verb = "POST"
		}
		if spec.Input == "" {
			spec.Input = "json"
		}
		m.Methods = append(m.Methods, spec)
	}
	return m, nil
}

// Manifest describes an algorithm integration. Platform-known fields such as
// code/name/version/health may be omitted; the runtime overrides them with platform values.
// The listen port is assigned dynamically by the platform.
type Manifest struct {
	Code         string
	Name         string
	Version      string
	Language     string
	Entry        string
	Health       string
	Transport    string
	Port         int
	Units        map[string][]string // Legacy "method -> units" declaration kept for compatibility; methods takes precedence.
	Methods      []MethodSpec        // Method-level contract.
	Dependencies []string            // Third-party dependencies (e.g. for Python).
}

// ParamSpec defines a single parameter or result field.
type ParamSpec struct {
	Key      string   `json:"key"`
	Type     string   `json:"type"`
	Unit     string   `json:"unit"`
	Required bool     `json:"required"`
	Enum     []string `json:"enum"`
	Desc     string   `json:"desc"`
}

// ExampleSpec is a sample invocation.
type ExampleSpec struct {
	Request  string `json:"request"`
	Response string `json:"response"`
}

// MethodSpec is one method/endpoint exposed by the algorithm (route is fixed to /api/v1/{code}/{Name}).
type MethodSpec struct {
	Name    string      `json:"name"`
	Verb    string      `json:"verb"`
	Input   string      `json:"input"`
	Params  []ParamSpec `json:"params"`
	Result  []ParamSpec `json:"result"`
	Example ExampleSpec `json:"example"`
}

// parseParamSpec parses a parameter/result line: key|type|unit|required|desc|enum.
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
