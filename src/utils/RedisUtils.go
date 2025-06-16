package utils

import (
	"context"
	redisClient "hmdp-Go/src/config/redis"
	"time"
)

type RedisUtils struct {
}

var RedisUtil *RedisUtils

func (*RedisUtils) TryLock(redisKey string) bool {
	// return true if not exists and set , result false if exists
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, _ := redisClient.GetRedisClient().SetNX(ctx, redisKey, REDIS_LOCK_VALUE, 10*time.Second).Result()
	return res
}

func (*RedisUtils) ClearLock(redisKey string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := redisClient.GetRedisClient().Del(ctx, redisKey).Err()
	return err
}
