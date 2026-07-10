package leadengine

import "testing"

func TestConfigFromEnvUsesScreenFizzEnvAndTables(t *testing.T) {
	t.Setenv("SCREENFIZZ_SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("SCREENFIZZ_SUPABASE_SERVICE_ROLE_KEY", "service-key")
	t.Setenv("SCREENFIZZ_BREVO_API_KEY", "screenfizz-brevo-key")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}
	if cfg.CampaignsTable != defaultCampaignsTable {
		t.Fatalf("CampaignsTable = %q, want %q", cfg.CampaignsTable, defaultCampaignsTable)
	}
	if cfg.BusinessesTable != defaultBusinessesTable {
		t.Fatalf("BusinessesTable = %q, want %q", cfg.BusinessesTable, defaultBusinessesTable)
	}
	if cfg.SearchAreasTable != defaultSearchAreasTable {
		t.Fatalf("SearchAreasTable = %q, want %q", cfg.SearchAreasTable, defaultSearchAreasTable)
	}
	if cfg.ProspectsTable != defaultProspectsTable {
		t.Fatalf("ProspectsTable = %q, want %q", cfg.ProspectsTable, defaultProspectsTable)
	}
	if cfg.PromptsTable != defaultPromptsTable {
		t.Fatalf("PromptsTable = %q, want %q", cfg.PromptsTable, defaultPromptsTable)
	}
	if cfg.ApifyAPIURL != defaultApifyAPIURL {
		t.Fatalf("ApifyAPIURL = %q, want %q", cfg.ApifyAPIURL, defaultApifyAPIURL)
	}
	if cfg.AIAPIURL != defaultAIAPIURL || cfg.AIModel != defaultAIModel {
		t.Fatalf("AI defaults = %q, %q", cfg.AIAPIURL, cfg.AIModel)
	}
	if cfg.BrevoAPIURL != defaultBrevoAPIURL {
		t.Fatalf("BrevoAPIURL = %q, want %q", cfg.BrevoAPIURL, defaultBrevoAPIURL)
	}
	if cfg.BrevoAPIKey != "screenfizz-brevo-key" {
		t.Fatalf("BrevoAPIKey = %q", cfg.BrevoAPIKey)
	}
}
