package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/nekowasabi/jev-routing/internal/plan"
)

const (
	AppSelected         = "selected"
	AppDelivered        = "delivered"
	AppStarted          = "started"
	AppResultReceived   = "result_received"
	AppVerified         = "verified"
	AppFailed           = "failed"
	AppAwaitingApproval = "awaiting_approval"
	AppUnknown          = "unknown"
)

type HostCall struct {
	DecisionID     string
	Kind           string
	Name           string
	Command        []string
	Args           json.RawMessage
	StreamComplete bool
}

type HostExecutor interface {
	DeliverSkill(decisionID, body, source string) error
	StartCall(call HostCall) (callID string, err error)
}

type Application struct {
	DecisionID    string
	State         string
	Kind          string
	CapabilityID  string
	CallID        string
	DeliveredHash string
	Result        string
	Command       []string
	Verified      bool
	Host          string
	PluginOf      string
}

type AppStore struct {
	mu      sync.Mutex
	byID    map[string]*Application
	started map[string]int
}

func NewAppStore() *AppStore {
	return &AppStore{byID: map[string]*Application{}, started: map[string]int{}}
}

func (s *AppStore) Get(id string) *Application {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneApplication(s.byID[id])
}

func (s *AppStore) put(app *Application) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.byID[app.DecisionID] = cloneApplication(app)
	s.mu.Unlock()
}

func cloneApplication(app *Application) *Application {
	if app == nil {
		return nil
	}
	// Why: Store-owned copies prevent callers from mutating values that Snapshot reads under the store lock.
	cp := *app
	cp.Command = append([]string(nil), app.Command...)
	return &cp
}

func (s *AppStore) startCount(id string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started[id]
}

func (s *AppStore) incStart(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.started[id]++
	s.mu.Unlock()
}

func Apply(store *AppStore, route plan.RouteResult, cat plan.Catalog, bodies map[string]string, generated []string, exec HostExecutor) (*Application, error) {
	app := &Application{DecisionID: route.DecisionID, State: AppSelected, CapabilityID: route.CapabilityID}
	if route.Outcome != plan.RouteSelected {
		app.State = AppFailed
		store.put(app)
		return app, fmt.Errorf("not selected: %s", route.ReasonCode)
	}
	item, ok := plan.Lookup(cat, route.CapabilityID)
	if !ok {
		app.State = AppFailed
		store.put(app)
		return app, fmt.Errorf("unknown capability")
	}
	app.Kind = item.Kind
	if existing := store.Get(route.DecisionID); existing != nil && (existing.State == AppStarted || existing.State == AppUnknown || existing.State == AppDelivered || existing.State == AppResultReceived || existing.State == AppVerified) {
		return existing, nil
	}
	if item.Kind == plan.KindPlugin {
		if len(item.Target.PluginChildren) == 0 {
			app.State = AppFailed
			store.put(app)
			return app, fmt.Errorf("plugin has no children")
		}
		parent := item.ID
		route.CapabilityID = item.Target.PluginChildren[0]
		child, err := Apply(store, route, cat, bodies, generated, exec)
		if child != nil {
			child.PluginOf = parent
			store.put(child)
		}
		return child, err
	}
	switch item.Kind {
	case plan.KindSkill:
		body, ok := bodies[item.Target.SkillBodyRef]
		if !ok || body == "" {
			app.State = AppFailed
			store.put(app)
			return app, fmt.Errorf("skill body missing")
		}
		cited := "source: " + item.Target.SkillBodyRef + "\n" + body
		sum := sha256.Sum256([]byte(cited))
		app.DeliveredHash = hex.EncodeToString(sum[:])
		if exec != nil {
			if err := exec.DeliverSkill(route.DecisionID, cited, item.Target.SkillBodyRef); err != nil {
				app.State = AppFailed
				store.put(app)
				return app, err
			}
		}
		app.State = AppDelivered
		store.put(app)
		return app, nil
	case plan.KindSubagent:
		if exec == nil {
			app.State = AppFailed
			store.put(app)
			return app, fmt.Errorf("no executor")
		}
		if store.startCount(route.DecisionID) > 0 {
			app.State = AppUnknown
			store.put(app)
			return app, fmt.Errorf("already started")
		}
		id, err := exec.StartCall(HostCall{DecisionID: route.DecisionID, Kind: item.Kind, Name: item.Target.SubagentLauncher, StreamComplete: true})
		if err != nil {
			app.State = AppFailed
			store.put(app)
			return app, err
		}
		store.incStart(route.DecisionID)
		app.CallID = id
		app.State = AppStarted
		store.put(app)
		return app, nil
	case plan.KindMCP, plan.KindCLI:
		if exec == nil {
			app.State = AppFailed
			store.put(app)
			return app, fmt.Errorf("no executor")
		}
		call := HostCall{DecisionID: route.DecisionID, Kind: item.Kind, StreamComplete: true}
		if item.Kind == plan.KindCLI {
			cmd := generated
			if len(cmd) == 0 {
				cmd = append([]string{item.Target.CLICommand}, item.Target.CLIArgs...)
			}
			if !cliMatches(item, cmd) {
				app.State = AppFailed
				store.put(app)
				return app, fmt.Errorf("cli mismatch")
			}
			call.Name = item.Target.CLICommand
			call.Command = cmd
			app.Command = cmd
		} else {
			call.Name = item.Target.MCPTool
		}
		if store.startCount(route.DecisionID) > 0 {
			app.State = AppUnknown
			store.put(app)
			return app, fmt.Errorf("already started")
		}
		id, err := exec.StartCall(call)
		if err != nil {
			app.State = AppFailed
			store.put(app)
			return app, err
		}
		store.incStart(route.DecisionID)
		app.CallID = id
		app.State = AppStarted
		store.put(app)
		return app, nil
	default:
		app.State = AppFailed
		store.put(app)
		return app, fmt.Errorf("unsupported kind %s", item.Kind)
	}
}

func StartStreamCall(store *AppStore, route plan.RouteResult, item plan.Capability, exec HostExecutor, complete bool, name string) (*Application, error) {
	app := &Application{DecisionID: route.DecisionID, CapabilityID: route.CapabilityID, Kind: item.Kind, State: AppSelected}
	if !complete {
		app.State = AppFailed
		store.put(app)
		return app, fmt.Errorf("incomplete stream")
	}
	if store.startCount(route.DecisionID) > 0 {
		app.State = AppUnknown
		store.put(app)
		return app, fmt.Errorf("already started")
	}
	id, err := exec.StartCall(HostCall{DecisionID: route.DecisionID, Kind: item.Kind, Name: name, StreamComplete: true})
	if err != nil {
		app.State = AppFailed
		store.put(app)
		return app, err
	}
	store.incStart(route.DecisionID)
	app.CallID = id
	app.State = AppStarted
	store.put(app)
	return app, nil
}

func ObserveResult(store *AppStore, decisionID, callID, result string, exitCode *int) error {
	app := store.Get(decisionID)
	if app == nil {
		return fmt.Errorf("unknown decision")
	}
	if app.CallID != "" && callID != "" && app.CallID != callID {
		app.State = AppFailed
		store.put(app)
		return fmt.Errorf("call id mismatch")
	}
	if result == "" {
		app.State = AppUnknown
		store.put(app)
		return fmt.Errorf("missing result")
	}
	app.Result = result
	app.State = AppResultReceived
	if exitCode != nil {
		app.Verified = *exitCode == 0
		if app.Verified {
			app.State = AppVerified
		} else {
			app.State = AppFailed
		}
	}
	store.put(app)
	return nil
}

func cliMatches(item plan.Capability, generated []string) bool {
	if len(generated) == 0 || generated[0] != item.Target.CLICommand {
		return false
	}
	if strings.Contains(strings.Join(generated, " "), ";") || strings.Contains(generated[0], "/") && generated[0] != item.Target.CLICommand {
		return false
	}
	need := item.Target.CLIArgs
	if len(need) == 0 {
		return true
	}
	if len(generated) < 1+len(need) {
		return false
	}
	for i, a := range need {
		if generated[1+i] != a {
			return false
		}
	}
	return true
}

func (s *AppStore) Snapshot() []*Application {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Application, 0, len(s.byID))
	for _, app := range s.byID {
		out = append(out, cloneApplication(app))
	}
	return out
}

func ObserveHostCall(store *AppStore, callID, result string, exitCode int) error {
	if store == nil {
		return fmt.Errorf("unknown call")
	}
	store.mu.Lock()
	var decision, started string
	startedN := 0
	for _, app := range store.byID {
		if callID != "" && app.CallID == callID {
			decision = app.DecisionID
			break
		}
		if app.State == AppStarted {
			started = app.DecisionID
			startedN++
			if callID == "" {
				callID = app.CallID
				decision = app.DecisionID
			}
		}
	}
	if decision == "" && startedN == 1 {
		decision = started
		if app := store.byID[decision]; app != nil {
			callID = app.CallID
		}
	}
	store.mu.Unlock()
	if decision == "" {
		return fmt.Errorf("unknown call")
	}
	return ObserveResult(store, decision, callID, result, &exitCode)
}

func Success(app *Application) bool {
	if app == nil {
		return false
	}
	switch app.State {
	case AppVerified:
		return true
	case AppDelivered:
		return app.Kind == plan.KindSkill && app.DeliveredHash != ""
	default:
		return false
	}
}
