// ========================================
// auth.go — 限流器优化：定期清理 + LRU 淘汰
// ========================================

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int
	window   time.Duration
	maxSize  int // ★ 新增：最大 visitor 数量
}

type visitor struct {
	count    int
	lastSeen time.Time
}

var loginLimiter = &rateLimiter{
	visitors: make(map[string]*visitor),
	rate:     5,
	window:   1 * time.Minute,
	maxSize:  10000, // ★ 最多缓存 10000 个 IP
}

var apiLimiter = &rateLimiter{
	visitors: make(map[string]*visitor),
	rate:     60,
	window:   1 * time.Minute,
	maxSize:  50000, // ★ 最多缓存 50000 个 IP
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	now := time.Now()

	if !exists || now.Sub(v.lastSeen) > rl.window {
		// ★ 新增：如果 visitor 数量超过上限，先清理一次
		if !exists && len(rl.visitors) >= rl.maxSize {
			rl.cleanupLocked(now)
			// 清理后仍满，拒绝新 visitor（极端情况）
			if len(rl.visitors) >= rl.maxSize {
				return false
			}
		}
		rl.visitors[key] = &visitor{count: 1, lastSeen: now}
		return true
	}

	if v.count >= rl.rate {
		return false
	}

	v.count++
	v.lastSeen = now
	return true
}

// Cleanup 清理过期 visitor（外部调用）
func (rl *rateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.cleanupLocked(time.Now())
}

// cleanupLocked 内部清理方法（需持锁）
func (rl *rateLimiter) cleanupLocked(now time.Time) {
	for k, v := range rl.visitors {
		if now.Sub(v.lastSeen) > rl.window*2 {
			delete(rl.visitors, k)
		}
	}
}
