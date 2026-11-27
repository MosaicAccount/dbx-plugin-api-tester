package httptool

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"github.com/jlian/dbx-plugin-api-tester/internal/values"
)

const (
	defaultHTTPTimeoutMs = 30000
	maxHTTPTimeoutMs     = 120000
	defaultMaxBodyBytes  = 3 * 1024 * 1024
	maxBodyBytesLimit    = 4 * 1024 * 1024
	maxRequestBodyBytes  = 4 * 1024 * 1024
	defaultMaxRedirects  = 10
	maxRedirectsLimit    = 20
)

type SendParams struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Headers         []values.KeyValue `json:"headers"`
	BodyText        string            `json:"bodyText"`
	BodyBase64      string            `json:"bodyBase64"`
	BodyEncoding    string            `json:"bodyEncoding"`
	Multipart       *MultipartBody    `json:"multipart"`
	TimeoutMs       int               `json:"timeoutMs"`
	FollowRedirects *bool             `json:"followRedirects"`
	MaxRedirects    int               `json:"maxRedirects"`
	SkipTLSVerify   bool              `json:"skipTlsVerify"`
	MaxBodyBytes    int               `json:"maxBodyBytes"`
}

// MultipartBody 是 multipart/form-data 请求体：文本字段 + 文件部件。
//
// 文件内容以 base64 传入（沙箱拿不到本地路径），由 sidecar 在发送时按
// mime/multipart 组装，boundary 与 Content-Type 也在这里生成。
type MultipartBody struct {
	Fields []values.KeyValue `json:"fields"`
	Files  []MultipartFile   `json:"files"`
}

// MultipartFile 是一个文件部件；FileName 是发给服务端的原始文件名。
//
// 文件内容有两种来源：UploadID 指向 sidecar 里已经分块暂存的文件（正常路径，
// 支持大文件）；Base64 是内联内容（保留给旧调用与单元测试，受单请求大小上限约束）。
type MultipartFile struct {
	Name        string `json:"name"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	Base64      string `json:"base64"`
	UploadID    string `json:"uploadId"`
}

// UploadResolver 由 rpc 层注入，用来读取分块暂存的文件。
// 接口放在这里而不是直接依赖 uploads 包，避免 httptool 反向绑定存储实现。
//
// 发送只读取暂存文件，不负责删除：同一个请求经常需要重复发送，文件应一直可用，
// 直到前端显式 abort（移除文件/切换请求）或暂存区按空闲时间回收。
type UploadResolver interface {
	Stat(uploadID string) (int64, *apperr.Error)
	Open(uploadID string) (io.ReadCloser, *apperr.Error)
}

// bodyPlan 是待发送的请求正文：长度已知、可以按需重建（重定向时 GetBody 会再调
// 一次 open），multipart 的临时文件只在新一次 open 时才打开。
type bodyPlan struct {
	contentType string
	length      int64
	open        func() (io.ReadCloser, error)
}

type multiReadCloser struct {
	io.Reader
	closers []io.Closer
}

func (m *multiReadCloser) Close() error {
	var first error
	for _, closer := range m.closers {
		if err := closer.Close(); err != nil && first == nil {
			first = err
		}
	}
	m.closers = nil
	return first
}

type Redirect struct {
	Status int    `json:"status"`
	URL    string `json:"url"`
}

type SendResult struct {
	Status      int               `json:"status"`
	StatusText  string            `json:"statusText"`
	HTTPVersion string            `json:"httpVersion"`
	URL         string            `json:"url"`
	Headers     []values.KeyValue `json:"headers"`
	BodyKind    string            `json:"bodyKind"`
	BodyText    string            `json:"bodyText,omitempty"`
	BodyBase64  string            `json:"bodyBase64,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	SizeBytes   int64             `json:"sizeBytes"`
	Truncated   bool              `json:"truncated"`
	ElapsedMs   int64             `json:"elapsedMs"`
	Redirects   []Redirect        `json:"redirects"`
	ReceivedAt  string            `json:"receivedAt"`
}

// BuildVersion 由 main 注入，避免此包反向依赖插件身份常量。
var buildVersion = "0.0.0"

// SetVersion 由 main 在启动时调用一次。
func SetVersion(version string) { buildVersion = version }

func Version() string { return buildVersion }

// Send 发起一次 HTTP 请求。
//
// ctx 由调用方提供（rpc 层为每个 requestId 建立可取消上下文）；超时在这里再
// 叠加一层，两者任一触发都会中断请求。
func Send(ctx context.Context, params SendParams) (*SendResult, *apperr.Error) {
	return SendWithUploads(ctx, params, nil)
}

// SendWithUploads 与 Send 相同，但可以用 uploads 解析分块暂存的文件。
func SendWithUploads(ctx context.Context, params SendParams, uploads UploadResolver) (*SendResult, *apperr.Error) {
	method := strings.ToUpper(strings.TrimSpace(params.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !validHTTPMethod(method) {
		return nil, apperr.Invalid("Unsupported HTTP method")
	}

	target, err := parseTargetURL(params.URL)
	if err != nil {
		return nil, apperr.Invalid(err.Error())
	}

	body, pluginError := requestBody(params, uploads)
	if pluginError != nil {
		return nil, pluginError
	}

	timeoutMs := values.ClampInt(params.TimeoutMs, defaultHTTPTimeoutMs, 1, maxHTTPTimeoutMs)
	maxBody := values.ClampInt(params.MaxBodyBytes, defaultMaxBodyBytes, 1024, maxBodyBytesLimit)
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

	clonedTransport := newHTTPTransport(params.SkipTLSVerify)
	defer clonedTransport.CloseIdleConnections()

	client := &http.Client{
		Transport: clonedTransport,
		Timeout:   time.Duration(timeoutMs) * time.Millisecond,
	}
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

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	bodyReader, err := body.open()
	if err != nil {
		return nil, apperr.Internal("Cannot build the request body")
	}
	if body.length == 0 {
		// 自定义 reader 不会被 NewRequest 识别成 NoBody；长度 0 时显式换成
		// http.NoBody，避免空正文退化成 chunked 传输。
		_ = bodyReader.Close()
		bodyReader = http.NoBody
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bodyReader)
	if err != nil {
		_ = bodyReader.Close()
		return nil, apperr.Invalid("Cannot build the request: " + err.Error())
	}
	if body.length > 0 {
		defer func() { _ = bodyReader.Close() }()
	}
	// body.open 是自定义 reader，http.NewRequest 推不出长度，必须显式设置，
	// 否则会退化成 chunked 传输（部分服务端/网关不接受上传使用 chunked）。
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
			return nil, apperr.Invalid(fmt.Sprintf("Invalid header name %q", name))
		}
		if !validHeaderValue(header.Value) {
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
	// multipart 的 boundary 只有 sidecar 知道：用户手填的 Content-Type 一定对不上，直接覆盖。
	if body.contentType != "" {
		request.Header.Del("Content-Type")
		request.Header.Set("Content-Type", body.contentType)
	}

	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		if failure := contextFailure(ctx); failure != nil {
			return nil, failure
		}
		return nil, httpTransportError(err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBody)+1))
	if err != nil {
		if failure := contextFailure(ctx); failure != nil {
			return nil, failure
		}
		return nil, httpTransportError(err)
	}
	truncated := len(data) > maxBody
	if truncated {
		data = data[:maxBody]
	}
	elapsed := time.Since(started)

	contentType := response.Header.Get("Content-Type")
	bodyKind := classifyBody(data, contentType)
	result := &SendResult{
		Status:      response.StatusCode,
		StatusText:  statusText(response),
		HTTPVersion: response.Proto,
		URL:         response.Request.URL.String(),
		Headers:     headerEntries(response.Header),
		BodyKind:    bodyKind,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		Truncated:   truncated,
		ElapsedMs:   elapsed.Milliseconds(),
		Redirects:   redirects,
		ReceivedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	switch bodyKind {
	case "text":
		result.BodyText = string(data)
	case "binary":
		result.BodyBase64 = base64.StdEncoding.EncodeToString(data)
	}
	return result, nil
}

func validHTTPMethod(method string) bool {
	if method == "" || len(method) > 32 {
		return false
	}
	for _, character := range method {
		if character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func parseTargetURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("URL is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, errors.New("URL is not valid")
	}
	if !parsed.IsAbs() {
		return nil, errors.New("URL must be absolute and start with http:// or https://")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Only http:// and https:// URLs are supported")
	}
	if parsed.Host == "" {
		return nil, errors.New("URL must include a host")
	}
	return parsed, nil
}

// requestBody 生成请求正文；multipart 的 Content-Type 在 bodyPlan 里携带。
func requestBody(params SendParams, uploads UploadResolver) (*bodyPlan, *apperr.Error) {
	encoding := strings.ToLower(strings.TrimSpace(params.BodyEncoding))
	if encoding == "multipart" {
		if params.Multipart == nil {
			return nil, apperr.Invalid("Multipart body is missing")
		}
		return buildMultipartBody(params.Multipart, uploads)
	}
	if params.Multipart != nil {
		return nil, apperr.Invalid("Multipart body is only valid with the multipart body encoding")
	}

	var body []byte
	switch encoding {
	case "", "none":
		if params.BodyText != "" {
			body = []byte(params.BodyText)
		}
	case "text", "utf8", "utf-8":
		body = []byte(params.BodyText)
	case "base64":
		if params.BodyBase64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(params.BodyBase64)
			if err != nil {
				return nil, apperr.Invalid("Request body is not valid base64")
			}
			body = decoded
		}
	default:
		return nil, apperr.Invalid("Unsupported body encoding")
	}
	if len(body) > maxRequestBodyBytes {
		return nil, apperr.Invalid(fmt.Sprintf("Request body is larger than %d bytes", maxRequestBodyBytes))
	}
	return &bodyPlan{
		length: int64(len(body)),
		open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		},
	}, nil
}

// multipart 的规模限制。文件内容走暂存文件、流式发送，所以这里可以比内联
// base64 宽松得多；上限与前端 model.js 的 MULTIPART_* 常量保持一致。
const (
	maxMultipartFiles            = 100
	maxMultipartFileBytes  int64 = 1 << 30
	maxMultipartTotalBytes int64 = 5 << 30
)

// multipartQuoteEscaper 与 mime/multipart 内部的转义规则一致：
// 头参数里的反斜杠和双引号必须转义，否则文件名会截断 Content-Disposition。
var multipartQuoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, `\"`)

func multipartPartName(value string) (string, *apperr.Error) {
	name := strings.TrimSpace(value)
	if name == "" {
		// 前端在字段名留空时会退回默认名；这里再兜一次，避免旧调用直接失败。
		name = "file"
	}
	if strings.ContainsAny(name, "\r\n") {
		return "", apperr.Invalid("Multipart part name contains an invalid character")
	}
	return name, nil
}

// multipartPart 是一个已校验的部件。staged 部件只记录 uploadId 与大小，
// 正文在 open() 时才从暂存文件读出，避免把整个上传读进内存。
type multipartPart struct {
	disposition string
	contentType string
	data        []byte
	uploadID    string
	size        int64
}

// buildMultipartBody 组装 multipart 正文的元数据，并返回可按需重建的 bodyPlan。
//
// 长度自己算（而不是用 multipart.Writer + io.Pipe）：长度已知才能设置
// Content-Length，避免上传请求退化成 chunked；GetBody 复用同一个 open，
// 307/308 重定向时能重新打开暂存文件重发。
func buildMultipartBody(multipartBody *MultipartBody, uploads UploadResolver) (*bodyPlan, *apperr.Error) {
	if len(multipartBody.Files) > maxMultipartFiles {
		return nil, apperr.Invalid(fmt.Sprintf("A multipart request supports at most %d files", maxMultipartFiles))
	}

	boundary := multipart.NewWriter(io.Discard).Boundary()
	parts := make([]multipartPart, 0, len(multipartBody.Fields)+len(multipartBody.Files))
	total := int64(0)

	for _, field := range multipartBody.Fields {
		name, nameError := multipartPartName(field.Name)
		if nameError != nil {
			return nil, nameError
		}
		disposition := fmt.Sprintf(`name="%s"`, multipartQuoteEscaper.Replace(name))
		data := []byte(field.Value)
		parts = append(parts, multipartPart{disposition: disposition, data: data})
		total += int64(len(multipartPartHeader(boundary, disposition, "")) + len(data) + 2)
	}

	for _, file := range multipartBody.Files {
		name, nameError := multipartPartName(file.Name)
		if nameError != nil {
			return nil, nameError
		}
		fileName := strings.TrimSpace(file.FileName)
		if fileName == "" {
			fileName = "file"
		}
		if strings.ContainsAny(fileName, "\r\n") {
			return nil, apperr.Invalid("Multipart file name contains an invalid character")
		}
		contentType := strings.TrimSpace(file.ContentType)
		if contentType == "" || !validHeaderValue(contentType) {
			contentType = "application/octet-stream"
		}
		disposition := fmt.Sprintf(
			`name="%s"; filename="%s"`,
			multipartQuoteEscaper.Replace(name),
			multipartQuoteEscaper.Replace(fileName),
		)

		part := multipartPart{disposition: disposition, contentType: contentType}
		uploadID := strings.TrimSpace(file.UploadID)
		switch {
		case uploadID != "":
			if uploads == nil {
				return nil, apperr.Invalid("Uploaded files are not available")
			}
			size, sizeError := uploads.Stat(uploadID)
			if sizeError != nil {
				return nil, sizeError
			}
			part.uploadID = uploadID
			part.size = size
		case file.Base64 != "":
			data, decodeError := base64.StdEncoding.DecodeString(file.Base64)
			if decodeError != nil {
				return nil, apperr.Invalid(fmt.Sprintf("File %q is not valid base64", fileName))
			}
			part.data = data
			part.size = int64(len(data))
		default:
			return nil, apperr.Invalid(fmt.Sprintf("File %q has no content; choose it again", fileName))
		}
		if part.size > maxMultipartFileBytes {
			return nil, apperr.Invalid(fmt.Sprintf("File %q is larger than %d bytes", fileName, maxMultipartFileBytes))
		}
		parts = append(parts, part)
		total += int64(len(multipartPartHeader(boundary, disposition, contentType))) + part.size + 2
	}

	closing := []byte("--" + boundary + "--\r\n")
	total += int64(len(closing))
	if total > maxMultipartTotalBytes {
		return nil, apperr.Invalid(fmt.Sprintf("Request body is larger than %d bytes", maxMultipartTotalBytes))
	}

	plan := &bodyPlan{
		contentType: "multipart/form-data; boundary=" + boundary,
		length:      total,
	}
	plan.open = func() (io.ReadCloser, error) {
		readers := make([]io.Reader, 0, len(parts)*3+1)
		closers := make([]io.Closer, 0, len(parts))
		closeAll := func() {
			for _, closer := range closers {
				_ = closer.Close()
			}
		}
		for _, part := range parts {
			readers = append(readers, bytes.NewReader(multipartPartHeader(boundary, part.disposition, part.contentType)))
			if part.uploadID != "" {
				if uploads == nil {
					closeAll()
					return nil, apperr.Invalid("Uploaded files are not available")
				}
				file, openError := uploads.Open(part.uploadID)
				if openError != nil {
					closeAll()
					return nil, openError
				}
				closers = append(closers, file)
				readers = append(readers, file)
			} else {
				readers = append(readers, bytes.NewReader(part.data))
			}
			readers = append(readers, strings.NewReader("\r\n"))
		}
		readers = append(readers, bytes.NewReader(closing))
		return &multiReadCloser{Reader: io.MultiReader(readers...), closers: closers}, nil
	}
	return plan, nil
}

// multipartPartHeader 生成与 mime/multipart 兼容的部件头（含前导 boundary 与空行）。
func multipartPartHeader(boundary, disposition, contentType string) []byte {
	var header strings.Builder
	header.WriteString("--")
	header.WriteString(boundary)
	header.WriteString("\r\nContent-Disposition: form-data; ")
	header.WriteString(disposition)
	header.WriteString("\r\n")
	if contentType != "" {
		header.WriteString("Content-Type: ")
		header.WriteString(contentType)
		header.WriteString("\r\n")
	}
	header.WriteString("\r\n")
	return []byte(header.String())
}

func statusText(response *http.Response) string {
	if response.Status != "" {
		if _, text, ok := strings.Cut(response.Status, " "); ok && text != "" {
			return text
		}
	}
	if text := http.StatusText(response.StatusCode); text != "" {
		return text
	}
	return response.Status
}

func validHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\t' {
			continue
		}
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func headerEntries(header http.Header) []values.KeyValue {
	names := make([]string, 0, len(header))
	for name := range header {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]values.KeyValue, 0, len(names))
	for _, name := range names {
		for _, value := range header.Values(name) {
			entries = append(entries, values.KeyValue{Name: name, Value: value})
		}
	}
	return entries
}

func classifyBody(data []byte, contentType string) string {
	if len(data) == 0 {
		return "empty"
	}
	lower := strings.ToLower(contentType)
	textual := strings.HasPrefix(lower, "text/") ||
		strings.Contains(lower, "json") ||
		strings.Contains(lower, "xml") ||
		strings.Contains(lower, "javascript") ||
		strings.Contains(lower, "x-www-form-urlencoded") ||
		strings.Contains(lower, "graphql") ||
		strings.Contains(lower, "yaml") ||
		strings.Contains(lower, "csv")
	if textual || (utf8.Valid(data) && !bytes.ContainsRune(data, 0)) {
		return "text"
	}
	return "binary"
}

// contextFailure 优先返回"用户主动终止"或"超时"的明确分类。
//
// 网络层返回的 err 在取消场景下只是 context.Canceled，无法区分是谁取消的；
// context.Cause 能看到我们写入的 apperr.ErrCanceled 哨兵。
func contextFailure(ctx context.Context) *apperr.Error {
	if errors.Is(context.Cause(ctx), apperr.ErrCanceled) {
		return apperr.Canceled()
	}
	if errors.Is(context.Cause(ctx), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return apperr.Upstream(apperr.KindTimeout, "Request timed out")
	}
	return nil
}

func httpTransportError(err error) *apperr.Error {
	kind := "network"
	message := err.Error()

	var urlError *url.Error
	if errors.As(err, &urlError) && urlError.Err != nil {
		err = urlError.Err
		message = err.Error()
	}

	if errors.Is(err, context.Canceled) {
		kind = "canceled"
		message = "Request canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		kind = "timeout"
		message = "Request timed out"
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		kind = "timeout"
		message = "Request timed out"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		kind = "dns"
		message = "Cannot resolve host " + dnsError.Name
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) {
		if kind == "network" {
			kind = "connection"
		}
	}
	if strings.Contains(strings.ToLower(message), "x509") ||
		strings.Contains(strings.ToLower(message), "certificate") ||
		strings.Contains(strings.ToLower(message), "tls") {
		kind = "tls"
	}
	if strings.TrimSpace(message) == "" {
		message = "Request failed"
	}
	return apperr.Upstream(apperr.Kind(kind), "%s", message)
}
