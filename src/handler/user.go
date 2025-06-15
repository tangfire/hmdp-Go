package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"hmdp-Go/src/dto"
	"hmdp-Go/src/service"
	"net/http"
)

type UserHandler struct {
}

var userHandler *UserHandler

// @Description: send the phone code
// @Router: /user/code [POST]
func (*UserHandler) SendCode(c *gin.Context) {
	phoneStr := c.Query("phone")
	if phoneStr == "" {
		logrus.Warn("phone is empty")
		c.JSON(http.StatusOK, dto.Fail[string]("phone is empty"))
		return
	}
	err := service.UserManager.SaveCode(phoneStr)
	if err != nil {
		logrus.Warn("phone is not valid")
		c.JSON(http.StatusOK, dto.Fail[string]("phone is not valid"))
		return
	}
	c.JSON(http.StatusOK, dto.Ok[string]())
}

// @Description: user login in
// @Router: /user/login  [POST]
func (*UserHandler) Login(c *gin.Context) {
	// TODO 实现登录功能
	var loginInfo dto.LoginFormDto
	err := c.ShouldBindJSON(&loginInfo)
	if err != nil {
		logrus.Error(err.Error())
		c.JSON(http.StatusOK, dto.Fail[string]("bind json failed!"))
		return
	}
	token, err := service.UserManager.Login(&loginInfo)
	if err != nil {
		logrus.Error(err.Error())
		c.JSON(http.StatusOK, dto.Fail[string]("get token failed!"))
		return
	}
	c.JSON(http.StatusOK, dto.OkWithData(token))
}
