package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"hmdp-Go/src/dto"
	"hmdp-Go/src/middleware"
	"hmdp-Go/src/model"
	"hmdp-Go/src/service"
	"net/http"
	"strconv"
)

type BlogHandler struct {
}

var blogHandler *BlogHandler

// @Description: save the blog
// @Router:  /blog [POST]
func (*BlogHandler) SaveBlog(c *gin.Context) {
	var blog model.Blog
	err := c.ShouldBindJSON(&blog)
	if err != nil {
		logrus.Error("[Blog handler] bind json failed!")
		c.JSON(http.StatusOK, dto.Fail[string]("insert failed!"))
		return
	}

	user, err := middleware.GetUserInfo(c)
	if err != nil {
		logrus.Error(err.Error())
		c.JSON(http.StatusOK, dto.Fail[string]("failed to get user id"))
		return
	}
	userId := user.Id

	id, err := service.BlogManager.SaveBlog(userId, &blog)
	if err != nil {
		logrus.Error("[Blog handler] insert data into database failed!")
		c.JSON(http.StatusOK, dto.Fail[string]("insert failed!"))
		return
	}
	c.JSON(http.StatusOK, dto.OkWithData(id))
}

// @Description: modify the number of linked
// @Router:  /blog/like/:id  [PUT]
func (*BlogHandler) LikeBlog(c *gin.Context) {
	idStr := c.Param("id")
	if idStr == "" {
		logrus.Error("[Blog Handler] Give a empty string")
		c.JSON(http.StatusOK, dto.Fail[string]("like blog failed!"))
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		logrus.Error(err.Error())
		c.JSON(http.StatusOK, dto.Fail[string]("type transform failed!"))
		return
	}

	user, err := middleware.GetUserInfo(c)
	if err != nil {
		logrus.Error(err.Error())
		c.JSON(http.StatusOK, dto.Fail[string]("get user info failed!"))
		return
	}

	userId := user.Id

	err = service.BlogManager.LikeBlog(id, userId)

	if err != nil {
		logrus.Error(err.Error())
		c.JSON(http.StatusOK, dto.Fail[string]("like failed!"))
		return
	}
	c.JSON(http.StatusOK, dto.Ok[string]())
}
