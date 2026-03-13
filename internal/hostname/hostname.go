package hostname

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Policy struct {
	DefaultPattern string
	ByType         map[string]string
}

type Resolver struct {
	NodePattern string
	BMCPattern  string
	Domain      string
	Policy      Policy
}

func ExpandHostnamePattern(pattern string, nid int64, id string) string {
	out := strings.ReplaceAll(pattern, "{id}", id)
	re := regexp.MustCompile(`\{0*(\d+)d\}`)
	out = re.ReplaceAllStringFunc(out, func(match string) string {
		nStr := re.FindStringSubmatch(match)[1]
		width, _ := strconv.Atoi(nStr)
		return fmt.Sprintf("%0*d", width, nid)
	})
	return out
}

func (p Policy) HostnameFor(componentType string, nid int64, id string) (string, bool) {
	pattern := ""
	if p.ByType != nil {
		pattern = p.ByType[componentType]
	}
	if pattern == "" {
		pattern = p.DefaultPattern
	}
	if pattern == "" {
		return "", false
	}
	return ExpandHostnamePattern(pattern, nid, id), true
}

func (r Resolver) HostnameFor(componentType string, componentNID int64, componentID string) (string, bool) {
	nid := int64(0)
	if componentType == "Node" {
		nid = componentNID
	}
	if componentType == "Node" {
		name := ExpandHostnamePattern(r.NodePattern, nid, componentID)
		if r.Domain != "" {
			name += "." + r.Domain
		}
		return name, true
	}
	if componentType == "NodeBMC" {
		name := ExpandHostnamePattern(r.BMCPattern, nid, componentID)
		if r.Domain != "" {
			name += "." + r.Domain
		}
		return name, true
	}
	return r.Policy.HostnameFor(componentType, nid, componentID)
}
