package service

import (
	"context"
	"errors"
	"github.com/jinzhu/gorm"
	redisConfig "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"hmdp-Go/src/config/mysql"
	redisClient "hmdp-Go/src/config/redis"
	"hmdp-Go/src/model"
	"hmdp-Go/src/utils"
	"io/ioutil"
	"strconv"
	"sync"
	"time"
)

type VoucherOrderService struct {
}

var VoucherOrderManager *VoucherOrderService
var useLocks sync.Map

func InitOrderHandler() {
	// run the goroutine
	go SyncHandlerStream()
	// create the group
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := redisClient.GetRedisClient().XGroupCreate(ctx, "stream.orders", "g1", "0").Result()
	if err != nil {
		logrus.Error("create group failed!")
	}
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
//		voucherOrder.PayTime = time.Now()
//		voucherOrder.UseTime = time.Now()
//		voucherOrder.RefundTime = time.Now()
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

	script, err := ioutil.ReadFile("script/voucher_script.lua")
	if err != nil {
		return err
	}

	scriptBash := redisConfig.NewScript(string(script))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result, err := scriptBash.Run(ctx, redisClient.GetRedisClient(), keys, values...).Result()
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		// 1. get the message from the stream
		msgs, err := redisClient.GetRedisClient().XReadGroup(ctx, &redisConfig.XReadGroupArgs{
			Group:    "g1",
			Consumer: "c1",
			Streams:  []string{"stream.orders", ">"},
			Count:    1,
			Block:    2 * time.Second,
		}).Result()

		if err != nil {
			handlePendingList()
			continue
		}

		if msgs == nil || len(msgs) == 0 {
			continue
		}

		logrus.Info(msgs)
		for _, stream := range msgs {
			for _, message := range stream.Messages {
				// this is a signel obj
				var order model.VoucherOrder
				for key, value := range message.Values {
					switch key {
					case "id":
						order.Id, _ = strconv.ParseInt(value.(string), 10, 64)
					case "userId":
						order.UserId, _ = strconv.ParseInt(value.(string), 10, 64)
					case "voucherId":
						order.VoucherId, _ = strconv.ParseInt(value.(string), 10, 64)
					}
				}
				// ack the message
				logrus.Error("begin to process: ", order)
				handleVoucherOrder(order)
				logrus.Info("the message is ack", message.ID)
				_, err := redisClient.GetRedisClient().XAck(ctx, "stream.orders", "g1", message.ID).Result()
				if err != nil {
					handlePendingList()
					logrus.Error("ack the message failed")
					continue
				}
			}
		}
	}
}

func handleVoucherOrder(voucherOrder model.VoucherOrder) {
	userId := voucherOrder.UserId
	lock := getUserLock(userId)
	lock.Lock()
	defer lock.Unlock()
	logrus.Info("handle: ", voucherOrder)
	err := createNewOrderNew(mysql.GetMysqlDB(), voucherOrder)
	if err != nil {
		logrus.Error("add data into mysql failed!")
		return
	}
}

func createNewOrderNew(db *gorm.DB, voucherOrder model.VoucherOrder) error {
	return db.Transaction(func(tx *gorm.DB) error {
		voucherOrder.CreateTime = time.Now()
		voucherOrder.PayTime = time.Now()
		voucherOrder.UseTime = time.Now()
		voucherOrder.RefundTime = time.Now()
		voucherOrder.UpdateTime = time.Now()

		var seckillvoucher model.SecKillVoucher
		err := seckillvoucher.DecrVoucherStock(voucherOrder.VoucherId, tx)
		if err != nil {
			logrus.Error("create", err.Error())
			return err
		}

		// 4. update the voucher order
		err = voucherOrder.CreateVoucherOrder(tx)

		if err != nil {
			return err
		}
		return nil
	})
}

func handlePendingList() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		msgs, err := redisClient.GetRedisClient().XReadGroup(ctx, &redisConfig.XReadGroupArgs{
			Group:    "g1",
			Consumer: "c1",
			Streams:  []string{"stream.orders", "0"},
			Count:    1,
			Block:    2 * time.Second,
		}).Result()

		if err != nil {
			break
		}

		// there is not msg in pending list
		if len(msgs) == 1 && (len(msgs[0].Messages) == 0) {
			break
		}

		for _, stream := range msgs {
			for _, message := range stream.Messages {
				var order model.VoucherOrder
				for key, value := range message.Values {
					switch key {
					case "id":
						order.Id, _ = strconv.ParseInt(value.(string), 10, 64)
					case "userId":
						order.UserId, _ = strconv.ParseInt(value.(string), 10, 64)
					case "voucherId":
						order.VoucherId, _ = strconv.ParseInt(value.(string), 10, 64)
					}
				}
				handleVoucherOrder(order)
				err = redisClient.GetRedisClient().XAck(ctx, "stream.orders", "g1", message.ID).Err()
				if err != nil {
					time.Sleep(20 * time.Millisecond)
					break
				}
			}
		}
	}
}
