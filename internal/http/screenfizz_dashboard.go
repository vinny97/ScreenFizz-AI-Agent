package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	screenfizz "github.com/nextlevelbuilder/goclaw/internal/screenfizz/leadengine"
)

// ScreenFizzDashboardHandler exposes the internal, review-only ScreenFizz CRM.
type ScreenFizzDashboardHandler struct{}

func NewScreenFizzDashboardHandlerFromEnv() *ScreenFizzDashboardHandler {
	return &ScreenFizzDashboardHandler{}
}

func (h *ScreenFizzDashboardHandler) RegisterRoutes(mux *stdhttp.ServeMux) {
	mux.HandleFunc("GET /v1/screenfizz/dashboard", requireAuth(permissions.RoleAdmin, h.handleDashboard))
	mux.HandleFunc("PATCH /v1/screenfizz/prospects/{id}", requireAuth(permissions.RoleAdmin, h.handleProspectUpdate))
}

func (h *ScreenFizzDashboardHandler) handleDashboard(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if !requireMasterScope(w, r) {
		return
	}
	cfg, err := screenfizz.ConfigFromEnv()
	if err != nil {
		writeJSON(w, stdhttp.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	businesses, err := screenFizzRows(r.Context(), cfg, cfg.BusinessesTable, "*")
	if err != nil {
		writeJSON(w, stdhttp.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	prospects, err := screenFizzRows(r.Context(), cfg, cfg.ProspectsTable, "*,screenfizz_businesses(*)")
	if err != nil {
		writeJSON(w, stdhttp.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	stats := map[string]int{"businesses_found": len(businesses), "prospects": len(prospects)}
	for _, p := range prospects {
		status, _ := p["status"].(string)
		switch status {
		case "pending_review":
			stats["pending_review"]++
		case "approved":
			stats["approved"]++
		case "sent":
			stats["sent"]++
		case "replied":
			stats["replies"]++
		}
		if generated, _ := p["email_generated"].(bool); generated {
			stats["emails_generated"]++
		}
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"stats": stats, "businesses": businesses, "prospects": prospects})
}

func (h *ScreenFizzDashboardHandler) handleProspectUpdate(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if !requireMasterScope(w, r) {
		return
	}
	cfg, err := screenfizz.ConfigFromEnv()
	if err != nil {
		writeJSON(w, stdhttp.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	var input struct {
		Status       string  `json:"status"`
		EmailSubject *string `json:"email_subject"`
		EmailBody    *string `json:"email_body"`
		Regenerate   bool    `json:"regenerate"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	values := map[string]any{}
	if input.EmailSubject != nil {
		values["email_subject"] = *input.EmailSubject
	}
	if input.EmailBody != nil {
		values["email_body"] = *input.EmailBody
	}
	if input.Regenerate {
		values["email_generated"] = false
		values["status"] = "pending_review"
	} else if input.Status != "" {
		switch input.Status {
		case "pending_review", "approved", "skipped":
			values["status"] = input.Status
		default:
			writeJSON(w, stdhttp.StatusBadRequest, map[string]string{"error": "invalid status"})
			return
		}
	}
	if len(values) == 0 {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]string{"error": "no changes supplied"})
		return
	}
	if err := screenFizzPatch(r.Context(), cfg, r.PathValue("id"), values); err != nil {
		writeJSON(w, stdhttp.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if input.Regenerate {
		if err := screenfizz.GenerateProspectEmails(r.Context(), cfg); err != nil {
			writeJSON(w, stdhttp.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, stdhttp.StatusOK, map[string]bool{"ok": true})
}

func screenFizzRows(ctx context.Context, cfg screenfizz.Config, table, selectFields string) ([]map[string]any, error) {
	u := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(table)
	q := url.Values{"select": {selectFields}, "order": {"created_at.desc"}, "limit": {"1000"}}
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, u+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", cfg.SupabaseServiceRoleKey)
	req.Header.Set("Authorization", "Bearer "+cfg.SupabaseServiceRoleKey)
	resp, err := (&stdhttp.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func screenFizzPatch(ctx context.Context, cfg screenfizz.Config, id string, values map[string]any) error {
	body, err := json.Marshal(values)
	if err != nil {
		return err
	}
	u := strings.TrimRight(cfg.SupabaseURL, "/") + "/rest/v1/" + url.PathEscape(cfg.ProspectsTable) + "?id=eq." + url.QueryEscape(id)
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPatch, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("apikey", cfg.SupabaseServiceRoleKey)
	req.Header.Set("Authorization", "Bearer "+cfg.SupabaseServiceRoleKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&stdhttp.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}
