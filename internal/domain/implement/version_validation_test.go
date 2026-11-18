package implement

import "testing"

func TestValidateVersion(t *testing.T) {
	valid := []string{"v1", "v1.0.0", "V2-beta_1", "1.2.3"}
	for _, v := range valid {
		if err := validateVersion(v); err != nil {
			t.Fatalf("validateVersion(%q) unexpected error: %v", v, err)
		}
	}
	invalid := []string{"", "../x1", "v 1", "a/b1", "-v1", ".v1", "v1/../..", string(make([]byte, 65))}
	for _, v := range invalid {
		if err := validateVersion(v); err == nil {
			t.Fatalf("validateVersion(%q) expected an error", v)
		}
	}
}

func TestNormalizeUploadPath(t *testing.T) {
	cases := []struct {
		name     string
		relPath  string
		filename string
		want     string
		wantErr  bool
	}{
		{name: "relative kept", relPath: "pkg/main.py", want: "pkg/main.py"},
		{name: "fallback to filename", filename: "main.py", want: "main.py"},
		{name: "backslashes normalized", relPath: `pkg\sub\a.txt`, want: "pkg/sub/a.txt"},
		{name: "dot segments dropped", relPath: "./pkg/./a.txt", want: "pkg/a.txt"},
		{name: "traversal rejected", relPath: "../a.txt", wantErr: true},
		{name: "absolute rejected", relPath: "/etc/passwd", wantErr: true},
		{name: "drive letter rejected", relPath: `C:\a.txt`, wantErr: true},
		{name: "empty rejected", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeUploadPath(tc.relPath, tc.filename)
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

func TestZipEntryName(t *testing.T) {
	cases := map[string]string{
		"pkg/a.txt":        "pkg/a.txt",
		"../../etc/passwd": "etc/passwd",
		`pkg\a.txt`:        "pkg/a.txt",
		"C:/a.txt":         "a.txt",
		"..":               "file",
	}
	for in, want := range cases {
		if got := zipEntryName(in); got != want {
			t.Fatalf("zipEntryName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateManifestHealth(t *testing.T) {
	if _, err := validateManifestContent([]byte("entry: python main.py\nhealth: health\n")); err == nil {
		t.Fatal("expected an error for a health path without a leading slash")
	}
	if _, err := validateManifestContent([]byte("entry: python main.py\nhealth: /healthz\n")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
