package httptool

// 流式下载：把响应体直接写进用户选择的本地文件。
//
// 和 http/send 的关键区别是"响应体大小未知"。用户在下载一个文件，可能是几 KB，
// 也可能是几个 GB，所以这里不能沿用 send 的"整份读进内存 + 截断"做法：
//   - Start 只等到响应头就返回，正文留在 sidecar 里；
//   - 前端按块 Read，边收边写宿主写句柄，任何时刻内存占用只有一个分块；
//   - 全程没有总时长超时（大文件必然超过任何固定值），只有"等响应头"和
//     "单次读空闲"两个超时，加上会话空闲回收兜底。
//
// 会话是跨调用的状态（响应体在 Start 之后才被读取），所以用 DownloadManager
// 集中持有，前端拿 downloadId 来寻址，这和 grpc 的 StreamManager 是同一种模式。

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"github.com/jlian/dbx-plugin-api-tester/internal/values"
)

const (
	// 单次 Read 返回的默认/最小/最大字节数。
	//
	// 下限避免前端把大文件拆成成千上万次 RPC；上限保证 base64 之后单条
	// JSON-RPC 消息不会大到拖垮 stdio 桥接。
	defaultDownloadChunkBytes = 256 * 1024
	minDownloadChunkBytes     = 16 * 1024
	maxDownloadChunkBytes     = 1024 * 1024

	// 单次 Read 允许的最长等待（收不到任何新数据即判定卡住）。
	defaultDownloadReadTimeoutMs = 30000
	minDownloadReadTimeoutMs     = 100
	maxDownloadReadTimeoutMs     = 120000

	// 会话空闲回收：前端关掉页面、崩溃或用户放着不管时，别让连接一直挂着。
	defaultDownloadIdleTTL = 2 * time.Minute

	// 保存对话框里文件名栏的可见长度上限，超长名字会被截断。
	maxDownloadFileNameLen = 180
)

// DownloadStartResult 是 http/download/start 的返回结构。
//
// ContentLength 为 -1 表示未知（chunked 传输或被压缩），前端据此决定进度条
// 是显示确定百分比还是只显示已收字节。
type DownloadStartResult struct {
	DownloadID    string            `json:"downloadId"`
	Status        int               `json:"status"`
	StatusText    string            `json:"statusText"`
	URL           string            `json:"url"`
	Headers       []values.KeyValue `json:"headers"`
	ContentType   string            `json:"contentType"`
	ContentLength int64             `json:"contentLength"`
	FileName      string            `json:"fileName"`
	Redirects     []Redirect        `json:"redirects"`
	ElapsedMs     int64             `json:"elapsedMs"`
}

// DownloadReadParams 是 http/download/read 的参数。
type DownloadReadParams struct {
	DownloadID string `json:"downloadId"`
	MaxBytes   int    `json:"maxBytes"`
	TimeoutMs  int    `json:"timeoutMs"`
}

// DownloadReadResult 是一块响应体。
type DownloadReadResult struct {
	DataBase64    string `json:"dataBase64"`
	Length        int    `json:"length"`
	Eof           bool   `json:"eof"`
	ReceivedBytes int64  `json:"receivedBytes"`
}

type downloadSession struct {
	id            string
	response      *http.Response
	cancel        context.CancelFunc
	transport     *http.Transport
	timer         *time.Timer
	fileName      string
	contentType   string
	contentLength int64
	received      int64
	busy          bool
	readSeq       uint64
	// abort 的原因：complete / canceled / timeout / expired。只在 manager 锁内读写。
	reason string
}

// DownloadManager 保存进行中的下载会话，并发安全。
type DownloadManager struct {
	mu       sync.Mutex
	sessions map[string]*downloadSession
	idleTTL  time.Duration
	sequence uint64
	// uploads 让下载也能发 multipart 请求：正文里的文件来自 sidecar 暂存区，
	// 会话本身不持有上传状态。为 nil 时只支持非 multipart 正文。
	uploads UploadResolver
}

// NewDownloadManager 构造一个使用默认空闲回收时间的会话表。
// uploads 用于 multipart 请求的暂存文件，可为 nil。
func NewDownloadManager(uploads UploadResolver) *DownloadManager {
	return newDownloadManager(uploads, defaultDownloadIdleTTL)
}

// newDownloadManager 允许测试注入极短的空闲时间来验证回收路径。
func newDownloadManager(uploads UploadResolver, idleTTL time.Duration) *DownloadManager {
	if idleTTL <= 0 {
		idleTTL = defaultDownloadIdleTTL
	}
	return &DownloadManager{sessions: make(map[string]*downloadSession), idleTTL: idleTTL, uploads: uploads}
}

// Start 发起请求并停在"响应头已到"。
//
// 只在拿到 2xx 响应头时登记会话；4xx/5xx 直接失败，避免用户把一个错误页
// 当成文件保存下来。
func (m *DownloadManager) Start(params SendParams, downloadID string) (*DownloadStartResult, *apperr.Error) {
	method := strings.ToUpper(strings.TrimSpace(params.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !validHTTPMethod(method) {
		return nil, apperr.Invalid("Unsupported HTTP method")
	}

	target, parseError := parseTargetURL(params.URL)
	if parseError != nil {
		return nil, apperr.Invalid(parseError.Error())
	}

	body, bodyFailure := requestBody(params, m.uploads)
	if bodyFailure != nil {
		return nil, bodyFailure
	}
	bodyReader, openError := body.open()
	if openError != nil {
		return nil, apperr.Internal("Cannot build the request body")
	}
	if body.length == 0 {
		// 与 http/send 一致：空正文显式换成 http.NoBody，避免退化成 chunked。
		_ = bodyReader.Close()
		bodyReader = http.NoBody
	}

	timeoutMs := values.ClampInt(params.TimeoutMs, defaultHTTPTimeoutMs, 1, maxHTTPTimeoutMs)
	followRedirects := true
	if params.FollowRedirects != nil {
		followRedirects = *params.FollowRedirects
	}
	maxRedirects := params.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	if maxRedirects > maxRedirectsLimit {
		maxRedirects = maxRedirectsLimit
	}

	// 注意：这里不能给 http.Client 设 Timeout。下载的总时长和文件大小成正比，
	// 任何固定值都会切断大文件；首字节等待用下面的 headerTimer 单独兜底。
	transport := newHTTPTransport(params.SkipTLSVerify)
	// 下载要的是"服务器上的那份字节"：关掉 Go 的自动 gzip 解压，否则一个
	// Content-Encoding: gzip 的 .tar.gz 会被悄悄解开，存下来的内容和文件名对不上。
	// 用户显式填了 Accept-Encoding 时 Go 本来就不会自动解压，这里只影响隐式请求头。
	transport.DisableCompression = true
	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: transport}
	redirects := make([]Redirect, 0, 2)
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if !followRedirects {
			return http.ErrUseLastResponse
		}
		if len(via) > maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		if request.Response != nil {
			redirects = append(redirects, Redirect{
				Status: request.Response.StatusCode,
				URL:    request.URL.String(),
			})
		}
		return nil
	}

	// 超时后必须能被识别出来：ctx 取消的 err 只是 context.Canceled，
	// 区分不了"用户取消"和"等响应头超时"，所以单独记一个标志。
	var headerTimedOut atomic.Bool
	var responseReceived atomic.Bool
	headerTimer := time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
		// 响应头已经拿到就不再切断请求：这个 ctx 同时管着正文读取，
		// 在这里取消会让后面的 Read 全部失败。
		if responseReceived.Load() {
			return
		}
		headerTimedOut.Store(true)
		cancel()
	})

	cleanup := func() {
		headerTimer.Stop()
		cancel()
		transport.CloseIdleConnections()
	}

	request, buildError := http.NewRequestWithContext(ctx, method, target.String(), bodyReader)
	if buildError != nil {
		_ = bodyReader.Close()
		cleanup()
		return nil, apperr.Invalid("Cannot build the request: " + buildError.Error())
	}
	// 自定义 reader 推不出长度，必须显式设置，否则上传会退化成 chunked。
	request.ContentLength = body.length
	if body.length > 0 {
		request.GetBody = body.open
	}

	userAgentSet := false
	for _, header := range params.Headers {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		if textproto.CanonicalMIMEHeaderKey(name) == "" {
			cleanup()
			return nil, apperr.Invalid(fmt.Sprintf("Invalid header name %q", name))
		}
		if !validHeaderValue(header.Value) {
			cleanup()
			return nil, apperr.Invalid(fmt.Sprintf("Header %q contains an invalid value", name))
		}
		if strings.EqualFold(name, "Host") {
			request.Host = header.Value
			continue
		}
		if strings.EqualFold(name, "User-Agent") {
			userAgentSet = true
		}
		request.Header.Add(name, header.Value)
	}
	if !userAgentSet {
		request.Header.Set("User-Agent", "DBX-API-Tester/"+Version())
	}
	if body.contentType != "" {
		request.Header.Del("Content-Type")
		request.Header.Set("Content-Type", body.contentType)
	}

	started := time.Now()
	response, requestError := client.Do(request)
	responseReceived.Store(true)
	// Stop 返回 false 说明计时器已经触发：即使响应头恰好同时到达，
	// 也按超时失败处理，避免把一个 context 已被取消的会话交给前端。
	headerFired := !headerTimer.Stop() || headerTimedOut.Load()
	if requestError != nil {
		cancel()
		transport.CloseIdleConnections()
		if headerFired {
			return nil, apperr.Upstream(apperr.KindTimeout, "Timed out waiting for the download to start")
		}
		return nil, httpTransportError(requestError)
	}
	if headerFired {
		response.Body.Close()
		cancel()
		transport.CloseIdleConnections()
		return nil, apperr.Upstream(apperr.KindTimeout, "Timed out waiting for the download to start")
	}

	statusLabel := statusText(response)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		cancel()
		transport.CloseIdleConnections()
		return nil, apperr.Upstream(apperr.KindStatus, "HTTP %d %s", response.StatusCode, statusLabel)
	}

	id := strings.TrimSpace(downloadID)
	if id == "" {
		id = fmt.Sprintf("download-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&m.sequence, 1))
	}

	contentType := response.Header.Get("Content-Type")
	session := &downloadSession{
		id:            id,
		response:      response,
		cancel:        cancel,
		transport:     transport,
		fileName:      downloadFileName(response.Header.Get("Content-Disposition"), response.Request.URL),
		contentType:   contentType,
		contentLength: response.ContentLength,
	}

	m.mu.Lock()
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		response.Body.Close()
		cancel()
		transport.CloseIdleConnections()
		return nil, apperr.Invalid("Download %q is already running", id)
	}
	m.sessions[id] = session
	// 空闲回收只作用于"再也没人来读"的会话；每次 Read 都会重置。
	session.timer = time.AfterFunc(m.idleTTL, func() { m.abort(session, "expired") })
	m.mu.Unlock()

	return &DownloadStartResult{
		DownloadID:    id,
		Status:        response.StatusCode,
		StatusText:    statusLabel,
		URL:           response.Request.URL.String(),
		Headers:       headerEntries(response.Header),
		ContentType:   contentType,
		ContentLength: response.ContentLength,
		FileName:      session.fileName,
		Redirects:     redirects,
		ElapsedMs:     time.Since(started).Milliseconds(),
	}, nil
}

// Read 读出一块响应体。
//
// 每次最多返回 MaxBytes 字节（base64）；读到的量通常更少，取决于网络当时给了多少。
// 读到 EOF 时会话自动关闭——前端把最后一块写盘后不需要再调 finish。
func (m *DownloadManager) Read(params DownloadReadParams) (*DownloadReadResult, *apperr.Error) {
	id := strings.TrimSpace(params.DownloadID)
	if id == "" {
		return nil, apperr.Invalid("downloadId is required")
	}
	maxBytes := values.ClampInt(params.MaxBytes, defaultDownloadChunkBytes, minDownloadChunkBytes, maxDownloadChunkBytes)
	readTimeoutMs := values.ClampInt(params.TimeoutMs, defaultDownloadReadTimeoutMs, minDownloadReadTimeoutMs, maxDownloadReadTimeoutMs)

	m.mu.Lock()
	session := m.sessions[id]
	if session == nil {
		m.mu.Unlock()
		return nil, apperr.Invalid("Download %q is not found", id)
	}
	if session.busy {
		m.mu.Unlock()
		return nil, apperr.Invalid("Download %q is already being read", id)
	}
	session.busy = true
	session.readSeq += 1
	readToken := session.readSeq
	if session.timer != nil {
		session.timer.Reset(m.idleTTL)
	}
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		session.busy = false
		m.mu.Unlock()
	}()

	// 单次读也要有上限：服务端卡死时前端应该拿到明确的超时错误并放弃，
	// 而不是把界面永久停在"下载中"。
	var stalled atomic.Bool
	stallTimer := time.AfterFunc(time.Duration(readTimeoutMs)*time.Millisecond, func() {
		m.mu.Lock()
		// 读可能刚返回、Stop 还没来得及执行：令牌对不上或已经不忙就别误杀会话。
		stillReading := session.busy && session.readSeq == readToken
		m.mu.Unlock()
		if !stillReading {
			return
		}
		stalled.Store(true)
		m.abort(session, "timeout")
	})
	defer stallTimer.Stop()

	buffer := make([]byte, maxBytes)
	readBytes, readError := session.response.Body.Read(buffer)

	m.mu.Lock()
	// 先让令牌失效并结束 busy，再处理结果：从这一刻起超时计时器不会再动这个会话。
	session.readSeq += 1
	session.busy = false
	session.received += int64(readBytes)
	received := session.received
	reason := session.reason
	m.mu.Unlock()
	// Body.Read 已经返回，计时器没有意义了。
	stallTimer.Stop()

	if readBytes > 0 {
		eof := readError == io.EOF
		if eof {
			m.complete(session)
		}
		return &DownloadReadResult{
			DataBase64:    base64.StdEncoding.EncodeToString(buffer[:readBytes]),
			Length:        readBytes,
			Eof:           eof,
			ReceivedBytes: received,
		}, nil
	}

	if readError == nil {
		// 极少数 Reader 会返回 (0, nil)；当成"还没数据"让前端继续读，
		// 真正的结束信号只会是 EOF。
		return &DownloadReadResult{Eof: false, ReceivedBytes: received}, nil
	}

	// 到这里一定是结束或失败：先把会话收掉，避免连接泄漏。
	switch {
	case readError == io.EOF:
		m.complete(session)
		return &DownloadReadResult{Eof: true, ReceivedBytes: received}, nil
	case reason == "canceled":
		return nil, apperr.Canceled()
	case reason == "timeout" || stalled.Load():
		return nil, apperr.Upstream(apperr.KindTimeout, "Download stalled: no data received for %d ms", readTimeoutMs)
	case reason == "expired":
		return nil, apperr.Upstream(apperr.KindTimeout, "Download session expired before it was read")
	default:
		m.complete(session)
		return nil, httpTransportError(readError)
	}
}

// Cancel 终止一个下载并关闭连接；未知 id 幂等返回 false。
func (m *DownloadManager) Cancel(downloadID string) bool {
	id := strings.TrimSpace(downloadID)
	if id == "" {
		return false
	}

	m.mu.Lock()
	session := m.sessions[id]
	if session != nil {
		delete(m.sessions, id)
		session.reason = "canceled"
	}
	m.mu.Unlock()

	if session == nil {
		return false
	}
	m.release(session)
	return true
}

// CloseAll 回收全部会话，进程退出或测试收尾时调用。
func (m *DownloadManager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*downloadSession, 0, len(m.sessions))
	for id, session := range m.sessions {
		delete(m.sessions, id)
		session.reason = "canceled"
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	for _, session := range sessions {
		m.release(session)
	}
}

// complete 正常结束会话（读完、或读取过程中遇到不可恢复的错误）。
func (m *DownloadManager) complete(session *downloadSession) {
	m.mu.Lock()
	if m.sessions[session.id] == session {
		delete(m.sessions, session.id)
	}
	if session.reason == "" {
		session.reason = "complete"
	}
	m.mu.Unlock()
	m.release(session)
}

// abort 由计时器调用（等不到数据 / 会话空闲过久），会打断阻塞中的 Read。
func (m *DownloadManager) abort(session *downloadSession, reason string) {
	m.mu.Lock()
	if m.sessions[session.id] != session {
		// 已经结束或已被取消，别覆盖真实原因。
		m.mu.Unlock()
		return
	}
	delete(m.sessions, session.id)
	session.reason = reason
	m.mu.Unlock()
	m.release(session)
}

// release 关闭底层连接。cancel() 会让阻塞中的 Body.Read 立刻返回错误。
func (m *DownloadManager) release(session *downloadSession) {
	if session.timer != nil {
		session.timer.Stop()
	}
	session.cancel()
	session.response.Body.Close()
	session.transport.CloseIdleConnections()
}

// newHTTPTransport 复制默认 transport 并套上插件的 TLS 策略。
//
// send 与 download 共用同一份实现，避免"跳过证书校验"在一个路径生效、
// 另一个路径不生效这种割裂。
func newHTTPTransport(skipTLSVerify bool) *http.Transport {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	cloned := transport.Clone()
	cloned.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: skipTLSVerify,
	}
	return cloned
}

// downloadFileName 决定"另存为"对话框里的推荐文件名。
//
// 优先级：Content-Disposition（唯一权威来源，支持 filename* 的 UTF-8 编码）→
// URL 路径末段 → "download"。任何来源都要过一遍清洗，因为文件名来自服务端，
// 不能让它携带路径分隔符或控制字符。
func downloadFileName(contentDisposition string, target *url.URL) string {
	name := ""
	if strings.TrimSpace(contentDisposition) != "" {
		// mime.ParseMediaType 会解码 RFC 5987 的 filename*，这是首选路径。
		if _, params, parseError := mime.ParseMediaType(contentDisposition); parseError == nil {
			name = strings.TrimSpace(params["filename"])
		}
		if name == "" {
			// 不规范的头会让 ParseMediaType 整体失败，这里做一次宽松兜底：
			// 服务端写错引号不该让用户拿到 download 这种无意义的名字。
			name = rawContentDispositionName(contentDisposition)
		}
	}

	if name == "" && target != nil {
		base := path.Base(target.Path)
		if base != "" && base != "." && base != "/" {
			if decoded, decodeError := url.PathUnescape(base); decodeError == nil {
				name = decoded
			} else {
				name = base
			}
		}
	}

	name = sanitizeFileName(name)
	if name == "" {
		return "download"
	}
	return name
}

// rawContentDispositionName 从格式不规范的 Content-Disposition 里尽量抠出文件名。
func rawContentDispositionName(value string) string {
	lower := strings.ToLower(value)
	index := strings.Index(lower, "filename=")
	if index < 0 {
		return ""
	}
	raw := strings.TrimSpace(value[index+len("filename="):])
	// filename 必须是最后一个参数；遇到分号就截断。
	if end := strings.Index(raw, ";"); end >= 0 {
		raw = raw[:end]
	}
	return strings.Trim(strings.TrimSpace(raw), `"`)
}

// sanitizeFileName 清洗来自服务端的文件名。
//
// 目标是"任何平台都能直接保存"：去掉路径分隔符（防止写入插件数据目录之外）、
// 控制字符和 Windows 保留字符，并限制长度。
func sanitizeFileName(name string) string {
	// 先按路径语义取末段：服务端可能回一个带目录的 filename（../../etc/passwd），
	// 只做字符替换会留下 ".._.._etc_passwd" 这种怪名字。
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	if name == "." || name == "/" {
		return ""
	}
	name = strings.Map(func(character rune) rune {
		switch {
		case character == 0 || character < 0x20 || character == 0x7f:
			return -1
		case character == '/' || character == '\\':
			return '_'
		case strings.ContainsRune(`<>:"|?*`, character):
			return '_'
		default:
			return character
		}
	}, name)
	name = strings.Trim(strings.TrimSpace(name), ".")
	runes := []rune(name)
	if len(runes) > maxDownloadFileNameLen {
		runes = runes[:maxDownloadFileNameLen]
	}
	return string(runes)
}
