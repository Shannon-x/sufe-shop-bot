package httpadmin

import (
	"net/http"
	"time"
	
	"github.com/gin-gonic/gin"
	logger "shop-bot/internal/log"
)

// ErrorResponse 统一的错误响应结构
type ErrorResponse struct {
	Code      string    `json:"code"`               // 错误代码
	Message   string    `json:"message"`            // 用户友好的错误消息
	Details   string    `json:"details,omitempty"`  // 详细错误信息（仅在开发模式下显示）
	TraceID   string    `json:"trace_id"`           // 请求追踪ID
	Timestamp time.Time `json:"timestamp"`          // 错误发生时间
}

// AppError 应用程序错误
type AppError struct {
	Code       string // 错误代码
	Message    string // 用户友好的错误消息
	Details    string // 详细错误信息
	HTTPStatus int    // HTTP状态码
	Err        error  // 原始错误
}

func (e AppError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

// 预定义的错误代码
const (
	ErrCodeInternalError     = "INTERNAL_ERROR"
	ErrCodeBadRequest        = "BAD_REQUEST"
	ErrCodeNotFound          = "NOT_FOUND"
	ErrCodeUnauthorized      = "UNAUTHORIZED"
	ErrCodeForbidden         = "FORBIDDEN"
	ErrCodeValidationFailed  = "VALIDATION_FAILED"
	ErrCodeDatabaseError     = "DATABASE_ERROR"
	ErrCodeExternalService   = "EXTERNAL_SERVICE_ERROR"
	ErrCodeResourceExhausted = "RESOURCE_EXHAUSTED"
)

// 创建各种错误的辅助函数
func NewInternalError(err error) AppError {
	return AppError{
		Code:       ErrCodeInternalError,
		Message:    "Internal server error",
		Details:    err.Error(),
		HTTPStatus: http.StatusInternalServerError,
		Err:        err,
	}
}

func NewBadRequestError(message string, err error) AppError {
	details := ""
	if err != nil {
		details = err.Error()
	}
	return AppError{
		Code:       ErrCodeBadRequest,
		Message:    message,
		Details:    details,
		HTTPStatus: http.StatusBadRequest,
		Err:        err,
	}
}

func NewNotFoundError(resource string) AppError {
	return AppError{
		Code:       ErrCodeNotFound,
		Message:    resource + " not found",
		HTTPStatus: http.StatusNotFound,
	}
}

func NewValidationError(message string, err error) AppError {
	details := ""
	if err != nil {
		details = err.Error()
	}
	return AppError{
		Code:       ErrCodeValidationFailed,
		Message:    message,
		Details:    details,
		HTTPStatus: http.StatusBadRequest,
		Err:        err,
	}
}

func NewDatabaseError(err error) AppError {
	return AppError{
		Code:       ErrCodeDatabaseError,
		Message:    "Database operation failed",
		Details:    err.Error(),
		HTTPStatus: http.StatusInternalServerError,
		Err:        err,
	}
}

// ErrorHandlerMiddleware 统一错误处理中间件
func ErrorHandlerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		
		// 检查是否有错误
		if len(c.Errors) > 0 {
			err := c.Errors.Last()
			
			// 获取trace ID
			traceID, _ := c.Get("trace_id")
			traceIDStr, _ := traceID.(string)
			
			// 处理AppError类型
			if appErr, ok := err.Err.(AppError); ok {
				// 记录详细错误日志
				logger.Error("Request failed",
					"trace_id", traceIDStr,
					"code", appErr.Code,
					"message", appErr.Message,
					"details", appErr.Details,
					"error", appErr.Err,
					"path", c.Request.URL.Path,
					"method", c.Request.Method,
				)
				
				response := ErrorResponse{
					Code:      appErr.Code,
					Message:   appErr.Message,
					TraceID:   traceIDStr,
					Timestamp: time.Now(),
				}
				
				// 在开发模式下显示详细错误
				if gin.Mode() == gin.DebugMode && appErr.Details != "" {
					response.Details = appErr.Details
				}
				
				c.JSON(appErr.HTTPStatus, response)
				return
			}
			
			// 处理其他类型的错误
			logger.Error("Unhandled error",
				"trace_id", traceIDStr,
				"error", err.Err,
				"path", c.Request.URL.Path,
				"method", c.Request.Method,
			)
			
			response := ErrorResponse{
				Code:      ErrCodeInternalError,
				Message:   "Internal server error",
				TraceID:   traceIDStr,
				Timestamp: time.Now(),
			}
			
			// 在开发模式下显示原始错误
			if gin.Mode() == gin.DebugMode {
				response.Details = err.Err.Error()
			}
			
			c.JSON(http.StatusInternalServerError, response)
		}
	}
}

// HandleError 在handler中处理错误的辅助函数
func HandleError(c *gin.Context, err AppError) {
	c.Error(err)
	c.Abort()
}