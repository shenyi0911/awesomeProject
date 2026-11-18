package runtime

import "testing"

func TestSanitizeRel(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "plain", in: "package/main.py", want: "package/main.py"},
		{name: "traversal rejected", in: "../../etc/passwd", wantErr: true},
		{name: "strips dot segments", in: "a/./b", want: "a/b"},
		{name: "backslashes normalized", in: `a\b\c.txt`, want: "a/b/c.txt"},
		{name: "leading slash rejected", in: "/a/b", wantErr: true},
		{name: "drive letter rejected", in: `C:\a\b`, wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "only separators", in: "/", wantErr: true},
		{name: "only traversal", in: "../..", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sanitizeRel(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
