package service

import (
	"context"
	"errors"
	redisClient "hmdp-Go/src/config/redis"
	"hmdp-Go/src/dto"
	"hmdp-Go/src/model"
	"hmdp-Go/src/utils"
	"time"
)

type UserService struct {
}

var UserManager *UserService

func (*UserService) GetUserById(id int64) (model.User, error) {
	var userUtils model.User
	user, err := userUtils.GetUserById(id)
	return user, err
}

func (*UserService) SaveCode(phone string) error {

	if !utils.RegexUtil.IsPhoneValid(phone) {
		return errors.New("phone number is valid")
	}
	verifyCode := utils.RandomUtil.GenerateVerifyCode()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := redisClient.GetRedisClient().Set(ctx, utils.LOGIN_CODE_KEY+phone, verifyCode, time.Duration(time.Minute*utils.LOGIN_VERIFY_CODE_TTL)).Err()
	return err
}

func (*UserService) Login(loginInfo *dto.LoginFormDto) (string, error) {
	if !utils.RegexUtil.IsPhoneValid(loginInfo.Phone) {
		return "", errors.New("not a valid phone")
	}

	// if !utils.RegexUtil.IsPassWordValid(loginInfo.Password) {
	// 	return "", errors.New("not a valid password")
	// }

	// if !utils.RegexUtil.IsVerifyCodeValid(loginInfo.Code) {
	// 	return "", errors.New("not a valid verify code")
	// }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cacheCode, err := redisClient.GetRedisClient().Get(ctx, utils.LOGIN_CODE_KEY+loginInfo.Phone).Result()
	if err != nil {
		return "", err
	}

	if cacheCode != loginInfo.Code {
		return "", errors.New("a wrong verify code!")
	}

	var user model.User
	err = user.GetUserByPhone(loginInfo.Phone)
	if err != nil {
		user.Phone = loginInfo.Phone
		user.NickName = utils.USER_NICK_NAME_PREFIX + utils.RandomUtil.GenerateRandomStr(10)
		user.CreateTime = time.Now()
		user.UpdateTime = time.Now()
		err = user.SaveUser()
		if err != nil {
			return "", err
		}
	}

	var userDTO dto.UserDTO
	userDTO.Id = user.Id
	userDTO.Icon = user.Icon
	userDTO.NickName = user.NickName

	j := middleware.NewJWT()
	clamis := j.CreateClaims(userDTO)

	token, err := j.CreateToken(clamis)
	if err != nil {
		return "", errors.New("get token failed!")
	}

	return token, nil
}
