// Package inflight 跟踪正在执行的可取消请求。
//
// JSON-RPC 的 Handler 拿不到可取消的 context，但 "request/cancel" 必须能在
// http/send 或 grpc/invoke 阻塞时打断它。这里以请求 ID 为桥梁：请求开始前登记
// 自己的取消函数，取消请求按 ID 找到并调用它。
//
// 单独成包是为了让"请求生命周期"只在一处实现；httptool / grpctool 只负责
// 消费 context，不需要知道取消请求是从哪条 RPC 进来的。
package inflight

import (
	"context"
	"sync"
	"time"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
)

// retainCanceled 决定"先取消、后开始"竞态留下的标记保留多久。
//
// 前端点终止时，取消 RPC 可能比发送 RPC 先被 sidecar 取到；留一个短时标记，
// 后到的请求就会立刻以 canceled 结束，而不是白跑一遍网络。
const retainCanceled = 2 * time.Minute

type entry struct {
	cancel context.CancelCauseFunc
}

// Registry 保存进行中的请求，并发安全。
type Registry struct {
	mu       sync.Mutex
	active   map[string]*entry
	canceled map[string]time.Time
	now      func() time.Time
}

// New 构造一个注册表。
func New() *Registry {
	return &Registry{
		active:   make(map[string]*entry),
		canceled: make(map[string]time.Time),
		now:      time.Now,
	}
}

// Begin 为一次请求登记可取消上下文。
//
// id 为空时退化为不可取消的后台上下文，兼容不传 requestId 的调用方。
// 调用方必须 defer release()，否则该 ID 会一直占用到进程结束。
func (r *Registry) Begin(id string) (context.Context, func(), *apperr.Error) {
	if id == "" {
		return context.Background(), func() {}, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()

	if _, canceled := r.canceled[id]; canceled {
		delete(r.canceled, id)
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(apperr.ErrCanceled)
		return ctx, func() {}, nil
	}

	if _, exists := r.active[id]; exists {
		return nil, nil, apperr.Invalid("Request %q is already running", id)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	registered := &entry{cancel: cancel}
	r.active[id] = registered
	release := func() {
		r.mu.Lock()
		// 只注销自己那一条，避免误删复用同一 ID 的新请求。
		if current, ok := r.active[id]; ok && current == registered {
			delete(r.active, id)
		}
		r.mu.Unlock()
		cancel(nil)
	}
	return ctx, release, nil
}

// Cancel 终止指定请求。
//
// 返回 true 表示确实找到了正在运行的请求；false 表示它已经结束，或者取消请求
// 比发送请求先到——后者会留下标记，Begin 时立刻返回已取消的上下文。
func (r *Registry) Cancel(id string) bool {
	if id == "" {
		return false
	}

	r.mu.Lock()
	r.pruneLocked()
	current, running := r.active[id]
	if !running {
		r.canceled[id] = r.now()
	}
	r.mu.Unlock()

	// 取消回调可能同步唤醒等待者，放到锁外执行，避免和 release 互相等待。
	if running {
		current.cancel(apperr.ErrCanceled)
		return true
	}
	return false
}

func (r *Registry) pruneLocked() {
	deadline := r.now().Add(-retainCanceled)
	for id, at := range r.canceled {
		if at.Before(deadline) {
			delete(r.canceled, id)
		}
	}
}
