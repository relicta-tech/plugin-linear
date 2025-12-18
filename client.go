package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	linearAPIEndpoint = "https://api.linear.app/graphql"
	defaultTimeout    = 30 * time.Second
)

// LinearClient wraps the Linear GraphQL API.
type LinearClient struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
}

// NewLinearClient creates a new Linear API client.
func NewLinearClient(apiKey string) *LinearClient {
	return &LinearClient{
		endpoint: linearAPIEndpoint,
		apiKey:   apiKey,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
	}
}

// GraphQLRequest represents a GraphQL request.
type GraphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// GraphQLResponse represents a GraphQL response.
type GraphQLResponse struct {
	Data   json.RawMessage `json:"data,omitempty"`
	Errors []GraphQLError  `json:"errors,omitempty"`
}

// GraphQLError represents a GraphQL error.
type GraphQLError struct {
	Message    string   `json:"message"`
	Path       []string `json:"path,omitempty"`
	Extensions struct {
		Code string `json:"code,omitempty"`
	} `json:"extensions,omitempty"`
}

// Issue represents a Linear issue.
type Issue struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	State      State  `json:"state"`
	URL        string `json:"url"`
}

// State represents a workflow state.
type State struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Team represents a Linear team.
type Team struct {
	ID     string  `json:"id"`
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	States []State `json:"states"`
}

// Viewer represents the authenticated user.
type Viewer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// CreateIssueInput represents input for creating an issue.
type CreateIssueInput struct {
	TeamID      string `json:"teamId"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
	AssigneeID  string `json:"assigneeId,omitempty"`
}

// execute sends a GraphQL request to Linear.
func (c *LinearClient) execute(ctx context.Context, query string, variables map[string]any) (*GraphQLResponse, error) {
	reqBody := GraphQLRequest{
		Query:     query,
		Variables: variables,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: %s (status %d)", string(body), resp.StatusCode)
	}

	var gqlResp GraphQLResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return &gqlResp, fmt.Errorf("GraphQL error: %s", gqlResp.Errors[0].Message)
	}

	return &gqlResp, nil
}

// GetViewer returns the authenticated user.
func (c *LinearClient) GetViewer(ctx context.Context) (*Viewer, error) {
	query := `query { viewer { id name email } }`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Viewer Viewer `json:"viewer"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse viewer: %w", err)
	}

	return &result.Viewer, nil
}

// GetTeam returns a team by ID or key.
func (c *LinearClient) GetTeam(ctx context.Context, teamID, teamKey string) (*Team, error) {
	var query string
	var variables map[string]any

	if teamID != "" {
		query = `query GetTeam($id: String!) {
			team(id: $id) {
				id
				key
				name
				states {
					nodes {
						id
						name
						type
					}
				}
			}
		}`
		variables = map[string]any{"id": teamID}
	} else if teamKey != "" {
		// Use teams query and filter by key
		query = `query GetTeams {
			teams {
				nodes {
					id
					key
					name
					states {
						nodes {
							id
							name
							type
						}
					}
				}
			}
		}`
	} else {
		return nil, fmt.Errorf("either team_id or team_key is required")
	}

	resp, err := c.execute(ctx, query, variables)
	if err != nil {
		return nil, err
	}

	if teamID != "" {
		var result struct {
			Team struct {
				ID     string `json:"id"`
				Key    string `json:"key"`
				Name   string `json:"name"`
				States struct {
					Nodes []State `json:"nodes"`
				} `json:"states"`
			} `json:"team"`
		}
		if err := json.Unmarshal(resp.Data, &result); err != nil {
			return nil, fmt.Errorf("failed to parse team: %w", err)
		}

		return &Team{
			ID:     result.Team.ID,
			Key:    result.Team.Key,
			Name:   result.Team.Name,
			States: result.Team.States.Nodes,
		}, nil
	}

	// Find team by key
	var result struct {
		Teams struct {
			Nodes []struct {
				ID     string `json:"id"`
				Key    string `json:"key"`
				Name   string `json:"name"`
				States struct {
					Nodes []State `json:"nodes"`
				} `json:"states"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse teams: %w", err)
	}

	for _, t := range result.Teams.Nodes {
		if t.Key == teamKey {
			return &Team{
				ID:     t.ID,
				Key:    t.Key,
				Name:   t.Name,
				States: t.States.Nodes,
			}, nil
		}
	}

	return nil, fmt.Errorf("team with key '%s' not found", teamKey)
}

// GetIssueByIdentifier returns an issue by its identifier (e.g., ENG-123).
func (c *LinearClient) GetIssueByIdentifier(ctx context.Context, identifier string) (*Issue, error) {
	query := `query GetIssue($id: String!) {
		issue(id: $id) {
			id
			identifier
			title
			url
			state {
				id
				name
				type
			}
		}
	}`

	resp, err := c.execute(ctx, query, map[string]any{"id": identifier})
	if err != nil {
		return nil, err
	}

	var result struct {
		Issue Issue `json:"issue"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse issue: %w", err)
	}

	if result.Issue.ID == "" {
		return nil, fmt.Errorf("issue %s not found", identifier)
	}

	return &result.Issue, nil
}

// CreateIssue creates a new issue.
func (c *LinearClient) CreateIssue(ctx context.Context, input CreateIssueInput) (*Issue, error) {
	query := `mutation CreateIssue($input: IssueCreateInput!) {
		issueCreate(input: $input) {
			success
			issue {
				id
				identifier
				title
				url
				state {
					id
					name
					type
				}
			}
		}
	}`

	gqlInput := map[string]any{
		"teamId": input.TeamID,
		"title":  input.Title,
	}
	if input.Description != "" {
		gqlInput["description"] = input.Description
	}
	if input.Priority > 0 {
		gqlInput["priority"] = input.Priority
	}
	if input.ProjectID != "" {
		gqlInput["projectId"] = input.ProjectID
	}
	if input.AssigneeID != "" {
		gqlInput["assigneeId"] = input.AssigneeID
	}

	resp, err := c.execute(ctx, query, map[string]any{"input": gqlInput})
	if err != nil {
		return nil, err
	}

	var result struct {
		IssueCreate struct {
			Success bool  `json:"success"`
			Issue   Issue `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse create response: %w", err)
	}

	if !result.IssueCreate.Success {
		return nil, fmt.Errorf("failed to create issue")
	}

	return &result.IssueCreate.Issue, nil
}

// UpdateIssueState updates the state of an issue.
func (c *LinearClient) UpdateIssueState(ctx context.Context, issueID, stateID string) error {
	query := `mutation UpdateIssueState($id: String!, $input: IssueUpdateInput!) {
		issueUpdate(id: $id, input: $input) {
			success
		}
	}`

	resp, err := c.execute(ctx, query, map[string]any{
		"id":    issueID,
		"input": map[string]any{"stateId": stateID},
	})
	if err != nil {
		return err
	}

	var result struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return fmt.Errorf("failed to parse update response: %w", err)
	}

	if !result.IssueUpdate.Success {
		return fmt.Errorf("failed to update issue state")
	}

	return nil
}

// AddComment adds a comment to an issue.
func (c *LinearClient) AddComment(ctx context.Context, issueID, body string) error {
	query := `mutation AddComment($input: CommentCreateInput!) {
		commentCreate(input: $input) {
			success
		}
	}`

	resp, err := c.execute(ctx, query, map[string]any{
		"input": map[string]any{
			"issueId": issueID,
			"body":    body,
		},
	})
	if err != nil {
		return err
	}

	var result struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return fmt.Errorf("failed to parse comment response: %w", err)
	}

	if !result.CommentCreate.Success {
		return fmt.Errorf("failed to add comment")
	}

	return nil
}
