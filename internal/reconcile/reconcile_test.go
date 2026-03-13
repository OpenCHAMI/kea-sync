package reconcile

import (
	"net"
	"testing"
	"time"

	"github.com/OpenCHAMI/kea-sync/internal/hostname"
	"github.com/OpenCHAMI/kea-sync/internal/kea"
	"github.com/OpenCHAMI/kea-sync/internal/smd"
)

func TestBuildDesiredUsesFirstIPv4AndCoreSMDHostnameRules(t *testing.T) {
	subnets := []kea.Subnet{
		{
			ID:      7,
			CIDR:    "192.0.2.0/24",
			Network: mustCIDR(t, "192.0.2.0/24"),
		},
	}

	interfaces := []smd.EthernetInterface{
		{
			MACAddress:  "aa:bb:cc:dd:ee:01",
			ComponentID: "x3000c0s1b0n0",
			IPAddresses: []struct {
				IPAddress string `json:"IPAddress"`
				Network   string `json:"Network"`
			}{
				{IPAddress: "192.0.2.10", Network: "HMN"},
				{IPAddress: "192.0.2.11", Network: "NMN"},
			},
		},
	}
	components := []smd.Component{
		{ID: "x3000c0s1b0n0", NID: 7, Type: "Node"},
	}

	resolver := hostname.Resolver{
		NodePattern: "nid{04d}",
		BMCPattern:  "bmc{04d}",
		Domain:      "cluster.local",
		Policy: hostname.Policy{
			ByType: map[string]string{
				"Node": "ignored{04d}",
			},
		},
	}

	desired, warnings := BuildDesired(interfaces, components, resolver, subnets)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(desired) != 1 {
		t.Fatalf("expected 1 desired reservation, got %d", len(desired))
	}

	got := desired[0]
	if got.IPAddress != "192.0.2.10" {
		t.Fatalf("IPAddress = %q, want %q", got.IPAddress, "192.0.2.10")
	}
	if got.SubnetID != 7 {
		t.Fatalf("SubnetID = %d, want 7", got.SubnetID)
	}
	if got.Hostname != "nid0007.cluster.local" {
		t.Fatalf("Hostname = %q, want %q", got.Hostname, "nid0007.cluster.local")
	}
}

func TestBuildPlanLeavesUnmanagedReservationsAlone(t *testing.T) {
	desired := []DesiredReservation{
		{
			MAC:           "aa:bb:cc:dd:ee:01",
			IPAddress:     "192.0.2.10",
			Hostname:      "nid0001.cluster.local",
			SubnetID:      1,
			ComponentID:   "x1",
			ComponentType: "Node",
			NID:           1,
			UserContext:   ManagedUserContext(DesiredReservation{ComponentID: "x1", ComponentType: "Node", NID: 1}),
		},
		{
			MAC:           "aa:bb:cc:dd:ee:02",
			IPAddress:     "192.0.2.20",
			Hostname:      "nid0002.cluster.local",
			SubnetID:      1,
			ComponentID:   "x2",
			ComponentType: "Node",
			NID:           2,
			UserContext:   ManagedUserContext(DesiredReservation{ComponentID: "x2", ComponentType: "Node", NID: 2}),
		},
	}

	actual := []kea.Reservation{
		{
			SubnetID:  1,
			HWAddress: "aa:bb:cc:dd:ee:01",
			IPAddress: "192.0.2.11",
			Hostname:  "old.cluster.local",
			UserContext: ManagedUserContext(DesiredReservation{
				ComponentID:   "x1",
				ComponentType: "Node",
				NID:           1,
			}),
		},
		{
			SubnetID:  1,
			HWAddress: "aa:bb:cc:dd:ee:03",
			IPAddress: "192.0.2.30",
			Hostname:  "stale.cluster.local",
			UserContext: ManagedUserContext(DesiredReservation{
				ComponentID:   "x3",
				ComponentType: "Node",
				NID:           3,
			}),
		},
		{
			SubnetID:  1,
			HWAddress: "aa:bb:cc:dd:ee:ff",
			IPAddress: "192.0.2.20",
			Hostname:  "manual.cluster.local",
		},
	}

	plan := BuildPlan(time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC), desired, actual)
	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(plan.Updates))
	}
	if len(plan.Deletes) != 1 {
		t.Fatalf("expected 1 delete, got %d", len(plan.Deletes))
	}
	if len(plan.Adds) != 0 {
		t.Fatalf("expected no adds because unmanaged conflict blocks creation, got %d", len(plan.Adds))
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	if plan.Conflicts[0].Reason == "" {
		t.Fatalf("expected a conflict reason")
	}
}

func mustCIDR(t *testing.T, value string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", value, err)
	}
	return network
}
