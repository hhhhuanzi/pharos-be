package cron

import (
	"time"

	"github.com/ccfos/nightingale/v6/models"
	"github.com/ccfos/nightingale/v6/pkg/ctx"

	"github.com/robfig/cron/v3"
	"github.com/toolkits/pkg/logger"
)

// dh 二开：operation_log 表（pkg/dh/audit 写入）的清理任务，参考
// cron/clean_alert_his_event.go 的分批删除模式。ROADMAP.md G-11。
const (
	operationLogBatchSize = 500 // 每批删除数量
	operationLogSleepMs   = 100 // 每批删除后休眠时间（毫秒），防止长时间锁表
)

func cleanOperationLogInBatches(ctx *ctx.Context, day int) {
	threshold := time.Now().Unix() - 86400*int64(day)

	var totalDeleted int64
	for {
		result := models.DB(ctx).Where("create_at < ?", threshold).Limit(operationLogBatchSize).Delete(&models.OperationLog{})
		if result.Error != nil {
			logger.Errorf("Failed to clean operation log in batch: %v", result.Error)
			return
		}

		totalDeleted += result.RowsAffected
		if result.RowsAffected < int64(operationLogBatchSize) {
			break
		}

		time.Sleep(time.Duration(operationLogSleepMs) * time.Millisecond)
	}

	if totalDeleted > 0 {
		logger.Infof("Cleaned %d operation logs older than %d days", totalDeleted, day)
	}
}

// CleanOperationLog starts a cron job to clean old operation (audit) logs in batches.
// Runs daily at 3:00 AM.
// day: 数据保留天数，<= 0 时回退到默认值 90 天（与 CleanAlertHisEventDay 的
// "<=0 表示永久保留"语义不同——操作审计日志默认就应该有清理，避免无限增长）
func CleanOperationLog(ctx *ctx.Context, day int) {
	if day <= 0 {
		day = 90
	}

	c := cron.New()
	_, err := c.AddFunc("0 3 * * *", func() {
		cleanOperationLogInBatches(ctx, day)
	})

	if err != nil {
		logger.Errorf("Failed to add clean operation log cron job: %v", err)
		return
	}

	c.Start()
	logger.Infof("Operation log cleanup cron started, retention: %d days, batch_size: %d, sleep_ms: %d", day, operationLogBatchSize, operationLogSleepMs)
}
