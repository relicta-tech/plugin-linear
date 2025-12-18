package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/relicta-tech/relicta-plugin-sdk/plugin"
)

func TestGetInfo(t *testing.T) {
	p := &LinearPlugin{}
	info := p.GetInfo()

	if info.Name != "linear" {
		t.Errorf("expected name 'linear', got %q", info.Name)
	}

	if info.Version == "" {
		t.Error("expected non-empty version")
	}

	if len(info.Hooks) == 0 {
		t.Error("expected at least one hook")
	}

	// Check hooks include expected values
	hasPostPlan := false
	hasPostPublish := false
	for _, hook := range info.Hooks {
		if hook == plugin.HookPostPlan {
			hasPostPlan = true
		}
		if hook == plugin.HookPostPublish {
			hasPostPublish = true
		}
	}
	if !hasPostPlan {
		t.Error("expected HookPostPlan in hooks")
	}
	if !hasPostPublish {
		t.Error("expected HookPostPublish in hooks")
	}
}

func TestExtractIssues(t *testing.T) {
	tests := []struct {
		name     string
		commits  []string
		prefix   string
		expected []string
	}{
		{
			name:     "single issue",
			commits:  []string{"feat: add feature ENG-123"},
			prefix:   "",
			expected: []string{"ENG-123"},
		},
		{
			name:     "multiple issues same commit",
			commits:  []string{"fix: resolve ENG-123 and ENG-456"},
			prefix:   "",
			expected: []string{"ENG-123", "ENG-456"},
		},
		{
			name:     "multiple commits",
			commits:  []string{"feat: ENG-100", "fix: ENG-200", "chore: ENG-300"},
			prefix:   "",
			expected: []string{"ENG-100", "ENG-200", "ENG-300"},
		},
		{
			name:     "with prefix filter",
			commits:  []string{"feat: ENG-123 and TEAM-456"},
			prefix:   "ENG",
			expected: []string{"ENG-123"},
		},
		{
			name:     "duplicate issues",
			commits:  []string{"feat: ENG-123", "fix: also ENG-123"},
			prefix:   "",
			expected: []string{"ENG-123"},
		},
		{
			name:     "no issues",
			commits:  []string{"feat: add feature", "fix: bug fix"},
			prefix:   "",
			expected: []string{},
		},
		{
			name:     "different team keys",
			commits:  []string{"PROD-1", "DEV-2", "OPS-300"},
			prefix:   "",
			expected: []string{"PROD-1", "DEV-2", "OPS-300"},
		},
		{
			name:     "case insensitive prefix",
			commits:  []string{"feat: eng-123 lower case"},
			prefix:   "ENG",
			expected: []string{},
		},
		{
			name:     "issue in middle of text",
			commits:  []string{"This commit fixes ENG-999 which was broken"},
			prefix:   "",
			expected: []string{"ENG-999"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractIssues(tt.commits, tt.prefix)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d issues, got %d: %v", len(tt.expected), len(result), result)
				return
			}
			for i, expected := range tt.expected {
				if result[i] != expected {
					t.Errorf("expected issue %d to be %s, got %s", i, expected, result[i])
				}
			}
		})
	}
}

func TestRenderTemplate(t *testing.T) {
	releaseCtx := plugin.ReleaseContext{
		Version:      "1.2.3",
		TagName:      "v1.2.3",
		Branch:       "main",
		ReleaseType:  "minor",
		ReleaseNotes: "Bug fixes and improvements",
	}

	tests := []struct {
		name     string
		template string
		contains []string
	}{
		{
			name:     "version placeholder",
			template: "Release {{.Version}}",
			contains: []string{"Release 1.2.3"},
		},
		{
			name:     "multiple placeholders",
			template: "{{.Version}} on {{.Branch}}",
			contains: []string{"1.2.3 on main"},
		},
		{
			name:     "tag name",
			template: "Tag: {{.TagName}}",
			contains: []string{"Tag: v1.2.3"},
		},
		{
			name:     "release notes",
			template: "Notes: {{.ReleaseNotes}}",
			contains: []string{"Notes: Bug fixes and improvements"},
		},
		{
			name:     "release type",
			template: "Type: {{.ReleaseType}}",
			contains: []string{"Type: minor"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := renderTemplate(tt.template, releaseCtx)
			if err != nil {
				t.Fatalf("renderTemplate() error = %v", err)
			}
			for _, c := range tt.contains {
				if !strings.Contains(result, c) {
					t.Errorf("renderTemplate() = %q, want to contain %q", result, c)
				}
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	p := &LinearPlugin{}

	tests := []struct {
		name   string
		config map[string]any
		check  func(*Config) bool
	}{
		{
			name: "with all fields",
			config: map[string]any{
				"api_key":              "lin_api_test123",
				"team_id":              "team-123",
				"team_key":             "ENG",
				"project_id":           "proj-456",
				"issue_prefix":         "ENG",
				"released_state":       "Released",
				"create_release_issue": false,
				"update_linked_issues": true,
				"add_release_comment":  false,
				"comment_template":     "Custom comment",
			},
			check: func(cfg *Config) bool {
				return cfg.APIKey == "lin_api_test123" &&
					cfg.TeamID == "team-123" &&
					cfg.TeamKey == "ENG" &&
					cfg.ProjectID == "proj-456" &&
					cfg.IssuePrefix == "ENG" &&
					cfg.ReleasedState == "Released" &&
					cfg.CreateReleaseIssue == false &&
					cfg.UpdateLinkedIssues == true &&
					cfg.AddReleaseComment == false &&
					cfg.CommentTemplate == "Custom comment"
			},
		},
		{
			name:   "with defaults",
			config: map[string]any{},
			check: func(cfg *Config) bool {
				return cfg.ReleasedState == "Done" &&
					cfg.CreateReleaseIssue == true &&
					cfg.UpdateLinkedIssues == true &&
					cfg.AddReleaseComment == true &&
					cfg.CommentTemplate == "Released in {{.Version}}"
			},
		},
		{
			name: "issue prefix from team key",
			config: map[string]any{
				"team_key": "DEV",
			},
			check: func(cfg *Config) bool {
				return cfg.IssuePrefix == "DEV"
			},
		},
		{
			name: "release issue config",
			config: map[string]any{
				"release_issue": map[string]any{
					"title":    "Custom Release {{.Version}}",
					"priority": 2,
					"labels":   []any{"release", "custom"},
				},
			},
			check: func(cfg *Config) bool {
				return cfg.ReleaseIssue.Title == "Custom Release {{.Version}}" &&
					cfg.ReleaseIssue.Priority == 2 &&
					len(cfg.ReleaseIssue.Labels) == 2
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := p.parseConfig(tt.config)
			if !tt.check(cfg) {
				t.Errorf("parseConfig() did not produce expected config")
			}
		})
	}
}

func TestValidate(t *testing.T) {
	p := &LinearPlugin{}
	ctx := context.Background()

	tests := []struct {
		name      string
		config    map[string]any
		wantValid bool
	}{
		{
			name: "missing api key",
			config: map[string]any{
				"team_id": "team-123",
			},
			wantValid: false,
		},
		{
			name: "missing team",
			config: map[string]any{
				"api_key": "lin_api_test123",
			},
			wantValid: false,
		},
		{
			name: "invalid api key format",
			config: map[string]any{
				"api_key": "invalid-key",
				"team_id": "team-123",
			},
			wantValid: false,
		},
		{
			name: "invalid priority",
			config: map[string]any{
				"api_key": "lin_api_test123",
				"team_id": "team-123",
				"release_issue": map[string]any{
					"priority": 5,
				},
			},
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := p.Validate(ctx, tt.config)
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}

			isValid := len(resp.Errors) == 0
			if isValid != tt.wantValid {
				t.Errorf("Validate() valid = %v, want %v; errors: %v", isValid, tt.wantValid, resp.Errors)
			}
		})
	}
}

func TestExecutePostPlanDryRun(t *testing.T) {
	p := &LinearPlugin{}
	ctx := context.Background()

	releaseCtx := plugin.ReleaseContext{
		Version: "1.0.0",
		Branch:  "main",
		Changes: &plugin.CategorizedChanges{
			Features: []plugin.ConventionalCommit{
				{Hash: "abc", Type: "feat", Description: "Add ENG-123 feature"},
			},
			Fixes: []plugin.ConventionalCommit{
				{Hash: "def", Type: "fix", Description: "Fix ENG-456 bug"},
			},
		},
	}

	req := plugin.ExecuteRequest{
		Hook:   plugin.HookPostPlan,
		DryRun: true,
		Config: map[string]any{
			"api_key":      "lin_api_test",
			"team_key":     "ENG",
			"issue_prefix": "ENG",
		},
		Context: releaseCtx,
	}

	resp, err := p.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !resp.Success {
		t.Errorf("Execute() success = false, want true")
	}

	if !strings.Contains(resp.Message, "ENG-123") || !strings.Contains(resp.Message, "ENG-456") {
		t.Errorf("Execute() message should contain extracted issues, got: %s", resp.Message)
	}

	if outputs, ok := resp.Outputs["linked_issues"].([]string); ok {
		if len(outputs) != 2 {
			t.Errorf("Expected 2 linked issues, got %d", len(outputs))
		}
	}
}

func TestExecutePostPublishDryRun(t *testing.T) {
	p := &LinearPlugin{}
	ctx := context.Background()

	releaseCtx := plugin.ReleaseContext{
		Version:      "1.0.0",
		TagName:      "v1.0.0",
		Branch:       "main",
		ReleaseType:  "major",
		ReleaseNotes: "Release notes",
	}

	req := plugin.ExecuteRequest{
		Hook:   plugin.HookPostPublish,
		DryRun: true,
		Config: map[string]any{
			"api_key":              "lin_api_test",
			"team_key":             "ENG",
			"create_release_issue": true,
			"update_linked_issues": true,
			"add_release_comment":  true,
			"released_state":       "Done",
		},
		Context: releaseCtx,
	}

	resp, err := p.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !resp.Success {
		t.Errorf("Execute() success = false, want true")
	}

	if !strings.Contains(resp.Message, "Would create release issue") {
		t.Errorf("Execute() message should mention creating release issue, got: %s", resp.Message)
	}
}

func TestLinearClientGetViewer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "lin_api_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		response := map[string]any{
			"data": map[string]any{
				"viewer": map[string]any{
					"id":    "user-123",
					"name":  "Test User",
					"email": "test@example.com",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := &LinearClient{
		endpoint:   server.URL,
		apiKey:     "lin_api_test",
		httpClient: http.DefaultClient,
	}

	viewer, err := client.GetViewer(context.Background())
	if err != nil {
		t.Fatalf("GetViewer() error = %v", err)
	}

	if viewer.ID != "user-123" {
		t.Errorf("Expected viewer ID 'user-123', got '%s'", viewer.ID)
	}
	if viewer.Name != "Test User" {
		t.Errorf("Expected viewer name 'Test User', got '%s'", viewer.Name)
	}
}

func TestLinearClientCreateIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{
			"data": map[string]any{
				"issueCreate": map[string]any{
					"success": true,
					"issue": map[string]any{
						"id":         "issue-123",
						"identifier": "ENG-100",
						"title":      "Release v1.0.0",
						"url":        "https://linear.app/team/issue/ENG-100",
						"state": map[string]any{
							"id":   "state-1",
							"name": "Backlog",
							"type": "backlog",
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := &LinearClient{
		endpoint:   server.URL,
		apiKey:     "lin_api_test",
		httpClient: http.DefaultClient,
	}

	issue, err := client.CreateIssue(context.Background(), CreateIssueInput{
		TeamID:      "team-123",
		Title:       "Release v1.0.0",
		Description: "Release description",
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	if issue.Identifier != "ENG-100" {
		t.Errorf("Expected issue identifier 'ENG-100', got '%s'", issue.Identifier)
	}
}

func TestLinearClientUpdateIssueState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{
			"data": map[string]any{
				"issueUpdate": map[string]any{
					"success": true,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := &LinearClient{
		endpoint:   server.URL,
		apiKey:     "lin_api_test",
		httpClient: http.DefaultClient,
	}

	err := client.UpdateIssueState(context.Background(), "issue-123", "state-done")
	if err != nil {
		t.Fatalf("UpdateIssueState() error = %v", err)
	}
}

func TestLinearClientAddComment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{
			"data": map[string]any{
				"commentCreate": map[string]any{
					"success": true,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := &LinearClient{
		endpoint:   server.URL,
		apiKey:     "lin_api_test",
		httpClient: http.DefaultClient,
	}

	err := client.AddComment(context.Background(), "issue-123", "Released in v1.0.0")
	if err != nil {
		t.Fatalf("AddComment() error = %v", err)
	}
}

func TestLinearClientGetTeamByKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{
			"data": map[string]any{
				"teams": map[string]any{
					"nodes": []map[string]any{
						{
							"id":   "team-123",
							"key":  "ENG",
							"name": "Engineering",
							"states": map[string]any{
								"nodes": []map[string]any{
									{"id": "state-1", "name": "Backlog", "type": "backlog"},
									{"id": "state-2", "name": "In Progress", "type": "started"},
									{"id": "state-3", "name": "Done", "type": "completed"},
								},
							},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := &LinearClient{
		endpoint:   server.URL,
		apiKey:     "lin_api_test",
		httpClient: http.DefaultClient,
	}

	team, err := client.GetTeam(context.Background(), "", "ENG")
	if err != nil {
		t.Fatalf("GetTeam() error = %v", err)
	}

	if team.Key != "ENG" {
		t.Errorf("Expected team key 'ENG', got '%s'", team.Key)
	}
	if len(team.States) != 3 {
		t.Errorf("Expected 3 states, got %d", len(team.States))
	}
}
