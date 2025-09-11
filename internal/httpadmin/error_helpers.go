package httpadmin

import (
	"database/sql"
	"errors"
	
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// helper functions for resource cleanup
func SafeClose(resource interface{ Close() error }, funcName string) {
	if resource == nil {
		return
	}
	
	if err := resource.Close(); err != nil {
		logger.Error("Failed to close resource",
			"function", funcName,
			"error", err,
		)
	}
}

// TransactionHelper 事务辅助函数，确保事务正确提交或回滚
func TransactionHelper(db *gorm.DB, fn func(*gorm.DB) error) error {
	tx := db.Begin()
	if tx.Error != nil {
		return NewDatabaseError(tx.Error)
	}
	
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()
	
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	
	if err := tx.Commit().Error; err != nil {
		return NewDatabaseError(err)
	}
	
	return nil
}

// CheckDatabaseError 检查数据库错误类型
func CheckDatabaseError(err error) AppError {
	if err == nil {
		return AppError{}
	}
	
	// 检查是否是记录未找到错误
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, sql.ErrNoRows) {
		return NewNotFoundError("Record")
	}
	
	// 其他数据库错误
	return NewDatabaseError(err)
}

// ValidateRequiredField 验证必填字段
func ValidateRequiredField(c *gin.Context, fieldName string, fieldValue string) error {
	if fieldValue == "" {
		err := NewValidationError(fieldName + " is required", nil)
		HandleError(c, err)
		return err
	}
	return nil
}

// ValidatePositiveNumber 验证正数
func ValidatePositiveNumber(c *gin.Context, fieldName string, value int) error {
	if value <= 0 {
		err := NewValidationError(fieldName + " must be positive", nil)
		HandleError(c, err)
		return err
	}
	return nil
}