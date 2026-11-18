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
			name: "root manifest preferred",
			rels: []string{"algorithm.yaml", "package/algorithm.yaml", "SvyCalc.dll"},
			want: "algorithm.yaml",
		},
		{
			name: "nested single directory",
			rels: []string{"package/algorithm.yaml", "package/SvyCalc.dll", "package/SvyCalc.deps.json"},
			want: "package/algorithm.yaml",
		},
		{
			name: "shallowest depth wins",
			rels: []string{"a/b/algorithm.yaml", "pkg/algorithm.yaml"},
			want: "pkg/algorithm.yaml",
		},
		{
			name: "case insensitive",
			rels: []string{"Package/Algorithm.yaml"},
			want: "Package/Algorithm.yaml",
		},
		{
			name:    "missing",
			rels:    []string{"SvyCalc.dll", "readme.md"},
			want:    "",
			wantErr: false,
		},
		{
			name:    "multiple ambiguous at same depth",
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
					t.Fatalf("expected an error, got=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
		})
	}
}
