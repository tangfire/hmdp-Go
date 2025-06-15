package config

import (
	"hmdp-Go/src/config/mysql"
	"hmdp-Go/src/config/redis"
)

func Init() {
	mysql.Init()
	redis.Init()
}
