package uploads

import (
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

func appendText(t *testing.T, store *Store, id, text string) int64 {
	t.Helper()
	size, err := store.Append(id, base64.StdEncoding.EncodeToString([]byte(text)))
	if err != nil {
		t.Fatalf("append failed: %v", err)
	}
	return size
}

func TestStagedUploadRoundTrip(t *testing.T) {
	store := NewStore()
	defer store.Close()

	id, err := store.Begin("note.txt", 5)
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if size := appendText(t, store, id, "hello"); size != 5 {
		t.Fatalf("unexpected size after append: %d", size)
	}
	if size, statErr := store.Stat(id); statErr != nil || size != 5 {
		t.Fatalf("unexpected stat: size=%d err=%v", size, statErr)
	}

	reader, openErr := store.Open(id)
	if openErr != nil {
		t.Fatalf("open failed: %v", openErr)
	}
	data, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil || string(data) != "hello" {
		t.Fatalf("unexpected content: %q (%v)", data, readErr)
	}

	store.Release([]string{id})
	if _, statErr := store.Stat(id); statErr == nil {
		t.Fatal("expected the staged file to be released")
	}
}

func TestStagedUploadRejectsInvalidInput(t *testing.T) {
	store := NewStore()
	defer store.Close()

	if _, err := store.Begin("big.bin", MaxFileBytes+1); err == nil {
		t.Fatal("expected a size error for an oversized declared size")
	}
	if _, err := store.Append("missing", "aGVsbG8="); err == nil {
		t.Fatal("expected an error for an unknown upload id")
	}

	id, err := store.Begin("note.txt", 0)
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if _, appendErr := store.Append(id, "!!!"); appendErr == nil {
		t.Fatal("expected an error for invalid base64")
	}
	if _, appendErr := store.Append(id, ""); appendErr == nil {
		t.Fatal("expected an error for an empty chunk")
	}
	// 超过单文件上限：用声明大小不够，Append 还要按实际字节数再拦一次。
	large := strings.Repeat("a", maxChunkBytes+1)
	encodedLarge := base64.StdEncoding.EncodeToString([]byte(large))
	if _, appendErr := store.Append(id, encodedLarge); appendErr == nil {
		t.Fatal("expected an error for a chunk above the per-chunk limit")
	}
}

func TestAbortRemovesStagedFile(t *testing.T) {
	store := NewStore()
	defer store.Close()

	id, err := store.Begin("note.txt", 5)
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	appendText(t, store, id, "hello")
	if !store.Abort(id) {
		t.Fatal("expected abort to report a removal")
	}
	if store.Abort(id) {
		t.Fatal("second abort should be a no-op")
	}
}
