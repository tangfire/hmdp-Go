package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

func ConfigRouter(r *gin.Engine) {
	r.GET("/ping", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, "pong")
	})

	userControllerWithOutMid := r.Group("/user")

	{
		userControllerWithOutMid.POST("/code", userHandler.SendCode)
		userControllerWithOutMid.POST("/login", userHandler.Login)
	}
}
