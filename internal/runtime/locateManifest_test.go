package runtime

import "testing"

func TestLocateManifest(t *testing.T) {
	cases := []struct {
		name    string
		rels    []string
		want    string
		wantErr bool
	}{
		{
			name: "根目录manifest优先",
			rels: []string{"algorithm.yaml", "package/algorithm.yaml", "SvyCalc.dll"},
			want: "algorithm.yaml",
		},
		{
			name: "嵌套单目录",
			rels: []string{"package/algorithm.yaml", "package/SvyCalc.dll", "package/SvyCalc.deps.json"},
			want: "package/algorithm.yaml",
		},
		{
			name: "取层级最浅",
			rels: []string{"a/b/algorithm.yaml", "pkg/algorithm.yaml"},
			want: "pkg/algorithm.yaml",
		},
		{
			name: "大小写不敏感",
			rels: []string{"Package/Algorithm.yaml"},
			want: "Package/Algorithm.yaml",
		},
		{
			name:    "缺失",
			rels:    []string{"SvyCalc.dll", "readme.md"},
			want:    "",
			wantErr: false,
		},
		{
			name:    "同层多个歧义",
			rels:    []string{"pkg1/algorithm.yaml", "pkg2/algorithm.yaml"},
			want:    "",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := locateManifest(tc.rels)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际 got=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望报错: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
		})
	}
}
