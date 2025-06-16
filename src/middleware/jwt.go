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

type CustomClamis struct {
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

func (j *JWT) CreateClaims(userInfo dto.UserDTO) CustomClamis {
	now := time.Now().Unix()

	claims := CustomClamis{
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

func (j *JWT) CreateToken(clamis CustomClamis) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, clamis)
	return token.SignedString(j.SigningKey)
}

func (j *JWT) CreateTokenByOldToken(oldToken string, claims CustomClamis) (string, error) {
	v, err, _ := control.Do("JWT"+oldToken, func() (interface{}, error) {
		return j.CreateToken(claims)
	})
	return v.(string), err
}

func (j *JWT) ParseToken(tokenStr string) (*CustomClamis, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &CustomClamis{}, func(token *jwt.Token) (interface{}, error) {
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
		if clamis, ok := token.Claims.(*CustomClamis); ok {
			return clamis, nil
		}
		return nil, TokenInValid
	} else {
		return nil, TokenInValid
	}

}

func GetClamis(c *gin.Context) (*CustomClamis, error) {
	token := c.Request.Header.Get(JWT_TOKEN_KEY)
	j := NewJWT()
	clamis, err := j.ParseToken(token)
	if err != nil {
		logrus.Error("Failed to obtain parsed clamis")
	}
	return clamis, err
}

func GetUserInfo(c *gin.Context) (dto.UserDTO, error) {
	if clamis, exists := c.Get("claims"); !exists {
		if cl, err := GetClamis(c); err != nil {
			return dto.UserDTO{}, err
		} else {
			return cl.UserDTO, nil
		}
	} else {
		waitUse := clamis.(*CustomClamis)
		return waitUse.UserDTO, nil
	}
}

func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Request.Header.Get(JWT_TOKEN_KEY)
		if token == "" {
			logrus.Error("token is empty")
			c.JSON(http.StatusUnauthorized, dto.Fail[string]("not Authorization"))
			c.Abort()
			return
		}
		j := NewJWT()
		claims, err := j.ParseToken(token)
		if err != nil {
			if err == TokenExpired {
				logrus.Warn("token is expired")
				c.JSON(http.StatusOK, dto.Fail[string]("token is expired"))
				c.Abort()
				return
			}
		}
		c.Set("claims", claims)
		c.Next()
	}
}
