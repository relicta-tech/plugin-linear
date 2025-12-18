// Package main implements the Linear plugin for Relicta.
package main

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/relicta-tech/relicta-plugin-sdk/helpers"
	"github.com/relicta-tech/relicta-plugin-sdk/plugin"
)

// Version is set at build time.
var Version = "0.1.0"

// LinearPlugin implements the plugin.Plugin interface for Linear integration.
type LinearPlugin struct{}

// Config represents Linear plugin configuration.
type Config struct {
	APIKey             string             `json:"api_key"`
	TeamID             string             `json:"team_id"`
	TeamKey            string             `json:"team_key"`
	ProjectID          string             `json:"project_id,omitempty"`
	IssuePrefix        string             `json:"issue_prefix"`
	ReleasedState      string             `json:"released_state"`
	CreateReleaseIssue bool               `json:"create_release_issue"`
	ReleaseIssue       ReleaseIssueConfig `json:"release_issue"`
	UpdateLinkedIssues bool               `json:"update_linked_issues"`
	AddReleaseComment  bool               `json:"add_release_comment"`
	CommentTemplate    string             `json:"comment_template"`
}

// ReleaseIssueConfig contains settings for release tracking issues.
type ReleaseIssueConfig struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Labels      []string `json:"labels"`
	Priority    int      `json:"priority"`
	Assignee    string   `json:"assignee,omitempty"`
}

// GetInfo returns plugin metadata.
func (p *LinearPlugin) GetInfo() plugin.Info {
	return plugin.Info{
		Name:        "linear",
		Version:     Version,
		Description: "Linear issue tracking integration - link releases to issues and update statuses",
		Author:      "Relicta",
		Hooks: []plugin.Hook{
			plugin.HookPostPlan,
			plugin.HookPostPublish,
			plugin.HookOnError,
		},
	}
}

// Execute handles plugin execution for the specified hook.
func (p *LinearPlugin) Execute(ctx context.Context, req plugin.ExecuteRequest) (*plugin.ExecuteResponse, error) {
	cfg := p.parseConfig(req.Config)

	switch req.Hook {
	case plugin.HookPostPlan:
		return p.handlePostPlan(ctx, cfg, req.Context, req.DryRun)
	case plugin.HookPostPublish:
		return p.handlePostPublish(ctx, cfg, req.Context, req.DryRun)
	case plugin.HookOnError:
		return p.handleOnError(ctx, cfg, req.Context, req.DryRun)
	default:
		return &plugin.ExecuteResponse{
			Success: true,
			Message: fmt.Sprintf("Hook %s not implemented", req.Hook),
		}, nil
	}
}

// Validate validates the plugin configuration.
func (p *LinearPlugin) Validate(ctx context.Context, config map[string]any) (*plugin.ValidateResponse, error) {
	vb := helpers.NewValidationBuilder()
	cfg := p.parseConfig(config)

	// Validate API key
	if cfg.APIKey == "" {
		vb.AddError("api_key", "Linear API key is required")
		return vb.Build(), nil
	}

	// Validate team configuration
	if cfg.TeamID == "" && cfg.TeamKey == "" {
		vb.AddError("team_id", "Either team_id or team_key is required")
	}

	// Validate priority range
	if cfg.ReleaseIssue.Priority < 0 || cfg.ReleaseIssue.Priority > 4 {
		vb.AddError("release_issue.priority", "Priority must be between 0 and 4")
	}

	// Validate API key format (Linear API keys start with "lin_api_")
	if cfg.APIKey != "" && !strings.HasPrefix(cfg.APIKey, "lin_api_") {
		vb.AddError("api_key", "Invalid Linear API key format (should start with 'lin_api_')")
	}

	// Test API connectivity if key is provided
	if cfg.APIKey != "" && strings.HasPrefix(cfg.APIKey, "lin_api_") {
		client := NewLinearClient(cfg.APIKey)
		if _, err := client.GetViewer(ctx); err != nil {
			vb.AddError("api_key", fmt.Sprintf("Failed to authenticate with Linear: %v", err))
		}
	}

	return vb.Build(), nil
}

// parseConfig parses and applies defaults to the configuration.
func (p *LinearPlugin) parseConfig(raw map[string]any) *Config {
	parser := helpers.NewConfigParser(raw)

	cfg := &Config{
		APIKey:             parser.GetString("api_key", "LINEAR_API_KEY", ""),
		TeamID:             parser.GetString("team_id", "LINEAR_TEAM_ID", ""),
		TeamKey:            parser.GetString("team_key", "", ""),
		ProjectID:          parser.GetString("project_id", "", ""),
		IssuePrefix:        parser.GetString("issue_prefix", "", ""),
		ReleasedState:      parser.GetString("released_state", "", "Done"),
		CreateReleaseIssue: parser.GetBool("create_release_issue", true),
		UpdateLinkedIssues: parser.GetBool("update_linked_issues", true),
		AddReleaseComment:  parser.GetBool("add_release_comment", true),
		CommentTemplate:    parser.GetString("comment_template", "", "Released in {{.Version}}"),
	}

	// Parse release issue config
	if releaseIssue, ok := raw["release_issue"].(map[string]any); ok {
		riParser := helpers.NewConfigParser(releaseIssue)
		cfg.ReleaseIssue = ReleaseIssueConfig{
			Title:       riParser.GetString("title", "", "Release {{.Version}}"),
			Description: riParser.GetString("description", "", defaultReleaseDescription),
			Priority:    riParser.GetInt("priority", 4),
			Assignee:    riParser.GetString("assignee", "", ""),
		}
		if labels, ok := releaseIssue["labels"].([]any); ok {
			for _, l := range labels {
				if s, ok := l.(string); ok {
					cfg.ReleaseIssue.Labels = append(cfg.ReleaseIssue.Labels, s)
				}
			}
		}
	} else {
		cfg.ReleaseIssue = ReleaseIssueConfig{
			Title:       "Release {{.Version}}",
			Description: defaultReleaseDescription,
			Priority:    4,
			Labels:      []string{"release"},
		}
	}

	// Use team key as issue prefix if not specified
	if cfg.IssuePrefix == "" && cfg.TeamKey != "" {
		cfg.IssuePrefix = cfg.TeamKey
	}

	return cfg
}

const defaultReleaseDescription = `## Release {{.Version}}

**Released:** {{.Date}}
**Tag:** {{.TagName}}
**Type:** {{.ReleaseType}}

### Changes
{{.ReleaseNotes}}`

// handlePostPlan extracts linked issues from commits.
func (p *LinearPlugin) handlePostPlan(ctx context.Context, cfg *Config, releaseCtx plugin.ReleaseContext, dryRun bool) (*plugin.ExecuteResponse, error) {
	// Extract issues from commit messages
	var commitMessages []string
	if releaseCtx.Changes != nil {
		for _, c := range releaseCtx.Changes.Features {
			commitMessages = append(commitMessages, c.Description)
		}
		for _, c := range releaseCtx.Changes.Fixes {
			commitMessages = append(commitMessages, c.Description)
		}
		for _, c := range releaseCtx.Changes.Breaking {
			commitMessages = append(commitMessages, c.Description)
		}
		for _, c := range releaseCtx.Changes.Other {
			commitMessages = append(commitMessages, c.Description)
		}
	}

	issues := extractIssues(commitMessages, cfg.IssuePrefix)

	if len(issues) == 0 {
		return &plugin.ExecuteResponse{
			Success: true,
			Message: "No linked Linear issues found in commits",
			Outputs: map[string]any{
				"linked_issues": []string{},
			},
		}, nil
	}

	return &plugin.ExecuteResponse{
		Success: true,
		Message: fmt.Sprintf("Found %d linked Linear issues: %s", len(issues), strings.Join(issues, ", ")),
		Outputs: map[string]any{
			"linked_issues": issues,
		},
	}, nil
}

// handlePostPublish creates release issue and updates linked issues.
func (p *LinearPlugin) handlePostPublish(ctx context.Context, cfg *Config, releaseCtx plugin.ReleaseContext, dryRun bool) (*plugin.ExecuteResponse, error) {
	var results []string

	if dryRun {
		if cfg.CreateReleaseIssue {
			title, _ := renderTemplate(cfg.ReleaseIssue.Title, releaseCtx)
			results = append(results, fmt.Sprintf("Would create release issue: %s", title))
		}
		if cfg.UpdateLinkedIssues {
			results = append(results, fmt.Sprintf("Would update linked issues to state: %s", cfg.ReleasedState))
		}
		if cfg.AddReleaseComment {
			comment, _ := renderTemplate(cfg.CommentTemplate, releaseCtx)
			results = append(results, fmt.Sprintf("Would add comment to linked issues: %s", comment))
		}

		return &plugin.ExecuteResponse{
			Success: true,
			Message: strings.Join(results, "; "),
		}, nil
	}

	client := NewLinearClient(cfg.APIKey)

	// Get team info
	team, err := client.GetTeam(ctx, cfg.TeamID, cfg.TeamKey)
	if err != nil {
		return &plugin.ExecuteResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to get team: %v", err),
		}, nil
	}

	// Create release issue
	if cfg.CreateReleaseIssue {
		issue, err := p.createReleaseIssue(ctx, client, cfg, releaseCtx, team)
		if err != nil {
			return &plugin.ExecuteResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to create release issue: %v", err),
			}, nil
		}
		results = append(results, fmt.Sprintf("Created release issue: %s (%s)", issue.Identifier, issue.URL))
	}

	// Extract and update linked issues
	if cfg.UpdateLinkedIssues || cfg.AddReleaseComment {
		var commitMessages []string
		if releaseCtx.Changes != nil {
			for _, c := range releaseCtx.Changes.Features {
				commitMessages = append(commitMessages, c.Description)
			}
			for _, c := range releaseCtx.Changes.Fixes {
				commitMessages = append(commitMessages, c.Description)
			}
			for _, c := range releaseCtx.Changes.Breaking {
				commitMessages = append(commitMessages, c.Description)
			}
			for _, c := range releaseCtx.Changes.Other {
				commitMessages = append(commitMessages, c.Description)
			}
		}

		issues := extractIssues(commitMessages, cfg.IssuePrefix)
		if len(issues) > 0 {
			updated, commented, errs := p.processLinkedIssues(ctx, client, cfg, releaseCtx, team, issues)
			if updated > 0 {
				results = append(results, fmt.Sprintf("Updated %d issue(s) to '%s'", updated, cfg.ReleasedState))
			}
			if commented > 0 {
				results = append(results, fmt.Sprintf("Added release comment to %d issue(s)", commented))
			}
			if len(errs) > 0 {
				for _, e := range errs {
					results = append(results, fmt.Sprintf("Warning: %s", e))
				}
			}
		}
	}

	if len(results) == 0 {
		results = append(results, "No actions taken")
	}

	return &plugin.ExecuteResponse{
		Success: true,
		Message: strings.Join(results, "; "),
	}, nil
}

// handleOnError handles release failure notifications.
func (p *LinearPlugin) handleOnError(ctx context.Context, cfg *Config, releaseCtx plugin.ReleaseContext, dryRun bool) (*plugin.ExecuteResponse, error) {
	// For now, just log that an error occurred
	// Could be extended to create a failure tracking issue
	return &plugin.ExecuteResponse{
		Success: true,
		Message: "Release failure noted (no Linear action taken)",
	}, nil
}

// createReleaseIssue creates a new issue for tracking the release.
func (p *LinearPlugin) createReleaseIssue(ctx context.Context, client *LinearClient, cfg *Config, releaseCtx plugin.ReleaseContext, team *Team) (*Issue, error) {
	title, err := renderTemplate(cfg.ReleaseIssue.Title, releaseCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to render title template: %w", err)
	}

	description, err := renderTemplate(cfg.ReleaseIssue.Description, releaseCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to render description template: %w", err)
	}

	input := CreateIssueInput{
		TeamID:      team.ID,
		Title:       title,
		Description: description,
		Priority:    cfg.ReleaseIssue.Priority,
	}

	if cfg.ProjectID != "" {
		input.ProjectID = cfg.ProjectID
	}

	return client.CreateIssue(ctx, input)
}

// processLinkedIssues updates state and adds comments to linked issues.
func (p *LinearPlugin) processLinkedIssues(ctx context.Context, client *LinearClient, cfg *Config, releaseCtx plugin.ReleaseContext, team *Team, issueIDs []string) (updated int, commented int, errs []string) {
	// Find the released state ID
	var releasedStateID string
	if cfg.UpdateLinkedIssues && cfg.ReleasedState != "" {
		for _, state := range team.States {
			if strings.EqualFold(state.Name, cfg.ReleasedState) {
				releasedStateID = state.ID
				break
			}
		}
		if releasedStateID == "" {
			errs = append(errs, fmt.Sprintf("State '%s' not found in team workflow", cfg.ReleasedState))
		}
	}

	// Render comment template
	var comment string
	if cfg.AddReleaseComment {
		var err error
		comment, err = renderTemplate(cfg.CommentTemplate, releaseCtx)
		if err != nil {
			errs = append(errs, fmt.Sprintf("Failed to render comment template: %v", err))
			cfg.AddReleaseComment = false
		}
	}

	for _, issueID := range issueIDs {
		// Get issue details
		issue, err := client.GetIssueByIdentifier(ctx, issueID)
		if err != nil {
			errs = append(errs, fmt.Sprintf("Issue %s not found: %v", issueID, err))
			continue
		}

		// Update state
		if cfg.UpdateLinkedIssues && releasedStateID != "" {
			if err := client.UpdateIssueState(ctx, issue.ID, releasedStateID); err != nil {
				errs = append(errs, fmt.Sprintf("Failed to update %s: %v", issueID, err))
			} else {
				updated++
			}
		}

		// Add comment
		if cfg.AddReleaseComment && comment != "" {
			if err := client.AddComment(ctx, issue.ID, comment); err != nil {
				errs = append(errs, fmt.Sprintf("Failed to add comment to %s: %v", issueID, err))
			} else {
				commented++
			}
		}
	}

	return updated, commented, errs
}

// issuePattern matches Linear issue identifiers like ENG-123, TEAM-456.
var issuePattern = regexp.MustCompile(`\b([A-Z]{2,10})-(\d+)\b`)

// extractIssues extracts Linear issue identifiers from commit messages.
func extractIssues(commits []string, prefix string) []string {
	seen := make(map[string]bool)
	var issues []string

	for _, commit := range commits {
		matches := issuePattern.FindAllStringSubmatch(commit, -1)
		for _, match := range matches {
			if prefix == "" || strings.EqualFold(match[1], prefix) {
				id := match[0]
				if !seen[id] {
					seen[id] = true
					issues = append(issues, id)
				}
			}
		}
	}
	return issues
}

// templateData provides data for template rendering.
type templateData struct {
	Version      string
	TagName      string
	Branch       string
	ReleaseType  string
	ReleaseNotes string
	Date         string
	CommitSHA    string
}

// renderTemplate renders a Go template with release context.
func renderTemplate(tmplStr string, ctx plugin.ReleaseContext) (string, error) {
	tmpl, err := template.New("").Parse(tmplStr)
	if err != nil {
		return "", err
	}

	data := templateData{
		Version:      ctx.Version,
		TagName:      ctx.TagName,
		Branch:       ctx.Branch,
		ReleaseType:  ctx.ReleaseType,
		ReleaseNotes: ctx.ReleaseNotes,
		Date:         time.Now().Format("2006-01-02"),
		CommitSHA:    ctx.CommitSHA,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}
