package github

import (
	"os"
	"slices"
	"strings"
)

// GenerateInstructions creates server instructions based on enabled toolsets
func GenerateInstructions(enabledToolsets []string) string {
	// For testing - add a flag to disable instructions
	if os.Getenv("DISABLE_INSTRUCTIONS") == "true" {
		return "" // Baseline mode
	}

	var instructions []string

	// Core instruction - always included if context toolset enabled
	if slices.Contains(enabledToolsets, "context") {
		instructions = append(instructions, "Always call 'get_me' first to understand current user permissions and context.")
	}

	// Individual toolset instructions
	for _, toolset := range enabledToolsets {
		if inst := getToolsetInstructions(toolset); inst != "" {
			instructions = append(instructions, inst)
		}
	}

	// Base instruction with context management
	baseInstruction := `The GitHub MCP Server provides tools to interact with GitHub platform.

Tool selection guidance:
	1. Use 'list_*' tools for broad, simple retrieval and pagination of all items of a type (e.g., all issues, all PRs, all branches) with basic filtering.
	2. Use 'search_*' tools for targeted queries with specific criteria, keywords, or complex filters (e.g., issues with certain text, PRs by author, code containing functions).

Context management:
	1. Use pagination whenever possible with batches of 5-10 items.
	2. Use minimal_output parameter set to true if the full information is not needed to accomplish a task.

Tool usage guidance:
	1. For 'search_*' tools: Use separate 'sort' and 'order' parameters if available for sorting results - do not include 'sort:' syntax in query strings. Query strings should contain only search criteria (e.g., 'org:google language:python'), not sorting instructions.`

	allInstructions := []string{baseInstruction}
	allInstructions = append(allInstructions, instructions...)

	return strings.Join(allInstructions, " ")
}

// getToolsetInstructions returns specific instructions for individual toolsets
func getToolsetInstructions(toolset string) string {
	switch toolset {
	case "pull_requests":
		return `## Pull Requests

PR review workflow: Always use 'pull_request_review_write' with method 'create' to create a pending review, then 'add_comment_to_pending_review' to add comments, and finally 'pull_request_review_write' with method 'submit_pending' to submit the review for complex reviews with line-specific comments.`
	case "issues":
		return `## Issues

Check 'list_issue_types' first for organizations to use proper issue types. Use 'search_issues' before creating new issues to avoid duplicates. Always set 'state_reason' when closing issues.`
	case "discussions":
		return `## Discussions
		
Use 'list_discussion_categories' to understand available categories before creating discussions. Filter by category for better organization.`
	case "projects":
		return `## Projects

Read Tools:
	- list_projects
	- get_project
	- list_project_fields
	- get_project_field
	- list_project_items
	- get_project_item
Write Tools:
	- add_project_item
	- update_project_item
	- delete_project_item

Field usage:
	- Call list_project_fields first to understand available fields and get IDs/types before filtering.
	- Use EXACT returned field names (case-insensitive match). Don't invent names or IDs.
	- Iteration synonyms (sprint/cycle/iteration) only if that field exists; map to the actual name (e.g. sprint:@current).
	- Only include filters for fields that exist and are relevant.

Pagination (mandatory):
	Forward (normal) flow:
	- Loop while pageInfo.hasNextPage=true using after=pageInfo.nextCursor.
	- Keep query, fields, per_page IDENTICAL on every page.
	Backward (rare) flow:
	- Use before=pageInfo.prevCursor only when explicitly navigating to a previous page.
	Parameters:
	- per_page: results per page (max 50). Choose a stable value; do not change mid-sequence.
	- after: forward cursor from prior response (pageInfo.nextCursor).
	- before: backward cursor from prior response (pageInfo.prevCursor); seldom needed.

Fields parameter:
	- Include field IDs on EVERY paginated list_project_items call if you need values. Omit → title only.

Counting rules:
	- Count items array length after full pagination.
	- If multi-page: collect all pages, dedupe by item.id (fallback node_id) before totals.
	- Never count field objects, content, or nested arrays as separate items.
	- item.id = project item ID (for updates/deletes). item.content.id = underlying issue/PR ID.

Summary vs list:
	- Summaries ONLY if user uses verbs: analyze | summarize | summary | report | overview | insights.
	- Listing verbs (list/show/get/fetch/display/enumerate) → enumerate + total.

Examples:
	- list_projects: "roadmap is:open"
	- list_project_items: state:open is:issue sprint:@current priority:high updated:>@today-7d

Self-check before returning:
	- Paginated fully
	- Dedupe by id/node_id
	- Correct IDs used
	- Field names valid
	- Summary only if requested.

Return COMPLETE data or state what's missing (e.g. pages skipped).`
	default:
		return ""
	}
}
