// 新增distributed_lock.go
package utils

import (
	"context"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"time"
)

type DistributedLock struct {
	client *redis.Client
}

func NewDistributedLock(client *redis.Client) *DistributedLock {
	return &DistributedLock{client: client}
}

// Lock 加锁时生成唯一令牌
func (dl *DistributedLock) Lock(ctx context.Context, key string, ttl time.Duration) (bool, string, error) {
	token := uuid.New().String()
	result, err := dl.client.SetNX(ctx, key, token, ttl).Result()
	return result, token, err
}

// Unlock 解锁时验证令牌
func (dl *DistributedLock) Unlock(ctx context.Context, key, token string) error {
	script := `
        if redis.call("GET", KEYS[1]) == ARGV[1] then
            return redis.call("DEL", KEYS[1])
        else
            return 0
        end
    `
	_, err := dl.client.Eval(ctx, script, []string{key}, token).Result()
	return err
}

// WatchDog 看门狗机制（自动续期）
func (dl *DistributedLock) WatchDog(ctx context.Context, key, token string, ttl time.Duration, stopChan <-chan struct{}) {
	ticker := time.NewTicker(ttl / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 续期时验证令牌
			script := `
                if redis.call("GET", KEYS[1]) == ARGV[1] then
                    return redis.call("EXPIRE", KEYS[1], ARGV[2])
                else
                    return 0
                end
            `
			result, err := dl.client.Eval(ctx, script, []string{key}, token, int(ttl/time.Second)).Result()
			if err != nil || result == nil {
				logrus.Warnf("锁续期失败: key=%s, err=%v", key, err)
				return
			}

		case <-stopChan:
			return
		case <-ctx.Done():
			return
		}
	}
}
