package inflight

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
)

func TestBeginAndCancelPropagatesCause(t *testing.T) {
	registry := New()
	ctx, release, err := registry.Begin("req-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer release()

	if !registry.Cancel("req-1") {
		t.Fatal("expected Cancel to find the running request")
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not canceled")
	}
	if !errors.Is(context.Cause(ctx), apperr.ErrCanceled) {
		t.Fatalf("expected the canceled sentinel as cause, got %v", context.Cause(ctx))
	}
}

// 取消请求可能比发送请求先到 sidecar：标记必须在 Begin 时被消费，
// 否则用户点了终止却还会真的发出网络请求。
func TestCancelBeforeBegin(t *testing.T) {
	registry := New()
	if registry.Cancel("req-early") {
		t.Fatal("nothing should be running yet")
	}

	ctx, release, err := registry.Begin("req-early")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer release()

	if !errors.Is(context.Cause(ctx), apperr.ErrCanceled) {
		t.Fatalf("expected a pre-canceled context, got %v", context.Cause(ctx))
	}
}

func TestBeginWithEmptyIDIsNotCancelable(t *testing.T) {
	registry := New()
	ctx, release, err := registry.Begin("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer release()

	if registry.Cancel("") {
		t.Fatal("an empty request ID cannot be canceled")
	}
	if ctx.Err() != nil {
		t.Fatalf("background context should stay alive, got %v", ctx.Err())
	}
}

func TestReleasedRequestDoesNotBlockNewRequests(t *testing.T) {
	registry := New()
	_, release, err := registry.Begin("req-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	release()

	if registry.Cancel("req-1") {
		t.Fatal("a released request must not be cancelable")
	}

	// 注意：上面这次 Cancel 会留下"预取消"标记（真实场景里 ID 是 UUID，
	// 不会复用），所以换一个 ID 验证复用登记不会继承旧状态。
	ctx, releaseAgain, err := registry.Begin("req-2")
	if err != nil {
		t.Fatalf("a new request should be accepted: %v", err)
	}
	defer releaseAgain()
	if ctx.Err() != nil {
		t.Fatal("the new request should not inherit the old cancellation")
	}
}

func TestDuplicateRunningIDIsRejected(t *testing.T) {
	registry := New()
	_, release, err := registry.Begin("req-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer release()

	_, _, duplicateErr := registry.Begin("req-1")
	if duplicateErr == nil {
		t.Fatal("expected a duplicate request ID error")
	}
	if duplicateErr.Code != apperr.CodeInvalidParams {
		t.Fatalf("expected invalid-params, got %d", duplicateErr.Code)
	}
}

// 早到的取消标记只应影响紧随其后的那次 Begin，过期后必须被清理，
// 否则一个陈旧的 ID 会在两分钟内反复干扰后续请求。
func TestStaleCanceledMarkerIsPruned(t *testing.T) {
	registry := New()
	now := time.Now()
	registry.now = func() time.Time { return now }

	registry.Cancel("req-stale")
	if _, _, err := registry.Begin("req-stale"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	registry.Cancel("req-old")
	now = now.Add(retainCanceled + time.Second)
	ctx, release, err := registry.Begin("req-old")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer release()
	if ctx.Err() != nil {
		t.Fatal("an expired canceled marker must not cancel a new request")
	}
}
