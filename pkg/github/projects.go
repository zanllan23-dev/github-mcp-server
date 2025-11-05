package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	ghErrors "github.com/github/github-mcp-server/pkg/errors"
	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/go-github/v77/github"
	"github.com/google/go-querystring/query"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	ProjectUpdateFailedError = "failed to update a project item"
	ProjectAddFailedError    = "failed to add a project item"
	ProjectDeleteFailedError = "failed to delete a project item"
	ProjectListFailedError   = "failed to list project items"
	MaxProjectsPerPage       = 50
)

func ListProjects(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_projects",
			mcp.WithDescription(t("TOOL_LIST_PROJECTS_DESCRIPTION", `List Projects for a user or organization`)),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_LIST_PROJECTS_USER_TITLE", "List projects"),
				ReadOnlyHint: ToBoolPtr(true),
			}),
			mcp.WithString("owner_type",
				mcp.Required(), mcp.Description("Owner type"), mcp.Enum("user", "org"),
			),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("If owner_type == user it is the handle for the GitHub user account. If owner_type == org it is the name of the organization. The name is not case sensitive."),
			),
			mcp.WithString("query",
				mcp.Description(`Filter projects by a search query
				
Scope: title text + open/closed state.
PERMITTED qualifiers: is:open, is:closed (state), simple title terms.
FORBIDDEN: is:issue, is:pr, assignee:, label:, status:, sprint-name:, parent-issue:, team-name:, priority:, etc.
Examples:
	- roadmap is:open
	- is:open feature planning`),
			),
			mcp.WithNumber("per_page",
				mcp.Description(fmt.Sprintf("Results per page (max %d)", MaxProjectsPerPage)),
			),
			mcp.WithString("after",
				mcp.Description("Forward pagination cursor from previous pageInfo.nextCursor."),
			),
			mcp.WithString("before",
				mcp.Description("Backward pagination cursor from previous pageInfo.prevCursor (rare)."),
			),
		), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := RequiredParam[string](req, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			ownerType, err := RequiredParam[string](req, "owner_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			queryStr, err := OptionalParam[string](req, "query")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pagination, err := extractPaginationOptions(req)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			client, err := getClient(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			var resp *github.Response
			var projects []*github.ProjectV2
			var queryPtr *string

			if queryStr != "" {
				queryPtr = &queryStr
			}

			minimalProjects := []MinimalProject{}
			opts := &github.ListProjectsOptions{
				ListProjectsPaginationOptions: pagination,
				Query:                         queryPtr,
			}

			if ownerType == "org" {
				projects, resp, err = client.Projects.ListOrganizationProjects(ctx, owner, opts)
			} else {
				projects, resp, err = client.Projects.ListUserProjects(ctx, owner, opts)
			}

			if err != nil {
				return ghErrors.NewGitHubAPIErrorResponse(ctx,
					"failed to list projects",
					resp,
					err,
				), nil
			}
			defer func() { _ = resp.Body.Close() }()

			for _, project := range projects {
				minimalProjects = append(minimalProjects, *convertToMinimalProject(project))
			}

			response := map[string]any{
				"projects": minimalProjects,
				"pageInfo": buildPageInfo(resp),
			}

			r, err := json.Marshal(response)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetProject(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_project",
			mcp.WithDescription(t("TOOL_GET_PROJECT_DESCRIPTION", "Get Project for a user or org")),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_GET_PROJECT_USER_TITLE", "Get project"),
				ReadOnlyHint: ToBoolPtr(true),
			}),
			mcp.WithNumber("project_number",
				mcp.Required(),
				mcp.Description("The project's number"),
			),
			mcp.WithString("owner_type",
				mcp.Required(),
				mcp.Description("Owner type"),
				mcp.Enum("user", "org"),
			),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("If owner_type == user it is the handle for the GitHub user account. If owner_type == org it is the name of the organization. The name is not case sensitive."),
			),
		), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {

			projectNumber, err := RequiredInt(req, "project_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			owner, err := RequiredParam[string](req, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			ownerType, err := RequiredParam[string](req, "owner_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			client, err := getClient(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			var resp *github.Response
			var project *github.ProjectV2

			if ownerType == "org" {
				project, resp, err = client.Projects.GetOrganizationProject(ctx, owner, projectNumber)
			} else {
				project, resp, err = client.Projects.GetUserProject(ctx, owner, projectNumber)
			}
			if err != nil {
				return ghErrors.NewGitHubAPIErrorResponse(ctx,
					"failed to get project",
					resp,
					err,
				), nil
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get project: %s", string(body))), nil
			}

			minimalProject := convertToMinimalProject(project)
			r, err := json.Marshal(minimalProject)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func ListProjectFields(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_project_fields",
			mcp.WithDescription(t("TOOL_LIST_PROJECT_FIELDS_DESCRIPTION", "List Project fields for a user or org")),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_LIST_PROJECT_FIELDS_USER_TITLE", "List project fields"),
				ReadOnlyHint: ToBoolPtr(true),
			}),
			mcp.WithString("owner_type",
				mcp.Required(),
				mcp.Description("Owner type"),
				mcp.Enum("user", "org")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("If owner_type == user it is the handle for the GitHub user account. If owner_type == org it is the name of the organization. The name is not case sensitive."),
			),
			mcp.WithNumber("project_number",
				mcp.Required(),
				mcp.Description("The project's number."),
			),
			mcp.WithNumber("per_page",
				mcp.Description(fmt.Sprintf("Results per page (max %d)", MaxProjectsPerPage)),
			),
			mcp.WithString("after",
				mcp.Description("Forward pagination cursor from previous pageInfo.nextCursor."),
			),
			mcp.WithString("before",
				mcp.Description("Backward pagination cursor from previous pageInfo.prevCursor (rare)."),
			),
		), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := RequiredParam[string](req, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			ownerType, err := RequiredParam[string](req, "owner_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			projectNumber, err := RequiredInt(req, "project_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pagination, err := extractPaginationOptions(req)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			client, err := getClient(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			var resp *github.Response
			var projectFields []*github.ProjectV2Field

			opts := &github.ListProjectsOptions{
				ListProjectsPaginationOptions: pagination,
			}

			if ownerType == "org" {
				projectFields, resp, err = client.Projects.ListOrganizationProjectFields(ctx, owner, projectNumber, opts)
			} else {
				projectFields, resp, err = client.Projects.ListUserProjectFields(ctx, owner, projectNumber, opts)
			}

			if err != nil {
				return ghErrors.NewGitHubAPIErrorResponse(ctx,
					"failed to list project fields",
					resp,
					err,
				), nil
			}
			defer func() { _ = resp.Body.Close() }()

			response := map[string]any{
				"fields":   projectFields,
				"pageInfo": buildPageInfo(resp),
			}

			r, err := json.Marshal(response)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetProjectField(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_project_field",
			mcp.WithDescription(t("TOOL_GET_PROJECT_FIELD_DESCRIPTION", "Get Project field for a user or org")),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_GET_PROJECT_FIELD_USER_TITLE", "Get project field"),
				ReadOnlyHint: ToBoolPtr(true),
			}),
			mcp.WithString("owner_type",
				mcp.Required(),
				mcp.Description("Owner type"), mcp.Enum("user", "org")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("If owner_type == user it is the handle for the GitHub user account. If owner_type == org it is the name of the organization. The name is not case sensitive."),
			),
			mcp.WithNumber("project_number",
				mcp.Required(),
				mcp.Description("The project's number.")),
			mcp.WithNumber("field_id",
				mcp.Required(),
				mcp.Description("The field's id."),
			),
		), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := RequiredParam[string](req, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			ownerType, err := RequiredParam[string](req, "owner_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			projectNumber, err := RequiredInt(req, "project_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			fieldID, err := RequiredBigInt(req, "field_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			client, err := getClient(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			var resp *github.Response
			var projectField *github.ProjectV2Field

			if ownerType == "org" {
				projectField, resp, err = client.Projects.GetOrganizationProjectField(ctx, owner, projectNumber, fieldID)
			} else {
				projectField, resp, err = client.Projects.GetUserProjectField(ctx, owner, projectNumber, fieldID)
			}

			if err != nil {
				return ghErrors.NewGitHubAPIErrorResponse(ctx,
					"failed to get project field",
					resp,
					err,
				), nil
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get project field: %s", string(body))), nil
			}
			r, err := json.Marshal(projectField)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func ListProjectItems(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_project_items",
			mcp.WithDescription(t("TOOL_LIST_PROJECT_ITEMS_DESCRIPTION", `Search project items with advanced filtering`)),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_LIST_PROJECT_ITEMS_USER_TITLE", "List project items"),
				ReadOnlyHint: ToBoolPtr(true),
			}),
			mcp.WithString("owner_type",
				mcp.Required(),
				mcp.Description("Owner type"),
				mcp.Enum("user", "org"),
			),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("If owner_type == user it is the handle for the GitHub user account. If owner_type == org it is the name of the organization. The name is not case sensitive."),
			),
			mcp.WithNumber("project_number", mcp.Required(),
				mcp.Description("The project's number."),
			),
			mcp.WithString("query",
				mcp.Description(`Query string for advanced filtering of project items. See Projects server instructions (list_project_items query rules) for full construction heuristics, syntax essentials, qualifier glossary, pagination mandate, recovery guidance, and prohibited behaviors.`),
			),
			mcp.WithNumber("per_page",
				mcp.Description(fmt.Sprintf("Results per page (max %d)", MaxProjectsPerPage)),
			),
			mcp.WithString("after",
				mcp.Description("Forward pagination cursor from previous pageInfo.nextCursor."),
			),
			mcp.WithString("before",
				mcp.Description("Backward pagination cursor from previous pageInfo.prevCursor (rare)."),
			),
			mcp.WithArray("fields",
				mcp.Description("Field IDs to include (e.g. [\"102589\", \"985201\"]). CRITICAL: Always provide to get field values. Without this, only titles returned. Get IDs from list_project_fields first."),
				mcp.WithStringItems(),
			),
		), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := RequiredParam[string](req, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			ownerType, err := RequiredParam[string](req, "owner_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			projectNumber, err := RequiredInt(req, "project_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			queryStr, err := OptionalParam[string](req, "query")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			fields, err := OptionalBigIntArrayParam(req, "fields")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pagination, err := extractPaginationOptions(req)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			client, err := getClient(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			var resp *github.Response
			var projectItems []*github.ProjectV2Item
			var queryPtr *string

			if queryStr != "" {
				queryPtr = &queryStr
			}

			opts := &github.ListProjectItemsOptions{
				Fields: fields,
				ListProjectsOptions: github.ListProjectsOptions{
					ListProjectsPaginationOptions: pagination,
					Query:                         queryPtr,
				},
			}

			if ownerType == "org" {
				projectItems, resp, err = client.Projects.ListOrganizationProjectItems(ctx, owner, projectNumber, opts)
			} else {
				projectItems, resp, err = client.Projects.ListUserProjectItems(ctx, owner, projectNumber, opts)
			}

			if err != nil {
				return ghErrors.NewGitHubAPIErrorResponse(ctx,
					ProjectListFailedError,
					resp,
					err,
				), nil
			}
			defer func() { _ = resp.Body.Close() }()

			response := map[string]any{
				"items":    projectItems,
				"pageInfo": buildPageInfo(resp),
			}

			r, err := json.Marshal(response)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetProjectItem(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_project_item",
			mcp.WithDescription(t("TOOL_GET_PROJECT_ITEM_DESCRIPTION", "Get a specific Project item for a user or org")),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_GET_PROJECT_ITEM_USER_TITLE", "Get project item"),
				ReadOnlyHint: ToBoolPtr(true),
			}),
			mcp.WithString("owner_type",
				mcp.Required(),
				mcp.Description("Owner type"),
				mcp.Enum("user", "org"),
			),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("If owner_type == user it is the handle for the GitHub user account. If owner_type == org it is the name of the organization. The name is not case sensitive."),
			),
			mcp.WithNumber("project_number",
				mcp.Required(),
				mcp.Description("The project's number."),
			),
			mcp.WithNumber("item_id",
				mcp.Required(),
				mcp.Description("The item's ID."),
			),
			mcp.WithArray("fields",
				mcp.Description("Specific list of field IDs to include in the response (e.g. [\"102589\", \"985201\", \"169875\"]). If not provided, only the title field is included."),
				mcp.WithStringItems(),
			),
		), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := RequiredParam[string](req, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			ownerType, err := RequiredParam[string](req, "owner_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			projectNumber, err := RequiredInt(req, "project_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			itemID, err := RequiredBigInt(req, "item_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			fields, err := OptionalBigIntArrayParam(req, "fields")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			client, err := getClient(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			var url string
			if ownerType == "org" {
				url = fmt.Sprintf("orgs/%s/projectsV2/%d/items/%d", owner, projectNumber, itemID)
			} else {
				url = fmt.Sprintf("users/%s/projectsV2/%d/items/%d", owner, projectNumber, itemID)
			}

			opts := fieldSelectionOptions{}

			if len(fields) > 0 {
				opts.Fields = fields
			}

			url, err = addOptions(url, opts)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			projectItem := projectV2Item{}

			httpRequest, err := client.NewRequest("GET", url, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create request: %w", err)
			}

			resp, err := client.Do(ctx, httpRequest, &projectItem)
			if err != nil {
				return ghErrors.NewGitHubAPIErrorResponse(ctx,
					"failed to get project item",
					resp,
					err,
				), nil
			}
			defer func() { _ = resp.Body.Close() }()

			r, err := json.Marshal(projectItem)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func AddProjectItem(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("add_project_item",
			mcp.WithDescription(t("TOOL_ADD_PROJECT_ITEM_DESCRIPTION", "Add a specific Project item for a user or org")),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_ADD_PROJECT_ITEM_USER_TITLE", "Add project item"),
				ReadOnlyHint: ToBoolPtr(false),
			}),
			mcp.WithString("owner_type",
				mcp.Required(),
				mcp.Description("Owner type"), mcp.Enum("user", "org"),
			),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("If owner_type == user it is the handle for the GitHub user account. If owner_type == org it is the name of the organization. The name is not case sensitive."),
			),
			mcp.WithNumber("project_number",
				mcp.Required(),
				mcp.Description("The project's number."),
			),
			mcp.WithString("item_type",
				mcp.Required(),
				mcp.Description("The item's type, either issue or pull_request."),
				mcp.Enum("issue", "pull_request"),
			),
			mcp.WithNumber("item_id",
				mcp.Required(),
				mcp.Description("The numeric ID of the issue or pull request to add to the project."),
			),
		), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := RequiredParam[string](req, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			ownerType, err := RequiredParam[string](req, "owner_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			projectNumber, err := RequiredInt(req, "project_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			itemID, err := RequiredBigInt(req, "item_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			itemType, err := RequiredParam[string](req, "item_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if itemType != "issue" && itemType != "pull_request" {
				return mcp.NewToolResultError("item_type must be either 'issue' or 'pull_request'"), nil
			}

			client, err := getClient(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			newItem := &github.AddProjectItemOptions{
				ID:   itemID,
				Type: toNewProjectType(itemType),
			}

			var resp *github.Response
			var addedItem *github.ProjectV2Item

			if ownerType == "org" {
				addedItem, resp, err = client.Projects.AddOrganizationProjectItem(ctx, owner, projectNumber, newItem)
			} else {
				addedItem, resp, err = client.Projects.AddUserProjectItem(ctx, owner, projectNumber, newItem)
			}

			if err != nil {
				return ghErrors.NewGitHubAPIErrorResponse(ctx,
					ProjectAddFailedError,
					resp,
					err,
				), nil
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusCreated {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("%s: %s", ProjectAddFailedError, string(body))), nil
			}
			r, err := json.Marshal(addedItem)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func UpdateProjectItem(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("update_project_item",
			mcp.WithDescription(t("TOOL_UPDATE_PROJECT_ITEM_DESCRIPTION", "Update a specific Project item for a user or org")),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_UPDATE_PROJECT_ITEM_USER_TITLE", "Update project item"),
				ReadOnlyHint: ToBoolPtr(false),
			}),
			mcp.WithString("owner_type",
				mcp.Required(), mcp.Description("Owner type"),
				mcp.Enum("user", "org"),
			),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("If owner_type == user it is the handle for the GitHub user account. If owner_type == org it is the name of the organization. The name is not case sensitive."),
			),
			mcp.WithNumber("project_number",
				mcp.Required(),
				mcp.Description("The project's number."),
			),
			mcp.WithNumber("item_id",
				mcp.Required(),
				mcp.Description("The unique identifier of the project item. This is not the issue or pull request ID."),
			),
			mcp.WithObject("updated_field",
				mcp.Required(),
				mcp.Description("Object consisting of the ID of the project field to update and the new value for the field. To clear the field, set value to null. Example: {\"id\": 123456, \"value\": \"New Value\"}"),
			),
		), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := RequiredParam[string](req, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			ownerType, err := RequiredParam[string](req, "owner_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			projectNumber, err := RequiredInt(req, "project_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			itemID, err := RequiredInt(req, "item_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			rawUpdatedField, exists := req.GetArguments()["updated_field"]
			if !exists {
				return mcp.NewToolResultError("missing required parameter: updated_field"), nil
			}

			fieldValue, ok := rawUpdatedField.(map[string]any)
			if !ok || fieldValue == nil {
				return mcp.NewToolResultError("field_value must be an object"), nil
			}

			updatePayload, err := buildUpdateProjectItem(fieldValue)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			client, err := getClient(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			var projectsURL string
			if ownerType == "org" {
				projectsURL = fmt.Sprintf("orgs/%s/projectsV2/%d/items/%d", owner, projectNumber, itemID)
			} else {
				projectsURL = fmt.Sprintf("users/%s/projectsV2/%d/items/%d", owner, projectNumber, itemID)
			}
			httpRequest, err := client.NewRequest("PATCH", projectsURL, updateProjectItemPayload{
				Fields: []updateProjectItem{*updatePayload},
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create request: %w", err)
			}
			updatedItem := projectV2Item{}

			resp, err := client.Do(ctx, httpRequest, &updatedItem)
			if err != nil {
				return ghErrors.NewGitHubAPIErrorResponse(ctx,
					ProjectUpdateFailedError,
					resp,
					err,
				), nil
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("%s: %s", ProjectUpdateFailedError, string(body))), nil
			}
			r, err := json.Marshal(updatedItem)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func DeleteProjectItem(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("delete_project_item",
			mcp.WithDescription(t("TOOL_DELETE_PROJECT_ITEM_DESCRIPTION", "Delete a specific Project item for a user or org")),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_DELETE_PROJECT_ITEM_USER_TITLE", "Delete project item"),
				ReadOnlyHint: ToBoolPtr(false),
			}),
			mcp.WithString("owner_type",
				mcp.Required(),
				mcp.Description("Owner type"),
				mcp.Enum("user", "org"),
			),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("If owner_type == user it is the handle for the GitHub user account. If owner_type == org it is the name of the organization. The name is not case sensitive."),
			),
			mcp.WithNumber("project_number",
				mcp.Required(),
				mcp.Description("The project's number."),
			),
			mcp.WithNumber("item_id",
				mcp.Required(),
				mcp.Description("The internal project item ID to delete from the project (not the issue or pull request ID)."),
			),
		), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := RequiredParam[string](req, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			ownerType, err := RequiredParam[string](req, "owner_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			projectNumber, err := RequiredInt(req, "project_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			itemID, err := RequiredBigInt(req, "item_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			client, err := getClient(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			var resp *github.Response
			if ownerType == "org" {
				resp, err = client.Projects.DeleteOrganizationProjectItem(ctx, owner, projectNumber, itemID)
			} else {
				resp, err = client.Projects.DeleteUserProjectItem(ctx, owner, projectNumber, itemID)
			}

			if err != nil {
				return ghErrors.NewGitHubAPIErrorResponse(ctx,
					ProjectDeleteFailedError,
					resp,
					err,
				), nil
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusNoContent {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("%s: %s", ProjectDeleteFailedError, string(body))), nil
			}
			return mcp.NewToolResultText("project item successfully deleted"), nil
		}
}

type fieldSelectionOptions struct {
	// Specific list of field IDs to include in the response. If not provided, only the title field is included.
	// The comma tag encodes the slice as comma-separated values: fields=102589,985201,169875
	Fields []int64 `url:"fields,omitempty,comma"`
}

type updateProjectItemPayload struct {
	Fields []updateProjectItem `json:"fields"`
}

type updateProjectItem struct {
	ID    int `json:"id"`
	Value any `json:"value"`
}

type projectV2ItemFieldValue struct {
	ID       *int64 `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	DataType string `json:"data_type,omitempty"`
	Value    any    `json:"value,omitempty"`
}

type projectV2Item struct {
	ArchivedAt  *github.Timestamp          `json:"archived_at,omitempty"`
	Content     *projectV2ItemContent      `json:"content,omitempty"`
	ContentType *string                    `json:"content_type,omitempty"`
	CreatedAt   *github.Timestamp          `json:"created_at,omitempty"`
	Creator     *github.User               `json:"creator,omitempty"`
	Description *string                    `json:"description,omitempty"`
	Fields      []*projectV2ItemFieldValue `json:"fields,omitempty"`
	ID          *int64                     `json:"id,omitempty"`
	ItemURL     *string                    `json:"item_url,omitempty"`
	NodeID      *string                    `json:"node_id,omitempty"`
	ProjectURL  *string                    `json:"project_url,omitempty"`
	Title       *string                    `json:"title,omitempty"`
	UpdatedAt   *github.Timestamp          `json:"updated_at,omitempty"`
}

type projectV2ItemContent struct {
	Body        *string           `json:"body,omitempty"`
	ClosedAt    *github.Timestamp `json:"closed_at,omitempty"`
	CreatedAt   *github.Timestamp `json:"created_at,omitempty"`
	ID          *int64            `json:"id,omitempty"`
	Number      *int              `json:"number,omitempty"`
	State       *string           `json:"state,omitempty"`
	StateReason *string           `json:"stateReason,omitempty"`
	Title       *string           `json:"title,omitempty"`
	UpdatedAt   *github.Timestamp `json:"updated_at,omitempty"`
	URL         *string           `json:"url,omitempty"`
}

type pageInfo struct {
	HasNextPage     bool   `json:"hasNextPage"`
	HasPreviousPage bool   `json:"hasPreviousPage"`
	NextCursor      string `json:"nextCursor,omitempty"`
	PrevCursor      string `json:"prevCursor,omitempty"`
}

func toNewProjectType(projType string) string {
	switch strings.ToLower(projType) {
	case "issue":
		return "Issue"
	case "pull_request":
		return "PullRequest"
	default:
		return ""
	}
}

func buildUpdateProjectItem(input map[string]any) (*updateProjectItem, error) {
	if input == nil {
		return nil, fmt.Errorf("updated_field must be an object")
	}

	idField, ok := input["id"]
	if !ok {
		return nil, fmt.Errorf("updated_field.id is required")
	}

	idFieldAsFloat64, ok := idField.(float64) // JSON numbers are float64
	if !ok {
		return nil, fmt.Errorf("updated_field.id must be a number")
	}

	valueField, ok := input["value"]
	if !ok {
		return nil, fmt.Errorf("updated_field.value is required")
	}
	payload := &updateProjectItem{ID: int(idFieldAsFloat64), Value: valueField}

	return payload, nil
}

func buildPageInfo(resp *github.Response) pageInfo {
	return pageInfo{
		HasNextPage:     resp.After != "",
		HasPreviousPage: resp.Before != "",
		NextCursor:      resp.After,
		PrevCursor:      resp.Before,
	}
}

func extractPaginationOptions(request mcp.CallToolRequest) (github.ListProjectsPaginationOptions, error) {
	perPage, err := OptionalIntParamWithDefault(request, "per_page", MaxProjectsPerPage)
	if err != nil {
		return github.ListProjectsPaginationOptions{}, err
	}
	if perPage > MaxProjectsPerPage {
		perPage = MaxProjectsPerPage
	}

	after, err := OptionalParam[string](request, "after")
	if err != nil {
		return github.ListProjectsPaginationOptions{}, err
	}

	before, err := OptionalParam[string](request, "before")
	if err != nil {
		return github.ListProjectsPaginationOptions{}, err
	}

	return github.ListProjectsPaginationOptions{
		PerPage: &perPage,
		After:   &after,
		Before:  &before,
	}, nil
}

// addOptions adds the parameters in opts as URL query parameters to s. opts
// must be a struct whose fields may contain "url" tags.
func addOptions(s string, opts any) (string, error) {
	v := reflect.ValueOf(opts)
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return s, nil
	}

	origURL, err := url.Parse(s)
	if err != nil {
		return s, err
	}

	origValues := origURL.Query()

	// Use the github.com/google/go-querystring library to parse the struct
	newValues, err := query.Values(opts)
	if err != nil {
		return s, err
	}

	// Merge the values
	for key, values := range newValues {
		for _, value := range values {
			origValues.Add(key, value)
		}
	}

	origURL.RawQuery = origValues.Encode()
	return origURL.String(), nil
}

func ManageProjectItemsPrompt(t translations.TranslationHelperFunc) (tool mcp.Prompt, handler server.PromptHandlerFunc) {
	return mcp.NewPrompt("ManageProjectItems",
			mcp.WithPromptDescription(t("PROMPT_MANAGE_PROJECT_ITEMS_DESCRIPTION", "Guide for GitHub Projects V2: discovery, fields, querying, updates.")),
			mcp.WithArgument("owner", mcp.ArgumentDescription("The owner of the project (user or organization name)"), mcp.RequiredArgument()),
			mcp.WithArgument("owner_type", mcp.ArgumentDescription("Type of owner: 'user' or 'org'"), mcp.RequiredArgument()),
		), func(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			owner := request.Params.Arguments["owner"]
			ownerType := request.Params.Arguments["owner_type"]

			task := ""
			if t, exists := request.Params.Arguments["task"]; exists {
				task = fmt.Sprintf("%v", t)
			}

			messages := []mcp.PromptMessage{
				{
					Role: "system",
					Content: mcp.NewTextContent(`System guide: GitHub Projects V2.
Goal: Pick correct tool, fetch COMPLETE data (no early pagination stop), apply accurate filters, and count items correctly.

Available tools (9 total):

Read-only tools:
- list_projects: List all projects for a user/org
- get_project: Get details of a single project by project_number
- list_project_fields: List all fields in a project (CALL THIS FIRST before filtering)
- get_project_field: Get details of a single field by field_id
- list_project_items: List items (issues/PRs) in a project with filtering & field values
- get_project_item: Get a single item by item_id

Write tools:
- add_project_item: Add an issue or PR to a project
- update_project_item: Update field values for an item (status, priority, etc.)
- delete_project_item: Remove an item from a project

Core rules:
- list_projects: NEVER include item-level filters (no is:issue, assignee:, label:, etc.)
- Before filtering on fields, call list_project_fields to get field IDs
- Always paginate until pageInfo.hasNextPage=false
- Keep query, fields, per_page identical across pages
- Include field IDs on every list_project_items page if you need values
- Prefer explicit is:issue / is:pr unless mixed set requested
- Only summarize if verbs like analyze / summarize / report / overview / insights appear; otherwise enumerate

Field resolution:
- Use exact returned field names; don't invent
- Iteration synonyms map to actual existing name (Sprint → sprint:@current, etc.). If none exist, omit
- Only add filters for fields that exist and matter to the user goal

Query syntax essentials:
AND space | OR comma | NOT prefix - | quote multi-word values | hyphenate names | ranges points:1..5 | comparisons updated:>@today-7d priority:>1 | wildcards title:*crash*

Pagination pattern:
Call list_project_items → if hasNextPage true, repeat with after=nextCursor → stop only when false → then count/deduplicate

Counting:
- Items array length after full pagination (dedupe by item.id or node_id)
- Never count fields array, content, assignees, labels as separate items
- item.id = project item identifier; content.id = underlying issue/PR id

Edge handling:
Empty pages → total=0 still return pageInfo
Duplicates → keep first for totals
Missing field values → null/omit, never fabricate

Self-check: paginated? deduped? correct IDs? field names valid? summary allowed?`),
				},
				{
					Role: "user",
					Content: mcp.NewTextContent(fmt.Sprintf("I want to work with GitHub Projects for %s (owner_type: %s).%s",
						owner,
						ownerType,
						func() string {
							if task != "" {
								return fmt.Sprintf(" Focus: %s.", task)
							}
							return ""
						}())),
				},
				{
					Role:    "assistant",
					Content: mcp.NewTextContent("Start by listing projects: use list_projects tool with owner and owner_type parameters."),
				},
				{
					Role:    "user",
					Content: mcp.NewTextContent("How do I work with fields and items?"),
				},
				{
					Role: "assistant",
					Content: mcp.NewTextContent(`Fields & items workflow:
1. Call list_project_fields to get field definitions → map lowercased name -> {id,type}
2. Use only existing field names; no invention
3. Iteration mapping: pick sprint/cycle/iteration only if present (sprint:@current etc.)
4. Include only relevant fields (e.g. Priority + Label for high priority bugs)
5. Build query after resolving fields ("last week" → updated:>@today-7d)
6. Call list_project_items with query and field IDs → paginate until hasNextPage=false
7. Keep query/fields/per_page stable across all pages
8. Include field IDs on every page when you need their values
Missing field? Omit or clarify—never guess.`),
				},
				{
					Role:    "user",
					Content: mcp.NewTextContent("How do I update item field values?"),
				},
				{
					Role: "assistant",
					Content: mcp.NewTextContent(`Updating fields (update_project_item tool):
Input format: updated_field parameter with {id: <field_id>, value: <new_value>}
Examples:
- Text field: {"id":123,"value":"hello"}
- Single-select: {"id":456,"value":789} (value is option ID, not name)
- Number: {"id":321,"value":5}
- Date: {"id":654,"value":"2025-03-15"}
- Clear field: {"id":123,"value":null}

Rules:
- item_id parameter = project item ID (from list_project_items), NOT issue/PR ID
- Get field IDs from list_project_fields first
- For select/iteration fields, pass option/iteration ID as value, not the name
- To add an item first: use add_project_item tool with issue/PR ID
- To remove an item: use delete_project_item tool`),
				},
				{
					Role:    "user",
					Content: mcp.NewTextContent("Show me a workflow example."),
				},
				{
					Role: "assistant",
					Content: mcp.NewTextContent(`Workflow example:
1. list_projects → pick project_number
2. list_project_fields → build field map {name: {id, type}}
3. Build query (e.g. is:issue sprint:@current priority:high updated:>@today-7d)
4. list_project_items with field IDs → paginate fully (loop until hasNextPage=false)
5. Optional: get_project_item for specific item details
6. Optional: add_project_item to add issue/PR to project
7. Optional: update_project_item to change field values for an item
8. Optional: delete_project_item to remove from an item from project

Important:
- Iteration filter must match existing field name
- Keep fields parameter consistent across pages
- Summarize only if explicitly asked
- item_id for updates/deletes comes from list_project_items response
- content.id in items is the underlying issue/PR ID (use with add_project_item)`),
				},
				{
					Role:    "user",
					Content: mcp.NewTextContent("How do I handle pagination?"),
				},
				{
					Role: "assistant",
					Content: mcp.NewTextContent(`Pagination with list_project_items:
1. Make initial call with query, fields, per_page parameters
2. Check response pageInfo.hasNextPage
3. If true: call again with same query/fields/per_page + after=pageInfo.nextCursor
4. Repeat step 2-3 until hasNextPage=false
5. Collect all items from all pages before counting/analyzing

Critical: Do NOT change query, fields, or per_page between pages. Always include same field IDs on every page if you need field values.`),
				},
				{
					Role:    "user",
					Content: mcp.NewTextContent("How do I get more details about items?"),
				},
				{
					Role: "assistant",
					Content: mcp.NewTextContent(`Getting additional item details:

- First inspect item's content object for info, e.g. title, assignees, labels
- If additional detail is needed, and relevant fields are present from list_project_fields, include their IDs in list_project_items and request with list_project_items again.
- If more detail needed, use separate issue/PR tools`),
				},
				{
					Role: "assistant",
					Content: mcp.NewTextContent(`Query patterns for list_project_items:

Common scenarios:
- Blocked issues: is:issue (label:blocked OR status:"Blocked")
- Overdue tasks: is:issue due-date:<@today state:open
- PRs ready for review: is:pr review-status:"Ready for Review" state:open
- Stale issues: is:issue updated:<@today-30d state:open
- High priority bugs: is:issue label:bug priority:high state:open
- Team sprint PRs: is:pr team-name:"Backend Team" sprint:@current

Rules:
- Summarize only if user requests it with verbs like "analyze", "summarize", "report"
- Deduplicate by item.id before counting totals
- Quote multi-word values: status:"In Progress"
- Never invent field names or IDs - always verify with list_project_fields first
- Use explicit is:issue or is:pr unless user wants mixed items`),
				},
			}
			return &mcp.GetPromptResult{
				Messages: messages,
			}, nil
		}
}
