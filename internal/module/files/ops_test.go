package files

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestValidationErrorMessage(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want string
	}{
		{errors.New("enter a name"), "Enter a name."},
		{errors.New("choose a different name"), "Choose a different name."},
		{errors.New("already punctuated!"), "Already punctuated!"},
	} {
		if got := validationErrorMessage(tt.err); got != tt.want {
			t.Errorf("validationErrorMessage(%q) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

func TestParseChmodMode(t *testing.T) {
	tests := []struct {
		raw     string
		want    os.FileMode
		wantErr bool
	}{
		{"0644", 0o644, false},
		{"755", 0o755, false},
		{"0700", 0o700, false},
		{"4755", 0o755 | os.ModeSetuid, false},
		{"2750", 0o750 | os.ModeSetgid, false},
		{"1777", 0o777 | os.ModeSticky, false},
		{"", 0, true},
		{"0899", 0, true},
		{"077777", 0, true},
		{"rwxr-xr-x", 0, true},
	}
	for _, tc := range tests {
		got, err := parseChmodMode(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseChmodMode(%q) expected error, got %v", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseChmodMode(%q) unexpected error: %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseChmodMode(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestSanitizeRelativeSubpath(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"configs/nginx", "configs/nginx", false},
		{"/a/b/c/", "a/b/c", false},
		{"a\\b\\c.txt", "a/b/c.txt", false},
		{"../escape", "", true},
		{"a/../b", "", true},
		{"./here", "", true},
		{"", "", true},
		{"a/b\x00c", "", true},
	}
	for _, tc := range tests {
		got, err := sanitizeRelativeSubpath(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("sanitizeRelativeSubpath(%q) expected error, got %q", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("sanitizeRelativeSubpath(%q) unexpected error: %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("sanitizeRelativeSubpath(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestParseOpPayloadJSONAndForm(t *testing.T) {
	// JSON payload
	jReq := httptest.NewRequest(http.MethodPost, "/ops", strings.NewReader(`{"path":"/var/log","paths":["/a","/","/b"],"recursive":true,"mode":"0755"}`))
	jReq.Header.Set("Content-Type", "application/json")
	jRec := httptest.NewRecorder()
	jp, err := parseOpPayload(jRec, jReq)
	if err != nil {
		t.Fatalf("parseOpPayload JSON error: %v", err)
	}
	if jp.Path != "/var/log" || !jp.Recursive || jp.Mode != "0755" {
		t.Fatalf("unexpected JSON payload: %+v", jp)
	}
	clean := cleanTargetPaths(jp)
	if len(clean) != 2 || clean[0] != "/a" || clean[1] != "/b" {
		t.Fatalf("cleanTargetPaths = %v, want [/a /b]", clean)
	}

	// Form payload with JSON-encoded paths field
	fReq := httptest.NewRequest(http.MethodPost, "/ops", strings.NewReader(`path=%2Fetc&paths=%5B%22%2Fetc%2Fhosts%22%5D&tail=true`))
	fReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	fRec := httptest.NewRecorder()
	fp, err := parseOpPayload(fRec, fReq)
	if err != nil {
		t.Fatalf("parseOpPayload form error: %v", err)
	}
	if fp.Path != "/etc" || !fp.Tail || len(fp.Paths) != 1 || fp.Paths[0] != "/etc/hosts" {
		t.Fatalf("unexpected form payload: %+v", fp)
	}
}
