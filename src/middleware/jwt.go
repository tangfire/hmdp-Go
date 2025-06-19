package middleware

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
	"hmdp-Go/src/dto"
	"net/http"
	"time"
)

var control = &singleflight.Group{}

type CustomClaims struct {
	dto.UserDTO
	BufferTime int64
	jwt.RegisteredClaims
}

type JWT struct {
	SigningKey []byte
}

const (
	JWT_SECRET_KEY = "hmdp key"
	JWT_ISSUER     = "loser"
	JWT_TOKEN_KEY  = "authorization"
)

var (
	TokenExpired     = errors.New("Token is expired")
	TokenNotValidYet = errors.New("Token is active yet")
	TokenMalformed   = errors.New("Not a token")
	TokenInvalid     = errors.New("Could not handle this token")
)

func NewJWT() *JWT {
	return &JWT{
		[]byte(JWT_SECRET_KEY),
	}
}

func (j *JWT) CreateClaims(userInfo dto.UserDTO) CustomClaims {
	now := time.Now()
	return CustomClaims{
		UserDTO:    userInfo,
		BufferTime: 86400, // buffer time 1 day
		RegisteredClaims: jwt.RegisteredClaims{
			NotBefore: jwt.NewNumericDate(now.Add(-10 * time.Minute)),  // 生效时间（留有余量）
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)), // 7天有效期
			Issuer:    JWT_ISSUER,
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
}

func (j *JWT) CreateToken(claims CustomClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.SigningKey)
}

func (j *JWT) CreateTokenByOldToken(oldToken string, claims CustomClaims) (string, error) {
	v, err, _ := control.Do("JWT"+oldToken, func() (interface{}, error) {
		return j.CreateToken(claims)
	})
	return v.(string), err
}

func (j *JWT) ParseToken(tokenStr string) (*CustomClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &CustomClaims{}, func(token *jwt.Token) (interface{}, error) {
		return j.SigningKey, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenMalformed) {
			return nil, TokenMalformed
		} else if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, TokenExpired
		} else if errors.Is(err, jwt.ErrTokenNotValidYet) {
			return nil, TokenNotValidYet
		}
		return nil, TokenInvalid
	}

	if claims, ok := token.Claims.(*CustomClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, TokenInvalid
}

// RefreshTokenWithControl 统一刷新方法（带并发控制）
func (j *JWT) RefreshTokenWithControl(oldToken string, userDTO dto.UserDTO) (string, error) {
	newClaims := j.CreateClaims(userDTO)
	return j.CreateTokenByOldToken(oldToken, newClaims)
}

// GlobalTokenMiddleware 全局中间件：处理所有请求的Token解析和刷新
func GlobalTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Request.Header.Get(JWT_TOKEN_KEY)
		if token == "" {
			c.Next()
			return
		}

		j := NewJWT()
		claims, err := j.ParseToken(token)

		// 关键修复：只有有效的claims才应被设置到上下文
		validClaims := false

		// 统一刷新处理函数
		refreshToken := func() {
			if claims == nil {
				logrus.Warn("Refresh attempted with nil claims")
				return
			}

			newToken, refreshErr := j.RefreshTokenWithControl(token, claims.UserDTO)
			if refreshErr == nil {
				c.Header("X-New-Token", newToken)
				c.Request.Header.Set(JWT_TOKEN_KEY, newToken)

				// 更新上下文
				if newClaims, parseErr := j.ParseToken(newToken); parseErr == nil {
					c.Set("claims", newClaims)
					validClaims = true
					logrus.Info("Token refreshed")
				} else {
					logrus.Warn("Failed to parse new token: ", parseErr)
				}
			} else {
				logrus.Warn("Token refresh failed: ", refreshErr)
			}
		}

		// 1. 缓冲期刷新（Token过期但在缓冲期内）
		if errors.Is(err, TokenExpired) && claims != nil {
			expireTime := claims.ExpiresAt.Time
			bufferDeadline := expireTime.Add(time.Duration(claims.BufferTime) * time.Second)
			if time.Now().Before(bufferDeadline) {
				refreshToken()
			}
		} else if err == nil && claims != nil {
			// 2. 静默刷新（有效期不足30分钟）
			if time.Until(claims.ExpiresAt.Time) < 30*time.Minute {
				refreshToken()
			} else {
				// 3. 有效token直接设置上下文
				c.Set("claims", claims)
				validClaims = true
			}
		}

		// 关键修复：记录无效token情况
		if !validClaims && err != nil {
			logrus.WithFields(logrus.Fields{
				"error": err,
				"token": maskToken(token), // 安全记录token
			}).Debug("Invalid token detected")
		}

		c.Next()
	}
}

// 安全地记录token（只显示部分内容）
func maskToken(token string) string {
	if len(token) < 10 {
		return "***"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

// AuthRequired 认证中间件：仅用于需要登录的路由
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("claims")
		if !exists || claims == nil {
			c.JSON(http.StatusUnauthorized, dto.Fail[string]("请先登录"))
			c.Abort()
			return
		}

		if _, ok := claims.(*CustomClaims); !ok {
			c.JSON(http.StatusUnauthorized, dto.Fail[string]("无效的用户凭证"))
			c.Abort()
			return
		}

		c.Next()
	}
}

// GetUserInfo 获取用户信息（用于业务处理）
func GetUserInfo(c *gin.Context) (dto.UserDTO, error) {
	claims, exists := c.Get("claims")
	if !exists {
		return dto.UserDTO{}, errors.New("请求未经验证")
	}

	customClaims, ok := claims.(*CustomClaims)
	if !ok {
		return dto.UserDTO{}, errors.New("claims 类型错误")
	}

	return customClaims.UserDTO, nil
}
