package assets

import (
	"bytes"
	"io/fs"
	"path"
	"regexp"
	"testing"
)

var legacyByteUnitPattern = regexp.MustCompile(`\b(?:KiB|MiB|GiB|TiB|PiB)\b`)

func TestUIAssetsUseShortByteUnitLabels(t *testing.T) {
	var humanBytesDefinitions int
	roots := []struct {
		fsys fs.FS
		root string
		keep func(string) bool
	}{
		{
			fsys: templateFiles,
			root: "web/templates",
			keep: func(filePath string) bool { return path.Ext(filePath) == ".gohtml" },
		},
		{
			fsys: staticFiles,
			root: "web/static",
			keep: func(filePath string) bool { return path.Ext(filePath) == ".js" },
		},
	}

	for _, source := range roots {
		err := fs.WalkDir(source.fsys, source.root, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !source.keep(filePath) {
				return nil
			}
			content, err := fs.ReadFile(source.fsys, filePath)
			if err != nil {
				return err
			}
			if unit := legacyByteUnitPattern.Find(content); unit != nil {
				t.Errorf("%s contains legacy UI byte unit %q", filePath, unit)
			}
			if path.Ext(filePath) == ".js" {
				humanBytesDefinitions += bytes.Count(content, []byte("function humanBytes("))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", source.root, err)
		}
	}

	if humanBytesDefinitions != 1 {
		t.Errorf("JavaScript humanBytes definitions = %d, want one shared implementation", humanBytesDefinitions)
	}
}
