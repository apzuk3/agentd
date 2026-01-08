package executor

import (
	"context"

	"github.com/syss-io/executor/gen/proto/go/executor/v1/executorv1connect"
	"gorm.io/gorm"
)

type ExecutionLogger interface {
	LogEvent(ctx context.Context, sessionID string, eventType string, eventData any) error
}

type ServiceImpl struct {
	Logger         ExecutionLogger
	DB             *gorm.DB
	TokenValidator TokenValidator
}

var _ executorv1connect.ExecutorHandler = (*ServiceImpl)(nil)
