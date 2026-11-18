package manifest

import "testing"

func TestParseMinimalDefaults(t *testing.T) {
	m, err := Parse([]byte("entry: python main.py\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Entry != "python main.py" {
		t.Fatalf("Entry = %q, want %q", m.Entry, "python main.py")
	}
	if m.Health != "/health" {
		t.Fatalf("Health = %q, want default /health", m.Health)
	}
	if m.Transport != "dapr" {
		t.Fatalf("Transport = %q, want default dapr", m.Transport)
	}
	if m.Units == nil {
		t.Fatal("Units map must be initialized")
	}
}

func TestParseEmptyInput(t *testing.T) {
	m, err := Parse(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Entry != "" {
		t.Fatalf("Entry = %q, want empty", m.Entry)
	}
	if m.Methods != nil {
		t.Fatalf("Methods = %v, want nil", m.Methods)
	}
}

func TestParseFullManifest(t *testing.T) {
	doc := `
# top-level fields
code: demo            # inline comment
name: Demo
version: v1
language: Python
entry: python main.py
health: /healthz
transport: dapr
port: 8080
dependencies: [numpy, pandas]
units:
  calculate: [m, cm]
methods:
  - name: calculate
    verb: POST
    input: json
    params:
      - area|float|m2|true|area|a,b
    result:
      - value|float|m2|true|result|
    example:
      request: '{"a":1}'
      response: '{"value":1}'
  - name: status
`
	m, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Code != "demo" {
		t.Fatalf("Code = %q, want demo", m.Code)
	}
	if m.Health != "/healthz" {
		t.Fatalf("Health = %q, want /healthz", m.Health)
	}
	if m.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", m.Port)
	}
	if len(m.Dependencies) != 2 || m.Dependencies[0] != "numpy" || m.Dependencies[1] != "pandas" {
		t.Fatalf("Dependencies = %v, want [numpy pandas]", m.Dependencies)
	}
	if got := m.Units["calculate"]; len(got) != 2 || got[0] != "m" || got[1] != "cm" {
		t.Fatalf("Units[calculate] = %v, want [m cm]", got)
	}
	if len(m.Methods) != 2 {
		t.Fatalf("len(Methods) = %d, want 2", len(m.Methods))
	}

	calc := m.Methods[0]
	if calc.Name != "calculate" || calc.Verb != "POST" || calc.Input != "json" {
		t.Fatalf("unexpected method: %+v", calc)
	}
	if len(calc.Params) != 1 {
		t.Fatalf("len(Params) = %d, want 1", len(calc.Params))
	}
	p := calc.Params[0]
	if p.Key != "area" || p.Type != "float" || !p.Required || p.Desc != "area" || len(p.Enum) != 2 {
		t.Fatalf("unexpected param spec: %+v", p)
	}
	if len(calc.Result) != 1 || calc.Result[0].Key != "value" {
		t.Fatalf("unexpected result spec: %+v", calc.Result)
	}
	if calc.Example.Request != `{"a":1}` || calc.Example.Response != `{"value":1}` {
		t.Fatalf("unexpected example: %+v", calc.Example)
	}

	// Missing verb/input fall back to POST/json.
	status := m.Methods[1]
	if status.Verb != "POST" || status.Input != "json" {
		t.Fatalf("status defaults = %+v, want POST/json", status)
	}
}

func TestParseDependenciesBlockList(t *testing.T) {
	doc := `
entry: python main.py
dependencies:
  - numpy
  - pandas==2.0
`
	m, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Dependencies) != 2 || m.Dependencies[1] != "pandas==2.0" {
		t.Fatalf("Dependencies = %v, want [numpy pandas==2.0]", m.Dependencies)
	}
}

func TestParseParamSpecMissingFields(t *testing.T) {
	doc := `
entry: python main.py
methods:
  - name: calculate
    params:
      - key|string
`
	m, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Methods) != 1 || len(m.Methods[0].Params) != 1 {
		t.Fatalf("unexpected methods: %+v", m.Methods)
	}
	p := m.Methods[0].Params[0]
	if p.Key != "key" || p.Type != "string" || p.Unit != "" || p.Required || p.Enum != nil {
		t.Fatalf("unexpected param spec: %+v", p)
	}
}

func TestParsePreservesQuotedHashAndColon(t *testing.T) {
	doc := `
entry: python main.py
methods:
  - name: calculate
    example:
      request: '{"url":"http://example.com/#frag","id":1}'
      response: '{"text":"a: b # not a comment"}'
`
	m, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ex := m.Methods[0].Example
	if ex.Request != `{"url":"http://example.com/#frag","id":1}` {
		t.Fatalf("Request = %q", ex.Request)
	}
	if ex.Response != `{"text":"a: b # not a comment"}` {
		t.Fatalf("Response = %q", ex.Response)
	}
}

func TestParseInvalidYAML(t *testing.T) {
	if _, err := Parse([]byte("entry: [unclosed\n")); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestParseQuotedPort(t *testing.T) {
	m, err := Parse([]byte("entry: python main.py\nport: \"8081\"\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Port != 8081 {
		t.Fatalf("Port = %d, want 8081", m.Port)
	}
}
