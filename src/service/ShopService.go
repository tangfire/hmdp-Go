package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jinzhu/gorm"
	redisConfig "github.com/redis/go-redis/v9"
	"hmdp-Go/src/config/mysql"
	redisClient "hmdp-Go/src/config/redis"
	"hmdp-Go/src/model"
	"hmdp-Go/src/utils"
	"strconv"
	"time"
)

type ShopService struct {
}

var ShopManager *ShopService

const (
	MAX_REDIS_DATA_QUEUE = 10
)

var redisDataQueue chan int64

func init() {
	redisDataQueue = make(chan int64, MAX_REDIS_DATA_QUEUE)
	go ShopManager.SyncUpdateCache()
}

func (*ShopService) QueryShopById(id int64) (model.Shop, error) {
	var shop model.Shop
	shop.Id = id
	err := shop.QueryShopById(id)
	return shop, err
}

func (*ShopService) SaveShop(shop *model.Shop) error {
	err := shop.SaveShop()
	return err
}

func (*ShopService) UpdateShop(shop *model.Shop) error {
	err := shop.UpdateShop(mysql.GetMysqlDB())
	return err
}

func (*ShopService) QueryByType(typeId int, current int) ([]model.Shop, error) {
	var shopUtils model.Shop
	shops, err := shopUtils.QueryShopByType(typeId, current)
	return shops, err
}

func (*ShopService) QueryByName(name string, current int) ([]model.Shop, error) {
	var shopUtils model.Shop
	shops, err := shopUtils.QueryShopByName(name, current)
	return shops, err
}

func (*ShopService) QueryShopByIdWithCache(id int64) (model.Shop, error) {
	redisKey := utils.CACHE_SHOP_KEY + strconv.FormatInt(id, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shopInfo, err := redisClient.GetRedisClient().Get(ctx, redisKey).Result()
	if err == nil {
		var shop model.Shop
		err = json.Unmarshal([]byte(shopInfo), &shop)
		if err != nil {
			return model.Shop{}, err
		}
		return shop, nil
	}

	if err == redisConfig.Nil {
		var shop model.Shop
		shop.Id = id
		err = shop.QueryShopById(id)
		if err != nil {
			return model.Shop{}, err
		}

		redisValue, err := json.Marshal(shop)
		if err != nil {
			return model.Shop{}, err
		}

		// 超时剔除策略
		err = redisClient.GetRedisClient().Set(ctx, redisKey, string(redisValue), time.Duration(time.Minute)).Err()

		if err != nil {
			return model.Shop{}, err
		}
		return shop, nil
	}

	return model.Shop{}, err
}

// 缓存更新的最佳实践方法
func (*ShopService) UpdateShopWithCacheCallBack(db *gorm.DB, shop *model.Shop) error {
	return db.Transaction(func(tx *gorm.DB) error {
		err := shop.QueryShopById(shop.Id)
		if err != nil {
			return err
		}

		// update the database
		err = shop.UpdateShop(tx)
		if err != nil {
			return err
		}

		// delete the cache
		redisKey := utils.CACHE_SHOP_KEY + strconv.FormatInt(shop.Id, 10)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = redisClient.GetRedisClient().Del(ctx, redisKey).Err()

		if err != nil {
			return err
		}

		return nil
	})
}

func (*ShopService) UpdateShopWithCache(shop *model.Shop) error {
	return ShopManager.UpdateShopWithCacheCallBack(mysql.GetMysqlDB(), shop)
}

// 缓存穿透的解决方法: 缓存空对象
func (*ShopService) QueryShopByIdWithCacheNull(id int64) (model.Shop, error) {
	redisKey := utils.CACHE_SHOP_KEY + strconv.FormatInt(id, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shopInfoStr, err := redisClient.GetRedisClient().Get(ctx, redisKey).Result()

	if err == nil {
		var shopInfo model.Shop
		if shopInfoStr == "" {
			return model.Shop{}, nil
		}
		err = json.Unmarshal([]byte(shopInfoStr), &shopInfo)
		if err != nil {
			return model.Shop{}, err
		}
		return shopInfo, nil
	}

	if err == redisConfig.Nil {
		var shopInfo model.Shop
		shopInfo.Id = id
		err = shopInfo.QueryShopById(id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = redisClient.GetRedisClient().Set(ctx, redisKey, "", time.Duration(time.Minute)).Err()
			if err != nil {
				return model.Shop{}, err
			}
			return model.Shop{}, nil
		}

		redisValue, err := json.Marshal(shopInfo)
		if err != nil {
			return model.Shop{}, err
		}
		err = redisClient.GetRedisClient().Set(ctx, redisKey, string(redisValue), time.Duration(time.Minute)).Err()
		if err != nil {
			return model.Shop{}, err
		}
		return shopInfo, nil
	}
	return model.Shop{}, nil
}

// 利用互斥锁解决热点 Key 问题(也就是缓存击穿问题)
func (*ShopService) QueryShopByIdPassThrough(id int64) (model.Shop, error) {
	redisKey := utils.CACHE_SHOP_KEY + strconv.FormatInt(id, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shopInfoStr, err := redisClient.GetRedisClient().Get(ctx, redisKey).Result()

	if err == nil {
		if shopInfoStr == "" {
			return model.Shop{}, err
		}

		var shopInfo model.Shop
		err = json.Unmarshal([]byte(shopInfoStr), &shopInfo)
		if err != nil {
			return model.Shop{}, err
		}
		return shopInfo, err
	}

	if err == redisConfig.Nil {
		lockKey := utils.CACHE_LOCK_KEY + strconv.FormatInt(id, 10)
		flag := utils.RedisUtil.TryLock(lockKey)
		// 没有获取到锁
		if !flag {
			time.Sleep(time.Millisecond * 50)
			return ShopManager.QueryShopByIdPassThrough(id)
		}

		// 重新建立缓存
		defer utils.RedisUtil.ClearLock(lockKey)
		var shopInfo model.Shop
		err = shopInfo.QueryShopById(id)

		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = redisClient.GetRedisClient().Set(ctx, redisKey, "", time.Minute).Err()
			return model.Shop{}, err
		}

		if err != nil {
			return model.Shop{}, err
		}

		redisValue, err := json.Marshal(shopInfo)
		if err != nil {
			return model.Shop{}, err
		}

		err = redisClient.GetRedisClient().Set(ctx, redisKey, string(redisValue), time.Minute).Err()
		if err != nil {
			return model.Shop{}, err
		}
		return shopInfo, nil
	}
	return model.Shop{}, err
}

// @Description: use the logic expire to deal with the cache pass through
func (*ShopService) QueryShopByIdWithLogicExpire(id int64) (model.Shop, error) {
	redisKey := utils.CACHE_SHOP_KEY + strconv.FormatInt(id, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	redisDataStr, err := redisClient.GetRedisClient().Get(ctx, redisKey).Result()

	// hot key is in redis
	if err == redisConfig.Nil {
		return model.Shop{}, nil
	}

	if err == nil {
		if redisDataStr == "" {
			return model.Shop{}, nil
		}

		var redisData utils.RedisData[model.Shop]
		err = json.Unmarshal([]byte(redisDataStr), &redisData)
		if err != nil {
			return model.Shop{}, err
		}

		if redisData.ExpireTime.After(time.Now()) {
			return redisData.Data, nil
		}
		// 否则过期,需要重新建立缓存

		lockKey := utils.CACHE_LOCK_KEY + strconv.FormatInt(id, 10)
		flag := utils.RedisUtil.TryLock(lockKey)

		// if not get the lock
		if !flag {
			return redisData.Data, nil
		}

		// if get the lock
		defer utils.RedisUtil.ClearLock(lockKey)
		redisDataQueue <- id
		// go func() {
		// 	var shopInfo model.Shop
		// 	err = shopInfo.QueryShopById(id)
		// 	if err != nil {
		// 		return
		// 	}
		// 	var redisDataToSave utils.RedisData[model.Shop]
		//
		// 	redisDataToSave.Data = shopInfo
		// 	// the time of hot key exists
		// 	redisDataToSave.ExpireTime = time.Now().Add(time.Second * utils.HOT_KEY_EXISTS_TIME)
		//
		// 	redisValue,err := json.Marshal(redisDataToSave)
		// 	err = redisClient.GetRedisClient().Set(ctx , redisKey , string(redisValue) , 0).Err()
		// 	if err != nil {
		// 		return
		// 	}
		// 	return
		// }()
		//
		return redisData.Data, nil
	}

	return model.Shop{}, err
}

func (*ShopService) SyncUpdateCache() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for {
		id := <-redisDataQueue

		redisKey := utils.CACHE_SHOP_KEY + strconv.FormatInt(id, 10)

		var shopInfo model.Shop
		err := shopInfo.QueryShopById(id)

		if err != nil {
			continue
		}

		var redisDataToSave utils.RedisData[model.Shop]

		redisDataToSave.Data = shopInfo
		// the time of hot key exists
		redisDataToSave.ExpireTime = time.Now().Add(time.Second * utils.HOT_KEY_EXISTS_TIME)

		redisValue, err := json.Marshal(redisDataToSave)
		err = redisClient.GetRedisClient().Set(ctx, redisKey, string(redisValue), 0).Err()
		if err != nil {
			continue
		}
	}
}
