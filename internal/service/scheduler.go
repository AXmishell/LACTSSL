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
				m.Store.Maintenance()
			}
		}
	}()
}

func (m *Manager) tick() {
	now := time.Now().UTC()
	if tasks, err := m.Store.DueTasks(now); err == nil {
		for _, task := range tasks {
			go func(id int64) {
				if err := m.RunTask(id, "task.trigger.scheduled"); err != nil {
					// 错误已记录到任务日志，这里仅输出到服务日志。
					m.Store.AddLogKey("task", id, "error", "log.task.schedule_failed", map[string]any{"message": err.Error()})
				}
			}(task.ID)
		}
	}
	if monitors, err := m.Store.DueMonitors(now); err == nil {
		for _, monitor := range monitors {
			go func(id int64) {
				_ = m.CheckMonitor(id)
			}(monitor.ID)
		}
	}
}
