package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendHTTPGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Test", "yes")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"ok":true,"method":"` + request.Method + `"}`))
	}))
	defer server.Close()

	result, pluginError := sendHTTP(httpSendParams{Method: "GET", URL: server.URL})
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

	result, pluginError := sendHTTP(httpSendParams{
		Method:       "post",
		URL:          server.URL,
		Headers:      []keyValue{{Name: "X-Custom", Value: "abc"}},
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
	result, pluginError := sendHTTP(httpSendParams{
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

	result, pluginError := sendHTTP(httpSendParams{Method: "GET", URL: server.URL})
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
	result, pluginError := sendHTTP(httpSendParams{Method: "GET", URL: server.URL + "/redirect", FollowRedirects: &follow})
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
	result, pluginError = sendHTTP(httpSendParams{Method: "GET", URL: server.URL + "/redirect", FollowRedirects: &noFollow})
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

	result, pluginError := sendHTTP(httpSendParams{Method: "GET", URL: server.URL, MaxBodyBytes: 1024})
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

	_, pluginError := sendHTTP(httpSendParams{Method: "GET", URL: server.URL, TimeoutMs: 50})
	if pluginError == nil {
		t.Fatal("expected a timeout error")
	}
	data, _ := json.Marshal(pluginError.Data)
	if !strings.Contains(string(data), "timeout") {
		t.Fatalf("expected timeout kind, got %s", data)
	}
}

func TestSendHTTPValidation(t *testing.T) {
	cases := []httpSendParams{
		{Method: "GET", URL: ""},
		{Method: "GET", URL: "ftp://example.com/file"},
		{Method: "GET", URL: "example.com"},
		{Method: "IN VALID", URL: "http://example.com"},
		{Method: "GET", URL: "http://example.com", Headers: []keyValue{{Name: "X-Test", Value: "bad\r\nInjected: 1"}}},
		{Method: "GET", URL: "http://example.com", Headers: []keyValue{{Name: "Bad Name", Value: "x"}}},
	}
	for index, params := range cases {
		if _, pluginError := sendHTTP(params); pluginError == nil {
			t.Fatalf("case %d should be rejected", index)
		}
	}
}

func hasHeader(entries []keyValue, name, value string) bool {
	for _, entry := range entries {
		if strings.EqualFold(entry.Name, name) && entry.Value == value {
			return true
		}
	}
	return false
}
