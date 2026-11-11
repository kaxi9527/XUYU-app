package modelhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

func safeLabel(value string, max int) bool {
	return value != "" && strings.TrimSpace(value) == value && len([]rune(value)) <= max && !strings.ContainsFunc(value, unicode.IsControl)
}

func (c *Client) ListModels(ctx context.Context, target models.Target, secret *credentials.Secret, cursor string) (models.ModelPage, error) {
	target, err := models.NormalizeTarget(target)
	if err != nil || len(cursor) > 2048 || strings.ContainsFunc(cursor, unicode.IsControl) {
		return models.ModelPage{}, problem("invalid_request")
	}
	address := target.BaseURL + "/models"
	query := url.Values{}
	switch target.Protocol {
	case models.Anthropic:
		query.Set("limit", "100")
		if cursor != "" {
			query.Set("after_id", cursor)
		}
	case models.Gemini:
		query.Set("pageSize", "100")
		if cursor != "" {
			query.Set("pageToken", cursor)
		}
	default:
		if cursor != "" {
			return models.ModelPage{}, problem("list_unsupported")
		}
	}
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	data, err := c.request(ctx, http.MethodGet, address, target, secret, nil)
	if err != nil {
		var failure *models.ConnectionProblem
		if errors.As(err, &failure) && (failure.HTTPStatus == 404 || failure.HTTPStatus == 405 || failure.HTTPStatus == 501) {
			return models.ModelPage{}, &models.ConnectionProblem{Code: "list_unsupported", HTTPStatus: failure.HTTPStatus}
		}
		return models.ModelPage{}, err
	}
	var envelope struct {
		Data          json.RawMessage `json:"data"`
		Models        json.RawMessage `json:"models"`
		HasMore       bool            `json:"has_more"`
		LastID        string          `json:"last_id"`
		NextPageToken string          `json:"nextPageToken"`
	}
	if err = object(data, &envelope); err != nil {
		return models.ModelPage{}, err
	}
	raw := envelope.Data
	if target.Protocol == models.Gemini {
		raw = envelope.Models
	}
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return models.ModelPage{}, problem("invalid_response")
	}
	var records []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		ClaudeName  string `json:"display_name"`
	}
	if json.Unmarshal(raw, &records) != nil {
		return models.ModelPage{}, problem("invalid_response")
	}
	page := models.ModelPage{Models: []models.AvailableModel{}}
	if len(records) > 1000 {
		records = records[:1000]
		page.Truncated = true
	}
	seen := map[string]bool{}
	for _, record := range records {
		id, name := record.ID, record.ClaudeName
		if target.Protocol == models.Gemini {
			id = record.Name
			name = record.DisplayName
		}
		if !safeLabel(id, 256) {
			return models.ModelPage{}, problem("invalid_response")
		}
		if name == "" {
			name = id
		}
		if !safeLabel(name, 256) {
			return models.ModelPage{}, problem("invalid_response")
		}
		if !seen[id] {
			page.Models = append(page.Models, models.AvailableModel{ID: id, Name: name})
			seen[id] = true
		}
	}
	if target.Protocol == models.Anthropic && envelope.HasMore && !page.Truncated {
		page.NextCursor = envelope.LastID
	}
	if target.Protocol == models.Gemini && !page.Truncated {
		page.NextCursor = envelope.NextPageToken
	}
	if len(page.NextCursor) > 2048 || strings.ContainsFunc(page.NextCursor, unicode.IsControl) || page.NextCursor != "" && page.NextCursor == cursor {
		return models.ModelPage{}, problem("invalid_response")
	}
	if target.Protocol == models.Anthropic && envelope.HasMore && page.NextCursor == "" || target.Protocol != models.Anthropic && target.Protocol != models.Gemini && envelope.HasMore {
		page.Truncated = true
	}
	return page, nil
}
