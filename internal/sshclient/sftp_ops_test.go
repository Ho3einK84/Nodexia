package sshclient

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

func newTestSFTPPair(t *testing.T, workDir string) *sftp.Client {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	srv, err := sftp.NewServer(serverConn, sftp.WithServerWorkingDirectory(workDir))
	if err != nil {
		_ = serverConn.Close()
		_ = clientConn.Close()
		t.Fatalf("sftp.NewServer: %v", err)
	}
	go func() {
		_ = srv.Serve()
		_ = srv.Close()
	}()

	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		_ = clientConn.Close()
		_ = serverConn.Close()
		t.Fatalf("sftp.NewClientPipe: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return client
}

func TestSFTPListStatAndSymlinks(t *testing.T) {
	root := t.TempDir()
	client := newTestSFTPPair(t, root)
	ctx := context.Background()

	if err := os.MkdirAll(filepath.Join(root, "sub", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "nested", "inner.conf"), []byte("k=v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "link_sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "does-not-exist"), filepath.Join(root, "link_broken")); err != nil {
		t.Fatal(err)
	}

	listing, err := listDirectoryWithClient(ctx, client, "~")
	if err != nil {
		t.Fatalf("listDirectoryWithClient(~): %v", err)
	}
	if listing.Path != root {
		t.Fatalf("listing.Path = %q, want %q", listing.Path, root)
	}
	if len(listing.Entries) != 4 {
		t.Fatalf("got %d entries, want 4", len(listing.Entries))
	}

	byName := map[string]FileEntry{}
	for _, e := range listing.Entries {
		byName[e.Name] = e
	}
	if !byName["sub"].IsDir || byName["sub"].ModeOctal != "0755" {
		t.Errorf("unexpected dir entry: %+v", byName["sub"])
	}
	if byName["alpha.txt"].ModeOctal != "0640" || byName["alpha.txt"].Size != 5 {
		t.Errorf("unexpected file entry: %+v", byName["alpha.txt"])
	}
	if !byName["link_sub"].IsSymlink || !byName["link_sub"].LinkIsDir || byName["link_sub"].BrokenLink {
		t.Errorf("expected link_sub to be dir symlink, got %+v", byName["link_sub"])
	}
	if !byName["link_broken"].IsSymlink || !byName["link_broken"].BrokenLink {
		t.Errorf("expected link_broken to be broken symlink, got %+v", byName["link_broken"])
	}

	stat, err := statPathWithClient(ctx, client, filepath.Join(root, "sub"), true)
	if err != nil {
		t.Fatalf("statPathWithClient: %v", err)
	}
	if stat.DirDirs != 1 || stat.DirFiles != 1 || stat.DirTotalBytes != 4 {
		t.Errorf("unexpected recursive dir stat: %+v", stat)
	}
}

func TestSFTPReadTailAndWritePreservesPerms(t *testing.T) {
	root := t.TempDir()
	client := newTestSFTPPair(t, root)
	ctx := context.Background()

	// Create a multi-line UTF-8 log file with custom permissions 0750.
	logPath := filepath.Join(root, "app.log")
	lines := []string{
		"line-01 سلام دنیا",
		"line-02 warning check",
		"line-03 final line",
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o750); err != nil {
		t.Fatal(err)
	}

	// Tail last 28 bytes — must start cleanly on a line boundary and stay valid UTF-8.
	tailRes, err := readFileWithClient(client, logPath, 28, true)
	if err != nil {
		t.Fatalf("readFileWithClient(tail): %v", err)
	}
	if !tailRes.IsTail || !tailRes.Truncated || tailRes.IsBinary {
		t.Fatalf("unexpected tail flags: %+v", tailRes)
	}
	if !strings.Contains(string(tailRes.Content), "line-03 final line") {
		t.Errorf("tail output missing last line: %q", string(tailRes.Content))
	}

	// Binary detection.
	binPath := filepath.Join(root, "bin.dat")
	if err := os.WriteFile(binPath, []byte{0x01, 0x00, 0x02, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	binRes, err := readFileWithClient(client, binPath, 1024, false)
	if err != nil {
		t.Fatalf("readFileWithClient(bin): %v", err)
	}
	if !binRes.IsBinary {
		t.Error("expected IsBinary=true for file with NUL bytes")
	}

	// WriteFileContent updating an existing file must keep 0750 permissions.
	if _, err := writeFileWithClient(client, logPath, []byte("updated\n"), false); err != nil {
		t.Fatalf("writeFileWithClient(update): %v", err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Errorf("file mode after write = %o, want 0750", info.Mode().Perm())
	}

	// createOnly must refuse overwriting an existing file.
	if _, err := writeFileWithClient(client, logPath, []byte("nope"), true); err == nil {
		t.Fatal("expected createOnly=true to fail on existing file")
	}
	// createOnly succeeds for a brand-new file.
	newPath := filepath.Join(root, "fresh.conf")
	if _, err := writeFileWithClient(client, newPath, []byte("ok=1\n"), true); err != nil {
		t.Fatalf("writeFileWithClient(createOnly): %v", err)
	}
	_ = ctx
}

func TestSFTPCopyMoveChmodRemoveAndSymlink(t *testing.T) {
	root := t.TempDir()
	client := newTestSFTPPair(t, root)
	ctx := context.Background()

	srcDir := filepath.Join(root, "src")
	dstDir := filepath.Join(root, "dst")
	_ = os.MkdirAll(filepath.Join(srcDir, "nested"), 0o755)
	_ = os.MkdirAll(dstDir, 0o755)
	fileA := filepath.Join(srcDir, "config.yml")
	_ = os.WriteFile(fileA, []byte("port: 8080\n"), 0o640)
	_ = os.WriteFile(filepath.Join(srcDir, "nested", "a.txt"), []byte("aaa"), 0o644)

	// Duplicate in same directory creates config_copy.yml
	dupRes, err := copyPathsWithClient(ctx, client, []string{fileA}, srcDir, true)
	if err != nil || len(dupRes.Failed) != 0 || len(dupRes.Succeeded) != 1 {
		t.Fatalf("duplicate failed: err=%v res=%+v", err, dupRes)
	}
	if filepath.Base(dupRes.Succeeded[0]) != "config_copy.yml" {
		t.Fatalf("duplicate path = %q, want config_copy.yml", dupRes.Succeeded[0])
	}

	// Copy folder recursively into dstDir.
	copyRes, err := copyPathsWithClient(ctx, client, []string{filepath.Join(srcDir, "nested")}, dstDir, false)
	if err != nil || len(copyRes.Failed) != 0 {
		t.Fatalf("copy folder failed: err=%v res=%+v", err, copyRes)
	}
	got, err := os.ReadFile(filepath.Join(dstDir, "nested", "a.txt"))
	if err != nil || string(got) != "aaa" {
		t.Fatalf("copied nested file content = %q, err = %v", string(got), err)
	}

	// Move file into dstDir, and verify moving dir into itself is rejected.
	moveRes, err := movePathsWithClient(ctx, client, []string{dupRes.Succeeded[0], srcDir}, dstDir, false)
	if err != nil || len(moveRes.Succeeded) != 2 {
		t.Fatalf("move failed: err=%v res=%+v", err, moveRes)
	}
	selfMove, err := movePathsWithClient(ctx, client, []string{dstDir}, filepath.Join(dstDir, "nested"), false)
	if err != nil || len(selfMove.Failed) != 1 {
		t.Fatalf("expected self-move rejection, got err=%v res=%+v", err, selfMove)
	}

	// Recursive chmod.
	chmodRes := chmodPathsWithClient(ctx, client, []string{filepath.Join(dstDir, "nested")}, 0o700, true)
	if len(chmodRes.Failed) != 0 {
		t.Fatalf("chmod failed: %+v", chmodRes)
	}
	if st, _ := os.Stat(filepath.Join(dstDir, "nested", "a.txt")); st.Mode().Perm() != 0o700 {
		t.Errorf("chmod recursive file perm = %o, want 0700", st.Mode().Perm())
	}

	// Create symlink to dstDir/nested and delete the symlink — target dir MUST survive!
	sym := filepath.Join(root, "sym_nested")
	if err := createSymlinkWithClient(client, filepath.Join(dstDir, "nested"), sym); err != nil {
		t.Fatalf("createSymlinkWithClient: %v", err)
	}
	remRes := batchRemoveWithClient(ctx, client, []string{sym}, true)
	if len(remRes.Failed) != 0 || len(remRes.Succeeded) != 1 {
		t.Fatalf("batchRemove symlink failed: %+v", remRes)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "nested", "a.txt")); err != nil {
		t.Fatalf("removing symlink must NOT delete target directory contents: %v", err)
	}
}

func TestSFTPSearchCompressAndExtractWithZipSlipGuard(t *testing.T) {
	root := t.TempDir()
	client := newTestSFTPPair(t, root)
	ctx := context.Background()

	work := filepath.Join(root, "work")
	_ = os.MkdirAll(filepath.Join(work, "sub"), 0o755)
	_ = os.MkdirAll(filepath.Join(work, ".hidden"), 0o755)
	_ = os.WriteFile(filepath.Join(work, "sub", "nginx.conf"), []byte("server_name example.com;\nlisten 443;\n"), 0o644)
	_ = os.WriteFile(filepath.Join(work, ".hidden", "secret.conf"), []byte("secret=1\n"), 0o600)

	// Filename search excluding hidden.
	s1, err := searchFilesWithClient(ctx, client, work, "conf", false, false)
	if err != nil {
		t.Fatalf("searchFilesWithClient(name): %v", err)
	}
	if len(s1.Matches) != 1 || s1.Matches[0].Entry.Name != "nginx.conf" {
		t.Fatalf("unexpected filename search matches: %+v", s1.Matches)
	}

	// Content search including hidden.
	s2, err := searchFilesWithClient(ctx, client, work, "secret=1", true, true)
	if err != nil {
		t.Fatalf("searchFilesWithClient(content): %v", err)
	}
	if len(s2.Matches) != 1 || s2.Matches[0].Line != 1 {
		t.Fatalf("unexpected content search matches: %+v", s2.Matches)
	}

	// Compress sub into bundle.tar.gz and bundle.zip, then extract both.
	if _, err := compressPathsWithClient(ctx, client, work, []string{"sub"}, "bundle.tar.gz", "tar.gz"); err != nil {
		t.Fatalf("compress tar.gz: %v", err)
	}
	if _, err := compressPathsWithClient(ctx, client, work, []string{"sub"}, "bundle.zip", "zip"); err != nil {
		t.Fatalf("compress zip: %v", err)
	}

	extTar := filepath.Join(root, "out_tar")
	ex1, err := extractArchiveWithClient(ctx, client, filepath.Join(work, "bundle.tar.gz"), extTar)
	if err != nil || ex1.ExtractedFiles != 1 {
		t.Fatalf("extract tar.gz failed: err=%v res=%+v", err, ex1)
	}
	if _, err := os.Stat(filepath.Join(extTar, "sub", "nginx.conf")); err != nil {
		t.Fatalf("extracted tar file missing: %v", err)
	}

	extZip := filepath.Join(root, "out_zip")
	ex2, err := extractArchiveWithClient(ctx, client, filepath.Join(work, "bundle.zip"), extZip)
	if err != nil || ex2.ExtractedFiles != 1 {
		t.Fatalf("extract zip failed: err=%v res=%+v", err, ex2)
	}
	if _, err := os.Stat(filepath.Join(extZip, "sub", "nginx.conf")); err != nil {
		t.Fatalf("extracted zip file missing: %v", err)
	}

	// ZipSlip defense: craft malicious zip and tar.gz with ../evil and symlink entries.
	var zBuf bytes.Buffer
	zw := zip.NewWriter(&zBuf)
	wEvil, _ := zw.Create("../../evil.txt")
	_, _ = wEvil.Write([]byte("pwn"))
	wGood, _ := zw.Create("safe/ok.txt")
	_, _ = wGood.Write([]byte("good"))
	_ = zw.Close()

	evilZip := filepath.Join(root, "evil.zip")
	_ = os.WriteFile(evilZip, zBuf.Bytes(), 0o644)
	safeDest := filepath.Join(root, "safe_out")
	exEvil, err := extractArchiveWithClient(ctx, client, evilZip, safeDest)
	if err != nil {
		t.Fatalf("extract evil.zip error: %v", err)
	}
	if exEvil.SkippedEntries != 1 || exEvil.ExtractedFiles != 1 {
		t.Fatalf("expected 1 skipped ZipSlip entry and 1 safe file, got %+v", exEvil)
	}
	if _, err := os.Stat(filepath.Join(root, "evil.txt")); err == nil {
		t.Fatal("ZipSlip wrote outside extraction directory!")
	}

	// Also verify tar symlink skipping.
	var tBuf bytes.Buffer
	gw := gzip.NewWriter(&tBuf)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{Name: "link_out", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	body := []byte("ok")
	_ = tw.WriteHeader(&tar.Header{Name: "ok.txt", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gw.Close()
	evilTar := filepath.Join(root, "evil.tgz")
	_ = os.WriteFile(evilTar, tBuf.Bytes(), 0o644)
	exTarEvil, err := extractArchiveWithClient(ctx, client, evilTar, filepath.Join(root, "safe_tar_out"))
	if err != nil {
		t.Fatalf("extract evil.tgz error: %v", err)
	}
	if exTarEvil.SkippedEntries != 1 || exTarEvil.ExtractedFiles != 1 {
		t.Fatalf("expected 1 skipped symlink and 1 extracted file, got %+v", exTarEvil)
	}
}
