package hostname

import "testing"

func TestResolverHostnameFor(t *testing.T) {
	resolver := Resolver{
		NodePattern: "nid{04d}",
		BMCPattern:  "bmc{04d}",
		Domain:      "cluster.local",
		Policy: Policy{
			DefaultPattern: "",
			ByType: map[string]string{
				"HSNSwitch": "{id}",
			},
		},
	}

	tests := []struct {
		name          string
		componentType string
		componentID   string
		nid           int64
		want          string
		ok            bool
	}{
		{name: "node", componentType: "Node", componentID: "x3000c0s1b0n0", nid: 7, want: "nid0007.cluster.local", ok: true},
		{name: "bmc", componentType: "NodeBMC", componentID: "x3000c0s1b0", nid: 7, want: "bmc0000.cluster.local", ok: true},
		{name: "switch policy", componentType: "HSNSwitch", componentID: "x3000c0w42", nid: 0, want: "x3000c0w42", ok: true},
		{name: "unknown", componentType: "Unknown", componentID: "x0", nid: 0, want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolver.HostnameFor(tt.componentType, tt.nid, tt.componentID)
			if ok != tt.ok {
				t.Fatalf("HostnameFor() ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("HostnameFor() = %q, want %q", got, tt.want)
			}
		})
	}
}
