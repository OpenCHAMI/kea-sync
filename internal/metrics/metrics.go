package metrics

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OpenCHAMI/kea-sync/internal/reconcile"
)

type Collector struct {
	mu          sync.RWMutex
	runs        map[string]int64
	lastSuccess time.Time
	lastPlan    reconcile.Plan
}

func NewCollector() *Collector {
	return &Collector{
		runs: map[string]int64{
			"preview_success": 0,
			"preview_error":   0,
			"apply_success":   0,
			"apply_error":     0,
		},
	}
}

func (c *Collector) Record(mode string, plan reconcile.Plan, _ time.Duration, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.runs[mode+"_"+result]++
	c.lastPlan = plan
	if mode == "apply" && err == nil {
		c.lastSuccess = time.Now().UTC()
	}
}

func (c *Collector) Text() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var builder strings.Builder
	builder.WriteString("# HELP kea_sync_runs_total Total reconcile runs by mode and result.\n")
	builder.WriteString("# TYPE kea_sync_runs_total counter\n")
	builder.WriteString(fmt.Sprintf("kea_sync_runs_total{mode=\"preview\",result=\"success\"} %d\n", c.runs["preview_success"]))
	builder.WriteString(fmt.Sprintf("kea_sync_runs_total{mode=\"preview\",result=\"error\"} %d\n", c.runs["preview_error"]))
	builder.WriteString(fmt.Sprintf("kea_sync_runs_total{mode=\"apply\",result=\"success\"} %d\n", c.runs["apply_success"]))
	builder.WriteString(fmt.Sprintf("kea_sync_runs_total{mode=\"apply\",result=\"error\"} %d\n", c.runs["apply_error"]))

	builder.WriteString("# HELP kea_sync_last_plan_items Last computed plan item counts.\n")
	builder.WriteString("# TYPE kea_sync_last_plan_items gauge\n")
	builder.WriteString(fmt.Sprintf("kea_sync_last_plan_items{kind=\"desired\"} %d\n", c.lastPlan.DesiredCount))
	builder.WriteString(fmt.Sprintf("kea_sync_last_plan_items{kind=\"managed\"} %d\n", c.lastPlan.ManagedCount))
	builder.WriteString(fmt.Sprintf("kea_sync_last_plan_items{kind=\"unmanaged\"} %d\n", c.lastPlan.UnmanagedCount))
	builder.WriteString(fmt.Sprintf("kea_sync_last_plan_items{kind=\"add\"} %d\n", len(c.lastPlan.Adds)))
	builder.WriteString(fmt.Sprintf("kea_sync_last_plan_items{kind=\"update\"} %d\n", len(c.lastPlan.Updates)))
	builder.WriteString(fmt.Sprintf("kea_sync_last_plan_items{kind=\"delete\"} %d\n", len(c.lastPlan.Deletes)))
	builder.WriteString(fmt.Sprintf("kea_sync_last_plan_items{kind=\"conflict\"} %d\n", len(c.lastPlan.Conflicts)))

	builder.WriteString("# HELP kea_sync_last_successful_sync_unix Unix timestamp of the last successful apply run.\n")
	builder.WriteString("# TYPE kea_sync_last_successful_sync_unix gauge\n")
	builder.WriteString(fmt.Sprintf("kea_sync_last_successful_sync_unix %d\n", c.lastSuccess.Unix()))
	return builder.String()
}
