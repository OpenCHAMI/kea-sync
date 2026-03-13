package reconcile

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/OpenCHAMI/kea-sync/internal/hostname"
	"github.com/OpenCHAMI/kea-sync/internal/kea"
	"github.com/OpenCHAMI/kea-sync/internal/smd"
)

const (
	ManagedByKey   = "managed-by"
	ManagedByValue = "kea-sync"
	SourceKey      = "source"
	SourceValue    = "smd"
)

type DesiredReservation struct {
	MAC           string         `json:"mac"`
	IPAddress     string         `json:"ip_address"`
	Hostname      string         `json:"hostname,omitempty"`
	SubnetID      int64          `json:"subnet_id"`
	ComponentID   string         `json:"component_id"`
	ComponentType string         `json:"component_type"`
	NID           int64          `json:"nid,omitempty"`
	UserContext   map[string]any `json:"user_context,omitempty"`
}

type ReservationSummary struct {
	MAC       string `json:"mac,omitempty"`
	IPAddress string `json:"ip_address,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	SubnetID  int64  `json:"subnet_id"`
	Managed   bool   `json:"managed"`
}

type AddAction struct {
	Desired DesiredReservation `json:"desired"`
	Reason  string             `json:"reason,omitempty"`
}

type UpdateAction struct {
	Current ReservationSummary `json:"current"`
	Desired DesiredReservation `json:"desired"`
	Reason  string             `json:"reason,omitempty"`

	CurrentRecord kea.Reservation `json:"-"`
}

type DeleteAction struct {
	Current ReservationSummary `json:"current"`
	Reason  string             `json:"reason,omitempty"`

	CurrentRecord kea.Reservation `json:"-"`
}

type Conflict struct {
	Desired  DesiredReservation   `json:"desired"`
	Existing []ReservationSummary `json:"existing"`
	Reason   string               `json:"reason"`
}

type Plan struct {
	GeneratedAt    time.Time      `json:"generated_at"`
	DryRun         bool           `json:"dry_run"`
	Applied        bool           `json:"applied"`
	DesiredCount   int            `json:"desired_count"`
	ManagedCount   int            `json:"managed_count"`
	UnmanagedCount int            `json:"unmanaged_count"`
	Adds           []AddAction    `json:"adds"`
	Updates        []UpdateAction `json:"updates"`
	Deletes        []DeleteAction `json:"deletes"`
	Conflicts      []Conflict     `json:"conflicts"`
	Warnings       []string       `json:"warnings,omitempty"`
	ApplyErrors    []string       `json:"apply_errors,omitempty"`
}

func BuildDesired(interfaces []smd.EthernetInterface, components []smd.Component, resolver hostname.Resolver, subnets []kea.Subnet) ([]DesiredReservation, []string) {
	componentByID := make(map[string]smd.Component, len(components))
	for _, component := range components {
		componentByID[component.ID] = component
	}

	desiredByMAC := make(map[string]DesiredReservation, len(interfaces))
	var warnings []string

	for _, iface := range interfaces {
		mac := canonicalMAC(iface.MACAddress)
		if mac == "" {
			warnings = append(warnings, "skipping EthernetInterface with empty MAC address")
			continue
		}

		component, ok := componentByID[iface.ComponentID]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skipping MAC %s because component %q was not found in SMD state", mac, iface.ComponentID))
			continue
		}

		ipv4, ok := firstIPv4(iface.IPAddresses)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skipping MAC %s because no IPv4 address was found", mac))
			continue
		}

		subnet, ok := kea.MatchSubnet(subnets, ipv4)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skipping MAC %s because IPv4 %s did not match any Kea subnet", mac, ipv4.String()))
			continue
		}

		desired := DesiredReservation{
			MAC:           mac,
			IPAddress:     ipv4.String(),
			SubnetID:      subnet.ID,
			ComponentID:   component.ID,
			ComponentType: component.Type,
			NID:           component.NID,
		}
		if hostname, ok := resolver.HostnameFor(component.Type, component.NID, component.ID); ok {
			desired.Hostname = hostname
		}
		desired.UserContext = ManagedUserContext(desired)

		if _, exists := desiredByMAC[mac]; exists {
			warnings = append(warnings, fmt.Sprintf("duplicate SMD MAC %s encountered; using the last record to match CoreSMD behavior", mac))
		}
		desiredByMAC[mac] = desired
	}

	out := make([]DesiredReservation, 0, len(desiredByMAC))
	for _, desired := range desiredByMAC {
		out = append(out, desired)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SubnetID == out[j].SubnetID {
			return out[i].MAC < out[j].MAC
		}
		return out[i].SubnetID < out[j].SubnetID
	})

	return out, warnings
}

func BuildPlan(now time.Time, desired []DesiredReservation, current []kea.Reservation) Plan {
	plan := Plan{
		GeneratedAt:  now.UTC(),
		DesiredCount: len(desired),
	}

	desiredByMAC := make(map[string]DesiredReservation, len(desired))
	for _, item := range desired {
		desiredByMAC[item.MAC] = item
	}

	managedByMAC := make(map[string][]kea.Reservation)
	unmanagedBySubnetMAC := make(map[string][]kea.Reservation)
	unmanagedBySubnetIP := make(map[string][]kea.Reservation)
	currentByDeleteKey := make(map[string]kea.Reservation)
	used := make(map[string]struct{})
	blocked := make(map[string]struct{})

	for _, reservation := range current {
		if IsManagedReservation(reservation) {
			plan.ManagedCount++
			managedByMAC[canonicalMAC(reservation.HWAddress)] = append(managedByMAC[canonicalMAC(reservation.HWAddress)], reservation)
			continue
		}
		plan.UnmanagedCount++
		unmanagedBySubnetMAC[subnetMACKey(reservation.SubnetID, reservation.HWAddress)] = append(unmanagedBySubnetMAC[subnetMACKey(reservation.SubnetID, reservation.HWAddress)], reservation)
		unmanagedBySubnetIP[subnetIPKey(reservation.SubnetID, reservation.IPAddress)] = append(unmanagedBySubnetIP[subnetIPKey(reservation.SubnetID, reservation.IPAddress)], reservation)
	}

	deleteScheduled := make(map[string]struct{})

	for _, item := range desired {
		var conflicts []ReservationSummary

		for _, reservation := range unmanagedBySubnetMAC[subnetMACKey(item.SubnetID, item.MAC)] {
			conflicts = append(conflicts, summary(reservation))
		}
		for _, reservation := range unmanagedBySubnetIP[subnetIPKey(item.SubnetID, item.IPAddress)] {
			conflicts = append(conflicts, summary(reservation))
		}

		if len(conflicts) > 0 {
			blocked[item.MAC] = struct{}{}
			plan.Conflicts = append(plan.Conflicts, Conflict{
				Desired:  item,
				Existing: uniqueSummaries(conflicts),
				Reason:   "desired reservation collides with unmanaged Kea reservation",
			})
			continue
		}

		currentManaged := managedByMAC[item.MAC]
		if len(currentManaged) == 0 {
			plan.Adds = append(plan.Adds, AddAction{
				Desired: item,
				Reason:  "reservation missing from managed Kea state",
			})
			continue
		}

		chosenIndex := -1
		for idx, reservation := range currentManaged {
			if reservation.SubnetID == item.SubnetID {
				chosenIndex = idx
				break
			}
		}

		if chosenIndex == -1 {
			for _, reservation := range currentManaged {
				key := deleteKey(reservation)
				if _, exists := deleteScheduled[key]; exists {
					continue
				}
				deleteScheduled[key] = struct{}{}
				currentByDeleteKey[key] = reservation
				plan.Deletes = append(plan.Deletes, DeleteAction{
					Current:       summary(reservation),
					CurrentRecord: reservation,
					Reason:        "managed reservation moved to a different subnet",
				})
			}
			plan.Adds = append(plan.Adds, AddAction{
				Desired: item,
				Reason:  "managed reservation requires re-creation in a different subnet",
			})
			continue
		}

		chosen := currentManaged[chosenIndex]
		used[deleteKey(chosen)] = struct{}{}
		if NeedsUpdate(chosen, item) {
			plan.Updates = append(plan.Updates, UpdateAction{
				Current:       summary(chosen),
				CurrentRecord: chosen,
				Desired:       item,
				Reason:        "managed reservation drifted from desired SMD state",
			})
		}

		for idx, reservation := range currentManaged {
			if idx == chosenIndex {
				continue
			}
			key := deleteKey(reservation)
			if _, exists := deleteScheduled[key]; exists {
				continue
			}
			deleteScheduled[key] = struct{}{}
			currentByDeleteKey[key] = reservation
			plan.Deletes = append(plan.Deletes, DeleteAction{
				Current:       summary(reservation),
				CurrentRecord: reservation,
				Reason:        "duplicate managed reservation for the same MAC",
			})
		}
	}

	for mac, reservations := range managedByMAC {
		if _, exists := desiredByMAC[mac]; exists {
			continue
		}
		if _, exists := blocked[mac]; exists {
			continue
		}
		for _, reservation := range reservations {
			key := deleteKey(reservation)
			if _, alreadyUsed := used[key]; alreadyUsed {
				continue
			}
			if _, exists := deleteScheduled[key]; exists {
				continue
			}
			deleteScheduled[key] = struct{}{}
			currentByDeleteKey[key] = reservation
			plan.Deletes = append(plan.Deletes, DeleteAction{
				Current:       summary(reservation),
				CurrentRecord: reservation,
				Reason:        "managed reservation no longer exists in SMD desired state",
			})
		}
	}

	sort.Slice(plan.Adds, func(i, j int) bool {
		if plan.Adds[i].Desired.SubnetID == plan.Adds[j].Desired.SubnetID {
			return plan.Adds[i].Desired.MAC < plan.Adds[j].Desired.MAC
		}
		return plan.Adds[i].Desired.SubnetID < plan.Adds[j].Desired.SubnetID
	})
	sort.Slice(plan.Updates, func(i, j int) bool {
		if plan.Updates[i].Desired.SubnetID == plan.Updates[j].Desired.SubnetID {
			return plan.Updates[i].Desired.MAC < plan.Updates[j].Desired.MAC
		}
		return plan.Updates[i].Desired.SubnetID < plan.Updates[j].Desired.SubnetID
	})
	sort.Slice(plan.Deletes, func(i, j int) bool {
		if plan.Deletes[i].Current.SubnetID == plan.Deletes[j].Current.SubnetID {
			return plan.Deletes[i].Current.MAC < plan.Deletes[j].Current.MAC
		}
		return plan.Deletes[i].Current.SubnetID < plan.Deletes[j].Current.SubnetID
	})
	sort.Slice(plan.Conflicts, func(i, j int) bool {
		if plan.Conflicts[i].Desired.SubnetID == plan.Conflicts[j].Desired.SubnetID {
			return plan.Conflicts[i].Desired.MAC < plan.Conflicts[j].Desired.MAC
		}
		return plan.Conflicts[i].Desired.SubnetID < plan.Conflicts[j].Desired.SubnetID
	})

	return plan
}

func ManagedUserContext(desired DesiredReservation) map[string]any {
	return map[string]any{
		"openchami": map[string]any{
			ManagedByKey:     ManagedByValue,
			SourceKey:        SourceValue,
			"component-id":   desired.ComponentID,
			"component-type": desired.ComponentType,
			"nid":            desired.NID,
		},
	}
}

func IsManagedReservation(reservation kea.Reservation) bool {
	openchamiContext, ok := reservation.UserContext["openchami"].(map[string]any)
	if !ok {
		return false
	}
	return stringValue(openchamiContext[ManagedByKey]) == ManagedByValue
}

func NeedsUpdate(current kea.Reservation, desired DesiredReservation) bool {
	if canonicalMAC(current.HWAddress) != desired.MAC {
		return true
	}
	if current.SubnetID != desired.SubnetID {
		return true
	}
	if current.IPAddress != desired.IPAddress {
		return true
	}
	if current.Hostname != desired.Hostname {
		return true
	}
	return !managedContextMatches(current.UserContext, desired.UserContext)
}

func ReservationPayload(current *kea.Reservation, desired DesiredReservation) map[string]any {
	payload := map[string]any{}
	if current != nil && current.Raw != nil {
		payload = cloneMap(current.Raw)
	}

	for _, key := range []string{
		"duid",
		"client-id",
		"circuit-id",
		"flex-id",
		"ip-addresses",
		"prefixes",
	} {
		delete(payload, key)
	}

	payload["subnet-id"] = desired.SubnetID
	payload["hw-address"] = desired.MAC
	payload["ip-address"] = desired.IPAddress

	if desired.Hostname == "" {
		delete(payload, "hostname")
	} else {
		payload["hostname"] = desired.Hostname
	}

	mergedUserContext := cloneMap(nil)
	if current != nil {
		mergedUserContext = cloneMap(current.UserContext)
	}
	if mergedUserContext == nil {
		mergedUserContext = make(map[string]any)
	}
	for key, value := range desired.UserContext {
		mergedUserContext[key] = cloneValue(value)
	}
	payload["user-context"] = mergedUserContext

	return payload
}

func summary(reservation kea.Reservation) ReservationSummary {
	return ReservationSummary{
		MAC:       canonicalMAC(reservation.HWAddress),
		IPAddress: reservation.IPAddress,
		Hostname:  reservation.Hostname,
		SubnetID:  reservation.SubnetID,
		Managed:   IsManagedReservation(reservation),
	}
}

func firstIPv4(addresses []struct {
	IPAddress string `json:"IPAddress"`
	Network   string `json:"Network"`
}) (net.IP, bool) {
	if len(addresses) == 0 {
		return nil, false
	}
	ip := net.ParseIP(addresses[0].IPAddress)
	if ip == nil {
		return nil, false
	}
	ipv4 := ip.To4()
	if ipv4 == nil {
		return nil, false
	}
	return ipv4, true
}

func managedContextMatches(current, desired map[string]any) bool {
	currentJSON, _ := json.Marshal(cloneManagedContext(current))
	desiredJSON, _ := json.Marshal(cloneManagedContext(desired))
	return string(currentJSON) == string(desiredJSON)
}

func cloneManagedContext(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	if openchamiContext, ok := input["openchami"].(map[string]any); ok {
		return map[string]any{
			"openchami": cloneMap(openchamiContext),
		}
	}
	return nil
}

func subnetMACKey(subnetID int64, mac string) string {
	return fmt.Sprintf("%d|%s", subnetID, canonicalMAC(mac))
}

func subnetIPKey(subnetID int64, ip string) string {
	return fmt.Sprintf("%d|%s", subnetID, strings.TrimSpace(ip))
}

func deleteKey(reservation kea.Reservation) string {
	return fmt.Sprintf("%d|%s|%s", reservation.SubnetID, canonicalMAC(reservation.HWAddress), reservation.IPAddress)
}

func uniqueSummaries(input []ReservationSummary) []ReservationSummary {
	seen := make(map[string]struct{}, len(input))
	out := make([]ReservationSummary, 0, len(input))
	for _, item := range input {
		key := fmt.Sprintf("%d|%s|%s", item.SubnetID, item.MAC, item.IPAddress)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SubnetID == out[j].SubnetID {
			if out[i].MAC == out[j].MAC {
				return out[i].IPAddress < out[j].IPAddress
			}
			return out[i].MAC < out[j].MAC
		}
		return out[i].SubnetID < out[j].SubnetID
	})
	return out
}

func canonicalMAC(mac string) string {
	return strings.ToLower(strings.TrimSpace(mac))
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneValue(value)
	}
	return out
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, cloneValue(item))
		}
		return out
	default:
		return typed
	}
}
