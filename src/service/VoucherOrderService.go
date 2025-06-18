package service

import (
	"context"
	"errors"
	"github.com/jinzhu/gorm"
	"github.com/mitchellh/mapstructure"
	redisConfig "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"hmdp-Go/src/config/mysql"
	redisClient "hmdp-Go/src/config/redis"
	"hmdp-Go/src/model"
	"hmdp-Go/src/utils"
	"io/ioutil"
	"strconv"
	"strings"
	"sync"
	"time"
)

type VoucherOrderService struct {
}

var VoucherOrderManager *VoucherOrderService
var useLocks sync.Map
var voucherScript *redisConfig.Script

func init() {
	script, _ := ioutil.ReadFile("script/voucher_script.lua")

	voucherScript = redisConfig.NewScript(string(script))
}

func InitOrderHandler() {
	// 创建消费者组（忽略已存在的错误）
	ctx := context.Background()
	_, err := redisClient.GetRedisClient().XGroupCreateMkStream(ctx, "stream.orders", "g1", "0").Result()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		logrus.Errorf("创建消费者组失败: %v", err)
	}

	// 启动处理器
	go SyncHandlerStream()
	go handlePendingList()
}

//	func (vo *VoucherOrderService) AddSeckillVoucher(id int64, userId int64) error {
//		// 1. get the seckillvoucher
//		var seckillVoucher model.SecKillVoucher
//		err := seckillVoucher.QuerySeckillVoucherById(id)
//
//		if err != nil {
//			return errors.New("query database for seckillvoucher failed!")
//		}
//
//		if seckillVoucher.BeginTime.After(time.Now()) {
//			return errors.New("the seckill voucher begin time is after")
//		}
//
//		if seckillVoucher.EndTime.Before(time.Now()) {
//			return errors.New("the seckill voucher end time is before")
//		}
//
//		if seckillVoucher.Stock < 1 {
//			return errors.New("the stock is not enough")
//		}
//
//		// 2. create the new voucher
//		lock := getUserLock(userId)
//		// useLock.Lock()
//		// defer useLock.Unlock()
//		lock.Lock()
//		defer lock.Unlock()
//		err = createNewVoucherOrder(mysql.GetMysqlDB(), userId, id)
//		return err
//	}

//func createNewVoucherOrder(db *gorm.DB, userId int64, voucherId int64) error {
//	return db.Transaction(func(tx *gorm.DB) error {
//		// 1. query the voucher using the userId
//		var seckillVoucher model.SecKillVoucher
//		var vo model.VoucherOrder
//		// 在事务中使用
//		hasPurchased, err := vo.HasPurchasedVoucher(userId, voucherId)
//		if err != nil {
//			return err
//		}
//		if hasPurchased {
//			return errors.New("the user get has gotten a order")
//		}
//		// 2. create the seckillvoucher and add
//		var voucherOrder model.VoucherOrder
//		voucherOrder.CreateTime = time.Now()
//		voucherOrder.Id, err = utils.RedisWork.NextId("order")
//		if err != nil {
//			return err
//		}
//		voucherOrder.UserId = userId
//		voucherOrder.VoucherId = voucherId
//		voucherOrder.CreateTime = time.Now()
//		voucherOrder.UpdateTime = time.Now()
//
//		// 3. decr the stock
//		err = seckillVoucher.DecrVoucherStock(voucherId, tx)
//		if err != nil {
//			return err
//		}
//
//		// 4. update the voucher order
//		err = voucherOrder.CreateVoucherOrder(tx)
//
//		if err != nil {
//			return err
//		}
//
//		return nil
//	})
//}

func getUserLock(userId int64) *sync.Mutex {
	lock, _ := useLocks.LoadOrStore(userId, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (vo *VoucherOrderService) SeckillVoucher(voucherId int64, userId int64) error {
	orderId, err := utils.RedisWork.NextId("order")
	if err != nil {
		return err
	}

	keys := []string{}
	var values []interface{}
	values = append(values, strconv.FormatInt(voucherId, 10))
	values = append(values, strconv.FormatInt(userId, 10))
	values = append(values, strconv.FormatInt(orderId, 10))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result, err := voucherScript.Run(ctx, redisClient.GetRedisClient(), keys, values...).Result()
	if err != nil {
		return err
	}

	r := result.(int64)
	if r != 0 {
		return errors.New("the condition is not meet")
	}
	// success add task into queue
	return nil
}

// the goroutine to handle the message queue
func SyncHandlerStream() {
	ctx := context.Background()
	for {
		msgs, err := redisClient.GetRedisClient().XReadGroup(ctx, &redisConfig.XReadGroupArgs{
			Group:    "g1",
			Consumer: "c1",
			Streams:  []string{"stream.orders", ">"},
			Count:    100,
			Block:    200 * time.Millisecond,
		}).Result()

		if err != nil {
			if err == redisConfig.Nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			logrus.Errorf("XReadGroup error: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		if len(msgs) == 0 || len(msgs[0].Messages) == 0 {
			continue
		}

		// 修正：移除重复ACK
		for _, msg := range msgs[0].Messages {
			if err := processVoucherMessage(msg); err != nil {
				handleFailedMessage(msg, err)
			} else {
				if _, err := redisClient.GetRedisClient().XAck(ctx, "stream.orders", "g1", msg.ID).Result(); err != nil {
					logrus.Warnf("SyncHandler ACK失败: %v", err)
				}
			}
		}
	}
}

func handlePendingList() {
	ctx := context.Background()
	for {
		msgs, err := redisClient.GetRedisClient().XReadGroup(ctx, &redisConfig.XReadGroupArgs{
			Group:    "g1",
			Consumer: "c1",
			Streams:  []string{"stream.orders", "0"},
			Count:    50,
			Block:    5 * time.Second,
		}).Result()

		if err != nil {
			if err == redisConfig.Nil {
				time.Sleep(1 * time.Second)
				continue
			}
			logrus.Errorf("PendingList XReadGroup error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if len(msgs) == 0 || len(msgs[0].Messages) == 0 {
			time.Sleep(1 * time.Second)
			continue
		}

		// 修正：复用相同的处理逻辑
		for _, msg := range msgs[0].Messages {
			if err := processVoucherMessage(msg); err != nil {
				handleFailedMessage(msg, err)
			} else {
				if _, err := redisClient.GetRedisClient().XAck(ctx, "stream.orders", "g1", msg.ID).Result(); err != nil {
					logrus.Warnf("PendingList ACK失败: %v", err)
				}
			}
		}
	}
}

// 修正：优化死信队列处理
func handleFailedMessage(msg redisConfig.XMessage, err error) {
	logrus.Warnf("消息处理失败(ID:%s): %v", msg.ID, err)

	// 只有在达到最大重试次数时才转移到死信队列
	if strings.Contains(err.Error(), "超过最大重试次数") {
		ctx := context.Background()

		// 尝试添加到死信队列
		_, dlerr := redisClient.GetRedisClient().XAdd(ctx, &redisConfig.XAddArgs{
			Stream: "stream.orders.dead",
			Values: map[string]interface{}{
				"original_id": msg.ID,
				"values":      msg.Values,
				"error":       err.Error(),
				"time":        time.Now().Format(time.RFC3339),
			},
		}).Result()

		if dlerr != nil {
			logrus.Errorf("死信队列添加失败: %v", dlerr)
		} else {
			// 成功转移到死信队列后才ACK原消息
			if _, ackErr := redisClient.GetRedisClient().XAck(ctx, "stream.orders", "g1", msg.ID).Result(); ackErr != nil {
				logrus.Warnf("死信消息ACK失败: %v", ackErr)
			} else {
				logrus.Infof("已转移消息到死信队列: %s", msg.ID)
			}
		}
	}
}

// 新增：统一消息处理函数
func processVoucherMessage(msg redisConfig.XMessage) error {
	var order model.VoucherOrder
	if err := mapstructure.Decode(msg.Values, &order); err != nil {
		logrus.Warnf("消息解析失败: %v", err)
		return err
	}
	return processOrderWithRetry(order, 3)
}

// 带重试的订单处理
func processOrderWithRetry(order model.VoucherOrder, maxRetries int) error {
	for i := 0; i < maxRetries; i++ {
		err := handleVoucherOrder(order)
		if err == nil {
			return nil
		}

		// 只有乐观锁冲突才重试
		if !isOptimisticLockError(err) {
			return err
		}

		time.Sleep(time.Duration(i*50) * time.Millisecond) // 指数退避
	}
	return errors.New("超过最大重试次数")
}

// 判断是否为乐观锁冲突
func isOptimisticLockError(err error) bool {
	return strings.Contains(err.Error(), "库存不足") ||
		strings.Contains(err.Error(), "RowsAffected == 0")
}

func handleVoucherOrder(order model.VoucherOrder) error {
	// 最大重试次数（根据业务调整）
	const maxRetries = 2

	for i := 0; i < maxRetries; i++ {
		err := mysql.GetMysqlDB().Transaction(func(tx *gorm.DB) error {
			// 1. 检查是否已购买（防重）
			var count int64
			if err := tx.Model(&model.VoucherOrder{}).
				Where("user_id = ? AND voucher_id = ?", order.UserId, order.VoucherId).
				Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return errors.New("请勿重复购买")
			}

			// 2. 乐观锁扣减库存（无版本号）
			var sv model.SecKillVoucher
			if err := sv.DecrVoucherStock(order.VoucherId, tx); err != nil {
				return err
			}

			// 3. 创建订单
			order.CreateTime = time.Now()
			order.UpdateTime = time.Now()
			return order.CreateVoucherOrder(tx)
		})

		if err == nil {
			logrus.Infof("订单创建成功: 用户%d 优惠券%d", order.UserId, order.VoucherId)
			return nil
		}

		// 只有库存冲突才重试 添加详细的错误日志
		if strings.Contains(err.Error(), "库存不足") {
			logrus.Warnf("库存冲突重试(%d/%d): %v", i+1, maxRetries, err)
		} else {
			logrus.Errorf("订单处理失败: %v", err)
			return err
		}

		time.Sleep(time.Duration(i*10) * time.Millisecond) // 简单退避
	}
	return errors.New("手速太快了，请稍后再试")
}
