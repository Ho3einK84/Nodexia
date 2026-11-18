package files

import "testing"

func TestContentDispositionPreservesDotfiles(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		// A leading dot must survive as %2E so browsers don't strip it to "env".
		{".env", "attachment; filename=\".env\"; filename*=UTF-8''%2Eenv"},
		{"file.txt", "attachment; filename=\"file.txt\"; filename*=UTF-8''file%2Etxt"},
		{"my report.pdf", "attachment; filename=\"my report.pdf\"; filename*=UTF-8''my%20report%2Epdf"},
	}
	for _, c := range cases {
		if got := contentDisposition(c.name); got != c.want {
			t.Errorf("contentDisposition(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
