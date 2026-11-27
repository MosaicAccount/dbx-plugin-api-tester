// Package uploads 暂存 multipart 文件分块。
//
// 沙箱里的前端拿不到文件路径，也放不下几十 MB 的 base64 正文；文件由宿主分块读出，
// 经 JSON-RPC 逐块写进本包的临时文件，发送时再由 httptool 流式拼成 multipart 正文。
// 这样单个 JSON-RPC 消息始终只有几百 KB，请求体大小不再受桥接上限约束。
//
// 生命周期：Begin 建临时文件 → Append 追加若干块 → http/send 反复打开读取。
// 发送不删除暂存文件，同一个请求可以连续重发；用户移除文件、切换请求时用
// Abort 删除，闲置超过 IdleTTL 的残留由后续操作顺带回收，进程退出时由 Close 清理。
package uploads

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
)

const (
	// MaxFileBytes 是单个上传文件的上限。这里只是磁盘保护，不是协议上限；
	// 文件是流式写临时文件并流式发送的，改大不会增加内存占用。
	MaxFileBytes int64 = 1 << 30
	// MaxTotalBytes 是同一次请求里所有文件合计的上限。
	MaxTotalBytes int64 = 5 << 30
	// IdleTTL 是暂存文件在多长时间没有读写后被回收。
	IdleTTL = 10 * time.Minute

	// maxChunkBytes 限制单个分块，避免一次 RPC 把内存打满。前端按 512 KiB 分块。
	maxChunkBytes = 8 << 20
	// staleFileAge 是启动时清理上个进程遗留临时文件的门槛。
	staleFileAge = 24 * time.Hour

	tempPrefix = "dbx-apitest-upload-"
)

type stagedFile struct {
	path    string
	name    string
	size    int64
	touched time.Time
}

// Store 是进程内的暂存文件表。并发安全；文件内容只在私有临时文件里。
type Store struct {
	mu    sync.Mutex
	files map[string]*stagedFile
	total int64
	now   func() time.Time
}

// NewStore 构造暂存表，并清掉以前进程崩溃留下的旧文件。
func NewStore() *Store {
	removeStaleFiles(time.Now())
	return &Store{files: make(map[string]*stagedFile), now: time.Now}
}

// Begin 为一个待上传文件建暂存记录，返回后续分块使用的 uploadId。
//
// size 是宿主报告的文件大小，只用于提前拒绝明显超限的文件；真正的配额在 Append
// 里按实际字节数累计，避免声明大小与实际不一致时绕过限制。
func (s *Store) Begin(fileName string, size int64) (string, *apperr.Error) {
	if size < 0 {
		return "", apperr.Invalid("File size is not valid")
	}
	if size > MaxFileBytes {
		return "", apperr.Invalid("File %q is larger than %d bytes", displayName(fileName), MaxFileBytes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()

	id, idError := newUploadID()
	if idError != nil {
		return "", apperr.Internal("Cannot create an upload")
	}
	file, createError := os.CreateTemp("", tempPrefix+"*")
	if createError != nil {
		return "", apperr.Internal("Cannot create an upload")
	}
	path := file.Name()
	if closeError := file.Close(); closeError != nil {
		_ = os.Remove(path)
		return "", apperr.Internal("Cannot create an upload")
	}
	s.files[id] = &stagedFile{path: path, name: displayName(fileName), touched: s.now()}
	return id, nil
}

// Append 解码一块 base64 并追加到暂存文件，返回文件当前字节数。
func (s *Store) Append(id, dataBase64 string) (int64, *apperr.Error) {
	data, decodeError := base64.StdEncoding.DecodeString(dataBase64)
	if decodeError != nil {
		return 0, apperr.Invalid("Upload chunk is not valid base64")
	}
	if len(data) == 0 {
		return 0, apperr.Invalid("Upload chunk is empty")
	}
	if len(data) > maxChunkBytes {
		return 0, apperr.Invalid("Upload chunk is larger than %d bytes", maxChunkBytes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()

	entry := s.files[strings.TrimSpace(id)]
	if entry == nil {
		return 0, apperr.Invalid("Upload has expired; choose the file again")
	}
	if entry.size+int64(len(data)) > MaxFileBytes {
		return 0, apperr.Invalid("File %q is larger than %d bytes", entry.name, MaxFileBytes)
	}
	if s.total+int64(len(data)) > MaxTotalBytes {
		return 0, apperr.Invalid("Attached files must be %d bytes or smaller in total", MaxTotalBytes)
	}

	file, openError := os.OpenFile(entry.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if openError != nil {
		return 0, apperr.Internal("Cannot write the upload")
	}
	written, writeError := file.Write(data)
	closeError := file.Close()
	if writeError != nil || closeError != nil || written != len(data) {
		return 0, apperr.Internal("Cannot write the upload")
	}

	entry.size += int64(written)
	entry.touched = s.now()
	s.total += int64(written)
	return entry.size, nil
}

// Stat 返回暂存文件当前大小；发送前用它定 Content-Length。
func (s *Store) Stat(id string) (int64, *apperr.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	entry := s.files[strings.TrimSpace(id)]
	if entry == nil {
		return 0, apperr.Invalid("Upload has expired; choose the file again")
	}
	entry.touched = s.now()
	return entry.size, nil
}

// Open 打开暂存文件供发送读取。调用方负责关闭；文件本体会在 Abort/Close
// 或空闲回收时删除，所以同一个 uploadId 可以重复发送。
func (s *Store) Open(id string) (io.ReadCloser, *apperr.Error) {
	s.mu.Lock()
	s.sweepLocked()
	entry := s.files[strings.TrimSpace(id)]
	if entry == nil {
		s.mu.Unlock()
		return nil, apperr.Invalid("Upload has expired; choose the file again")
	}
	path := entry.path
	entry.touched = s.now()
	s.mu.Unlock()

	file, openError := os.Open(path)
	if openError != nil {
		return nil, apperr.Internal("Cannot read the upload")
	}
	return file, nil
}

// Abort 删除一个暂存文件；不存在时返回 false（重复删除不算错误）。
func (s *Store) Abort(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeLocked(strings.TrimSpace(id))
}

// Release 批量删除暂存文件；正常发送不再调用它，留给显式清理与测试使用。
func (s *Store) Release(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.removeLocked(strings.TrimSpace(id))
	}
}

// Close 清掉所有暂存文件，进程退出前调用。
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.files {
		s.removeLocked(id)
	}
}

func (s *Store) removeLocked(id string) bool {
	entry := s.files[id]
	if entry == nil {
		return false
	}
	delete(s.files, id)
	s.total -= entry.size
	if s.total < 0 {
		s.total = 0
	}
	_ = os.Remove(entry.path)
	return true
}

// sweepLocked 回收超时未使用的暂存文件，避免用户中途切走请求后长期占用磁盘。
func (s *Store) sweepLocked() {
	now := s.now()
	for id, entry := range s.files {
		if now.Sub(entry.touched) > IdleTTL {
			s.removeLocked(id)
		}
	}
}

func newUploadID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// removeStaleFiles 清掉上一次进程退出时没来得及删除的暂存文件。
//
// 只动本包前缀、且超过 staleFileAge 的文件，正在运行的其它实例不会受影响。
func removeStaleFiles(now time.Time) {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), tempPrefix+"*"))
	if err != nil {
		return
	}
	for _, path := range matches {
		info, infoError := os.Stat(path)
		if infoError != nil || info.IsDir() {
			continue
		}
		if now.Sub(info.ModTime()) > staleFileAge {
			_ = os.Remove(path)
		}
	}
}

func displayName(fileName string) string {
	name := strings.TrimSpace(fileName)
	if name == "" {
		return "file"
	}
	return name
}
