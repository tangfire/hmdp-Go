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
	JWT_SERCRET_KEY = "hmdp key"
	JWT_ISSUER      = "loser"
	JWT_TOKEN_KEY   = "authorization"
)

var (
	TokenExpired     = errors.New("Token is expired")
	TokenNotValidYet = errors.New("Token is active yet")
	TokenMalformed   = errors.New("Not a token")
	TokenInvalid     = errors.New("Could not handle this token")
)

func NewJWT() *JWT {
	return &JWT{
		[]byte(JWT_SERCRET_KEY),
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

func (j *JWT) CreateToken(clamis CustomClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, clamis)
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

// 作用于所有请求的 Token 刷新中间件
func RefreshTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Request.Header.Get(JWT_TOKEN_KEY)
		if token == "" {
			c.Next()
			return
		}

		j := NewJWT()
		claims, err := j.ParseToken(token)

		// 1. 缓冲期刷新（Token 过期但仍在 BufferTime 内）
		if errors.Is(err, TokenExpired) && claims != nil {
			expireTime := claims.ExpiresAt.Time
			bufferDeadline := expireTime.Add(time.Duration(claims.BufferTime) * time.Second)
			if time.Now().Before(bufferDeadline) {
				newToken, refreshErr := j.RefreshToken(token)
				if refreshErr == nil {
					c.Header("X-New-Token", newToken)
					if newClaims, parseErr := j.ParseToken(newToken); parseErr == nil {
						c.Set("claims", newClaims) // 更新上下文
					}
				} else {
					logrus.Warn("Token refresh failed in buffer period: ", refreshErr)
				}
			}
		}

		// 2. 静默刷新（有效期不足 30 分钟）
		if err == nil && claims != nil && time.Until(claims.ExpiresAt.Time) < 30*time.Minute {
			newClaims := j.CreateClaims(claims.UserDTO)
			if newToken, err := j.CreateTokenByOldToken(token, newClaims); err == nil {
				c.Header("X-New-Token", newToken)
				c.Set("claims", newClaims) // 更新上下文
			} else {
				logrus.Warnf("Silent refresh failed: %v", err)
			}
		}

		c.Next()
	}
}

// 仅用于认证路由的 JWT 验证
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Request.Header.Get(JWT_TOKEN_KEY)
		if token == "" {
			c.JSON(http.StatusUnauthorized, dto.Fail[string]("Authorization token required"))
			c.Abort()
			return
		}

		j := NewJWT()
		claims, err := j.ParseToken(token)
		if err != nil {
			status := http.StatusUnauthorized
			msg := "Invalid token"
			switch {
			case errors.Is(err, TokenExpired):
				msg = "Token expired"
			case errors.Is(err, TokenMalformed):
				msg = "Malformed token"
			default:
				logrus.Error("JWT error: ", err)
				msg = "Authentication error"
				status = http.StatusInternalServerError
			}

			c.JSON(status, dto.Fail[string](msg))
			c.Abort()
			return
		}

		c.Set("claims", claims)
		c.Next()
	}
}

func (j *JWT) RefreshToken(oldToken string) (string, error) {
	claims, err := j.ParseToken(oldToken)
	switch {
	case err == nil:
		if claims == nil {
			return "", errors.New("nil claims")
		}
		return j.CreateToken(j.CreateClaims(claims.UserDTO))
	case errors.Is(err, TokenExpired) && claims != nil:
		return j.CreateToken(j.CreateClaims(claims.UserDTO))
	default:
		return "", errors.New("invalid token for refresh")
	}
}

// 仅用于已认证的路由（确保中间件已设置 claims）
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
