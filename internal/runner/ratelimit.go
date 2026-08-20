package runner

import (
	"context"
	"sync"
	"time"
)

// adaptiveLimiter 根据限流信号动态调整请求间隔，避免大仓批量 429。
// 初始无额外延迟（interval=0），命中限流后指数退避，成功后逐步回落。
// 设计为无外部依赖的轻量实现，避免引入 x/time/rate。
type adaptiveLimiter struct {
	mu          sync.Mutex
	interval    time.Duration
	minInterval time.Duration
	maxInterval time.Duration
	last        time.Time
}

func newAdaptiveLimiter() *adaptiveLimiter {
	return &adaptiveLimiter{
		minInterval: 0,
		maxInterval: 5 * time.Second,
	}
}

// Wait 在需要时阻塞，直到满足当前间隔。尊重 ctx 取消。
func (l *adaptiveLimiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	iv := l.interval
	last := l.last
	l.mu.Unlock()

	if iv == 0 {
		l.mu.Lock()
		l.last = time.Now()
		l.mu.Unlock()
		return nil
	}

	wait := last.Add(iv).Sub(time.Now())
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	l.mu.Lock()
	l.last = time.Now()
	l.mu.Unlock()
	return nil
}

// OnThrottle 命中限流时调用，间隔指数退避。
func (l *adaptiveLimiter) OnThrottle() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.interval == 0 {
		l.interval = 400 * time.Millisecond
	} else {
		l.interval *= 2
		if l.interval > l.maxInterval {
			l.interval = l.maxInterval
		}
	}
}

// OnSuccess 连续成功时逐步回落。
func (l *adaptiveLimiter) OnSuccess() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.interval > 0 {
		l.interval -= 100 * time.Millisecond
		if l.interval < l.minInterval {
			l.interval = l.minInterval
		}
	}
}

// isRateLimitError 判断是否为限流类错误（429 / 限流文案）。
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// 匹配常见限流表述
	if containsFold(s, "429") || containsFold(s, "rate limit") || containsFold(s, "rate_limit") || containsFold(s, "too many requests") || containsFold(s, "限流") {
		return true
	}
	return false
}

func containsFold(s, substr string) bool {
	return len(s) >= len(substr) && indexFold(s, substr) >= 0
}

func indexFold(s, substr string) int {
	// 简化版不区分大小写查找
	ls := toLower(s)
	sub := toLower(substr)
	for i := 0; i+len(sub) <= len(ls); i++ {
		if ls[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
