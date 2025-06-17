package service

import (
	"context"
	"hmdp-Go/src/config/redis"
	"hmdp-Go/src/dto"
	"hmdp-Go/src/model"
	"hmdp-Go/src/utils"
	"strconv"
	"time"
)

type FollowService struct {
}

var FollowManager *FollowService

func (*FollowService) Follow(id int64, userId int64, isFollow bool) error {

	if isFollow {
		// remove the record
		var f model.Follow
		err := f.RemoveUserFollow(id, userId)
		if err != nil {
			return err
		}

	} else {
		// add the record
		var f model.Follow
		f.UserId = userId
		f.FollowUserId = id
		f.CreateTime = time.Now()
		err := f.SaveUserFollow()
		if err != nil {
			return err
		}
	}

	return nil
}

func (*FollowService) FollowCommons(id int64, userId int64) ([]dto.UserDTO, error) {
	redisKeySelf := utils.FOLLOW_USER_KEY + strconv.FormatInt(userId, 10)
	redisKeyTarget := utils.FOLLOW_USER_KEY + strconv.FormatInt(id, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idStrs, err := redis.GetRedisClient().SInter(ctx, redisKeySelf, redisKeyTarget).Result()
	if err != nil {
		return []dto.UserDTO{}, err
	}

	if idStrs == nil || len(idStrs) == 0 {
		return []dto.UserDTO{}, nil
	}

	var ids []int64
	for _, value := range idStrs {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return []dto.UserDTO{}, nil
		}
		ids = append(ids, id)
	}

	var userUtils model.User
	users, err := userUtils.GetUsersByIds(ids)
	if err != nil {
		return []dto.UserDTO{}, nil
	}

	userDTOs := make([]dto.UserDTO, len(users))
	for i := range users {
		userDTOs[i].Id = users[i].Id
		userDTOs[i].Icon = users[i].Icon
		userDTOs[i].NickName = users[i].NickName
	}
	return userDTOs, nil
}

func (*FollowService) IsFollow(id int64, userId int64) (bool, error) {
	var f model.Follow
	f.UserId = userId
	f.FollowUserId = id
	count, err := f.IsFollowing()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
