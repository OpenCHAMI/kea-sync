package smd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/OpenCHAMI/kea-sync/internal/httpjson"
)

type Client struct {
	http *httpjson.Client
}

type EthernetInterface struct {
	ID          string `json:"ID"`
	MACAddress  string `json:"MACAddress"`
	ComponentID string `json:"ComponentID"`
	Type        string `json:"Type"`
	Description string `json:"Description"`
	IPAddresses []struct {
		IPAddress string `json:"IPAddress"`
		Network   string `json:"Network"`
	} `json:"IPAddresses"`
}

type ComponentArray struct {
	Components []Component `json:"Components"`
}

type Component struct {
	ID   string `json:"ID"`
	NID  int64  `json:"NID"`
	Type string `json:"Type"`
}

func NewClient(client *httpjson.Client) *Client {
	return &Client{http: client}
}

func (c *Client) EthernetInterfaces(ctx context.Context) ([]EthernetInterface, error) {
	resp, err := c.http.Do(ctx, "GET", "/hsm/v2/Inventory/EthernetInterfaces", httpjson.RequestOptions{})
	if err != nil {
		return nil, err
	}

	var interfaces []EthernetInterface
	if err := decode(resp.Data, &interfaces); err != nil {
		return nil, fmt.Errorf("decode EthernetInterfaces response: %w", err)
	}
	return interfaces, nil
}

func (c *Client) Components(ctx context.Context) ([]Component, error) {
	resp, err := c.http.Do(ctx, "GET", "/hsm/v2/State/Components", httpjson.RequestOptions{})
	if err != nil {
		return nil, err
	}

	var components ComponentArray
	if err := decode(resp.Data, &components); err != nil {
		return nil, fmt.Errorf("decode Components response: %w", err)
	}
	return components.Components, nil
}

func (c *Client) Probe(ctx context.Context) error {
	if _, err := c.http.Do(ctx, "GET", "/hsm/v2/Inventory/EthernetInterfaces", httpjson.RequestOptions{
		Query: url.Values{"ComponentID": []string{"__kea_sync_probe__"}},
	}); err != nil {
		return fmt.Errorf("probe EthernetInterfaces: %w", err)
	}

	if _, err := c.http.Do(ctx, "GET", "/hsm/v2/State/Components", httpjson.RequestOptions{
		Query: url.Values{"id": []string{"__kea_sync_probe__"}},
	}); err != nil {
		return fmt.Errorf("probe Components: %w", err)
	}

	return nil
}

func decode(input any, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
