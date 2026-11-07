// Common tools and helper functions
package common

import (
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/akhilrex/hammond/db"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

// A helper function to generate random string
func RandString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

var (
	jwtSecret     []byte
	jwtSecretOnce sync.Once
)

// JWTSecret returns JWT_SECRET, or a random secret generated once and kept in CONFIG/jwt_secret.
// Upstream signed tokens with an empty key when JWT_SECRET was unset.
func JWTSecret() []byte {
	jwtSecretOnce.Do(func() {
		if s := os.Getenv("JWT_SECRET"); s != "" {
			jwtSecret = []byte(s)
			return
		}
		file := path.Join(os.Getenv("CONFIG"), "jwt_secret")
		if b, err := os.ReadFile(file); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
			jwtSecret = []byte(strings.TrimSpace(string(b)))
			return
		}
		buf := make([]byte, 32)
		if _, err := crand.Read(buf); err != nil {
			log.Fatal("cannot generate JWT secret: ", err)
		}
		jwtSecret = []byte(hex.EncodeToString(buf))
		if err := os.WriteFile(file, jwtSecret, 0600); err != nil {
			log.Println("JWT_SECRET not set and could not save generated secret (logins reset on restart): ", err)
		}
	})
	return jwtSecret
}

// A Util function to generate jwt_token which can be used in the request header
func GenToken(id string, role db.Role) (string, string) {
	jwt_token := jwt.New(jwt.GetSigningMethod("HS256"))
	// Set some claims
	jwt_token.Claims = jwt.MapClaims{
		"id":   id,
		"role": role,
		"exp":  time.Now().Add(time.Hour * 24 * 3).Unix(),
	}
	// Sign and get the complete encoded token as a string
	token, _ := jwt_token.SignedString(JWTSecret())

	refreshToken := jwt.New(jwt.SigningMethodHS256)
	rtClaims := refreshToken.Claims.(jwt.MapClaims)
	rtClaims["id"] = id
	rtClaims["exp"] = time.Now().Add(time.Hour * 24 * 20).Unix()

	rt, _ := refreshToken.SignedString(JWTSecret())

	return token, rt
}

// My own Error type that will help return my customized Error info
//  {"database": {"hello":"no such table", error: "not_exists"}}
type CommonError struct {
	Errors map[string]interface{} `json:"errors"`
}

// To handle the error returned by c.Bind in gin framework
// https://github.com/go-playground/validator/blob/v9/_examples/translations/main.go
func NewValidatorError(err error) CommonError {
	res := CommonError{}
	res.Errors = make(map[string]interface{})
	errs := err.(validator.ValidationErrors)
	for _, v := range errs {
		// can translate each error one at a time.
		//fmt.Println("gg",v.NameNamespace)
		if v.Param() != "" {
			res.Errors[v.Field()] = fmt.Sprintf("{%v: %v}", v.Tag(), v.Param())
		} else {
			res.Errors[v.Field()] = fmt.Sprintf("{key: %v}", v.Tag())
		}

	}
	return res
}

// Warp the error info in a object
func NewError(key string, err error) CommonError {
	res := CommonError{}
	res.Errors = make(map[string]interface{})
	res.Errors[key] = err.Error()
	return res
}

// Changed the c.MustBindWith() ->  c.ShouldBindWith().
// I don't want to auto return 400 when error happened.
// origin function is here: https://github.com/gin-gonic/gin/blob/master/context.go
func Bind(c *gin.Context, obj interface{}) error {
	b := binding.Default(c.Request.Method, c.ContentType())
	return c.ShouldBindWith(obj, b)
}
