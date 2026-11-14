package service

import (
	"context"
	"time"
)

// Start 启动后台调度循环：到期任务、证书监控和日常清理。
func (m *Manager) Start(ctx context.Context) {
	m.Store.ResetRunningTasks()
	m.Store.AddLogKey("system", 0, "info", "log.system.scheduler_started", nil)

	go func() {
		// 启动后先等待几秒，确保 HTTP 服务已就绪。
		time.Sleep(5 * time.Second)
		m.tick()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		maintenance := time.NewTicker(6 * time.Hour)
		defer maintenance.Stop()
		for {
			select {
			case <-ctx.Done():
				m.Store.AddLogKey("system", 0, "info", "log.system.scheduler_stopped", nil)
				return
			case <-ticker.C:
				m.tick()
			case <-maintenance.C:
				if err := m.Store.Maintenance(); err != nil {
					logger().Error("维护清理失败", "error", err)
				}
			}
		}
	}()
}

func (m *Manager) tick() {
	now := time.Now().UTC()
	tasks, err := m.Store.DueTasks(now)
	if err != nil {
		logger().Error("查询到期任务失败", "error", err)
	} else {
		for _, task := range tasks {
			go func(id int64) {
				if err := m.RunTask(id, "task.trigger.scheduled"); err != nil {
					// 错误已记录到任务日志，这里同时输出到进程日志便于排查。
					m.Store.AddLogKey("task", id, "error", "log.task.schedule_failed", map[string]any{"message": SafeError(err)})
					logger().Error("定时任务执行失败", "task_id", id, "error", SafeError(err))
				}
			}(task.ID)
		}
	}
	monitors, err := m.Store.DueMonitors(now)
	if err != nil {
		logger().Error("查询到期监控失败", "error", err)
		return
	}
	for _, monitor := range monitors {
		go func(id int64) {
			_ = m.CheckMonitor(id)
		}(monitor.ID)
	}
}
