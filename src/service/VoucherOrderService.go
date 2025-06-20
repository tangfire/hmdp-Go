package service

import (
	"context"
	"errors"
	"fmt"
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
	"time"
)

type VoucherOrderService struct {
}

var VoucherOrderManager *VoucherOrderService
var voucherScript *redisConfig.Script

// 最大重试次数配置
const (
	maxRetries = 3
	retryTTL   = 24 * time.Hour
)

func init() {
	script, _ := ioutil.ReadFile("script/voucher_script.lua")
	voucherScript = redisConfig.NewScript(string(script))
}

func InitOrderHandler() {
	// 创建消费者组
	ctx := context.Background()
	_, err := redisClient.GetRedisClient().XGroupCreateMkStream(ctx, "stream.orders", "g1", "0").Result()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		logrus.Errorf("创建消费者组失败: %v", err)
	}

	// 启动处理器
	go SyncHandlerStream()
	go handlePendingList()
}

func (vo *VoucherOrderService) SeckillVoucher(voucherId int64, userId int64) error {

	voucher, err := SecKillManager.QuerySeckillVoucherById(voucherId)
	if err != nil {
		return err
	}
	now := time.Now()
	if now.Before(voucher.BeginTime) {
		return errors.New("秒杀尚未开始")
	}
	if now.After(voucher.EndTime) {
		return errors.New("秒杀已结束")
	}
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
	return nil
}

// SyncHandlerStream 处理消息队列的goroutine
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
			if errors.Is(err, redisConfig.Nil) {
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

		for _, msg := range msgs[0].Messages {
			if err := processVoucherMessage(msg); err != nil {
				logrus.Warnf("消息处理失败(ID:%s)，进入Pending List: %v", msg.ID, err)
				// 不ACK，也不调用handleFailedMessage！
				// 消息会自动进入Pending List等待重试
			} else {
				if _, err := redisClient.GetRedisClient().XAck(ctx, "stream.orders", "g1", msg.ID).Result(); err != nil {
					logrus.Warnf("SyncHandler ACK失败: %v", err)
				}
			}
		}
	}
}

// 处理pending list中的消息（含重试逻辑）
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
			if errors.Is(err, redisConfig.Nil) {
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

		for _, msg := range msgs[0].Messages {
			// 获取当前重试次数
			retryCount := getRetryCount(ctx, msg.ID)

			if retryCount < maxRetries {
				// 尝试处理消息
				if err := processVoucherMessage(msg); err != nil {
					// 增加重试次数
					incrRetryCount(ctx, msg.ID, retryCount)
					logrus.Warnf("Pending重试失败(ID:%s 重试%d次): %v",
						msg.ID, retryCount+1, err)
				} else {
					// 处理成功：ACK并清除重试计数
					if _, ackErr := redisClient.GetRedisClient().XAck(ctx, "stream.orders", "g1", msg.ID).Result(); ackErr != nil {
						logrus.Warnf("PendingList ACK失败: %v", ackErr)
					}
					clearRetryCount(ctx, msg.ID)
				}
			} else {
				// 达到最大重试次数：转入死信队列
				handleFailedMessage(msg, fmt.Errorf("达到最大重试次数%d", maxRetries))
				// ACK后从Pending List移除
				if _, ackErr := redisClient.GetRedisClient().XAck(ctx, "stream.orders", "g1", msg.ID).Result(); ackErr != nil {
					logrus.Warnf("死信消息ACK失败: %v", ackErr)
				}
				clearRetryCount(ctx, msg.ID)
			}
		}
	}
}

// 获取消息重试次数
func getRetryCount(ctx context.Context, msgID string) int {
	key := fmt.Sprintf("retry:stream.orders:%s", msgID)
	countStr, err := redisClient.GetRedisClient().Get(ctx, key).Result()
	if err != nil {
		if !errors.Is(err, redisConfig.Nil) {
			logrus.Warnf("获取重试次数失败(%s): %v", key, err)
		}
		return 0
	}
	count, _ := strconv.Atoi(countStr)
	return count
}

// 增加重试次数
func incrRetryCount(ctx context.Context, msgID string, currentCount int) {
	key := fmt.Sprintf("retry:stream.orders:%s", msgID)
	newCount := currentCount + 1
	if err := redisClient.GetRedisClient().Set(ctx, key, newCount, retryTTL).Err(); err != nil {
		logrus.Errorf("设置重试次数失败(%s): %v", key, err)
	}
}

// 清除重试计数
func clearRetryCount(ctx context.Context, msgID string) {
	key := fmt.Sprintf("retry:stream.orders:%s", msgID)
	if err := redisClient.GetRedisClient().Del(ctx, key).Err(); err != nil {
		logrus.Warnf("清除重试计数失败(%s): %v", key, err)
	}
}

// 处理失败消息
// 死信队列中的消息通常需要人工干预或专门的修复程序处理。
func handleFailedMessage(msg redisConfig.XMessage, err error) {
	logrus.Warnf("消息处理失败(ID:%s): %v", msg.ID, err)

	ctx := context.Background()
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
	}
}

// 处理优惠券消息
func processVoucherMessage(msg redisConfig.XMessage) error {
	var order model.VoucherOrder
	if err := mapstructure.Decode(msg.Values, &order); err != nil {
		logrus.Warnf("消息解析失败: %v", err)
		return err
	}
	return createVoucherOrder(order)
}

// 创建优惠券订单
func createVoucherOrder(order model.VoucherOrder) error {

	lockKey := fmt.Sprintf("%s%d", utils.DISTRIBUTED_LOCK_KEY, order.VoucherId)
	lock := utils.NewDistributedLock(redisClient.GetRedisClient())

	// 获取分布式锁
	// 修改后的锁获取
	// 使用带超时的上下文 ✅
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel() // 确保释放资源
	acquired, token, err := lock.Lock(ctx, lockKey, 10*time.Second)
	if err != nil || !acquired {
		return errors.New("系统繁忙，请重试")
	}
	defer lock.Unlock(ctx, lockKey, token) // 传入令牌

	// 启动看门狗
	stopChan := make(chan struct{})
	defer close(stopChan)
	go lock.WatchDog(ctx, lockKey, token, 10*time.Second, stopChan)

	return mysql.GetMysqlDB().Transaction(func(tx *gorm.DB) error {
		// 1. 检查是否已购买
		//查询订单（历史订单检查）
		purchasedFlag, err := new(model.VoucherOrder).HasPurchasedVoucher(order.UserId, order.VoucherId, tx)
		if err != nil {
			return err
		}
		if purchasedFlag {
			return model.ErrDuplicateOrder
		}

		// 2. 扣减库存
		var sv model.SecKillVoucher
		if err := sv.DecrVoucherStock(order.VoucherId, tx); err != nil {
			return err
		}

		// 3. 创建订单
		order.CreateTime = time.Now()
		order.UpdateTime = time.Now()
		return order.CreateVoucherOrder(tx)
	})
}
