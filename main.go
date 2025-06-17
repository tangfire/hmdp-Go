package main

import (
	"github.com/gin-gonic/gin"
	"hmdp-Go/src/config"
	"hmdp-Go/src/handler"
	"hmdp-Go/src/service"
)

func main() {
	r := gin.Default()
	config.Init()
	handler.ConfigRouter(r)
	service.InitOrderHandler()

	r.Run(":8081")

}
