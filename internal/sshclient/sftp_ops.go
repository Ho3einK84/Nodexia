package sshclient

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
)

const (
	maxStatWalkEntries   = 15000
	maxSearchWalkEntries = 8000
	maxSearchResults     = 200
	maxSearchDepth       = 8
	maxSearchContentSize = 512 * 1024
	maxExtractFiles      = 10000
	maxExtractBytes      = 1 << 30 // 1 GiB
	maxCopyDepth         = 64
	copyBufSize          = 256 * 1024
)

type PathStat struct {
	Name          string
	Path          string
	IsDir         bool
	IsSymlink     bool
	LinkTarget    string
	LinkIsDir     bool
	BrokenLink    bool
	Size          int64
	Mode          string
	ModeOctal     string
	UID           uint32
	GID           uint32
	ModifiedAt    time.Time
	AccessTime    time.Time
	DirFiles      int
	DirDirs       int
	DirTotalBytes int64
	SizeTruncated bool
}

type RemoteReadResult struct {
	Name       string
	Path       string
	Size       int64
	Mode       string
	ModeOctal  string
	ModifiedAt time.Time
	UID        uint32
	GID        uint32
	Content    []byte
	Truncated  bool
	IsTail     bool
	IsBinary   bool
}

type WriteResult struct {
	Name       string
	Path       string
	Size       int64
	Mode       string
	ModeOctal  string
	ModifiedAt time.Time
}

type BatchFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type BatchOpResult struct {
	Succeeded []string       `json:"succeeded"`
	Failed    []BatchFailure `json:"failed"`
}

type SearchMatch struct {
	Entry   FileEntry
	RelPath string
	Line    int
	Snippet string
}

type SearchResult struct {
	RootDir   string
	Query     string
	Matches   []SearchMatch
	Truncated bool
	Scanned   int
}

type ExtractResult struct {
	DestDir        string
	ExtractedFiles int
	ExtractedDirs  int
	BytesWritten   int64
	SkippedEntries int
}

func (s *Service) ListDirectory(ctx context.Context, req ConnectionRequest, remotePath string) (DirectoryListing, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return DirectoryListing{}, err
	}
	defer cleanup()

	return listDirectoryWithClient(ctx, sftpClient, remotePath)
}

func listDirectoryWithClient(ctx context.Context, client *sftp.Client, remotePath string) (DirectoryListing, error) {
	homeDir, _ := client.Getwd()
	resolvedPath := resolveRemoteUserPath(remotePath, homeDir)

	entries, err := client.ReadDirContext(ctx, resolvedPath)
	if err != nil {
		return DirectoryListing{}, fmt.Errorf("sshclient: read directory %s: %w", resolvedPath, err)
	}

	items := make([]FileEntry, 0, len(entries))
	symlinkIndices := make([]int, 0)
	for _, entry := range entries {
		item := newFileEntry(resolvedPath, entry)
		if item.IsSymlink {
			symlinkIndices = append(symlinkIndices, len(items))
		}
		items = append(items, item)
	}

	if len(symlinkIndices) > 0 {
		resolveSymlinkMetadata(ctx, client, items, symlinkIndices)
	}

	sort.Slice(items, func(i, j int) bool {
		iDir := items[i].IsDir || items[i].LinkIsDir
		jDir := items[j].IsDir || items[j].LinkIsDir
		if iDir != jDir {
			return iDir
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	var vfsStats *FilesystemStats
	if vfs, vfsErr := client.StatVFS(resolvedPath); vfsErr == nil && vfs != nil && vfs.Blocks > 0 {
		bsize := vfs.Frsize
		if bsize == 0 {
			bsize = vfs.Bsize
		}
		total := vfs.Blocks * bsize
		free := vfs.Bfree * bsize
		avail := vfs.Bavail * bsize
		var used uint64
		if total >= free {
			used = total - free
		}
		var usedPct float64
		if total > 0 {
			usedPct = float64(used) / float64(total) * 100
		}
		vfsStats = &FilesystemStats{
			TotalBytes:     total,
			FreeBytes:      free,
			AvailableBytes: avail,
			UsedBytes:      used,
			UsedPercent:    usedPct,
		}
	}

	return DirectoryListing{
		Path:    resolvedPath,
		HomeDir: homeDir,
		VFS:     vfsStats,
		Entries: items,
	}, nil
}

func resolveSymlinkMetadata(ctx context.Context, client *sftp.Client, items []FileEntry, indices []int) {
	// Cap total resolved symlinks per listing so huge directories stay bounded.
	const maxResolve = 256
	if len(indices) > maxResolve {
		indices = indices[:maxResolve]
	}

	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, idx := range indices {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			p := items[i].Path
			if target, err := client.ReadLink(p); err == nil {
				items[i].LinkTarget = target
			}
			if targetInfo, err := client.Stat(p); err == nil {
				items[i].LinkIsDir = targetInfo.IsDir()
				if !targetInfo.IsDir() {
					items[i].Size = targetInfo.Size()
				}
			} else {
				items[i].BrokenLink = true
			}
		}(idx)
	}
	wg.Wait()
}

func (s *Service) OpenFile(ctx context.Context, req ConnectionRequest, remotePath string) (RemoteFile, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return RemoteFile{}, err
	}

	info, err := sftpClient.Stat(remotePath)
	if err != nil {
		cleanup()
		return RemoteFile{}, fmt.Errorf("sshclient: stat file %s: %w", remotePath, err)
	}
	if info.IsDir() {
		cleanup()
		return RemoteFile{}, fmt.Errorf("sshclient: %s is a directory", remotePath)
	}

	file, err := sftpClient.Open(remotePath)
	if err != nil {
		cleanup()
		return RemoteFile{}, fmt.Errorf("sshclient: open file %s: %w", remotePath, err)
	}

	return RemoteFile{
		Name:       info.Name(),
		Path:       remotePath,
		Size:       info.Size(),
		Mode:       info.Mode().String(),
		ModeOctal:  formatModeOctal(info.Mode()),
		ModifiedAt: info.ModTime().UTC(),
		Content: &remoteReadCloser{
			reader:  file,
			cleanup: cleanup,
			closers: nil,
		},
	}, nil
}

// UploadFile streams content into remotePath over SFTP, creating or truncating
// the destination. It refuses to overwrite an existing directory and returns
// the number of bytes written.
func (s *Service) UploadFile(ctx context.Context, req ConnectionRequest, remotePath string, content io.Reader) (int64, error) {
	return s.UploadFileWithOptions(ctx, req, remotePath, content, false)
}

// UploadFileWithOptions streams content into remotePath over SFTP, optionally
// creating missing parent directories for folder uploads and preserving existing
// file permissions on overwrite.
func (s *Service) UploadFileWithOptions(ctx context.Context, req ConnectionRequest, remotePath string, content io.Reader, createParents bool) (int64, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	return uploadFileWithClient(sftpClient, remotePath, content, createParents)
}

// UploadFileExclusive creates a brand-new remote file using O_EXCL (refusing to
// overwrite or follow pre-existing symlinks in shared directories such as /tmp)
// and applies perm (e.g. 0600) to the created file.
func (s *Service) UploadFileExclusive(ctx context.Context, req ConnectionRequest, remotePath string, content io.Reader, perm os.FileMode) (int64, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	remoteFile, err := sftpClient.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return 0, fmt.Errorf("sshclient: create exclusive remote file %s: %w", remotePath, err)
	}
	if perm != 0 {
		_ = remoteFile.Chmod(perm)
	}

	buf := make([]byte, copyBufSize)
	written, copyErr := io.CopyBuffer(remoteFile, content, buf)
	closeErr := remoteFile.Close()
	if copyErr != nil {
		_ = sftpClient.Remove(remotePath)
		return written, fmt.Errorf("sshclient: write remote file %s: %w", remotePath, copyErr)
	}
	if closeErr != nil {
		_ = sftpClient.Remove(remotePath)
		return written, fmt.Errorf("sshclient: finalize remote file %s: %w", remotePath, closeErr)
	}
	return written, nil
}

func uploadFileWithClient(client *sftp.Client, remotePath string, content io.Reader, createParents bool) (int64, error) {
	if createParents {
		parent := path.Dir(remotePath)
		if parent != "" && parent != "/" && parent != "." {
			if err := client.MkdirAll(parent); err != nil {
				return 0, fmt.Errorf("sshclient: create parent directory %s: %w", parent, err)
			}
		}
	}

	if info, statErr := client.Stat(remotePath); statErr == nil && info.IsDir() {
		return 0, fmt.Errorf("sshclient: %s is a directory", remotePath)
	}

	remoteFile, err := client.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return 0, fmt.Errorf("sshclient: create remote file %s: %w", remotePath, err)
	}

	buf := make([]byte, copyBufSize)
	written, copyErr := io.CopyBuffer(remoteFile, content, buf)
	closeErr := remoteFile.Close()
	if copyErr != nil {
		return written, fmt.Errorf("sshclient: write remote file %s: %w", remotePath, copyErr)
	}
	if closeErr != nil {
		return written, fmt.Errorf("sshclient: finalize remote file %s: %w", remotePath, closeErr)
	}
	return written, nil
}

// RemovePath deletes a file, symlink, or directory over SFTP. Directories are
// removed recursively when recursive is true; symlinks are always unlinked
// safely without descending into their target.
func (s *Service) RemovePath(ctx context.Context, req ConnectionRequest, remotePath string, recursive bool) error {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return err
	}
	defer cleanup()

	return removePathWithClient(sftpClient, remotePath, recursive)
}

func removePathWithClient(client *sftp.Client, remotePath string, recursive bool) error {
	info, err := client.Lstat(remotePath)
	if err != nil {
		return fmt.Errorf("sshclient: stat %s: %w", remotePath, err)
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		err = client.Remove(remotePath)
	case info.IsDir() && recursive:
		err = client.RemoveAll(remotePath)
	case info.IsDir():
		err = client.RemoveDirectory(remotePath)
	default:
		err = client.Remove(remotePath)
	}
	if err != nil {
		return fmt.Errorf("sshclient: remove %s: %w", remotePath, err)
	}
	return nil
}

func (s *Service) BatchRemovePaths(ctx context.Context, req ConnectionRequest, paths []string, recursive bool) (BatchOpResult, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return BatchOpResult{}, err
	}
	defer cleanup()

	return batchRemoveWithClient(ctx, sftpClient, paths, recursive), nil
}

func batchRemoveWithClient(ctx context.Context, client *sftp.Client, paths []string, recursive bool) BatchOpResult {
	res := BatchOpResult{
		Succeeded: make([]string, 0, len(paths)),
		Failed:    make([]BatchFailure, 0),
	}
	for _, p := range paths {
		if ctx.Err() != nil {
			res.Failed = append(res.Failed, BatchFailure{Path: p, Error: ctx.Err().Error()})
			continue
		}
		cleaned := path.Clean(p)
		if cleaned == "" || cleaned == "/" || cleaned == "." {
			res.Failed = append(res.Failed, BatchFailure{Path: p, Error: "refusing to delete root path"})
			continue
		}
		if err := removePathWithClient(client, cleaned, recursive); err != nil {
			res.Failed = append(res.Failed, BatchFailure{Path: cleaned, Error: err.Error()})
			continue
		}
		res.Succeeded = append(res.Succeeded, cleaned)
	}
	return res
}

// RenamePath renames or moves oldPath to newPath over SFTP. It refuses to
// clobber an existing destination so renames never silently overwrite data.
func (s *Service) RenamePath(ctx context.Context, req ConnectionRequest, oldPath, newPath string) error {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return err
	}
	defer cleanup()

	return renamePathWithClient(sftpClient, oldPath, newPath)
}

func renamePathWithClient(client *sftp.Client, oldPath, newPath string) error {
	if _, err := client.Lstat(newPath); err == nil {
		return fmt.Errorf("sshclient: %s already exists", newPath)
	}
	if err := client.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("sshclient: rename %s to %s: %w", oldPath, newPath, err)
	}
	return nil
}

// MakeDirectory creates a single directory over SFTP. It refuses to act when a
// file or directory already exists at the target.
func (s *Service) MakeDirectory(ctx context.Context, req ConnectionRequest, remotePath string) error {
	return s.MakeDirectoryAll(ctx, req, remotePath, false)
}

func (s *Service) MakeDirectoryAll(ctx context.Context, req ConnectionRequest, remotePath string, recursive bool) error {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return err
	}
	defer cleanup()

	return makeDirectoryWithClient(sftpClient, remotePath, recursive)
}

func makeDirectoryWithClient(client *sftp.Client, remotePath string, recursive bool) error {
	if info, err := client.Lstat(remotePath); err == nil {
		if recursive && info.IsDir() {
			return nil
		}
		return fmt.Errorf("sshclient: %s already exists", remotePath)
	}
	if recursive {
		if err := client.MkdirAll(remotePath); err != nil {
			return fmt.Errorf("sshclient: create directories %s: %w", remotePath, err)
		}
		return nil
	}
	if err := client.Mkdir(remotePath); err != nil {
		return fmt.Errorf("sshclient: create directory %s: %w", remotePath, err)
	}
	return nil
}

func (s *Service) StatPath(ctx context.Context, req ConnectionRequest, remotePath string, calcDirSize bool) (PathStat, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return PathStat{}, err
	}
	defer cleanup()

	return statPathWithClient(ctx, sftpClient, remotePath, calcDirSize)
}

func statPathWithClient(ctx context.Context, client *sftp.Client, remotePath string, calcDirSize bool) (PathStat, error) {
	info, err := client.Lstat(remotePath)
	if err != nil {
		return PathStat{}, fmt.Errorf("sshclient: stat %s: %w", remotePath, err)
	}

	uid, gid, atime := extractFileStatMeta(info)
	stat := PathStat{
		Name:       info.Name(),
		Path:       remotePath,
		IsDir:      info.IsDir(),
		IsSymlink:  info.Mode()&os.ModeSymlink != 0,
		Size:       info.Size(),
		Mode:       info.Mode().String(),
		ModeOctal:  formatModeOctal(info.Mode()),
		UID:        uid,
		GID:        gid,
		ModifiedAt: info.ModTime().UTC(),
		AccessTime: atime,
	}

	if stat.IsSymlink {
		if target, lerr := client.ReadLink(remotePath); lerr == nil {
			stat.LinkTarget = target
		}
		if tinfo, serr := client.Stat(remotePath); serr == nil {
			stat.LinkIsDir = tinfo.IsDir()
			if !tinfo.IsDir() {
				stat.Size = tinfo.Size()
			}
		} else {
			stat.BrokenLink = true
		}
	}

	if calcDirSize && stat.IsDir {
		files, dirs, totalBytes, truncated := walkDirSize(ctx, client, remotePath, maxStatWalkEntries)
		stat.DirFiles = files
		stat.DirDirs = dirs
		stat.DirTotalBytes = totalBytes
		stat.SizeTruncated = truncated
	}

	return stat, nil
}

func walkDirSize(ctx context.Context, client *sftp.Client, root string, maxEntries int) (files int, dirs int, totalBytes int64, truncated bool) {
	stack := []string{root}
	seen := 0
	for len(stack) > 0 {
		if ctx.Err() != nil {
			truncated = true
			return
		}
		curr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := client.ReadDir(curr)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			seen++
			if seen > maxEntries {
				truncated = true
				return
			}
			if entry.Mode()&os.ModeSymlink != 0 {
				files++
				continue
			}
			if entry.IsDir() {
				dirs++
				stack = append(stack, joinRemoteChild(curr, entry.Name()))
			} else {
				files++
				totalBytes += entry.Size()
			}
		}
	}
	return
}

func (s *Service) ReadFileContent(ctx context.Context, req ConnectionRequest, remotePath string, maxBytes int64, tail bool) (RemoteReadResult, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return RemoteReadResult{}, err
	}
	defer cleanup()

	return readFileWithClient(sftpClient, remotePath, maxBytes, tail)
}

func readFileWithClient(client *sftp.Client, remotePath string, maxBytes int64, tail bool) (RemoteReadResult, error) {
	if maxBytes <= 0 {
		maxBytes = 2 << 20
	}
	info, err := client.Stat(remotePath)
	if err != nil {
		return RemoteReadResult{}, fmt.Errorf("sshclient: stat %s: %w", remotePath, err)
	}
	if info.IsDir() {
		return RemoteReadResult{}, fmt.Errorf("sshclient: %s is a directory", remotePath)
	}

	uid, gid, _ := extractFileStatMeta(info)
	res := RemoteReadResult{
		Name:       info.Name(),
		Path:       remotePath,
		Size:       info.Size(),
		Mode:       info.Mode().String(),
		ModeOctal:  formatModeOctal(info.Mode()),
		ModifiedAt: info.ModTime().UTC(),
		UID:        uid,
		GID:        gid,
	}

	if info.Size() == 0 {
		res.Content = []byte{}
		return res, nil
	}

	file, err := client.Open(remotePath)
	if err != nil {
		return RemoteReadResult{}, fmt.Errorf("sshclient: open %s: %w", remotePath, err)
	}
	defer func() { _ = file.Close() }()

	if tail && info.Size() > maxBytes {
		offset := info.Size() - maxBytes
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return RemoteReadResult{}, fmt.Errorf("sshclient: seek %s: %w", remotePath, err)
		}
		buf, err := io.ReadAll(io.LimitReader(file, maxBytes))
		if err != nil {
			return RemoteReadResult{}, fmt.Errorf("sshclient: read tail of %s: %w", remotePath, err)
		}
		if offset > 0 {
			if nl := bytes.IndexByte(buf, '\n'); nl >= 0 && nl < 4096 {
				buf = buf[nl+1:]
			} else {
				// Avoid splitting a multi-byte UTF-8 character at the boundary.
				for len(buf) > 0 && (buf[0]&0xC0) == 0x80 {
					buf = buf[1:]
				}
			}
		}
		res.Content = buf
		res.IsTail = true
		res.Truncated = true
		res.IsBinary = isBinaryContent(buf)
		return res, nil
	}

	buf, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return RemoteReadResult{}, fmt.Errorf("sshclient: read %s: %w", remotePath, err)
	}
	if int64(len(buf)) > maxBytes {
		buf = buf[:maxBytes]
		res.Truncated = true
		// Trim trailing incomplete UTF-8 sequence if truncated mid-rune.
		for trim := 0; trim < 3 && len(buf) > 0 && !utf8.Valid(buf); trim++ {
			if (buf[len(buf)-1] & 0x80) == 0 {
				break
			}
			last := buf[len(buf)-1]
			buf = buf[:len(buf)-1]
			if (last & 0xC0) == 0xC0 {
				break
			}
		}
	}
	res.Content = buf
	res.IsBinary = isBinaryContent(buf)
	return res, nil
}

func isBinaryContent(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	if bytes.IndexByte(probe, 0) >= 0 {
		return true
	}
	return !utf8.Valid(data)
}

func (s *Service) WriteFileContent(ctx context.Context, req ConnectionRequest, remotePath string, content []byte, createOnly bool) (WriteResult, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return WriteResult{}, err
	}
	defer cleanup()

	return writeFileWithClient(sftpClient, remotePath, content, createOnly)
}

func writeFileWithClient(client *sftp.Client, remotePath string, content []byte, createOnly bool) (WriteResult, error) {
	if createOnly {
		if _, err := client.Lstat(remotePath); err == nil {
			return WriteResult{}, fmt.Errorf("sshclient: %s already exists", remotePath)
		}
	} else {
		if info, err := client.Stat(remotePath); err == nil && info.IsDir() {
			return WriteResult{}, fmt.Errorf("sshclient: %s is a directory", remotePath)
		}
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if createOnly {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := client.OpenFile(remotePath, flags)
	if err != nil {
		return WriteResult{}, fmt.Errorf("sshclient: open %s for writing: %w", remotePath, err)
	}

	written := 0
	for written < len(content) {
		n, werr := f.Write(content[written:])
		written += n
		if werr != nil {
			_ = f.Close()
			return WriteResult{}, fmt.Errorf("sshclient: write %s: %w", remotePath, werr)
		}
	}
	if err := f.Close(); err != nil {
		return WriteResult{}, fmt.Errorf("sshclient: close %s: %w", remotePath, err)
	}

	info, err := client.Stat(remotePath)
	if err != nil {
		return WriteResult{
			Name: path.Base(remotePath),
			Path: remotePath,
			Size: int64(len(content)),
		}, nil
	}
	return WriteResult{
		Name:       info.Name(),
		Path:       remotePath,
		Size:       info.Size(),
		Mode:       info.Mode().String(),
		ModeOctal:  formatModeOctal(info.Mode()),
		ModifiedAt: info.ModTime().UTC(),
	}, nil
}

func (s *Service) ChmodPaths(ctx context.Context, req ConnectionRequest, paths []string, mode os.FileMode, recursive bool) (BatchOpResult, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return BatchOpResult{}, err
	}
	defer cleanup()

	return chmodPathsWithClient(ctx, sftpClient, paths, mode, recursive), nil
}

func chmodPathsWithClient(ctx context.Context, client *sftp.Client, paths []string, mode os.FileMode, recursive bool) BatchOpResult {
	res := BatchOpResult{
		Succeeded: make([]string, 0, len(paths)),
		Failed:    make([]BatchFailure, 0),
	}
	for _, raw := range paths {
		p := path.Clean(raw)
		if p == "" || p == "/" || p == "." {
			res.Failed = append(res.Failed, BatchFailure{Path: raw, Error: "invalid target path"})
			continue
		}
		info, err := client.Lstat(p)
		if err != nil {
			res.Failed = append(res.Failed, BatchFailure{Path: p, Error: err.Error()})
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			res.Failed = append(res.Failed, BatchFailure{Path: p, Error: "cannot chmod a symbolic link directly"})
			continue
		}
		if err := client.Chmod(p, mode); err != nil {
			res.Failed = append(res.Failed, BatchFailure{Path: p, Error: err.Error()})
			continue
		}
		if recursive && info.IsDir() {
			if rerr := chmodDirRecursive(ctx, client, p, mode); rerr != nil {
				res.Failed = append(res.Failed, BatchFailure{Path: p, Error: rerr.Error()})
				continue
			}
		}
		res.Succeeded = append(res.Succeeded, p)
	}
	return res
}

func chmodDirRecursive(ctx context.Context, client *sftp.Client, root string, mode os.FileMode) error {
	stack := []string{root}
	count := 0
	for len(stack) > 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		curr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := client.ReadDir(curr)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			count++
			if count > maxStatWalkEntries {
				return errors.New("recursive chmod limit exceeded")
			}
			if entry.Mode()&os.ModeSymlink != 0 {
				continue
			}
			child := joinRemoteChild(curr, entry.Name())
			if err := client.Chmod(child, mode); err != nil {
				return err
			}
			if entry.IsDir() {
				stack = append(stack, child)
			}
		}
	}
	return nil
}

func (s *Service) MovePaths(ctx context.Context, req ConnectionRequest, sources []string, destDir string, overwrite bool) (BatchOpResult, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return BatchOpResult{}, err
	}
	defer cleanup()

	return movePathsWithClient(ctx, sftpClient, sources, destDir, overwrite)
}

func movePathsWithClient(ctx context.Context, client *sftp.Client, sources []string, destDir string, overwrite bool) (BatchOpResult, error) {
	destDir = path.Clean(destDir)
	destInfo, err := client.Stat(destDir)
	if err != nil {
		return BatchOpResult{}, fmt.Errorf("sshclient: stat destination %s: %w", destDir, err)
	}
	if !destInfo.IsDir() {
		return BatchOpResult{}, fmt.Errorf("sshclient: destination %s is not a directory", destDir)
	}

	res := BatchOpResult{
		Succeeded: make([]string, 0, len(sources)),
		Failed:    make([]BatchFailure, 0),
	}
	for _, rawSrc := range sources {
		if ctx.Err() != nil {
			res.Failed = append(res.Failed, BatchFailure{Path: rawSrc, Error: ctx.Err().Error()})
			continue
		}
		src := path.Clean(rawSrc)
		if src == "" || src == "/" || src == "." {
			res.Failed = append(res.Failed, BatchFailure{Path: rawSrc, Error: "cannot move root directory"})
			continue
		}
		dst := joinRemoteChild(destDir, path.Base(src))
		if src == dst {
			res.Succeeded = append(res.Succeeded, src)
			continue
		}
		if dst == src || strings.HasPrefix(destDir+"/", src+"/") {
			res.Failed = append(res.Failed, BatchFailure{Path: src, Error: "cannot move a directory into itself"})
			continue
		}
		if _, statErr := client.Lstat(dst); statErr == nil {
			if !overwrite {
				res.Failed = append(res.Failed, BatchFailure{Path: src, Error: fmt.Sprintf("%s already exists", path.Base(dst))})
				continue
			}
			if err := client.PosixRename(src, dst); err == nil {
				res.Succeeded = append(res.Succeeded, dst)
				continue
			}
			if rerr := removePathWithClient(client, dst, true); rerr != nil {
				res.Failed = append(res.Failed, BatchFailure{Path: src, Error: rerr.Error()})
				continue
			}
		}
		if err := client.Rename(src, dst); err != nil {
			res.Failed = append(res.Failed, BatchFailure{Path: src, Error: err.Error()})
			continue
		}
		res.Succeeded = append(res.Succeeded, dst)
	}
	return res, nil
}

func (s *Service) CopyPaths(ctx context.Context, req ConnectionRequest, sources []string, destDir string, duplicateIfSame bool) (BatchOpResult, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return BatchOpResult{}, err
	}
	defer cleanup()

	return copyPathsWithClient(ctx, sftpClient, sources, destDir, duplicateIfSame)
}

func copyPathsWithClient(ctx context.Context, client *sftp.Client, sources []string, destDir string, duplicateIfSame bool) (BatchOpResult, error) {
	destDir = path.Clean(destDir)
	destInfo, err := client.Stat(destDir)
	if err != nil {
		return BatchOpResult{}, fmt.Errorf("sshclient: stat destination %s: %w", destDir, err)
	}
	if !destInfo.IsDir() {
		return BatchOpResult{}, fmt.Errorf("sshclient: destination %s is not a directory", destDir)
	}

	res := BatchOpResult{
		Succeeded: make([]string, 0, len(sources)),
		Failed:    make([]BatchFailure, 0),
	}
	for _, rawSrc := range sources {
		if ctx.Err() != nil {
			res.Failed = append(res.Failed, BatchFailure{Path: rawSrc, Error: ctx.Err().Error()})
			continue
		}
		src := path.Clean(rawSrc)
		if src == "" || src == "/" || src == "." {
			res.Failed = append(res.Failed, BatchFailure{Path: rawSrc, Error: "cannot copy root directory"})
			continue
		}
		baseName := path.Base(src)
		dst := joinRemoteChild(destDir, baseName)
		if strings.HasPrefix(destDir+"/", src+"/") {
			res.Failed = append(res.Failed, BatchFailure{Path: src, Error: "cannot copy a directory into itself"})
			continue
		}
		if _, statErr := client.Lstat(dst); statErr == nil {
			if !duplicateIfSame {
				res.Failed = append(res.Failed, BatchFailure{Path: src, Error: fmt.Sprintf("%s already exists", baseName)})
				continue
			}
			dst = uniqueCopyPath(client, destDir, baseName)
		}
		if err := copyEntryWithClient(ctx, client, src, dst, 0); err != nil {
			res.Failed = append(res.Failed, BatchFailure{Path: src, Error: err.Error()})
			continue
		}
		res.Succeeded = append(res.Succeeded, dst)
	}
	return res, nil
}

func uniqueCopyPath(client *sftp.Client, destDir, baseName string) string {
	ext := path.Ext(baseName)
	stem := strings.TrimSuffix(baseName, ext)
	if stem == "" {
		// Hidden dotfiles like ".env" have path.Ext(".env") == ".env" and stem == ""
		stem = baseName
		ext = ""
	}
	for i := 1; i <= 50; i++ {
		suffix := "_copy"
		if i > 1 {
			suffix = fmt.Sprintf("_copy_%d", i)
		}
		candidate := joinRemoteChild(destDir, stem+suffix+ext)
		if _, err := client.Lstat(candidate); err != nil {
			return candidate
		}
	}
	return joinRemoteChild(destDir, fmt.Sprintf("%s_copy_%d%s", stem, time.Now().Unix(), ext))
}

func copyEntryWithClient(ctx context.Context, client *sftp.Client, src, dst string, depth int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if depth > maxCopyDepth {
		return errors.New("maximum directory copy depth exceeded")
	}

	info, err := client.Lstat(src)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, lerr := client.ReadLink(src)
		if lerr != nil {
			return lerr
		}
		return client.Symlink(target, dst)
	}

	if info.IsDir() {
		if err := client.Mkdir(dst); err != nil {
			return err
		}
		_ = client.Chmod(dst, info.Mode().Perm())
		entries, err := client.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyEntryWithClient(ctx, client, joinRemoteChild(src, entry.Name()), joinRemoteChild(dst, entry.Name()), depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	in, err := client.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := client.Create(dst)
	if err != nil {
		return err
	}
	buf := make([]byte, copyBufSize)
	_, copyErr := io.CopyBuffer(out, in, buf)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	_ = client.Chmod(dst, info.Mode().Perm())
	return nil
}

func (s *Service) CreateSymlink(ctx context.Context, req ConnectionRequest, target, linkPath string) error {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return err
	}
	defer cleanup()

	return createSymlinkWithClient(sftpClient, target, linkPath)
}

func createSymlinkWithClient(client *sftp.Client, target, linkPath string) error {
	if strings.TrimSpace(target) == "" {
		return errors.New("sshclient: symlink target cannot be empty")
	}
	if _, err := client.Lstat(linkPath); err == nil {
		return fmt.Errorf("sshclient: %s already exists", linkPath)
	}
	if err := client.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("sshclient: create symlink %s -> %s: %w", linkPath, target, err)
	}
	return nil
}

func (s *Service) SearchFiles(ctx context.Context, req ConnectionRequest, rootDir, query string, searchContent, includeHidden bool) (SearchResult, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return SearchResult{}, err
	}
	defer cleanup()

	return searchFilesWithClient(ctx, sftpClient, rootDir, query, searchContent, includeHidden)
}

func searchFilesWithClient(ctx context.Context, client *sftp.Client, rootDir, query string, searchContent, includeHidden bool) (SearchResult, error) {
	homeDir, _ := client.Getwd()
	resolvedRoot := resolveRemoteUserPath(rootDir, homeDir)
	queryLower := strings.ToLower(strings.TrimSpace(query))
	if queryLower == "" {
		return SearchResult{RootDir: resolvedRoot, Query: query}, nil
	}

	type queueItem struct {
		dir   string
		depth int
	}

	res := SearchResult{
		RootDir: resolvedRoot,
		Query:   query,
		Matches: make([]SearchMatch, 0),
	}

	stack := []queueItem{{dir: resolvedRoot, depth: 0}}
	for len(stack) > 0 {
		if ctx.Err() != nil {
			res.Truncated = true
			return res, nil
		}
		curr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := client.ReadDir(curr.dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				res.Truncated = true
				return res, nil
			}
			res.Scanned++
			if res.Scanned > maxSearchWalkEntries || len(res.Matches) >= maxSearchResults {
				res.Truncated = true
				return res, nil
			}
			name := entry.Name()
			if !includeHidden && strings.HasPrefix(name, ".") {
				continue
			}

			childPath := joinRemoteChild(curr.dir, name)
			rel := strings.TrimPrefix(childPath, strings.TrimRight(resolvedRoot, "/")+"/")
			if resolvedRoot == "/" {
				rel = strings.TrimPrefix(childPath, "/")
			}
			isSymlink := entry.Mode()&os.ModeSymlink != 0

			if !searchContent {
				if strings.Contains(strings.ToLower(name), queryLower) {
					item := newFileEntry(curr.dir, entry)
					res.Matches = append(res.Matches, SearchMatch{
						Entry:   item,
						RelPath: rel,
					})
				}
			} else if !entry.IsDir() && !isSymlink && entry.Size() > 0 && entry.Size() <= maxSearchContentSize {
				if lineNo, snippet, ok := searchFileContentMatch(client, childPath, queryLower); ok {
					item := newFileEntry(curr.dir, entry)
					res.Matches = append(res.Matches, SearchMatch{
						Entry:   item,
						RelPath: rel,
						Line:    lineNo,
						Snippet: snippet,
					})
				}
			}

			if entry.IsDir() && !isSymlink && curr.depth < maxSearchDepth {
				stack = append(stack, queueItem{dir: childPath, depth: curr.depth + 1})
			}
		}
	}

	return res, nil
}

func searchFileContentMatch(client *sftp.Client, filePath, queryLower string) (int, string, bool) {
	f, err := client.Open(filePath)
	if err != nil {
		return 0, "", false
	}
	defer func() { _ = f.Close() }()

	// Quick binary check on first 1024 bytes.
	head := make([]byte, 1024)
	n, _ := f.Read(head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return 0, "", false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, "", false
	}

	scanner := bufio.NewScanner(io.LimitReader(f, maxSearchContentSize))
	scanner.Buffer(make([]byte, 0, 16*1024), 128*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), queryLower) {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) > 180 {
				trimmed = trimmed[:180] + "…"
			}
			return lineNo, trimmed, true
		}
	}
	return 0, "", false
}

func (s *Service) StreamArchive(ctx context.Context, req ConnectionRequest, paths []string, format string, w io.Writer) error {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return err
	}
	defer cleanup()

	return streamArchiveWithClient(ctx, sftpClient, paths, format, w)
}

func (s *Service) CompressPaths(ctx context.Context, req ConnectionRequest, baseDir string, names []string, archiveName, format string) (FileEntry, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return FileEntry{}, err
	}
	defer cleanup()

	return compressPathsWithClient(ctx, sftpClient, baseDir, names, archiveName, format)
}

func compressPathsWithClient(ctx context.Context, client *sftp.Client, baseDir string, names []string, archiveName, format string) (FileEntry, error) {
	outPath := joinRemoteChild(baseDir, archiveName)
	if _, err := client.Lstat(outPath); err == nil {
		return FileEntry{}, fmt.Errorf("sshclient: %s already exists", archiveName)
	}

	paths := make([]string, 0, len(names))
	for _, n := range names {
		paths = append(paths, joinRemoteChild(baseDir, n))
	}

	outFile, err := client.Create(outPath)
	if err != nil {
		return FileEntry{}, fmt.Errorf("sshclient: create archive %s: %w", outPath, err)
	}
	if streamErr := streamArchiveWithClient(ctx, client, paths, format, outFile); streamErr != nil {
		_ = outFile.Close()
		_ = client.Remove(outPath)
		return FileEntry{}, streamErr
	}
	if closeErr := outFile.Close(); closeErr != nil {
		_ = client.Remove(outPath)
		return FileEntry{}, fmt.Errorf("sshclient: finalize archive %s: %w", outPath, closeErr)
	}

	info, err := client.Lstat(outPath)
	if err != nil {
		return FileEntry{Name: archiveName, Path: outPath}, nil
	}
	return newFileEntry(baseDir, info), nil
}

func streamArchiveWithClient(ctx context.Context, client *sftp.Client, paths []string, format string, w io.Writer) error {
	if len(paths) == 0 {
		return errors.New("sshclient: no paths specified for archive")
	}

	switch format {
	case "", "zip":
		zw := zip.NewWriter(w)
		usedNames := map[string]int{}
		for _, p := range paths {
			if ctx.Err() != nil {
				_ = zw.Close()
				return ctx.Err()
			}
			arcRoot := uniqueArchiveRootName(path.Base(p), usedNames)
			if err := writeZipEntry(ctx, client, zw, p, arcRoot, 0); err != nil {
				_ = zw.Close()
				return err
			}
		}
		return zw.Close()
	case "tar.gz", "tgz":
		gw := gzip.NewWriter(w)
		tw := tar.NewWriter(gw)
		usedNames := map[string]int{}
		for _, p := range paths {
			if ctx.Err() != nil {
				_ = tw.Close()
				_ = gw.Close()
				return ctx.Err()
			}
			arcRoot := uniqueArchiveRootName(path.Base(p), usedNames)
			if err := writeTarEntry(ctx, client, tw, p, arcRoot, 0); err != nil {
				_ = tw.Close()
				_ = gw.Close()
				return err
			}
		}
		if err := tw.Close(); err != nil {
			_ = gw.Close()
			return err
		}
		return gw.Close()
	default:
		return fmt.Errorf("sshclient: unsupported archive format %q", format)
	}
}

func uniqueArchiveRootName(base string, used map[string]int) string {
	if base == "" || base == "/" || base == "." {
		base = "root"
	}
	count := used[base]
	used[base] = count + 1
	if count == 0 {
		return base
	}
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return fmt.Sprintf("%s_%d%s", stem, count+1, ext)
}

func writeZipEntry(ctx context.Context, client *sftp.Client, zw *zip.Writer, remotePath, arcPath string, depth int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if depth > maxCopyDepth {
		return errors.New("sshclient: maximum archive directory depth exceeded")
	}
	info, err := client.Lstat(remotePath)
	if err != nil {
		return err
	}
	// Skip symlinks to avoid cyclic loops or escaping target paths.
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if info.IsDir() {
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = strings.TrimSuffix(arcPath, "/") + "/"
		if _, err := zw.CreateHeader(hdr); err != nil {
			return err
		}
		entries, err := client.ReadDir(remotePath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := writeZipEntry(ctx, client, zw, joinRemoteChild(remotePath, entry.Name()), path.Join(arcPath, entry.Name()), depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = arcPath
	hdr.Method = zip.Deflate
	entryWriter, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	f, err := client.Open(remotePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, copyBufSize)
	_, err = io.CopyBuffer(entryWriter, f, buf)
	return err
}

func writeTarEntry(ctx context.Context, client *sftp.Client, tw *tar.Writer, remotePath, arcPath string, depth int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if depth > maxCopyDepth {
		return errors.New("sshclient: maximum archive directory depth exceeded")
	}
	info, err := client.Lstat(remotePath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, lerr := client.ReadLink(remotePath)
		if lerr != nil {
			return lerr
		}
		hdr, herr := tar.FileInfoHeader(info, target)
		if herr != nil {
			return herr
		}
		hdr.Name = arcPath
		return tw.WriteHeader(hdr)
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	if info.IsDir() {
		hdr.Name = strings.TrimSuffix(arcPath, "/") + "/"
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		entries, err := client.ReadDir(remotePath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := writeTarEntry(ctx, client, tw, joinRemoteChild(remotePath, entry.Name()), path.Join(arcPath, entry.Name()), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	hdr.Name = arcPath
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := client.Open(remotePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, copyBufSize)
	_, err = io.CopyBuffer(tw, f, buf)
	return err
}

func (s *Service) ExtractArchive(ctx context.Context, req ConnectionRequest, archivePath, destDir string) (ExtractResult, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return ExtractResult{}, err
	}
	defer cleanup()

	return extractArchiveWithClient(ctx, sftpClient, archivePath, destDir)
}

func extractArchiveWithClient(ctx context.Context, client *sftp.Client, archivePath, destDir string) (ExtractResult, error) {
	destDir = path.Clean(destDir)
	if destDir == "" || !strings.HasPrefix(destDir, "/") {
		return ExtractResult{}, errors.New("sshclient: invalid extraction directory")
	}

	info, err := client.Stat(archivePath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("sshclient: stat archive %s: %w", archivePath, err)
	}
	if info.IsDir() {
		return ExtractResult{}, fmt.Errorf("sshclient: %s is a directory", archivePath)
	}

	if err := client.MkdirAll(destDir); err != nil {
		return ExtractResult{}, fmt.Errorf("sshclient: ensure destination %s: %w", destDir, err)
	}

	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZipWithClient(ctx, client, archivePath, info.Size(), destDir)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarWithClient(ctx, client, archivePath, destDir, true)
	case strings.HasSuffix(lower, ".tar"):
		return extractTarWithClient(ctx, client, archivePath, destDir, false)
	default:
		return ExtractResult{}, errors.New("sshclient: unsupported archive format (supported: .zip, .tar.gz, .tgz, .tar)")
	}
}

func extractZipWithClient(ctx context.Context, client *sftp.Client, archivePath string, size int64, destDir string) (ExtractResult, error) {
	f, err := client.Open(archivePath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("sshclient: open zip %s: %w", archivePath, err)
	}
	defer func() { _ = f.Close() }()

	zr, err := zip.NewReader(f, size)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("sshclient: parse zip %s: %w", archivePath, err)
	}

	res := ExtractResult{DestDir: destDir}
	buf := make([]byte, copyBufSize)
	for _, entry := range zr.File {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if res.ExtractedFiles+res.ExtractedDirs >= maxExtractFiles {
			return res, errors.New("sshclient: archive exceeds maximum entry count")
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			res.SkippedEntries++
			continue
		}
		targetPath, ok := safeArchiveTarget(destDir, entry.Name)
		if !ok {
			res.SkippedEntries++
			continue
		}
		if targetPath == destDir {
			continue
		}
		if entry.FileInfo().IsDir() {
			if err := client.MkdirAll(targetPath); err != nil {
				return res, err
			}
			res.ExtractedDirs++
			continue
		}

		parent := path.Dir(targetPath)
		if err := client.MkdirAll(parent); err != nil {
			return res, err
		}
		rc, err := entry.Open()
		if err != nil {
			return res, err
		}
		out, err := client.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			_ = rc.Close()
			return res, err
		}
		remaining := maxExtractBytes - res.BytesWritten
		if remaining < 0 {
			_ = rc.Close()
			_ = out.Close()
			return res, errors.New("sshclient: extracted size exceeds safety limit")
		}
		n, copyErr := io.CopyBuffer(out, io.LimitReader(rc, remaining+1), buf)
		_ = rc.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return res, copyErr
		}
		if closeErr != nil {
			return res, closeErr
		}
		res.BytesWritten += n
		if res.BytesWritten > maxExtractBytes {
			return res, errors.New("sshclient: extracted size exceeds safety limit")
		}
		if perm := entry.Mode().Perm(); perm != 0 {
			_ = client.Chmod(targetPath, perm)
		}
		res.ExtractedFiles++
	}
	return res, nil
}

func extractTarWithClient(ctx context.Context, client *sftp.Client, archivePath, destDir string, gzipped bool) (ExtractResult, error) {
	f, err := client.Open(archivePath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("sshclient: open tar %s: %w", archivePath, err)
	}
	defer func() { _ = f.Close() }()

	var r io.Reader = f
	if gzipped {
		gr, gerr := gzip.NewReader(f)
		if gerr != nil {
			return ExtractResult{}, fmt.Errorf("sshclient: open gzip %s: %w", archivePath, gerr)
		}
		defer func() { _ = gr.Close() }()
		r = gr
	}

	tr := tar.NewReader(r)
	res := ExtractResult{DestDir: destDir}
	buf := make([]byte, copyBufSize)
	for {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return res, fmt.Errorf("sshclient: read tar %s: %w", archivePath, err)
		}
		if res.ExtractedFiles+res.ExtractedDirs >= maxExtractFiles {
			return res, errors.New("sshclient: archive exceeds maximum entry count")
		}
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			res.SkippedEntries++
			continue
		}
		targetPath, ok := safeArchiveTarget(destDir, hdr.Name)
		if !ok {
			res.SkippedEntries++
			continue
		}
		if targetPath == destDir {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := client.MkdirAll(targetPath); err != nil {
				return res, err
			}
			res.ExtractedDirs++
		case tar.TypeReg:
			if err := client.MkdirAll(path.Dir(targetPath)); err != nil {
				return res, err
			}
			out, err := client.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
			if err != nil {
				return res, err
			}
			remaining := maxExtractBytes - res.BytesWritten
			if remaining < 0 {
				_ = out.Close()
				return res, errors.New("sshclient: extracted size exceeds safety limit")
			}
			n, copyErr := io.CopyBuffer(out, io.LimitReader(tr, remaining+1), buf)
			closeErr := out.Close()
			if copyErr != nil {
				return res, copyErr
			}
			if closeErr != nil {
				return res, closeErr
			}
			res.BytesWritten += n
			if res.BytesWritten > maxExtractBytes {
				return res, errors.New("sshclient: extracted size exceeds safety limit")
			}
			if perm := os.FileMode(hdr.Mode).Perm(); perm != 0 {
				_ = client.Chmod(targetPath, perm)
			}
			res.ExtractedFiles++
		default:
			res.SkippedEntries++
		}
	}
	return res, nil
}

func safeArchiveTarget(destDir, rawEntryName string) (string, bool) {
	if strings.ContainsRune(rawEntryName, '\x00') {
		return "", false
	}
	norm := strings.ReplaceAll(rawEntryName, "\\", "/")
	norm = strings.TrimSpace(norm)
	if norm == "" || strings.HasPrefix(norm, "/") {
		return "", false
	}
	for _, seg := range strings.Split(norm, "/") {
		if seg == ".." {
			return "", false
		}
	}
	cleaned := path.Clean(norm)
	if cleaned == "." {
		return destDir, true
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	target := joinRemoteChild(destDir, cleaned)
	if destDir == "/" {
		return target, strings.HasPrefix(target, "/")
	}
	prefix := strings.TrimRight(destDir, "/") + "/"
	if target != destDir && !strings.HasPrefix(target, prefix) {
		return "", false
	}
	return target, true
}

func resolveRemoteUserPath(rawPath, homeDir string) string {
	trimmed := strings.TrimSpace(rawPath)
	home := strings.TrimSpace(homeDir)
	if home == "" {
		home = "/"
	}
	switch {
	case trimmed == "", trimmed == "~":
		return path.Clean(home)
	case strings.HasPrefix(trimmed, "~/"):
		return path.Clean(joinRemoteChild(home, trimmed[2:]))
	default:
		cleaned := path.Clean(trimmed)
		if !strings.HasPrefix(cleaned, "/") {
			return path.Clean(joinRemoteChild(home, cleaned))
		}
		return cleaned
	}
}

func joinRemoteChild(parent, name string) string {
	parent = strings.TrimRight(parent, "/")
	name = strings.TrimLeft(name, "/")
	if parent == "" {
		return "/" + name
	}
	if name == "" {
		return parent
	}
	return parent + "/" + name
}

func formatModeOctal(m os.FileMode) string {
	perm := uint32(m.Perm())
	if m&os.ModeSetuid != 0 {
		perm |= 0o4000
	}
	if m&os.ModeSetgid != 0 {
		perm |= 0o2000
	}
	if m&os.ModeSticky != 0 {
		perm |= 0o1000
	}
	return fmt.Sprintf("%04o", perm)
}

func extractFileStatMeta(info os.FileInfo) (uid uint32, gid uint32, atime time.Time) {
	if sys, ok := info.Sys().(*sftp.FileStat); ok && sys != nil {
		return sys.UID, sys.GID, sys.AccessTime().UTC()
	}
	return 0, 0, time.Time{}
}

func newFileEntry(parent string, info os.FileInfo) FileEntry {
	childPath := joinRemoteChild(parent, info.Name())
	uid, gid, atime := extractFileStatMeta(info)

	return FileEntry{
		Name:       info.Name(),
		Path:       childPath,
		Size:       info.Size(),
		Mode:       info.Mode().String(),
		ModeOctal:  formatModeOctal(info.Mode()),
		UID:        uid,
		GID:        gid,
		IsDir:      info.IsDir(),
		IsSymlink:  info.Mode()&os.ModeSymlink != 0,
		ModifiedAt: info.ModTime().UTC(),
		AccessTime: atime,
	}
}
