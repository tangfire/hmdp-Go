package service

import (
	"context"
	"fmt"
	"github.com/sirupsen/logrus"
	"hmdp-Go/src/config/mysql"
	"hmdp-Go/src/config/redis"
	"hmdp-Go/src/model"
	"hmdp-Go/src/utils"
	"strconv"
	"time"
)

type VoucherService struct {
}

var VoucherManager *VoucherService

func (*VoucherService) AddVoucher(voucher *model.Voucher) error {
	err := voucher.AddVoucher(mysql.GetMysqlDB())
	return err
}

func (*VoucherService) QueryVoucherOfShop(shopId int64) ([]model.Voucher, error) {
	var vocherUtils model.Voucher
	return vocherUtils.QueryVoucherByShop(shopId)
}

func (vs *VoucherService) AddSeckillVoucher(voucher *model.Voucher) error {
	// 1. 开启数据库事务
	tx := mysql.GetMysqlDB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 2. 操作1：写入主表（需修改AddVoucher方法接收tx参数）
	if err := voucher.AddVoucher(tx); err != nil {
		tx.Rollback()
		return fmt.Errorf("写入主表失败: %w", err)
	}

	// 3. 操作2：写入秒杀表（需修改AddSeckillVoucher方法接收tx参数）
	seckillVoucher := model.SecKillVoucher{
		VoucherId:  voucher.Id,
		Stock:      voucher.Stock,
		BeginTime:  voucher.BeginTime,
		EndTime:    voucher.EndTime,
		CreateTime: voucher.CreateTime,
		UpdateTime: voucher.UpdateTime,
	}
	if err := seckillVoucher.AddSeckillVoucher(tx); err != nil {
		tx.Rollback()
		return fmt.Errorf("写入秒杀表失败: %w", err)
	}

	// 4. 提交数据库事务（确保前两步完全成功）
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("事务提交失败: %w", err)
	}

	// 5. 事务成功后，异步更新Redis
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		redisKey := utils.SECKILL_STOCK_KEY + strconv.FormatInt(voucher.Id, 10)
		if err := redis.GetRedisClient().Set(ctx, redisKey, voucher.Stock, 24*time.Hour).Err(); err != nil {
			// Redis更新失败时，可通过以下方式补偿：
			// a. 记录日志并报警
			logrus.Errorf("Redis缓存更新失败: key=%s, error=%v", redisKey, err)
			// b. 启动重试机制（示例）
			retryUpdateRedis(redisKey, voucher.Stock)
		}
	}()

	return nil
}

// 辅助函数：Redis更新重试
func retryUpdateRedis(key string, stock int) {
	for i := 0; i < 3; i++ {
		time.Sleep(time.Duration(i+1) * time.Second) // 退避策略
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := redis.GetRedisClient().Set(ctx, key, stock, 24*time.Hour).Err()
		cancel()
		if err == nil {
			return
		}
		logrus.Warnf("Redis重试%d次失败: %v", i+1, err)
	}
}
