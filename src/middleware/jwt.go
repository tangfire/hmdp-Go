package middleware

import (
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"
	"hmdp-Go/src/dto"
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
