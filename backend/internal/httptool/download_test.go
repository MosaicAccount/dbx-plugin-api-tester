package httptool

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"github.com/jlian/dbx-plugin-api-tester/internal/values"
)

// readWholeDownload 走一遍生产链路：Start → 循环 Read → EOF。
//
// 返回拼起来的字节和 Start 的结果；顺带校验"每次 Read 不超过请求的 maxBytes"，
// 因为这是"内存占用只有一个分块"的前提。
func readWholeDownload(t *testing.T, manager *DownloadManager, targetURL string, chunkBytes int) ([]byte, *DownloadStartResult) {
	t.Helper()

	start, startFailure := manager.Start(SendParams{Method: "GET", URL: targetURL}, "")
	if startFailure != nil {
		t.Fatalf("start failed: %v", startFailure)
	}
	return drainDownload(t, manager, start.DownloadID, chunkBytes), start
}

// drainDownload 把已登记的会话读到 EOF，逐块校验长度声明与 base64 编码。
func drainDownload(t *testing.T, manager *DownloadManager, downloadID string, chunkBytes int) []byte {
	t.Helper()

	var body []byte
	reads := 0
	for reads = 0; reads < 100000; reads++ {
		result, readFailure := manager.Read(DownloadReadParams{
			DownloadID: downloadID,
			MaxBytes:   chunkBytes,
		})
		if readFailure != nil {
			t.Fatalf("read %d failed: %v", reads, readFailure)
		}
		if result.Length > chunkBytes {
			t.Fatalf("read returned %d bytes, more than the requested %d", result.Length, chunkBytes)
		}
		decoded, decodeError := base64.StdEncoding.DecodeString(result.DataBase64)
		if decodeError != nil {
			t.Fatalf("chunk %d is not valid base64: %v", reads, decodeError)
		}
		if len(decoded) != result.Length {
			t.Fatalf("chunk %d declared %d bytes but decoded %d", reads, result.Length, len(decoded))
		}
		if len(decoded) > 0 {
			body = append(body, decoded...)
		}
		if result.Eof {
			if result.ReceivedBytes != int64(len(body)) {
				t.Fatalf("receivedBytes = %d, actual %d", result.ReceivedBytes, len(body))
			}
			return body
		}
	}
	t.Fatalf("download did not finish after %d reads", reads)
	return nil
}

// 关键回归：响应体远大于 http/send 的 4 MB 上限时，下载必须逐块送完且字节完全一致。
func TestDownloadStreamsLargeBodyWithoutTruncation(t *testing.T) {
	payload := make([]byte, 12*1024*1024+12345)
	for index := range payload {
		payload[index] = byte(index*31 + index/7)
	}
	expectedSum := sha256.Sum256(payload)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.Header().Set("Content-Disposition", `attachment; filename="report-2024.bin"`)
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		// 分几段写，制造"一次 Read 拿不完一个分块"之外的真实网络节奏。
		for offset := 0; offset < len(payload); offset += 1024 * 1024 {
			end := offset + 1024*1024
			if end > len(payload) {
				end = len(payload)
			}
			if _, err := writer.Write(payload[offset:end]); err != nil {
				return
			}
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	manager := NewDownloadManager(nil)
	defer manager.CloseAll()

	body, start := readWholeDownload(t, manager, server.URL, 256*1024)

	if start.ContentLength != int64(len(payload)) {
		t.Fatalf("contentLength = %d, want %d", start.ContentLength, len(payload))
	}
	if start.FileName != "report-2024.bin" {
		t.Fatalf("fileName = %q", start.FileName)
	}
	if start.Status != http.StatusOK {
		t.Fatalf("status = %d", start.Status)
	}
	if len(body) != len(payload) {
		t.Fatalf("downloaded %d bytes, want %d (truncated?)", len(body), len(payload))
	}
	if sum := sha256.Sum256(body); sum != expectedSum {
		t.Fatal("downloaded bytes differ from the served payload")
	}

	// EOF 之后会话必须已经关闭，不能继续读。
	if _, failure := manager.Read(DownloadReadParams{DownloadID: start.DownloadID}); failure == nil {
		t.Fatal("expected the session to be gone after EOF")
	}
}

// chunked 响应没有 Content-Length：前端必须能靠 -1 判断"总大小未知"。
func TestDownloadReportsUnknownLengthForChunkedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		// 先 flush 一次，逼 net/http 用 chunked 传输（否则小响应会被缓冲成定长）。
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		for index := 0; index < 8; index++ {
			_, _ = writer.Write([]byte("0123456789"))
		}
	}))
	defer server.Close()

	manager := NewDownloadManager(nil)
	defer manager.CloseAll()

	body, start := readWholeDownload(t, manager, server.URL, 64*1024)
	if start.ContentLength != -1 {
		t.Fatalf("contentLength = %d, want -1 for a chunked response", start.ContentLength)
	}
	if len(body) != 80 {
		t.Fatalf("downloaded %d bytes, want 80", len(body))
	}
}

// 非 2xx 必须在 Start 阶段就失败，不能把错误页当文件存下来。
func TestDownloadRejectsNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte("<html>not found</html>"))
	}))
	defer server.Close()

	manager := NewDownloadManager(nil)
	defer manager.CloseAll()

	_, failure := manager.Start(SendParams{Method: "GET", URL: server.URL}, "")
	if failure == nil {
		t.Fatal("expected a failure for a 404 download")
	}
	if failure.Kind != apperr.KindStatus {
		t.Fatalf("kind = %q, want %q", failure.Kind, apperr.KindStatus)
	}
	if failure.Code != apperr.CodeUpstream {
		t.Fatalf("code = %d, want %d", failure.Code, apperr.CodeUpstream)
	}
	if !bytes.Contains([]byte(failure.Message), []byte("404")) {
		t.Fatalf("message %q should mention the status code", failure.Message)
	}
}

// 会话取消要立刻打断阻塞中的 Read，否则用户点了取消界面还会卡住。
func TestDownloadCancelInterruptsBlockedRead(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		// 响应头发完就挂着：模拟"服务端迟迟不给正文"。
		<-release
	}))
	defer server.Close()
	defer close(release)

	manager := NewDownloadManager(nil)
	defer manager.CloseAll()

	start, failure := manager.Start(SendParams{Method: "GET", URL: server.URL}, "dl-cancel")
	if failure != nil {
		t.Fatalf("start failed: %v", failure)
	}
	<-started

	outcome := make(chan *apperr.Error, 1)
	go func() {
		_, readFailure := manager.Read(DownloadReadParams{DownloadID: start.DownloadID, TimeoutMs: 5000})
		outcome <- readFailure
	}()

	// 等 Read 真正阻塞后再取消，模拟用户看到"下载中"才点取消。
	time.Sleep(50 * time.Millisecond)
	if !manager.Cancel(start.DownloadID) {
		t.Fatal("cancel should report that the session was running")
	}

	select {
	case readFailure := <-outcome:
		if readFailure == nil || readFailure.Kind != apperr.KindCanceled {
			t.Fatalf("expected a canceled read, got %#v", readFailure)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not interrupt the blocked read")
	}

	// 未知/已取消的 id 幂等返回 false。
	if manager.Cancel(start.DownloadID) {
		t.Fatal("cancel of a finished session should be false")
	}
	if manager.Cancel("missing") {
		t.Fatal("cancel of an unknown session should be false")
	}
}

// 服务端卡住时，单次 Read 必须在读超时后报错并放弃会话，不能永久挂住。
func TestDownloadReadTimesOutWhenServerStalls(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-release
	}))
	defer server.Close()
	defer close(release)

	manager := NewDownloadManager(nil)
	defer manager.CloseAll()

	start, failure := manager.Start(SendParams{Method: "GET", URL: server.URL}, "")
	if failure != nil {
		t.Fatalf("start failed: %v", failure)
	}

	_, readFailure := manager.Read(DownloadReadParams{
		DownloadID: start.DownloadID,
		TimeoutMs:  150,
	})
	if readFailure == nil || readFailure.Kind != apperr.KindTimeout {
		t.Fatalf("expected a timeout, got %#v", readFailure)
	}

	// 会话在超时后作废，前端重复读会得到"会话不存在"。
	if _, again := manager.Read(DownloadReadParams{DownloadID: start.DownloadID, TimeoutMs: 150}); again == nil {
		t.Fatal("expected the session to be gone after a read timeout")
	}
}

// 前端关掉页面后不再来读：会话必须被空闲回收，连接不能一直挂着。
func TestDownloadSessionExpiresWhenIdle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(300 * time.Millisecond)
		_, _ = writer.Write([]byte("late"))
	}))
	defer server.Close()

	manager := newDownloadManager(nil, 60*time.Millisecond)
	defer manager.CloseAll()

	start, failure := manager.Start(SendParams{Method: "GET", URL: server.URL}, "")
	if failure != nil {
		t.Fatalf("start failed: %v", failure)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		manager.mu.Lock()
		_, alive := manager.sessions[start.DownloadID]
		manager.mu.Unlock()
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an idle download session was never reclaimed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 参数边界：缺 downloadId / 并发读同一会话都要有明确错误。
func TestDownloadReadValidatesSession(t *testing.T) {
	manager := NewDownloadManager(nil)
	defer manager.CloseAll()

	if _, failure := manager.Read(DownloadReadParams{}); failure == nil || failure.Code != apperr.CodeInvalidParams {
		t.Fatalf("expected invalid-params for a missing downloadId, got %#v", failure)
	}
	if _, failure := manager.Read(DownloadReadParams{DownloadID: "missing"}); failure == nil || failure.Code != apperr.CodeInvalidParams {
		t.Fatalf("expected invalid-params for an unknown session, got %#v", failure)
	}
	if manager.Cancel("") {
		t.Fatal("cancel without an id should be false")
	}
}

// 文件名来自服务端，必须清洗到"任何平台都能直接保存"。
func TestDownloadFileNameSanitization(t *testing.T) {
	target, parseError := url.Parse("https://example.com/files/%E6%8A%A5%E5%91%8A.pdf?token=1")
	if parseError != nil {
		t.Fatalf("cannot parse URL: %v", parseError)
	}

	cases := []struct {
		name   string
		header string
		target *url.URL
		want   string
	}{
		{name: "quoted", header: `attachment; filename="a.zip"`, target: target, want: "a.zip"},
		{name: "rfc5987", header: "attachment; filename*=UTF-8''%E6%8A%A5%E5%91%8A.zip", target: target, want: "报告.zip"},
		{name: "path traversal", header: `attachment; filename="../../etc/passwd"`, target: target, want: "passwd"},
		{name: "windows separators", header: `attachment; filename="..\\..\\boot.ini"`, target: target, want: "boot.ini"},
		{name: "control chars", header: "attachment; filename=\"a\x01b.txt\"", target: target, want: "ab.txt"},
		{name: "malformed header", header: `attachment; filename = report.txt`, target: target, want: "report.txt"},
		{name: "url fallback", header: "", target: target, want: "报告.pdf"},
		{name: "no name at all", header: "", target: &url.URL{Path: "/"}, want: "download"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := downloadFileName(testCase.header, testCase.target); got != testCase.want {
				t.Fatalf("downloadFileName(%q) = %q, want %q", testCase.header, got, testCase.want)
			}
		})
	}
}

// 重复使用同一个 downloadId 必须被拒绝，避免两个下载互相踩响应体。
func TestDownloadRejectsDuplicateSessionID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", "4")
		_, _ = writer.Write([]byte("test"))
	}))
	defer server.Close()

	manager := NewDownloadManager(nil)
	defer manager.CloseAll()

	if _, failure := manager.Start(SendParams{Method: "GET", URL: server.URL}, "same-id"); failure != nil {
		t.Fatalf("first start failed: %v", failure)
	}
	if _, failure := manager.Start(SendParams{Method: "GET", URL: server.URL}, "same-id"); failure == nil || failure.Code != apperr.CodeInvalidParams {
		t.Fatalf("expected invalid-params for a duplicate downloadId, got %#v", failure)
	}
}

// 下载路径也要支持请求头和请求体：有些文件接口是 POST + 鉴权头。
func TestDownloadSendsHeadersAndBody(t *testing.T) {
	var seenHeader atomic.Value
	var seenBody atomic.Value
	var requestMethod atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		seenHeader.Store(request.Header.Get("X-Token"))
		seenBody.Store(string(body))
		requestMethod.Store(request.Method)
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.Header().Set("Content-Length", "2")
		_, _ = writer.Write([]byte("ok"))
	}))
	defer server.Close()

	manager := NewDownloadManager(nil)
	defer manager.CloseAll()

	start, failure := manager.Start(SendParams{
		Method:       "POST",
		URL:          server.URL,
		Headers:      []values.KeyValue{{Name: "X-Token", Value: "secret"}},
		BodyText:     `{"id":7}`,
		BodyEncoding: "text",
	}, "")
	if failure != nil {
		t.Fatalf("start failed: %v", failure)
	}

	body := make([]byte, 0, 2)
	for {
		result, readFailure := manager.Read(DownloadReadParams{DownloadID: start.DownloadID, MaxBytes: 64 * 1024})
		if readFailure != nil {
			t.Fatalf("read failed: %v", readFailure)
		}
		decoded, _ := base64.StdEncoding.DecodeString(result.DataBase64)
		body = append(body, decoded...)
		if result.Eof {
			break
		}
	}
	if string(body) != "ok" {
		t.Fatalf("downloaded body = %q, want %q", body, "ok")
	}
	if requestMethod.Load() != "POST" {
		t.Fatalf("request method was not forwarded: %v", requestMethod.Load())
	}
	if seenHeader.Load() != "secret" {
		t.Fatalf("request header was not forwarded: %v", seenHeader.Load())
	}
	if seenBody.Load() != `{"id":7}` {
		t.Fatalf("request body was not forwarded: %v", seenBody.Load())
	}
}

// 首字节超时必须被识别成 timeout（而不是笼统的网络错误），前端才能给出准确提示。
func TestDownloadStartTimesOutBeforeHeaders(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		<-release
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(release)

	manager := NewDownloadManager(nil)
	defer manager.CloseAll()

	started := time.Now()
	_, failure := manager.Start(SendParams{Method: "GET", URL: server.URL, TimeoutMs: 150}, "")
	if failure == nil || failure.Kind != apperr.KindTimeout {
		t.Fatalf("expected a timeout, got %#v", failure)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("the timeout took too long: %v", elapsed)
	}
}

// 文件下载必须拿到"服务器上的那份字节"：默认不能带 Accept-Encoding: gzip，
// 否则 Go 会自动解压，存成 .tar.gz 的文件内容和扩展名就对不上了。
func TestDownloadDoesNotRequestCompression(t *testing.T) {
	var acceptEncoding atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		acceptEncoding.Store(request.Header.Get("Accept-Encoding"))
		writer.Header().Set("Content-Length", "4")
		_, _ = writer.Write([]byte("raw!"))
	}))
	defer server.Close()

	manager := NewDownloadManager(nil)
	defer manager.CloseAll()

	body, _ := readWholeDownload(t, manager, server.URL, 64*1024)
	if string(body) != "raw!" {
		t.Fatalf("downloaded body = %q, want %q", body, "raw!")
	}
	encoding, _ := acceptEncoding.Load().(string)
	if strings.Contains(strings.ToLower(encoding), "gzip") {
		t.Fatalf("download should not ask for compression, got Accept-Encoding: %q", encoding)
	}
}

// testUploadResolver 是最小的暂存区替身：下载只通过 Stat/Open 读暂存文件，
// 不需要真实的临时文件与回收逻辑。
type testUploadResolver struct {
	files map[string][]byte
}

func (r *testUploadResolver) Stat(id string) (int64, *apperr.Error) {
	data, ok := r.files[id]
	if !ok {
		return 0, apperr.Invalid("Upload has expired; choose the file again")
	}
	return int64(len(data)), nil
}

func (r *testUploadResolver) Open(id string) (io.ReadCloser, *apperr.Error) {
	if _, ok := r.files[id]; !ok {
		return nil, apperr.Invalid("Upload has expired; choose the file again")
	}
	return io.NopCloser(bytes.NewReader(r.files[id])), nil
}

// 下载和发送共用一套参数，multipart 正文里的文件同样来自 sidecar 暂存区：
// 这条链路要能正常拼出 multipart，且长度已知、不退化成分块传输。
func TestDownloadSendsMultipartFromStagedUpload(t *testing.T) {
	var partName, partFile, partBody atomic.Value
	var contentLength atomic.Int64
	var chunked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		contentLength.Store(request.ContentLength)
		chunked.Store(len(request.TransferEncoding) > 0)
		reader, err := request.MultipartReader()
		if err != nil {
			t.Errorf("cannot read multipart body: %v", err)
			return
		}
		part, partError := reader.NextPart()
		if partError != nil {
			t.Errorf("cannot read multipart part: %v", partError)
			return
		}
		data, _ := io.ReadAll(part)
		partName.Store(part.FormName())
		partFile.Store(part.FileName())
		partBody.Store(string(data))
		writer.Header().Set("Content-Type", "application/octet-stream")
		_, _ = writer.Write([]byte("ok"))
	}))
	defer server.Close()

	resolver := &testUploadResolver{files: map[string][]byte{"u1": []byte("hello")}}
	manager := NewDownloadManager(resolver)
	defer manager.CloseAll()

	start, failure := manager.Start(SendParams{
		Method:       "POST",
		URL:          server.URL,
		BodyEncoding: "multipart",
		Multipart: &MultipartBody{
			Files: []MultipartFile{{Name: "files", FileName: "a.txt", UploadID: "u1"}},
		},
	}, "")
	if failure != nil {
		t.Fatalf("start failed: %v", failure)
	}

	body := drainDownload(t, manager, start.DownloadID, 64*1024)
	if string(body) != "ok" {
		t.Fatalf("downloaded body = %q, want %q", body, "ok")
	}
	if partName.Load() != "files" || partFile.Load() != "a.txt" || partBody.Load() != "hello" {
		t.Fatalf("unexpected multipart part: name=%v file=%v body=%v", partName.Load(), partFile.Load(), partBody.Load())
	}
	if contentLength.Load() <= 0 {
		t.Fatalf("expected a known Content-Length, got %d", contentLength.Load())
	}
	if chunked.Load() {
		t.Fatal("multipart download should not use chunked transfer encoding")
	}
	// 发送只读暂存文件，下载完也必须还能重发。
	if _, statError := resolver.Stat("u1"); statError != nil {
		t.Fatalf("staged upload should stay available after download: %v", statError)
	}
}
