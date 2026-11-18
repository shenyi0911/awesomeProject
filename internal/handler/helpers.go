package handler

import (
	"github.com/gin-gonic/gin"

	"awesomeProject/internal/domain/errs"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/reqctx"
	"awesomeProject/pkg/response"
)

// respondError maps domain error categories to HTTP business codes instead of matching strings.
func respondError(c *gin.Context, err error) {
	switch errs.KindOf(err) {
	case errs.KindBadRequest, errs.KindConflict:
		// Validation failures and state conflicts both map to 400 to keep the existing frontend contract.
		response.Error(c, response.CodeBadRequest, err.Error())
	case errs.KindNotFound:
		response.Error(c, response.CodeNotFound, err.Error())
	case errs.KindUnauthorized:
		response.Error(c, response.CodeUnauthorized, err.Error())
	case errs.KindForbidden:
		response.Error(c, response.CodeForbidden, err.Error())
	case errs.KindTooMany:
		response.Error(c, response.CodeTooManyRequests, err.Error())
	case errs.KindUnavailable:
		response.Error(c, response.CodeServiceUnavailable, publicMessage(err, "service unavailable"))
	case errs.KindBadGateway:
		response.Error(c, response.CodeBadGateway, publicMessage(err, "upstream call failed"))
	case errs.KindInternal:
		internalError(c, err)
	default:
		internalError(c, err)
	}
}

// publicMessage returns the user-facing category message without the wrapped cause.
func publicMessage(err error, fallback string) string {
	if msg := errs.PublicOf(err); msg != "" {
		return msg
	}
	return fallback
}

// internalError logs the real cause and returns a generic message, avoiding leakage of internal details.
func internalError(c *gin.Context, err error) {
	logger.Error("request failed",
		"request_id", reqctx.IDFrom(c.Request.Context()),
		"path", c.Request.URL.Path,
		"error", err,
	)
	response.Error(c, response.CodeInternalError, "internal server error")
}

// operatorOf reads the authenticated operator from the context, falling back to the request
// parameters and finally "system".
func operatorOf(c *gin.Context) string {
	if op := reqctx.OperatorFrom(c.Request.Context()); op != "" {
		return op
	}
	if op := c.Query("operator"); op != "" {
		return op
	}
	if op := c.PostForm("operator"); op != "" {
		return op
	}
	return "system"
}
