package httptool

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"github.com/jlian/dbx-plugin-api-tester/internal/uploads"
	"github.com/jlian/dbx-plugin-api-tester/internal/values"
)

func TestSendHTTPGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Test", "yes")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"ok":true,"method":"` + request.Method + `"}`))
	}))
	defer server.Close()

	result, pluginError := Send(context.Background(), SendParams{Method: "GET", URL: server.URL})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	if result.Status != 200 || result.StatusText != "OK" {
		t.Fatalf("unexpected status: %d %q", result.Status, result.StatusText)
	}
	if result.BodyKind != "text" || !strings.Contains(result.BodyText, `"ok":true`) {
		t.Fatalf("unexpected body: %q", result.BodyText)
	}
	if result.ContentType != "application/json" {
		t.Fatalf("unexpected content type: %q", result.ContentType)
	}
	if !hasHeader(result.Headers, "X-Test", "yes") {
		t.Fatalf("missing response header: %v", result.Headers)
	}
}

func TestSendHTTPPostBodyAndHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte(request.Method + "|" + request.Header.Get("X-Custom") + "|" + string(body)))
	}))
	defer server.Close()

	result, pluginError := Send(context.Background(), SendParams{
		Method:       "post",
		URL:          server.URL,
		Headers:      []values.KeyValue{{Name: "X-Custom", Value: "abc"}},
		BodyText:     `{"hello":"world"}`,
		BodyEncoding: "text",
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	if result.BodyText != `POST|abc|{"hello":"world"}` {
		t.Fatalf("unexpected echo: %q", result.BodyText)
	}
}

func TestSendHTTPBase64Body(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		_, _ = writer.Write([]byte(base64.StdEncoding.EncodeToString(body)))
	}))
	defer server.Close()

	payload := []byte{0, 1, 2, 3, 254, 255}
	result, pluginError := Send(context.Background(), SendParams{
		Method:       "PUT",
		URL:          server.URL,
		BodyBase64:   base64.StdEncoding.EncodeToString(payload),
		BodyEncoding: "base64",
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	if result.BodyText != base64.StdEncoding.EncodeToString(payload) {
		t.Fatalf("unexpected body: %q", result.BodyText)
	}
}

func TestSendHTTPBinaryResponse(t *testing.T) {
	payload := []byte{0, 1, 2, 3, 200, 255}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/octet-stream")
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	result, pluginError := Send(context.Background(), SendParams{Method: "GET", URL: server.URL})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	if result.BodyKind != "binary" {
		t.Fatalf("unexpected body kind: %q", result.BodyKind)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.BodyBase64)
	if err != nil || string(decoded) != string(payload) {
		t.Fatalf("unexpected binary payload: %v", err)
	}
}

func TestSendHTTPRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirect" {
			http.Redirect(writer, request, "/final", http.StatusFound)
			return
		}
		_, _ = writer.Write([]byte("final"))
	}))
	defer server.Close()

	follow := true
	result, pluginError := Send(context.Background(), SendParams{Method: "GET", URL: server.URL + "/redirect", FollowRedirects: &follow})
	if pluginError != nil {
		t.Fatalf("unexpected error when following redirects: %v", pluginError)
	}
	if result.Status != 200 || result.BodyText != "final" {
		t.Fatalf("unexpected followed redirect result: %d %q", result.Status, result.BodyText)
	}
	if len(result.Redirects) != 1 || result.Redirects[0].Status != http.StatusFound {
		t.Fatalf("unexpected redirect chain: %+v", result.Redirects)
	}

	noFollow := false
	result, pluginError = Send(context.Background(), SendParams{Method: "GET", URL: server.URL + "/redirect", FollowRedirects: &noFollow})
	if pluginError != nil {
		t.Fatalf("unexpected error without redirects: %v", pluginError)
	}
	if result.Status != http.StatusFound {
		t.Fatalf("expected the 302 response, got %d", result.Status)
	}
}

func TestSendHTTPTruncatesLargeBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte(strings.Repeat("a", 4096)))
	}))
	defer server.Close()

	result, pluginError := Send(context.Background(), SendParams{Method: "GET", URL: server.URL, MaxBodyBytes: 1024})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	if !result.Truncated || result.SizeBytes != 1024 || len(result.BodyText) != 1024 {
		t.Fatalf("unexpected truncation: truncated=%v size=%d length=%d", result.Truncated, result.SizeBytes, len(result.BodyText))
	}
}

func TestSendHTTPTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = writer.Write([]byte("late"))
	}))
	defer server.Close()

	_, pluginError := Send(context.Background(), SendParams{Method: "GET", URL: server.URL, TimeoutMs: 50})
	if pluginError == nil {
		t.Fatal("expected a timeout error")
	}
	if pluginError.Kind != apperr.KindTimeout {
		t.Fatalf("expected timeout kind, got %q", pluginError.Kind)
	}
	if pluginError.Code != apperr.CodeUpstream {
		t.Fatalf("expected upstream code, got %d", pluginError.Code)
	}
}

// 用户点终止时，错误分类必须是 canceled 而不是 timeout：
// 前端据此显示"已终止"，也不该提示去检查网络或加大超时。
func TestSendHTTPCanceledByUser(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-release
		_, _ = writer.Write([]byte("late"))
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(50*time.Millisecond, func() { cancel(apperr.ErrCanceled) })

	_, pluginError := Send(ctx, SendParams{Method: "GET", URL: server.URL, TimeoutMs: 5000})
	if pluginError == nil {
		t.Fatal("expected the request to be canceled")
	}
	if pluginError.Kind != apperr.KindCanceled {
		t.Fatalf("expected canceled kind, got %q (%s)", pluginError.Kind, pluginError.Message)
	}
	if pluginError.Code != apperr.CodeUpstream {
		t.Fatalf("expected upstream code, got %d", pluginError.Code)
	}
}

func TestSendHTTPValidation(t *testing.T) {
	cases := []SendParams{
		{Method: "GET", URL: ""},
		{Method: "GET", URL: "ftp://example.com/file"},
		{Method: "GET", URL: "example.com"},
		{Method: "IN VALID", URL: "http://example.com"},
		{Method: "GET", URL: "http://example.com", Headers: []values.KeyValue{{Name: "X-Test", Value: "bad\r\nInjected: 1"}}},
		{Method: "GET", URL: "http://example.com", Headers: []values.KeyValue{{Name: "Bad Name", Value: "x"}}},
	}
	for index, params := range cases {
		if _, pluginError := Send(context.Background(), params); pluginError == nil {
			t.Fatalf("case %d should be rejected", index)
		}
	}
}

func hasHeader(entries []values.KeyValue, name, value string) bool {
	for _, entry := range entries {
		if strings.EqualFold(entry.Name, name) && entry.Value == value {
			return true
		}
	}
	return false
}

// 空正文不能退化成 chunked：客户端用自定义 reader 时如果不换 NoBody，
// POST 会带上 Transfer-Encoding: chunked，部分网关会直接拒绝。
func TestSendHTTPEmptyBodyIsNotChunked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if len(request.TransferEncoding) != 0 {
			t.Errorf("unexpected transfer encoding: %v", request.TransferEncoding)
		}
		if request.ContentLength != 0 {
			t.Errorf("unexpected content length: %d", request.ContentLength)
		}
		_, _ = writer.Write([]byte("ok"))
	}))
	defer server.Close()

	if _, pluginError := Send(context.Background(), SendParams{
		Method:       "POST",
		URL:          server.URL,
		BodyEncoding: "none",
	}); pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
}

func TestSendHTTPMultipartBody(t *testing.T) {
	fileContent := "hello multipart file"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/form-data; boundary=") {
			t.Errorf("unexpected content type: %q", request.Header.Get("Content-Type"))
		}
		reader, err := request.MultipartReader()
		if err != nil {
			t.Errorf("cannot read multipart body: %v", err)
			return
		}
		fields := map[string]string{}
		files := map[string]string{}
		for {
			part, partError := reader.NextPart()
			if partError == io.EOF {
				break
			}
			if partError != nil {
				t.Errorf("cannot read multipart part: %v", partError)
				return
			}
			data, _ := io.ReadAll(part)
			if part.FileName() != "" {
				files[part.FormName()] = part.FileName() + "|" + string(data)
				continue
			}
			fields[part.FormName()] = string(data)
		}
		_, _ = writer.Write([]byte(fields["title"] + "|" + files["upload"]))
	}))
	defer server.Close()

	result, pluginError := Send(context.Background(), SendParams{
		Method:       "POST",
		URL:          server.URL,
		BodyEncoding: "multipart",
		// 用户手填的 Content-Type 一定和 sidecar 生成的 boundary 对不上，必须被覆盖。
		Headers: []values.KeyValue{{Name: "Content-Type", Value: "text/plain"}},
		Multipart: &MultipartBody{
			Fields: []values.KeyValue{{Name: "title", Value: "hello"}},
			Files: []MultipartFile{{
				Name:        "upload",
				FileName:    "note.txt",
				ContentType: "text/plain",
				Base64:      base64.StdEncoding.EncodeToString([]byte(fileContent)),
			}},
		},
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	if result.BodyText != "hello|note.txt|"+fileContent {
		t.Fatalf("unexpected echo: %q", result.BodyText)
	}
}

// 请求头回显：用户填的、插件补的都要能看到，Host/Content-Length 不在
// request.Header 里，必须单独补成条目，否则界面上会漏掉线上真实存在的头。
func TestSendHTTPEchoesSentRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	defer server.Close()

	result, pluginError := Send(context.Background(), SendParams{
		Method:       "POST",
		URL:          server.URL + "/path?q=1",
		Headers:      []values.KeyValue{{Name: "X-Custom", Value: "abc"}},
		BodyText:     `{"hello":"world"}`,
		BodyEncoding: "text",
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	sent := result.Request
	if sent == nil {
		t.Fatal("sent request is missing")
	}
	if sent.Method != "POST" || sent.URL != server.URL+"/path?q=1" {
		t.Fatalf("unexpected request line: %s %s", sent.Method, sent.URL)
	}
	if sent.HTTPVersion != "HTTP/1.1" {
		t.Fatalf("unexpected http version: %q", sent.HTTPVersion)
	}
	if !hasHeader(sent.Headers, "Host", strings.TrimPrefix(server.URL, "http://")) {
		t.Fatalf("missing host header: %v", sent.Headers)
	}
	if !hasHeader(sent.Headers, "X-Custom", "abc") {
		t.Fatalf("missing custom header: %v", sent.Headers)
	}
	if !hasHeader(sent.Headers, "User-Agent", "DBX-API-Tester/"+Version()) {
		t.Fatalf("missing auto user-agent: %v", sent.Headers)
	}
	if !hasHeader(sent.Headers, "Content-Length", "17") || sent.ContentLength != 17 {
		t.Fatalf("unexpected content length: %d %v", sent.ContentLength, sent.Headers)
	}
	// 展示顺序按报文习惯：Host 在第一行。
	if len(sent.Headers) == 0 || sent.Headers[0].Name != "Host" {
		t.Fatalf("host must be the first entry: %v", sent.Headers)
	}
	if sent.Body == nil || sent.Body.Kind != "text" || sent.Body.Size != 17 {
		t.Fatalf("unexpected body summary: %+v", sent.Body)
	}
	if sent.Body.Text != `{"hello":"world"}` || sent.Body.Truncated {
		t.Fatalf("unexpected body echo: %q truncated=%v", sent.Body.Text, sent.Body.Truncated)
	}
}

// 正文回显有上限：真实字节照常发完，界面只截一段，且不能把多字节字符切成半个。
func TestSendHTTPTruncatesEchoedBodyAtRuneBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = writer.Write([]byte("ok"))
	}))
	defer server.Close()

	// 让第 maxEchoBodyBytes 个字节落在一个三字节字符中间。
	body := strings.Repeat("a", maxEchoBodyBytes-1) + "中文"
	result, pluginError := Send(context.Background(), SendParams{
		Method:       "POST",
		URL:          server.URL,
		BodyText:     body,
		BodyEncoding: "text",
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	sentBody := result.Request.Body
	if sentBody == nil || !sentBody.Truncated {
		t.Fatalf("body should be marked truncated: %+v", sentBody)
	}
	if sentBody.Size != int64(len(body)) {
		t.Fatalf("size must reflect the wire body: %d != %d", sentBody.Size, len(body))
	}
	if !utf8.ValidString(sentBody.Text) {
		t.Fatalf("echoed body must stay valid utf-8")
	}
	if len(sentBody.Text) > maxEchoBodyBytes {
		t.Fatalf("echoed body exceeds the limit: %d", len(sentBody.Text))
	}
}

// multipart 的 Content-Type 带随机 boundary：用户填的值被覆盖，回显必须是覆盖后的。
func TestSendHTTPEchoesMultipartContentType(t *testing.T) {
	var wireContentType string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		wireContentType = request.Header.Get("Content-Type")
		_, _ = writer.Write([]byte("ok"))
	}))
	defer server.Close()

	result, pluginError := Send(context.Background(), SendParams{
		Method:       "POST",
		URL:          server.URL,
		BodyEncoding: "multipart",
		Headers:      []values.KeyValue{{Name: "Content-Type", Value: "text/plain"}},
		Multipart: &MultipartBody{
			Fields: []values.KeyValue{{Name: "title", Value: "hello"}},
			Files: []MultipartFile{{
				Name:        "upload",
				FileName:    "note.txt",
				ContentType: "text/plain",
				Base64:      base64.StdEncoding.EncodeToString([]byte("hello multipart file")),
			}},
		},
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	if result.Request == nil || !hasHeader(result.Request.Headers, "Content-Type", wireContentType) {
		t.Fatalf("echoed content type does not match wire: %v", result.Request)
	}
	if !strings.HasPrefix(wireContentType, "multipart/form-data; boundary=") {
		t.Fatalf("unexpected wire content type: %q", wireContentType)
	}
	// multipart 只给摘要：字段值 + 文件元数据，不读文件内容。
	multipartBody := result.Request.Body
	if multipartBody == nil || multipartBody.Kind != "multipart" || multipartBody.Multipart == nil {
		t.Fatalf("unexpected body summary: %+v", multipartBody)
	}
	if !hasHeader(multipartBody.Multipart.Fields, "title", "hello") {
		t.Fatalf("missing echoed field: %v", multipartBody.Multipart.Fields)
	}
	if len(multipartBody.Multipart.Files) != 1 {
		t.Fatalf("unexpected files: %v", multipartBody.Multipart.Files)
	}
	file := multipartBody.Multipart.Files[0]
	if file.Name != "upload" || file.FileName != "note.txt" || file.ContentType != "text/plain" {
		t.Fatalf("unexpected file summary: %+v", file)
	}
	if file.Size != int64(len("hello multipart file")) {
		t.Fatalf("unexpected file size: %d", file.Size)
	}
	if multipartBody.Size <= 0 || multipartBody.Size != result.Request.ContentLength {
		t.Fatalf("multipart size must equal content length: %d vs %d", multipartBody.Size, result.Request.ContentLength)
	}
}

// 空字段名不再让整次请求失败：前端允许用户不填，sidecar 统一退回 "file"。
func TestSendHTTPMultipartEmptyPartNameDefaultsToFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
		_, _ = writer.Write([]byte(part.FormName()))
	}))
	defer server.Close()

	result, pluginError := Send(context.Background(), SendParams{
		Method:       "POST",
		URL:          server.URL,
		BodyEncoding: "multipart",
		Multipart: &MultipartBody{
			Fields: []values.KeyValue{{Name: "  ", Value: "1"}},
		},
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	if result.BodyText != "file" {
		t.Fatalf("unexpected part name: %q", result.BodyText)
	}
}

// 文件正常走分块暂存：发送时流式读取、Content-Length 已知（不是 chunked），
// 发送完成后暂存文件被释放；同一字段名可以挂多个文件。
func TestSendHTTPMultipartFromStagedUploads(t *testing.T) {
	store := uploads.NewStore()
	defer store.Close()

	first, firstError := store.Begin("alpha.txt", 5)
	if firstError != nil {
		t.Fatalf("begin failed: %v", firstError)
	}
	if _, err := store.Append(first, base64.StdEncoding.EncodeToString([]byte("alpha"))); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	second, secondError := store.Begin("beta.txt", 4)
	if secondError != nil {
		t.Fatalf("begin failed: %v", secondError)
	}
	if _, err := store.Append(second, base64.StdEncoding.EncodeToString([]byte("beta"))); err != nil {
		t.Fatalf("append failed: %v", err)
	}

	var contentLength int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		contentLength = request.ContentLength
		// 先读原始正文，确认 Content-Length 与实际字节数一致（否则上传会变成 chunked
		// 或被服务端截断），再用同一个 boundary 解析。
		raw, readError := io.ReadAll(request.Body)
		if readError != nil {
			t.Errorf("cannot read request body: %v", readError)
			return
		}
		if int64(len(raw)) != request.ContentLength {
			t.Errorf("Content-Length %d does not match body size %d", request.ContentLength, len(raw))
		}
		boundary := strings.TrimPrefix(request.Header.Get("Content-Type"), "multipart/form-data; boundary=")
		reader := multipart.NewReader(bytes.NewReader(raw), boundary)
		parts := make([]string, 0, 2)
		for {
			part, partError := reader.NextPart()
			if partError == io.EOF {
				break
			}
			if partError != nil {
				t.Errorf("cannot read multipart part: %v", partError)
				return
			}
			data, _ := io.ReadAll(part)
			parts = append(parts, part.FormName()+":"+part.FileName()+"="+string(data))
		}
		_, _ = writer.Write([]byte(strings.Join(parts, ",")))
	}))
	defer server.Close()

	params := SendParams{
		Method:       "POST",
		URL:          server.URL,
		BodyEncoding: "multipart",
		Multipart: &MultipartBody{
			Fields: []values.KeyValue{{Name: "title", Value: "hello"}},
			Files: []MultipartFile{
				{Name: "files", FileName: "alpha.txt", ContentType: "text/plain", UploadID: first},
				{Name: "files", FileName: "beta.txt", ContentType: "text/plain", UploadID: second},
			},
		},
	}
	// 同一个请求经常要连续发送（比如改 URL、改 header 后再试）。发送只能读取
	// 暂存文件，不能把 uploadId 消耗掉，否则第二次会报 Upload has expired。
	for attempt := 1; attempt <= 2; attempt++ {
		result, pluginError := SendWithUploads(context.Background(), params, store)
		if pluginError != nil {
			t.Fatalf("send %d: unexpected error: %v", attempt, pluginError)
		}
		if result.BodyText != "title:=hello,files:alpha.txt=alpha,files:beta.txt=beta" {
			t.Fatalf("send %d: unexpected echo: %q", attempt, result.BodyText)
		}
		if contentLength <= 0 {
			t.Fatalf("send %d: expected a known Content-Length, got %d", attempt, contentLength)
		}
	}
	for _, id := range []string{first, second} {
		if _, statError := store.Stat(id); statError != nil {
			t.Fatalf("expected upload %s to stay available after sending: %v", id, statError)
		}
	}
}

func TestSendHTTPMultipartRejectsInvalidInput(t *testing.T) {
	encode := func(text string) string { return base64.StdEncoding.EncodeToString([]byte(text)) }
	cases := []struct {
		name   string
		params SendParams
	}{
		{
			name:   "missing multipart body",
			params: SendParams{Method: "POST", URL: "http://127.0.0.1:1", BodyEncoding: "multipart"},
		},
		{
			name: "multipart body with text encoding",
			params: SendParams{
				Method:       "POST",
				URL:          "http://127.0.0.1:1",
				BodyEncoding: "text",
				Multipart:    &MultipartBody{Fields: []values.KeyValue{{Name: "a", Value: "1"}}},
			},
		},
		{
			name: "invalid base64",
			params: SendParams{
				Method:       "POST",
				URL:          "http://127.0.0.1:1",
				BodyEncoding: "multipart",
				Multipart:    &MultipartBody{Files: []MultipartFile{{Name: "upload", FileName: "a.txt", Base64: "!!!"}}},
			},
		},
		{
			name: "line break in file name",
			params: SendParams{
				Method:       "POST",
				URL:          "http://127.0.0.1:1",
				BodyEncoding: "multipart",
				Multipart:    &MultipartBody{Files: []MultipartFile{{Name: "upload", FileName: "bad\r\nname.txt", Base64: encode("x")}}},
			},
		},
		{
			name: "too many files",
			params: SendParams{
				Method:       "POST",
				URL:          "http://127.0.0.1:1",
				BodyEncoding: "multipart",
				Multipart:    &MultipartBody{Files: manyMultipartFiles(maxMultipartFiles+1, encode("x"))},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, pluginError := Send(context.Background(), testCase.params)
			if pluginError == nil {
				t.Fatal("expected an invalid params error")
			}
			if pluginError.Code != apperr.CodeInvalidParams {
				t.Fatalf("unexpected error code: %d (%s)", pluginError.Code, pluginError.Message)
			}
		})
	}
}

func manyMultipartFiles(count int, base64Content string) []MultipartFile {
	files := make([]MultipartFile, 0, count)
	for index := 0; index < count; index++ {
		files = append(files, MultipartFile{
			Name:     "upload",
			FileName: "file.txt",
			Base64:   base64Content,
		})
	}
	return files
}
