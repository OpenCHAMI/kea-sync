package kea

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"sort"
	"strings"

	"github.com/OpenCHAMI/kea-sync/internal/httpjson"
)

type Client struct {
	http    *httpjson.Client
	service string
}

type ControlResponse struct {
	Result    int            `json:"result"`
	Text      string         `json:"text"`
	Arguments map[string]any `json:"arguments"`
}

type Subnet struct {
	ID                   int64
	CIDR                 string
	Network              *net.IPNet
	IPReservationsUnique *bool
}

type Reservation struct {
	Raw         map[string]any
	SubnetID    int64
	HWAddress   string
	IPAddress   string
	Hostname    string
	UserContext map[string]any
}

func NewClient(client *httpjson.Client, service string) *Client {
	return &Client{http: client, service: service}
}

func (c *Client) Probe(ctx context.Context) error {
	resp, err := c.command(ctx, "config-hash-get", nil)
	if err != nil {
		return err
	}
	if resp.Result != 0 {
		return fmt.Errorf("config-hash-get failed: %s", resp.Text)
	}
	return nil
}

func (c *Client) ConfiguredSubnets(ctx context.Context) ([]Subnet, error) {
	resp, err := c.command(ctx, "config-get", nil)
	if err != nil {
		return nil, err
	}
	if resp.Result != 0 {
		return nil, fmt.Errorf("config-get failed: %s", resp.Text)
	}

	subnets := extractSubnets(resp.Arguments)
	sort.Slice(subnets, func(i, j int) bool {
		if subnets[i].Network == nil || subnets[j].Network == nil {
			return subnets[i].ID < subnets[j].ID
		}
		onesI, _ := subnets[i].Network.Mask.Size()
		onesJ, _ := subnets[j].Network.Mask.Size()
		if onesI == onesJ {
			return subnets[i].ID < subnets[j].ID
		}
		return onesI > onesJ
	})

	return subnets, nil
}

func (c *Client) ReservationsBySubnet(ctx context.Context, subnetID int64, limit int) ([]Reservation, error) {
	if limit <= 0 {
		limit = 100
	}

	var out []Reservation
	from := int64(0)
	sourceIndex := int64(0)
	first := true

	for {
		args := map[string]any{
			"subnet-id": subnetID,
			"limit":     limit,
		}
		if !first {
			args["from"] = from
			args["source-index"] = sourceIndex
		}

		resp, err := c.command(ctx, "reservation-get-page", args)
		if err != nil {
			return nil, err
		}
		if resp.Result != 0 && resp.Result != 3 {
			return nil, fmt.Errorf("reservation-get-page failed for subnet %d: %s", subnetID, resp.Text)
		}

		rawHosts, _ := resp.Arguments["hosts"].([]any)
		for _, host := range rawHosts {
			hostMap, ok := host.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, parseReservation(hostMap))
		}

		next, ok := resp.Arguments["next"].(map[string]any)
		if !ok || resp.Result == 3 || len(rawHosts) == 0 {
			break
		}

		from = int64FromAny(next["from"])
		sourceIndex = int64FromAny(next["source-index"])
		first = false
	}

	return out, nil
}

func (c *Client) AddReservation(ctx context.Context, reservation map[string]any) error {
	resp, err := c.command(ctx, "reservation-add", map[string]any{"reservation": reservation})
	if err != nil {
		return err
	}
	if resp.Result != 0 {
		return fmt.Errorf("reservation-add failed: %s", resp.Text)
	}
	return nil
}

func (c *Client) UpdateReservation(ctx context.Context, reservation map[string]any) error {
	resp, err := c.command(ctx, "reservation-update", map[string]any{"reservation": reservation})
	if err != nil {
		return err
	}
	if resp.Result != 0 {
		return fmt.Errorf("reservation-update failed: %s", resp.Text)
	}
	return nil
}

func (c *Client) DeleteReservationByHWAddress(ctx context.Context, subnetID int64, hwAddress string) error {
	resp, err := c.command(ctx, "reservation-del", map[string]any{
		"subnet-id":       subnetID,
		"identifier-type": "hw-address",
		"identifier":      hwAddress,
	})
	if err != nil {
		return err
	}
	if resp.Result != 0 {
		return fmt.Errorf("reservation-del failed: %s", resp.Text)
	}
	return nil
}

func (c *Client) DeleteReservationByIPAddress(ctx context.Context, subnetID int64, ipAddress string) error {
	resp, err := c.command(ctx, "reservation-del", map[string]any{
		"subnet-id":  subnetID,
		"ip-address": ipAddress,
	})
	if err != nil {
		return err
	}
	if resp.Result != 0 {
		return fmt.Errorf("reservation-del failed: %s", resp.Text)
	}
	return nil
}

func MatchSubnet(subnets []Subnet, ip net.IP) (Subnet, bool) {
	var match Subnet
	bestMask := -1
	found := false

	for _, subnet := range subnets {
		if subnet.Network == nil || !subnet.Network.Contains(ip) {
			continue
		}
		maskBits, _ := subnet.Network.Mask.Size()
		if !found || maskBits > bestMask || (maskBits == bestMask && subnet.ID < match.ID) {
			match = subnet
			bestMask = maskBits
			found = true
		}
	}

	return match, found
}

func (c *Client) command(ctx context.Context, command string, arguments map[string]any) (ControlResponse, error) {
	body := map[string]any{
		"command": command,
	}
	if arguments != nil {
		body["arguments"] = arguments
	}
	if c.service != "" {
		body["service"] = []string{c.service}
	}

	resp, err := c.http.Do(ctx, "POST", "/", httpjson.RequestOptions{Body: body})
	if err != nil {
		return ControlResponse{}, err
	}

	return parseControlResponse(resp.Data)
}

func parseControlResponse(data any) (ControlResponse, error) {
	switch typed := data.(type) {
	case map[string]any:
		return mapToResponse(typed), nil
	case []any:
		if len(typed) == 0 {
			return ControlResponse{}, fmt.Errorf("empty Kea control response")
		}
		first, ok := typed[0].(map[string]any)
		if !ok {
			return ControlResponse{}, fmt.Errorf("unexpected Kea control response type %T", typed[0])
		}
		return mapToResponse(first), nil
	default:
		return ControlResponse{}, fmt.Errorf("unexpected Kea control response type %T", data)
	}
}

func mapToResponse(input map[string]any) ControlResponse {
	args, _ := input["arguments"].(map[string]any)
	return ControlResponse{
		Result:    int(int64FromAny(input["result"])),
		Text:      stringFromAny(input["text"]),
		Arguments: args,
	}
}

func parseReservation(input map[string]any) Reservation {
	userContext, _ := input["user-context"].(map[string]any)
	return Reservation{
		Raw:         cloneMap(input),
		SubnetID:    int64FromAny(input["subnet-id"]),
		HWAddress:   strings.ToLower(stringFromAny(input["hw-address"])),
		IPAddress:   stringFromAny(input["ip-address"]),
		Hostname:    stringFromAny(input["hostname"]),
		UserContext: cloneMap(userContext),
	}
}

func extractSubnets(input any) []Subnet {
	seen := make(map[string]struct{})
	var out []Subnet

	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if rawSubnets, ok := typed["subnet4"]; ok {
				items, _ := rawSubnets.([]any)
				for _, item := range items {
					subnetMap, ok := item.(map[string]any)
					if !ok {
						continue
					}
					id := int64FromAny(subnetMap["id"])
					cidr := stringFromAny(subnetMap["subnet"])
					_, network, err := net.ParseCIDR(cidr)
					if err != nil {
						continue
					}
					key := fmt.Sprintf("%d|%s", id, cidr)
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}

					var unique *bool
					if value, ok := subnetMap["ip-reservations-unique"].(bool); ok {
						copied := value
						unique = &copied
					}

					out = append(out, Subnet{
						ID:                   id,
						CIDR:                 cidr,
						Network:              network,
						IPReservationsUnique: unique,
					})
				}
			}

			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}

	walk(input)
	return out
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		if typed > math.MaxInt64 {
			return 0
		}
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func stringFromAny(value any) string {
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
