package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Code 业务状态码
type Code int

const (
	CodeSuccess Code = 0
	CodeError   Code = 1

	CodeBadRequest   Code = 400
	CodeUnauthorized Code = 401
	CodeForbidden    Code = 403
	CodeNotFound     Code = 404

	CodeInternalError      Code = 500
	CodeBadGateway         Code = 502
	CodeServiceUnavailable Code = 503
)

// Response 统一响应结构
type Response struct {
	Code    Code        `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    CodeSuccess,
		Message: "success",
		Data:    data,
	})
}

func SuccessWithMessage(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    CodeSuccess,
		Message: message,
		Data:    data,
	})
}

func Error(c *gin.Context, code Code, message string) {
	c.JSON(httpStatusOf(code), Response{
		Code:    code,
		Message: message,
		Data:    nil,
	})
}

func ErrorWithData(c *gin.Context, code Code, message string, data interface{}) {
	c.JSON(httpStatusOf(code), Response{
		Code:    code,
		Message: message,
		Data:    data,
	})
}

func httpStatusOf(code Code) int {
	switch code {
	case CodeBadRequest:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeInternalError:
		return http.StatusInternalServerError
	case CodeBadGateway:
		return http.StatusBadGateway
	case CodeServiceUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusOK
	}
}
