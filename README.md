# Relicta Linear Plugin

A Relicta plugin that integrates with Linear for release tracking, issue updates, and project management.

## Features

- **Release Tracking**: Automatically create Linear issues for each release
- **Issue Linking**: Extract and link Linear issues from commit messages
- **Status Updates**: Move linked issues to a "Released" state after publishing
- **Release Comments**: Add release information as comments on linked issues
- **Custom Templates**: Customize issue titles, descriptions, and comments using Go templates

## Installation

Download the appropriate binary for your platform from the [releases page](https://github.com/relicta-tech/plugin-linear/releases).

## Configuration

Add the Linear plugin to your `relicta.yaml`:

```yaml
plugins:
  - name: linear
    enabled: true
    hooks:
      - PostPlan
      - PostPublish
    config:
      # API key (required, use environment variable)
      api_key: ${LINEAR_API_KEY}

      # Team configuration (one required)
      team_id: "your-team-uuid"
      # or
      team_key: "ENG"

      # Optional: Project to link releases to
      project_id: "project-uuid"

      # Issue prefix pattern in commits (defaults to team_key)
      issue_prefix: "ENG"

      # State to move issues to after release
      released_state: "Done"

      # Create a release tracking issue
      create_release_issue: true

      # Release issue settings
      release_issue:
        title: "Release {{.Version}}"
        description: |
          ## Release {{.Version}}

          **Released:** {{.Date}}
          **Tag:** {{.TagName}}

          ### Changes
          {{.ReleaseNotes}}
        labels:
          - "release"
        priority: 4  # 0=none, 1=urgent, 2=high, 3=medium, 4=low

      # Update linked issues
      update_linked_issues: true

      # Add release comment to linked issues
      add_release_comment: true
      comment_template: "Released in {{.Version}}"
```

## Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `LINEAR_API_KEY` | Linear API key | Yes |
| `LINEAR_TEAM_ID` | Default team ID | No |

## Getting an API Key

1. Go to Linear Settings > Account > API
2. Click "Create Key"
3. Give your key a descriptive label
4. Copy the key (it starts with `lin_api_`)
5. Store it securely as an environment variable

## Issue Linking

The plugin automatically extracts Linear issue identifiers from commit messages. Issue identifiers follow the pattern `TEAM-123` where:

- `TEAM` is a 2-10 character uppercase prefix (your team key)
- `123` is the issue number

Example commit messages:
```
feat: Add user authentication ENG-123
fix: Resolve login bug (ENG-456)
chore: Update dependencies [ENG-789]
```

## Template Variables

The following variables are available in templates:

| Variable | Description |
|----------|-------------|
| `{{.Version}}` | Release version (e.g., "1.2.3") |
| `{{.TagName}}` | Git tag name (e.g., "v1.2.3") |
| `{{.Branch}}` | Release branch name |
| `{{.ReleaseType}}` | Type of release (major, minor, patch) |
| `{{.ReleaseNotes}}` | Generated release notes |
| `{{.Date}}` | Current date (YYYY-MM-DD) |
| `{{.CommitSHA}}` | Full commit SHA |

## Hooks

| Hook | Trigger | Action |
|------|---------|--------|
| `PostPlan` | After analyzing commits | Extract linked issues from commits |
| `PostPublish` | After successful release | Create release issue, update linked issues |
| `OnError` | On release failure | Log failure (future: create failure issue) |

## Development

### Prerequisites

- Go 1.24+
- Linear API key for testing

### Building

```bash
go build -o linear .
```

### Testing

```bash
go test -v ./...
```

### Linting

```bash
golangci-lint run
```

## License

MIT License - see [LICENSE](LICENSE) for details.
