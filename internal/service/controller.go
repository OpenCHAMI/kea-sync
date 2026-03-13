package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/OpenCHAMI/kea-sync/internal/config"
	"github.com/OpenCHAMI/kea-sync/internal/hostname"
	"github.com/OpenCHAMI/kea-sync/internal/kea"
	"github.com/OpenCHAMI/kea-sync/internal/metrics"
	"github.com/OpenCHAMI/kea-sync/internal/reconcile"
	"github.com/OpenCHAMI/kea-sync/internal/smd"
	"github.com/OpenCHAMI/kea-sync/internal/version"
)

type LastSync struct {
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Mode        string    `json:"mode,omitempty"`
	Success     bool      `json:"success"`
	Error       string    `json:"error,omitempty"`
	Plan        SyncStats `json:"plan"`
}

type SyncStats struct {
	Desired   int `json:"desired"`
	Managed   int `json:"managed"`
	Unmanaged int `json:"unmanaged"`
	Adds      int `json:"adds"`
	Updates   int `json:"updates"`
	Deletes   int `json:"deletes"`
	Conflicts int `json:"conflicts"`
}

type Health struct {
	Status   string   `json:"status"`
	Version  string   `json:"version"`
	SMD      string   `json:"smd"`
	Kea      string   `json:"kea"`
	LastSync LastSync `json:"last_sync"`
}

type Controller struct {
	cfg      config.Config
	log      *slog.Logger
	smd      *smd.Client
	kea      *kea.Client
	renderer hostname.Resolver
	metrics  *metrics.Collector

	mu       sync.Mutex
	lastSync LastSync
}

func NewController(cfg config.Config, logger *slog.Logger, smdClient *smd.Client, keaClient *kea.Client, collector *metrics.Collector) *Controller {
	return &Controller{
		cfg: cfg,
		log: logger,
		smd: smdClient,
		kea: keaClient,
		renderer: hostname.Resolver{
			Policy: hostname.Policy{
				DefaultPattern: cfg.Hostnames.DefaultPattern,
				ByType:         cfg.Hostnames.ByType,
			},
			NodePattern: cfg.Hostnames.NodePattern,
			BMCPattern:  cfg.Hostnames.BMCPattern,
			Domain:      cfg.Hostnames.Domain,
		},
		metrics: collector,
	}
}

func (c *Controller) Metrics() *metrics.Collector {
	return c.metrics
}

func (c *Controller) Preview(ctx context.Context) (reconcile.Plan, error) {
	return c.execute(ctx, false)
}

func (c *Controller) Sync(ctx context.Context) (reconcile.Plan, error) {
	return c.execute(ctx, true)
}

func (c *Controller) Ready(ctx context.Context) error {
	if err := c.smd.Probe(ctx); err != nil {
		return err
	}
	if err := c.kea.Probe(ctx); err != nil {
		return err
	}
	return nil
}

func (c *Controller) Health(ctx context.Context) Health {
	health := Health{
		Status:   "ok",
		Version:  version.Version,
		SMD:      "ok",
		Kea:      "ok",
		LastSync: c.LastSync(),
	}

	if err := c.smd.Probe(ctx); err != nil {
		health.Status = "degraded"
		health.SMD = err.Error()
	}
	if err := c.kea.Probe(ctx); err != nil {
		health.Status = "degraded"
		health.Kea = err.Error()
	}

	return health
}

func (c *Controller) LastSync() LastSync {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastSync
}

func (c *Controller) StartBackground(ctx context.Context) {
	if c.cfg.SyncInterval <= 0 {
		c.log.Info("periodic sync disabled")
		return
	}

	go func() {
		ticker := time.NewTicker(c.cfg.SyncInterval)
		defer ticker.Stop()

		c.runBackgroundSync(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.runBackgroundSync(ctx)
			}
		}
	}()
}

func (c *Controller) execute(ctx context.Context, apply bool) (reconcile.Plan, error) {
	startedAt := time.Now().UTC()
	mode := "preview"
	if apply {
		mode = "apply"
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	timeout := c.cfg.HTTPWriteTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	plan, err := c.plan(ctx)
	plan.DryRun = !apply
	plan.GeneratedAt = startedAt

	if err == nil && apply {
		plan.Applied = true
		err = c.apply(ctx, &plan)
	}

	duration := time.Since(startedAt)
	c.metrics.Record(mode, plan, duration, err)
	c.lastSync = LastSync{
		StartedAt:   startedAt,
		CompletedAt: time.Now().UTC(),
		Mode:        mode,
		Success:     err == nil,
		Error:       errString(err),
		Plan:        stats(plan),
	}

	if err != nil {
		c.log.Error("sync run failed", "mode", mode, "error", err)
		return plan, err
	}

	c.log.Info("sync run completed",
		"mode", mode,
		"desired", plan.DesiredCount,
		"managed", plan.ManagedCount,
		"unmanaged", plan.UnmanagedCount,
		"adds", len(plan.Adds),
		"updates", len(plan.Updates),
		"deletes", len(plan.Deletes),
		"conflicts", len(plan.Conflicts),
	)

	return plan, nil
}

func (c *Controller) plan(ctx context.Context) (reconcile.Plan, error) {
	subnets, err := c.kea.ConfiguredSubnets(ctx)
	if err != nil {
		return reconcile.Plan{}, fmt.Errorf("load Kea subnets: %w", err)
	}
	if len(subnets) == 0 {
		return reconcile.Plan{}, fmt.Errorf("no DHCPv4 subnets discovered from Kea config")
	}

	interfaces, err := c.smd.EthernetInterfaces(ctx)
	if err != nil {
		return reconcile.Plan{}, fmt.Errorf("load SMD EthernetInterfaces: %w", err)
	}
	components, err := c.smd.Components(ctx)
	if err != nil {
		return reconcile.Plan{}, fmt.Errorf("load SMD Components: %w", err)
	}

	desired, warnings := reconcile.BuildDesired(interfaces, components, c.renderer, subnets)

	current := make([]kea.Reservation, 0)
	for _, subnet := range subnets {
		reservations, err := c.kea.ReservationsBySubnet(ctx, subnet.ID, c.cfg.PageLimit)
		if err != nil {
			return reconcile.Plan{}, fmt.Errorf("list Kea reservations for subnet %d: %w", subnet.ID, err)
		}
		current = append(current, reservations...)
	}

	plan := reconcile.BuildPlan(time.Now().UTC(), desired, current)
	plan.Warnings = append(plan.Warnings, warnings...)

	for _, subnet := range subnets {
		if subnet.IPReservationsUnique != nil && !*subnet.IPReservationsUnique {
			continue
		}
		duplicates := duplicateDesiredIPs(plan)
		if len(duplicates) > 0 {
			plan.Warnings = append(plan.Warnings, duplicates...)
			break
		}
	}

	return plan, nil
}

func (c *Controller) apply(ctx context.Context, plan *reconcile.Plan) error {
	var errs []string

	for _, action := range plan.Deletes {
		if err := c.deleteReservation(ctx, action.CurrentRecord); err != nil {
			errs = append(errs, fmt.Sprintf("delete %s/%d failed: %v", action.Current.IPAddress, action.Current.SubnetID, err))
		}
	}

	for _, action := range plan.Updates {
		payload := reconcile.ReservationPayload(&action.CurrentRecord, action.Desired)
		if err := c.kea.UpdateReservation(ctx, payload); err != nil {
			errs = append(errs, fmt.Sprintf("update %s/%d failed: %v", action.Desired.MAC, action.Desired.SubnetID, err))
		}
	}

	for _, action := range plan.Adds {
		payload := reconcile.ReservationPayload(nil, action.Desired)
		if err := c.kea.AddReservation(ctx, payload); err != nil {
			errs = append(errs, fmt.Sprintf("add %s/%d failed: %v", action.Desired.MAC, action.Desired.SubnetID, err))
		}
	}

	if len(errs) > 0 {
		plan.ApplyErrors = append(plan.ApplyErrors, errs...)
		return fmt.Errorf("%d Kea operations failed", len(errs))
	}

	return nil
}

func (c *Controller) deleteReservation(ctx context.Context, reservation kea.Reservation) error {
	if reservation.HWAddress != "" {
		return c.kea.DeleteReservationByHWAddress(ctx, reservation.SubnetID, reservation.HWAddress)
	}
	if reservation.IPAddress != "" {
		return c.kea.DeleteReservationByIPAddress(ctx, reservation.SubnetID, reservation.IPAddress)
	}
	return fmt.Errorf("reservation missing both hw-address and ip-address")
}

func (c *Controller) runBackgroundSync(ctx context.Context) {
	if _, err := c.Sync(ctx); err != nil {
		c.log.Error("background sync failed", "error", err)
	}
}

func stats(plan reconcile.Plan) SyncStats {
	return SyncStats{
		Desired:   plan.DesiredCount,
		Managed:   plan.ManagedCount,
		Unmanaged: plan.UnmanagedCount,
		Adds:      len(plan.Adds),
		Updates:   len(plan.Updates),
		Deletes:   len(plan.Deletes),
		Conflicts: len(plan.Conflicts),
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func duplicateDesiredIPs(plan reconcile.Plan) []string {
	seen := make(map[string][]string)
	desired := make([]reconcile.DesiredReservation, 0, len(plan.Adds)+len(plan.Updates))
	for _, action := range plan.Adds {
		desired = append(desired, action.Desired)
	}
	for _, action := range plan.Updates {
		desired = append(desired, action.Desired)
	}

	var warnings []string
	for _, item := range desired {
		key := fmt.Sprintf("%d|%s", item.SubnetID, item.IPAddress)
		seen[key] = append(seen[key], item.MAC)
	}
	for key, macs := range seen {
		if len(macs) < 2 {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("multiple desired reservations share %s across MACs %s; Kea must allow duplicate IP reservations for this to converge", key, strings.Join(macs, ",")))
	}
	return warnings
}
