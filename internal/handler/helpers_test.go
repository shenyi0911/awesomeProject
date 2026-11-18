package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"awesomeProject/internal/domain/errs"
	"awesomeProject/pkg/reqctx"
	"awesomeProject/pkg/response"
)

func TestOperatorOf(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newContext := func(req *http.Request) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = req
		return c
	}

	c := newContext(httptest.NewRequest(http.MethodGet, "/?operator=bob", nil))
	if got := operatorOf(c); got != "bob" {
		t.Fatalf("query operator = %q, want bob", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(reqctx.WithOperator(req.Context(), "alice"))
	if got := operatorOf(newContext(req)); got != "alice" {
		t.Fatalf("authenticated operator = %q, want alice", got)
	}

	if got := operatorOf(newContext(httptest.NewRequest(http.MethodGet, "/", nil))); got != "system" {
		t.Fatalf("fallback operator = %q, want system", got)
	}
}

func TestInvokeGenericRejectsInvalidMethod(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewCalculateHandler(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/demo/x", nil)
	c.Params = gin.Params{
		{Key: "code", Value: "demo"},
		{Key: "method", Value: "../escape"},
	}
	h.InvokeGeneric(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRespondErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{name: "not found", err: errs.New(errs.KindNotFound, "missing"), wantCode: http.StatusNotFound},
		{name: "forbidden", err: errs.New(errs.KindForbidden, "denied"), wantCode: http.StatusForbidden},
		{name: "conflict as bad request", err: errs.New(errs.KindConflict, "conflict"), wantCode: http.StatusBadRequest},
		{name: "internal sanitized", err: errors.New("secret detail"), wantCode: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			respondError(c, tc.err)
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantCode)
			}
			if tc.wantCode == http.StatusInternalServerError {
				var body response.Response
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if body.Message != "internal server error" {
					t.Fatalf("message = %q, want sanitized message", body.Message)
				}
			}
		})
	}
}
