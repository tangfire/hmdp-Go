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
			c.JSON(http.StatusUnauthorized, dto.Fail[string]("Authorization token required"))
			c.Abort()
			return
		}

		j := NewJWT()
		claims, err := j.ParseToken(token)

		// Token解析错误处理
		if err != nil {
			// 处理Token过期且处于缓冲期的情况
			if errors.Is(err, TokenExpired) && claims != nil {
				bufferRemaining := time.Until(claims.ExpiresAt.Add(time.Duration(claims.BufferTime) * time.Second))
				if bufferRemaining > 0 {
					newToken, err := j.RefreshToken(token)
					if err == nil {
						// 缓冲期刷新成功
						c.Header("X-New-Token", newToken)
						if newClaims, err := j.ParseToken(newToken); err == nil {
							// 更新claims并继续处理请求
							c.Set("claims", newClaims)
							c.Next()
							return
						}
					}
					logrus.Warn("Token refresh failed: ", err)
				}
			}

			// 其他错误情况
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

		// 静默刷新逻辑
		if time.Until(claims.ExpiresAt.Time) < 30*time.Minute {
			newClaims := j.CreateClaims(claims.UserDTO)
			if newToken, err := j.CreateTokenByOldToken(token, newClaims); err == nil {
				c.Header("X-New-Token", newToken)
				claims = &newClaims // 更新当前请求的Claims
			} else {
				logrus.Warnf("Silent refresh failed (continue with old token): %v", err)
			}
		}

		c.Set("claims", claims)
		c.Next()
	}
}

func (j *JWT) RefreshToken(oldToken string) (string, error) {
	claims, err := j.ParseToken(oldToken)
	switch {
	case err == nil:
		return j.CreateToken(j.CreateClaims(claims.UserDTO))
	case errors.Is(err, TokenExpired) && claims != nil:
		return j.CreateToken(j.CreateClaims(claims.UserDTO))
	default:
		return "", errors.New("invalid token for refresh")
	}
}
