package service

import (
	"context"
	redisConfig "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"hmdp-Go/src/config/redis"
	"hmdp-Go/src/model"
	"hmdp-Go/src/utils"
	"strconv"
	"time"
)

type BlogService struct {
}

var BlogManager *BlogService

func (*BlogService) SaveBlog(userId int64, blog *model.Blog) (res int64, err error) {
	blog.CreateTime = time.Now()
	blog.UpdateTime = time.Now()

	id, err := blog.SaveBlog()
	if err != nil {
		logrus.Error("[Blog Service] failed to insert data!")
		return
	}
	var f model.Follow
	follows, err := f.GetFollowsByFollowId(userId)
	if err != nil {
		return
	}

	if follows == nil || len(follows) == 0 {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, value := range follows {
		followUserId := value.UserId

		redisKey := utils.FEED_KEY + strconv.FormatInt(followUserId, 10)
		redis.GetRedisClient().ZAdd(ctx, redisKey, redisConfig.Z{
			Member: blog.Id,
			Score:  float64(time.Now().Unix()),
		})
	}

	res = id
	return
}

func (*BlogService) LikeBlog(id int64, userId int64) (err error) {
	// var blog model.Blog
	// blog.Id = id
	// err = blog.IncreseLike()
	// return
	userStr := strconv.FormatInt(userId, 10)
	redisKey := utils.BLOG_LIKE_KEY + strconv.FormatInt(id, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = redis.GetRedisClient().ZScore(ctx, redisKey, userStr).Result()

	flag := false

	if err != nil {
		if err == redisConfig.Nil {
			flag = true
		} else {
			return err
		}
	}

	var blog model.Blog
	blog.Id = id

	if flag {
		// add like
		blog.IncrLike()
		// add the user
		err = redis.GetRedisClient().ZAdd(ctx, redisKey,
			redisConfig.Z{
				Score:  float64(time.Now().Unix()),
				Member: userStr,
			}).Err()
	} else {
		// have the data
		blog.DecrLike()
		err = redis.GetRedisClient().ZRem(ctx, redisKey, userStr).Err()
	}
	return err
}
