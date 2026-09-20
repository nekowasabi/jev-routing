package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/host"
)

const (
	KindModel    = "model"
	KindSubagent = "subagent"
	KindSkill    = "skill"
	KindMCP      = "mcp_tool"
	KindCLI      = "cli"
	KindPlugin   = "plugin"

	AvailInstalled = "installed"
	AvailMissing   = "missing"

	PermUnknown = "unknown"
)

// Target is the required application binding. A name-only candidate is rejected.
type Target struct {
	SkillBodyRef     string        `json:"skill_body_ref,omitempty"`
	MCPConn          string        `json:"mcp_conn,omitempty"`
	MCPTool          string        `json:"mcp_tool,omitempty"`
	CLICommand       string        `json:"cli_command,omitempty"`
	CLIArgs          []string      `json:"cli_args,omitempty"`
	PluginChildren   []string      `json:"plugin_children,omitempty"`
	SubagentLauncher string        `json:"subagent_launcher,omitempty"`
	ModelPairs       []ModelEffort `json:"model_pairs,omitempty"`
}

type ModelEffort struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

type Capability struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	ValidationSpec json.RawMessage `json:"validation_spec,omitempty"`
	Provider       string          `json:"provider"`
	Hosts          []host.ID       `json:"hosts,omitempty"`
	InputSchema    json.RawMessage `json:"input_schema,omitempty"`
	Availability   string          `json:"availability"`
	Permission     string          `json:"permission"`
	Version        string          `json:"version"`
	Target         Target          `json:"target"`
	Explicit       bool            `json:"explicit,omitempty"`
}

type Catalog struct {
	Revision string       `json:"revision"`
	Items    []Capability `json:"items"`
}

type Inventory struct {
	Tools     []Spec
	Skills    []SkillIn
	CLIs      []CLIIn
	Plugins   []PluginIn
	Models    []ModelIn
	Subagents []SubagentIn
}

type SkillIn struct {
	Name, Provider, Version, BodyRef, Description string
	Hosts                                         []host.ID
	Explicit                                      bool
	Available                                     bool
	Permission                                    string
}

type CLIIn struct {
	Name, Provider, Version, Command, Description string
	Args                                          []string
	Hosts                                         []host.ID
	Available                                     bool
	Permission                                    string
}

type PluginIn struct {
	Name, Provider, Version, Description string
	Hosts                                []host.ID
	Available                            bool
	Children                             []Capability
}

type ModelIn struct {
	Name, Provider, Version, Description string
	Hosts                                []host.ID
	Pairs                                []ModelEffort
	Available                            bool
}

type SubagentIn struct {
	Name, Provider, Version, Launcher, Description string
	Hosts                                          []host.ID
	Available                                      bool
}

// LaunchProbe counts start/run attempts. BuildCatalog must not increment these.
type LaunchProbe struct {
	MCPStarts int
	CLIRuns   int
}

func (p *LaunchProbe) StartMCP(string)         { p.MCPStarts++ }
func (p *LaunchProbe) RunCLI(string, []string) { p.CLIRuns++ }

func (t Target) bound(kind string) bool {
	switch kind {
	case KindSkill:
		return t.SkillBodyRef != ""
	case KindMCP:
		return t.MCPConn != "" && t.MCPTool != ""
	case KindCLI:
		return t.CLICommand != ""
	case KindPlugin:
		return len(t.PluginChildren) > 0
	case KindSubagent:
		return t.SubagentLauncher != ""
	case KindModel:
		return len(t.ModelPairs) > 0
	default:
		return false
	}
}

func CapabilityID(kind, provider, name, version string) string {
	return kind + ":" + provider + ":" + name + "@" + version
}

func (c *Catalog) Add(item Capability) error {
	if item.Kind == "" || item.Name == "" || item.Provider == "" || item.Version == "" {
		return fmt.Errorf("capability missing identity")
	}
	if item.ID == "" {
		item.ID = CapabilityID(item.Kind, item.Provider, item.Name, item.Version)
	}
	if !item.Target.bound(item.Kind) {
		return fmt.Errorf("%s: missing application target", item.ID)
	}
	for _, existing := range c.Items {
		if existing.ID == item.ID {
			return fmt.Errorf("%s: id collision", item.ID)
		}
	}
	item.Description = judgmentDesc(item.Description)
	c.Items = append(c.Items, item)
	return nil
}

func (c *Catalog) Merge(items []Capability) {
	if c == nil {
		return
	}
	for _, item := range items {
		_ = c.addUnique(item)
	}
	c.Revision = c.ComputeRevision()
}

func InventoryFromSkillDir(dir string) (Inventory, map[string]string, error) {
	var inv Inventory
	bodies := map[string]string{}
	if strings.TrimSpace(dir) == "" {
		return inv, bodies, fmt.Errorf("empty skill dir")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return inv, bodies, err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.Contains(e.Name(), "..") {
			continue
		}
		p := filepath.Join(dir, e.Name(), "SKILL.md")
		raw, err := os.ReadFile(p)
		if err != nil || len(raw) == 0 {
			continue
		}
		inv.Skills = append(inv.Skills, SkillIn{
			Name: e.Name(), Provider: "local", Version: "1",
			BodyRef: p, Description: skillDirDesc(raw),
			Available: true, Explicit: true,
		})
		bodies[p] = string(raw)
	}
	return inv, bodies, nil
}

func skillDirDesc(raw []byte) string {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" {
			return line
		}
	}
	return ""
}

func (c *Catalog) addUnique(item Capability) error {
	if item.ID == "" {
		item.ID = CapabilityID(item.Kind, item.Provider, item.Name, item.Version)
	}
	for _, existing := range c.Items {
		if existing.ID == item.ID {
			return nil
		}
	}
	return c.Add(item)
}

func (c Catalog) Eligible() []Capability {
	var out []Capability
	for _, item := range c.Items {
		if item.Availability != AvailInstalled {
			continue
		}
		if !item.Target.bound(item.Kind) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (c Catalog) Specs() []Spec {
	out := make([]Spec, 0, len(c.Items))
	for _, item := range c.Eligible() {
		out = append(out, Spec{Name: item.Name, Desc: item.Description})
	}
	return out
}

func (c Catalog) ComputeRevision() string {
	parts := make([]string, 0, len(c.Items))
	for _, item := range c.Items {
		parts = append(parts, item.ID)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:8])
}

func RevisionOf(items []Capability) string {
	c := Catalog{Items: items}
	return c.ComputeRevision()
}

func judgmentDesc(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "Bearer ") || strings.Contains(s, "sk-") || strings.Contains(s, "PRIVATE_") {
		return ""
	}
	runes := []rune(s)
	if len(runes) > 240 {
		return string(runes[:240])
	}
	return s
}

func avail(ok bool) string {
	if ok {
		return AvailInstalled
	}
	return AvailMissing
}

func permOrUnknown(p string) string {
	if strings.TrimSpace(p) == "" {
		return PermUnknown
	}
	return p
}

func capabilityFromSpec(s Spec, h host.ID) Capability {
	kind, provider, target := KindMCP, "host", Target{MCPConn: "host-catalog", MCPTool: s.Name}
	n := strings.ToLower(s.Name)
	switch {
	case strings.HasPrefix(n, "mcp__") || strings.Contains(n, "mcp"):
		kind, provider = KindMCP, "mcp"
		target = Target{MCPConn: "host-catalog", MCPTool: s.Name}
	case isAgent(s.Name):
		kind, provider = KindSubagent, "host"
		target = Target{SubagentLauncher: s.Name}
	case n == "skill" || strings.HasPrefix(n, "skill"):
		kind, provider = KindSkill, "host"
		target = Target{SkillBodyRef: "host-skill:" + s.Name}
	case isExec(s.Name):
		kind, provider = KindCLI, "host"
		target = Target{CLICommand: s.Name}
	}
	return Capability{
		ID:           CapabilityID(kind, provider, s.Name, "host"),
		Kind:         kind,
		Name:         s.Name,
		Description:  judgmentDesc(s.Desc),
		Provider:     provider,
		Hosts:        []host.ID{h},
		Availability: AvailInstalled,
		Permission:   PermUnknown,
		Version:      "host",
		Target:       target,
	}
}

func CapabilitiesFromSpecs(specs []Spec, h host.ID) []Capability {
	out := make([]Capability, 0, len(specs))
	for _, s := range specs {
		if s.Name == "" || HostMeta(s.Name) {
			continue
		}
		out = append(out, capabilityFromSpec(s, h))
	}
	return out
}

// BuildCatalog materializes a snapshot. probe is accepted so tests can prove
// MCP/CLI processes are never started.
func BuildCatalog(inv Inventory, h host.ID, probe *LaunchProbe) (Catalog, error) {
	_ = probe
	var cat Catalog
	var errs []error
	add := func(item Capability) {
		if err := cat.Add(item); err != nil {
			errs = append(errs, err)
		}
	}
	for _, s := range inv.Tools {
		if s.Name == "" || HostMeta(s.Name) {
			continue
		}
		add(capabilityFromSpec(s, h))
	}
	for _, s := range inv.Skills {
		add(Capability{
			ID: CapabilityID(KindSkill, s.Provider, s.Name, s.Version), Kind: KindSkill, Name: s.Name,
			Description: s.Description, Provider: s.Provider, Hosts: s.Hosts,
			Availability: avail(s.Available), Permission: permOrUnknown(s.Permission),
			Version: s.Version, Explicit: s.Explicit,
			Target: Target{SkillBodyRef: s.BodyRef},
		})
	}
	for _, s := range inv.CLIs {
		add(Capability{
			ID: CapabilityID(KindCLI, s.Provider, s.Name, s.Version), Kind: KindCLI, Name: s.Name,
			Description: s.Description, Provider: s.Provider, Hosts: s.Hosts,
			Availability: avail(s.Available), Permission: permOrUnknown(s.Permission),
			Version: s.Version, Target: Target{CLICommand: s.Command, CLIArgs: s.Args},
		})
	}
	for _, s := range inv.Models {
		add(Capability{
			ID: CapabilityID(KindModel, s.Provider, s.Name, s.Version), Kind: KindModel, Name: s.Name,
			Description: s.Description, Provider: s.Provider, Hosts: s.Hosts,
			Availability: avail(s.Available), Permission: PermUnknown, Version: s.Version,
			Target: Target{ModelPairs: s.Pairs},
		})
	}
	for _, s := range inv.Subagents {
		add(Capability{
			ID: CapabilityID(KindSubagent, s.Provider, s.Name, s.Version), Kind: KindSubagent, Name: s.Name,
			Description: s.Description, Provider: s.Provider, Hosts: s.Hosts,
			Availability: avail(s.Available), Permission: PermUnknown, Version: s.Version,
			Target: Target{SubagentLauncher: s.Launcher},
		})
	}
	for _, s := range inv.Plugins {
		var childIDs []string
		for _, ch := range s.Children {
			ch.Provider = s.Name
			if ch.Version == "" {
				ch.Version = s.Version
			}
			if ch.ID == "" {
				ch.ID = CapabilityID(ch.Kind, ch.Provider, ch.Name, ch.Version)
			}
			if ch.Availability == "" {
				ch.Availability = avail(s.Available)
			}
			if ch.Permission == "" {
				ch.Permission = PermUnknown
			}
			if err := cat.addUnique(ch); err != nil {
				errs = append(errs, err)
				continue
			}
			childIDs = append(childIDs, ch.ID)
		}
		add(Capability{
			ID: CapabilityID(KindPlugin, s.Provider, s.Name, s.Version), Kind: KindPlugin, Name: s.Name,
			Description: s.Description, Provider: s.Provider, Hosts: s.Hosts,
			Availability: avail(s.Available), Permission: PermUnknown, Version: s.Version,
			Target: Target{PluginChildren: childIDs},
		})
	}
	cat.Revision = cat.ComputeRevision()
	if len(errs) > 0 {
		return cat, fmt.Errorf("catalog: %v", errs)
	}
	return cat, nil
}

func LoadCatalog(path string) (Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, err
	}
	var cat Catalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return Catalog{}, err
	}
	if cat.Revision == "" {
		cat.Revision = cat.ComputeRevision()
	}
	return cat, nil
}
