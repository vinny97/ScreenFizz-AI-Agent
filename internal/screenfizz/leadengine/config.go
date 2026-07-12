package leadengine

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

const (
	defaultCampaignsTable   = "screenfizz_campaigns"
	defaultBusinessesTable  = "screenfizz_businesses"
	defaultSearchAreasTable = "screenfizz_search_areas"
	defaultProspectsTable   = "screenfizz_prospects"
	defaultAIAPIURL         = "https://api.openai.com/v1/chat/completions"
	defaultAIModel          = "gpt-4o-mini"
	defaultBrevoAPIURL      = "https://api.brevo.com"
	defaultDailySendLimit   = 200
	defaultPromptsTable     = "screenfizz_prompts"
	defaultApifyAPIURL      = "https://api.apify.com/v2/actors/compass~crawler-google-places/runs"
)

// Config keeps ScreenFizz Lead Engine configuration separate from the
// Influocial lead engine.
type Config struct {
	SupabaseURL            string
	SupabaseServiceRoleKey string
	CampaignsTable         string
	BusinessesTable        string
	SearchAreasTable       string
	ProspectsTable         string
	AIAPIKey               string
	AIAPIURL               string
	AIModel                string
	BrevoAPIKey            string
	BrevoAPIURL            string
	BrevoSenderName        string
	BrevoSenderEmail       string
	BrevoWebhookSecret     string
	DailySendLimit         int
	DailyLeadTarget        int
	AutoApprove            bool
	PromptsTable           string
	ApifyAPIURL            string
	ApifyAPIToken          string
}

func ConfigFromEnv() (Config, error) {
	cfg := Config{
		SupabaseURL:            envOrDefault("SCREENFIZZ_SUPABASE_URL", strings.TrimSpace(os.Getenv("SUPABASE_URL"))),
		SupabaseServiceRoleKey: envOrDefault("SCREENFIZZ_SUPABASE_SERVICE_ROLE_KEY", strings.TrimSpace(os.Getenv("SUPABASE_SERVICE_ROLE_KEY"))),
		CampaignsTable:         envOrDefault("SCREENFIZZ_CAMPAIGNS_TABLE", defaultCampaignsTable),
		BusinessesTable:        envOrDefault("SCREENFIZZ_BUSINESSES_TABLE", defaultBusinessesTable),
		SearchAreasTable:       envOrDefault("SCREENFIZZ_SEARCH_AREAS_TABLE", defaultSearchAreasTable),
		ProspectsTable:         envOrDefault("SCREENFIZZ_PROSPECTS_TABLE", defaultProspectsTable),
		AIAPIKey:               envOrDefault("SCREENFIZZ_AI_API_KEY", strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))),
		AIAPIURL:               envOrDefault("SCREENFIZZ_AI_API_URL", defaultAIAPIURL),
		AIModel:                envOrDefault("SCREENFIZZ_AI_MODEL", defaultAIModel),
		BrevoAPIKey:            strings.TrimSpace(os.Getenv("SCREENFIZZ_BREVO_API_KEY")),
		BrevoAPIURL:            envOrDefault("SCREENFIZZ_BREVO_API_URL", defaultBrevoAPIURL),
		BrevoSenderName:        envOrDefault("SCREENFIZZ_SENDER_NAME", "ScreenFizz"),
		BrevoSenderEmail:       strings.TrimSpace(os.Getenv("SCREENFIZZ_SENDER_EMAIL")),
		BrevoWebhookSecret:     strings.TrimSpace(os.Getenv("SCREENFIZZ_BREVO_WEBHOOK_SECRET")),
		DailySendLimit:         envPositiveIntOrDefault("SCREENFIZZ_DAILY_SEND_LIMIT", defaultDailySendLimit),
		DailyLeadTarget:        envPositiveIntOrDefault("SCREENFIZZ_DAILY_LEAD_TARGET", 100),
		AutoApprove:            envBool("SCREENFIZZ_AUTO_APPROVE", false),
		PromptsTable:           envOrDefault("SCREENFIZZ_PROMPTS_TABLE", defaultPromptsTable),
		ApifyAPIURL:            envOrDefault("SCREENFIZZ_APIFY_API_URL", defaultApifyAPIURL),
		ApifyAPIToken:          strings.TrimSpace(os.Getenv("APIFY_API_TOKEN")),
	}
	if cfg.SupabaseURL == "" {
		return Config{}, errors.New("SCREENFIZZ_SUPABASE_URL is required")
	}
	if cfg.SupabaseServiceRoleKey == "" {
		return Config{}, errors.New("SCREENFIZZ_SUPABASE_SERVICE_ROLE_KEY is required")
	}
	return cfg, nil
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envPositiveIntOrDefault(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
