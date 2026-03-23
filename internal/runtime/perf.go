package runtime

import (
	"os"
	goruntime "runtime"
	"sort"
	"time"
)

func (r *Runtime) perfPayload() map[string]any {
	snapshot := r.metrics.snapshot()
	var memStats goruntime.MemStats
	goruntime.ReadMemStats(&memStats)

	sqliteBytes := int64(0)
	if stat, err := os.Stat(r.App.Database); err == nil {
		sqliteBytes = stat.Size()
	}

	routes := make([]map[string]any, 0, len(snapshot.Series))
	total2xx := uint64(0)
	for _, series := range snapshot.Series {
		countsByCode := make([]map[string]any, 0, len(series.StatusCount))
		statuses := make([]int, 0, len(series.StatusCount))
		for status := range series.StatusCount {
			statuses = append(statuses, status)
		}
		sort.Ints(statuses)
		for status, count := range series.StatusCount {
			if status >= 200 && status < 300 {
				total2xx += count
			}
		}
		for _, status := range statuses {
			countsByCode = append(countsByCode, map[string]any{
				"code":  status,
				"count": series.StatusCount[status],
			})
		}
		avgMs := 0.0
		if series.Count > 0 {
			avgMs = (series.SumSeconds / float64(series.Count)) * 1000
		}
		routes = append(routes, map[string]any{
			"method":       series.Method,
			"route":        series.Route,
			"count":        series.Count,
			"avgMs":        avgMs,
			"countsByCode": countsByCode,
		})
	}

	return map[string]any{
		"uptimeSeconds": time.Since(snapshot.StartedAt).Seconds(),
		"goroutines":    goruntime.NumGoroutine(),
		"memoryBytes":   memStats.Alloc,
		"sqliteBytes":   sqliteBytes,
		"http": map[string]any{
			"totalRequests": snapshot.TotalRequests,
			"success2xx":    total2xx,
			"errors4xx":     snapshot.Total4xx,
			"errors5xx":     snapshot.Total5xx,
			"routes":        routes,
		},
	}
}
