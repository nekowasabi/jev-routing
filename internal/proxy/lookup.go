package proxy

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/nekowasabi/jev-routing/internal/plan"
)

// applyLocalLookup resolves a bounded definition search from the workspace and
// removes the tool catalog. The model then only generates the reply. Skill,
// MCP, and other tools are not offered for that turn.
func applyLocalLookup(root map[string]any, user string, actions []plan.Action, opt Options) bool {
	if opt.Shadow || !opt.Transforms.Filter || len(actions) > 0 {
		return false
	}
	names := plan.DefinitionSymbols(user)
	if len(names) == 0 {
		return false
	}
	defs, ok := findFuncDefs(lookupRoot(), names)
	if !ok {
		return false
	}
	if !appendUserNote(root, definitionNote(names, defs)) {
		return false
	}
	clearToolCatalog(root)
	return true
}

func definitionNote(names []string, defs map[string]string) string {
	var b strings.Builder
	b.WriteString("\n\nLocal lookup already found these definitions. Respond with only a JSON object whose keys are the function names and whose values are these path:line strings. Do not call tools.\n")
	for _, name := range names {
		fmt.Fprintf(&b, "%s=%s\n", name, defs[name])
	}
	return b.String()
}

func lookupRoot() string {
	if v := strings.TrimSpace(os.Getenv("JEV_LOOKUP_ROOT")); v != "" {
		return v
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return cwd
	}
	return strings.TrimSpace(string(out))
}

func findFuncDefs(root string, names []string) (map[string]string, bool) {
	if root == "" || len(names) == 0 {
		return nil, false
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, false
	}
	pats := make(map[string]*regexp.Regexp, len(names))
	for _, name := range names {
		pats[name] = regexp.MustCompile(`^func\s+` + regexp.QuoteMeta(name) + `\s*[\(\[]`)
	}
	found := map[string]string{}
	walked := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || len(found) == len(names) {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "vendor", "node_modules", "artifacts", "bin", "testdata", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		walked++
		if walked > 4000 {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		scanFuncDefs(path, filepath.ToSlash(rel), pats, found)
		return nil
	})
	if len(found) != len(names) {
		return nil, false
	}
	return found, true
}

func scanFuncDefs(path, rel string, pats map[string]*regexp.Regexp, found map[string]string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 512*1024 {
		return
	}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		for name, pat := range pats {
			if _, ok := found[name]; ok {
				continue
			}
			if pat.MatchString(text) {
				found[name] = rel + ":" + strconv.Itoa(line)
			}
		}
	}
}

func appendUserNote(root map[string]any, note string) bool {
	if msgs := asSlice(root["messages"]); len(msgs) > 0 {
		for i := len(msgs) - 1; i >= 0; i-- {
			msg, ok := msgs[i].(map[string]any)
			if !ok || msg["role"] != "user" {
				continue
			}
			return appendNoteToContent(msg, note)
		}
	}
	switch in := root["input"].(type) {
	case string:
		if strings.TrimSpace(in) == "" {
			return false
		}
		root["input"] = in + note
		return true
	case []any:
		for i := len(in) - 1; i >= 0; i-- {
			item, ok := in[i].(map[string]any)
			if !ok {
				continue
			}
			role, _ := item["role"].(string)
			typ, _ := item["type"].(string)
			if role != "user" && typ != "message" {
				continue
			}
			if appendNoteToContent(item, note) {
				return true
			}
		}
	}
	return false
}

func appendNoteToContent(msg map[string]any, note string) bool {
	switch content := msg["content"].(type) {
	case string:
		msg["content"] = content + note
		return true
	case []any:
		for i := len(content) - 1; i >= 0; i-- {
			block, ok := content[i].(map[string]any)
			if !ok {
				continue
			}
			typ, _ := block["type"].(string)
			if typ != "text" && typ != "input_text" {
				continue
			}
			text, _ := block["text"].(string)
			block["text"] = text + note
			return true
		}
		content = append(content, map[string]any{"type": "text", "text": strings.TrimPrefix(note, "\n\n")})
		msg["content"] = content
		return true
	default:
		if _, ok := msg["content"]; ok {
			return false
		}
		msg["content"] = strings.TrimPrefix(note, "\n\n")
		return true
	}
}

func clearToolCatalog(root map[string]any) {
	delete(root, "tools")
	delete(root, "functions")
	delete(root, "additional_tools")
	delete(root, "tool_choice")
	in, ok := root["input"].([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(in))
	for _, raw := range in {
		item, ok := raw.(map[string]any)
		if ok && item["type"] == "additional_tools" {
			continue
		}
		kept = append(kept, raw)
	}
	root["input"] = kept
}
