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
	TokenInValid     = errors.New("Could not handle this token")
)

func NewJWT() *JWT {
	return &JWT{
		[]byte(JWT_SERCRET_KEY),
	}
}

func (j *JWT) CreateClaims(userInfo dto.UserDTO) CustomClaims {
	now := time.Now().Unix()

	claims := CustomClaims{
		UserDTO:    userInfo,
		BufferTime: 86400, // buffer time 1 day
		RegisteredClaims: jwt.RegisteredClaims{
			NotBefore: jwt.NewNumericDate(time.Unix(now-1000, 0)), // the time of jwt is useful
			ExpiresAt: jwt.NewNumericDate(time.Unix(now+604800, 0)),
			Issuer:    JWT_ISSUER,
			IssuedAt:  jwt.NewNumericDate(time.Unix(now, 0)),
		},
	}
	return claims
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
		} else {
			return nil, TokenInValid
		}
	}

	if token.Valid {
		if clamis, ok := token.Claims.(*CustomClaims); ok {
			return clamis, nil
		}
		return nil, TokenInValid
	} else {
		return nil, TokenInValid
	}

}

func GetClamis(c *gin.Context) (*CustomClaims, error) {
	token := c.Request.Header.Get(JWT_TOKEN_KEY)
	j := NewJWT()
	clamis, err := j.ParseToken(token)
	if err != nil {
		logrus.Error("Failed to obtain parsed clamis")
	}
	return clamis, err
}

// 仅用于已认证的路由（确保中间件已设置 claims）
func GetUserInfo(c *gin.Context) (dto.UserDTO, error) {
	claims, exists := c.Get("claims")
	if !exists {
		// 明确拒绝处理未经验证的请求
		return dto.UserDTO{}, errors.New("请求未经验证")
	}

	// 安全地进行类型断言
	customClaims, ok := claims.(*CustomClaims) // 注意修正拼写: CustomClamis→CustomClaims
	if !ok {
		return dto.UserDTO{}, errors.New("claims 类型错误")
	}

	return customClaims.UserDTO, nil
}

func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Request.Header.Get(JWT_TOKEN_KEY)
		if token == "" {
			c.JSON(http.StatusUnauthorized, dto.Fail[string]("未提供Token"))
			c.Abort()
			return
		}

		j := NewJWT()
		claims, err := j.ParseToken(token)

		// 统一错误处理
		if err != nil {
			status := http.StatusUnauthorized
			msg := "Token无效"

			switch {
			case errors.Is(err, TokenExpired):
				msg = "Token已过期"
				now := time.Now().Unix()
				if claims != nil && now-claims.ExpiresAt.Unix() < claims.BufferTime {
					newToken, err := j.RefreshToken(token)
					if err != nil {
						logrus.Warnf("刷新Token失败: %v", err)
					} else {
						c.Header("X-New-Token", newToken)
						if newClaims, err := j.ParseToken(newToken); err == nil {
							claims = newClaims
						}
					}
				}
			case errors.Is(err, TokenMalformed):
				msg = "Token格式错误"
			default:
				logrus.Errorf("JWT解析异常: %v", err)
				msg = "认证服务不可用"
				status = http.StatusInternalServerError
			}

			c.JSON(status, dto.Fail[string](msg))
			c.Abort()
			return
		}

		// 静默刷新（深拷贝claims避免竞态）
		newClaims := *claims
		if claims.ExpiresAt.Unix()-time.Now().Unix() < 1800 {
			if newToken, err := j.CreateTokenByOldToken(token, newClaims); err == nil {
				c.Header("X-New-Token", newToken)
			}
		}

		c.Set("claims", claims)
		c.Next()
	}
}

func (j *JWT) RefreshToken(oldToken string) (string, error) {
	claims, err := j.ParseToken(oldToken)
	if err != nil && !errors.Is(err, TokenExpired) {
		return "", err
	}

	// 创建新声明（延长有效期）
	newClaims := j.CreateClaims(claims.UserDTO)
	return j.CreateToken(newClaims)
}
