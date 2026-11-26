package httptool

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
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
	TimeoutMs       int               `json:"timeoutMs"`
	FollowRedirects *bool             `json:"followRedirects"`
	MaxRedirects    int               `json:"maxRedirects"`
	SkipTLSVerify   bool              `json:"skipTlsVerify"`
	MaxBodyBytes    int               `json:"maxBodyBytes"`
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

	body, pluginError := requestBody(params)
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

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	clonedTransport := transport.Clone()
	clonedTransport.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: params.SkipTLSVerify,
	}
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

	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, apperr.Invalid("Cannot build the request: " + err.Error())
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

func requestBody(params SendParams) ([]byte, *apperr.Error) {
	var body []byte
	switch strings.ToLower(strings.TrimSpace(params.BodyEncoding)) {
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
	return body, nil
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
