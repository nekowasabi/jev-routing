package bench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// stubTool is one catalog entry. props are "name: description" pairs, all strings;
// the first one is required.
type stubTool struct {
	name, desc string
	props      []string
}

// catalog interleaves unrelated SaaS/ops tools with distractors that overlap the
// agents' built-ins, so even a small N makes tool selection non-trivial.
var catalog = []stubTool{
	{"bench_left_fact", "Return the left value for the dual-facts benchmark.", []string{"request: request the left value"}},
	{"bench_right_fact", "Return the right value for the dual-facts benchmark.", []string{"request: request the right value"}},
	{"jira_search_issues", "Search Jira issues with a JQL query.", []string{"jql: JQL query", "max_results: maximum number of issues"}},
	{"repo_search_code", "Search source code in the repository for a pattern.", []string{"query: text or regex to search for", "path: directory to search in"}},
	{"slack_post_message", "Post a message to a Slack channel.", []string{"channel: channel name or id", "text: message text"}},
	{"fs_read_file", "Read a file from the filesystem and return its contents.", []string{"path: file path"}},
	{"gmail_send", "Send an email from the connected Gmail account.", []string{"to: recipient address", "subject: subject line", "body: message body"}},
	{"fs_write_file", "Write contents to a file, replacing it if it exists.", []string{"path: file path", "content: new file contents"}},
	{"calendar_create_event", "Create a Google Calendar event.", []string{"title: event title", "start: start time (RFC 3339)", "end: end time (RFC 3339)"}},
	{"run_shell_command", "Run a shell command and return stdout and stderr.", []string{"command: command line", "cwd: working directory"}},
	{"s3_list_objects", "List objects in an S3 bucket.", []string{"bucket: bucket name", "prefix: key prefix"}},
	{"git_diff", "Show the git diff of the working tree.", []string{"path: limit the diff to this path", "staged: show staged changes only"}},
	{"postgres_query", "Run a read-only SQL query against Postgres.", []string{"sql: SQL statement"}},
	{"test_runner_run", "Run the project's test suite and report failures.", []string{"pattern: test name pattern", "path: test directory"}},
	{"k8s_get_pods", "List Kubernetes pods in a namespace.", []string{"namespace: namespace", "selector: label selector"}},
	{"lint_run", "Run the linter on the given files.", []string{"paths: space-separated file paths", "fix: apply automatic fixes"}},
	{"sentry_list_issues", "List unresolved Sentry issues for a project.", []string{"project: project slug", "query: search query"}},
	{"web_fetch", "Fetch a URL and return the page as text.", []string{"url: URL to fetch"}},
	{"figma_get_file", "Get a Figma file's document tree.", []string{"file_key: Figma file key"}},
	{"docs_search", "Search technical documentation for an API or library.", []string{"query: search terms", "library: library name"}},
	{"notion_search", "Search pages in the Notion workspace.", []string{"query: search terms"}},
	{"fs_list_directory", "List the entries of a directory.", []string{"path: directory path", "recursive: list recursively"}},
	{"github_create_issue", "Create a GitHub issue.", []string{"repo: owner/name", "title: issue title", "body: issue body"}},
	{"fs_edit_file", "Replace a string in a file with another string.", []string{"path: file path", "old: text to replace", "new: replacement text"}},
	{"linear_create_issue", "Create a Linear issue in a team.", []string{"team: team key", "title: issue title", "description: issue description"}},
	{"git_log", "Show recent git commits.", []string{"path: limit to this path", "limit: number of commits"}},
	{"confluence_get_page", "Get a Confluence page by id.", []string{"page_id: page id"}},
	{"npm_run_script", "Run an npm script from package.json.", []string{"script: script name", "args: extra arguments"}},
	{"datadog_query_metrics", "Query Datadog metrics over a time range.", []string{"query: metric query", "from: start time", "to: end time"}},
	{"code_symbol_lookup", "Find the definition and references of a code symbol.", []string{"symbol: symbol name", "path: file or directory"}},
	{"pagerduty_list_incidents", "List open PagerDuty incidents.", []string{"service: service id", "status: incident status"}},
	{"fs_glob", "Find files matching a glob pattern.", []string{"pattern: glob pattern", "path: base directory"}},
	{"stripe_list_charges", "List recent Stripe charges.", []string{"customer: customer id", "limit: number of charges"}},
	{"git_status", "Show the git working tree status.", []string{"path: repository path"}},
	{"hubspot_get_contact", "Get a HubSpot contact by email.", []string{"email: contact email"}},
	{"js_eval", "Evaluate a JavaScript snippet in Node.js and return the result.", []string{"code: JavaScript source"}},
	{"zendesk_search_tickets", "Search Zendesk support tickets.", []string{"query: search query", "status: ticket status"}},
	{"code_format", "Format source files with the project's formatter.", []string{"paths: space-separated file paths"}},
	{"salesforce_query", "Run a SOQL query against Salesforce.", []string{"soql: SOQL query"}},
	{"web_search", "Search the web and return the top results.", []string{"query: search terms", "count: number of results"}},
	{"airtable_list_records", "List records from an Airtable table.", []string{"base: base id", "table: table name", "filter: filter formula"}},
	{"diff_apply_patch", "Apply a unified diff patch to the workspace.", []string{"patch: unified diff"}},
	{"gdrive_search_files", "Search files in Google Drive.", []string{"query: search terms"}},
	{"coverage_report", "Report test coverage for the project.", []string{"path: source directory", "format: text or json"}},
	{"twilio_send_sms", "Send an SMS message with Twilio.", []string{"to: phone number", "body: message text"}},
	{"dependency_audit", "Audit project dependencies for known vulnerabilities.", []string{"manifest: path to the manifest file"}},
	{"bigquery_run_query", "Run a BigQuery SQL query.", []string{"sql: SQL statement", "project: GCP project id"}},
	{"code_review_comment", "Leave a review comment on a line of code.", []string{"path: file path", "line: line number", "body: comment text"}},
	{"docker_list_containers", "List running Docker containers.", []string{"all: include stopped containers"}},
	{"task_tracker_add_todo", "Add an item to the task's todo list.", []string{"text: todo text"}},
	{"aws_describe_instances", "Describe EC2 instances in a region.", []string{"region: AWS region", "filters: instance filters"}},
	{"fs_move_file", "Move or rename a file.", []string{"from: source path", "to: destination path"}},
	{"terraform_plan", "Run terraform plan in a directory.", []string{"dir: Terraform directory"}},
	{"regex_test", "Test a regular expression against sample text.", []string{"pattern: regular expression", "text: sample text"}},
	{"grafana_get_dashboard", "Get a Grafana dashboard by uid.", []string{"uid: dashboard uid"}},
	{"fs_delete_file", "Delete a file from the filesystem.", []string{"path: file path"}},
	{"mixpanel_query_events", "Query Mixpanel events.", []string{"event: event name", "from_date: start date", "to_date: end date"}},
	{"benchmark_run", "Run a performance benchmark and report timings.", []string{"target: benchmark name", "iterations: number of iterations"}},
	{"trello_create_card", "Create a card on a Trello list.", []string{"list: list id", "name: card name"}},
	{"json_validate", "Validate a JSON document against a JSON Schema.", []string{"document: JSON document", "schema: JSON Schema"}},
	{"asana_list_tasks", "List tasks in an Asana project.", []string{"project: project id", "assignee: assignee email"}},
	{"spec_lookup", "Look up a section of the project specification.", []string{"section: section name or number"}},
}

// catalogTools returns the first n catalog tools as MCP tool definitions.
func catalogTools(n int) ([]map[string]any, error) {
	if n < 0 || n > len(catalog) {
		return nil, fmt.Errorf("--catalog must be between 0 and %d, not %d", len(catalog), n)
	}
	tools := make([]map[string]any, 0, n)
	for _, t := range catalog[:n] {
		props := map[string]any{}
		for _, p := range t.props {
			name, desc, _ := strings.Cut(p, ": ")
			props[name] = map[string]string{"type": "string", "description": desc}
		}
		first, _, _ := strings.Cut(t.props[0], ": ")
		tools = append(tools, map[string]any{
			"name":        t.name,
			"description": t.desc,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": []string{first}},
		})
	}
	return tools, nil
}

// MCPStub is the hidden `jev-routing bench-mcp N` command: an MCP server over stdio
// exposing N stub tools, so bench agents have a catalog to choose from.
func MCPStub(args []string) int {
	n := -1
	if len(args) == 1 {
		n, _ = strconv.Atoi(args[0])
	}
	if n < 1 {
		fmt.Fprintln(os.Stderr, "usage: jev-routing bench-mcp N")
		return 2
	}
	if err := serveMCP(os.Stdin, os.Stdout, n); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// serveMCP answers newline-delimited JSON-RPC 2.0 until r ends.
func serveMCP(r io.Reader, w io.Writer, n int) error {
	tools, err := catalogTools(n)
	if err != nil {
		return err
	}
	out := bufio.NewWriter(w)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
				Name            string `json:"name"`
			} `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue // notifications and junk get no reply
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			version := req.Params.ProtocolVersion
			if version == "" {
				version = "2025-06-18"
			}
			resp["result"] = map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "bench-catalog", "version": "1"},
			}
		case "tools/list":
			resp["result"] = map[string]any{"tools": tools}
		case "tools/call":
			value := map[string]string{"bench_left_fact": "left=17", "bench_right_fact": "right=23"}[req.Params.Name]
			if value != "" {
				resp["result"] = map[string]any{"content": []map[string]string{{"type": "text", "text": value}}, "isError": false}
			} else {
				resp["result"] = map[string]any{
					"content": []map[string]string{{"type": "text", "text": req.Params.Name + " is not connected in this workspace; use the built-in tools."}},
					"isError": true,
				}
			}
		case "ping":
			resp["result"] = map[string]any{}
		default:
			resp["error"] = map[string]any{"code": -32601, "message": "method not found: " + req.Method}
		}
		line, _ := json.Marshal(resp)
		out.Write(append(line, '\n'))
		if err := out.Flush(); err != nil {
			return err
		}
	}
	return sc.Err()
}
