package files

import (
	"errors"
	"testing"
)

func TestValidateBaseName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"plain", "report.txt", "report.txt", false},
		{"trims surrounding space", "  notes.md  ", "notes.md", false},
		{"empty", "", "", true},
		{"only space", "   ", "", true},
		{"with slash", "a/b", "", true},
		{"with backslash is allowed segment", "a\\b", "a\\b", false},
		{"null byte", "a\x00b", "", true},
		{"dot", ".", "", true},
		{"dotdot", "..", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateBaseName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateBaseName(%q) expected error, got %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateBaseName(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("validateBaseName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeUploadName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"plain", "data.csv", "data.csv", false},
		{"strips posix traversal", "../../etc/passwd", "passwd", false},
		{"strips windows path", "C:\\Users\\me\\photo.png", "photo.png", false},
		{"bare slash", "/", "", true},
		{"empty", "", "", true},
		{"trailing slash dir", "evil/", "evil", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeUploadName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("sanitizeUploadName(%q) expected error, got %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("sanitizeUploadName(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("sanitizeUploadName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTargetPath(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		wantOK bool
	}{
		{"absolute file", "/home/user/file.txt", "/home/user/file.txt", true},
		{"cleans dots", "/home/user/../user/file", "/home/user/file", true},
		{"relative becomes rooted", "name", "/name", true},
		{"empty rejected", "", "", false},
		{"root rejected", "/", "", false},
		{"escapes to root rejected", "/..", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := targetPath(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("targetPath(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Fatalf("targetPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFriendlyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"strips prefix and capitalises", errors.New("sshclient: rename a to b: file exists"), "Rename a to b: file exists"},
		{"already clean", errors.New("permission denied"), "Permission denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := friendlyError(tt.err); got != tt.want {
				t.Fatalf("friendlyError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
