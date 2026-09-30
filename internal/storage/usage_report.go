package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// UsageTotals is a time-window aggregate. PromptTokens includes cached and
// cache-creation tokens, matching the provider event contract; the two cache
// fields are also broken out so consumers can report their composition.
type UsageTotals struct {
	Requests            int64   `json:"requests"`
	PromptTokens        int64   `json:"promptTokens"`
	CompletionTokens    int64   `json:"completionTokens"`
	TotalTokens         int64   `json:"totalTokens"`
	CachedInputTokens   int64   `json:"cachedInputTokens"`
	CacheCreationTokens int64   `json:"cacheCreationTokens"`
	ReasoningTokens     int64   `json:"reasoningTokens"`
	EstimatedCostUSD    float64 `json:"estimatedCostUsd"`
}

// ModelUsageReport is one provider/model row in a user's usage report.
type ModelUsageReport struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	UsageTotals
}

// UserUsageReport is a read-only report. Month is populated only by the
// legacy current-month endpoint; the partner list endpoint uses the envelope
// window instead.
type UserUsageReport struct {
	UserID   string             `json:"userId,omitempty"`
	Username string             `json:"username"`
	Tags     map[string]string  `json:"tags,omitempty"`
	Month    string             `json:"month,omitempty"`
	AsOf     time.Time          `json:"asOf"`
	Totals   UsageTotals        `json:"totals"`
	Models   []ModelUsageReport `json:"models"`
}

// UsersUsageReport is the partner report envelope. The window is inclusive
// at from and exclusive at to, matching the dashboard and SQL event queries.
type UsersUsageReport struct {
	Users []UserUsageReport `json:"users"`
	From  time.Time         `json:"from"`
	To    time.Time         `json:"to"`
	AsOf  time.Time         `json:"asOf"`
}

// GetUsersUsageReport returns one report for every requested stable user ID,
// including users with no events in the selected window. The user_id column
// on usage_events preserves attribution when an SSO user changes email; the
// username fallback keeps pre-migration events visible as well.
func (s *Store) GetUsersUsageReport(ctx context.Context, userIDs []string, from, to time.Time) (UsersUsageReport, error) {
	if len(userIDs) == 0 {
		return UsersUsageReport{Users: []UserUsageReport{}, From: from, To: to, AsOf: time.Now().UTC()}, nil
	}
	users, err := s.GetUsersByIDs(ctx, userIDs)
	if err != nil {
		return UsersUsageReport{}, fmt.Errorf("resolve report users: %w", err)
	}
	if len(users) != len(userIDs) {
		return UsersUsageReport{}, fmt.Errorf("one or more users do not exist")
	}

	reports := make(map[string]*UserUsageReport, len(users))
	ordered := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		if _, exists := reports[userID]; exists {
			continue
		}
		user := users[userID]
		report := &UserUsageReport{
			UserID:   user.UserID,
			Username: user.Tags["email"],
			Tags:     user.Tags,
			AsOf:     time.Now().UTC(),
			Models:   []ModelUsageReport{},
		}
		reports[userID] = report
		ordered = append(ordered, userID)
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT up.user_id, COALESCE(e.provider, ''), COALESCE(e.model, ''),
			COUNT(e.id)::bigint,
			COALESCE(SUM(e.prompt_tokens), 0)::bigint,
			COALESCE(SUM(e.completion_tokens), 0)::bigint,
			COALESCE(SUM(e.total_tokens), 0)::bigint,
			COALESCE(SUM(e.cached_input_tokens), 0)::bigint,
			COALESCE(SUM(e.cache_creation_tokens), 0)::bigint,
			COALESCE(SUM(e.reasoning_tokens), 0)::bigint,
			COALESCE(SUM(%s), 0)
		FROM user_profiles up
		LEFT JOIN usage_events e ON
			(e.user_id = up.user_id OR
			 (e.user_id IS NULL AND e.username = COALESCE(up.tags->>'email', up.username)))
			AND e.timestamp >= $2 AND e.timestamp < $3
		LEFT JOIN model_pricing p ON e.model = p.model
		WHERE up.user_id = ANY($1)
		GROUP BY up.user_id, e.provider, e.model
		ORDER BY up.user_id, COALESCE(e.provider, ''), COALESCE(e.model, '')`, costUSDExpr),
		pq.Array(userIDs), from, to)
	if err != nil {
		return UsersUsageReport{}, fmt.Errorf("query users usage report: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var userID, provider, model string
		var usage ModelUsageReport
		if err := rows.Scan(&userID, &provider, &model,
			&usage.Requests, &usage.PromptTokens, &usage.CompletionTokens,
			&usage.TotalTokens, &usage.CachedInputTokens, &usage.CacheCreationTokens,
			&usage.ReasoningTokens, &usage.EstimatedCostUSD); err != nil {
			return UsersUsageReport{}, fmt.Errorf("scan users usage report: %w", err)
		}
		if usage.Requests == 0 {
			continue
		}
		usage.Provider = provider
		usage.Model = model
		report := reports[userID]
		report.Models = append(report.Models, usage)
		report.Totals.Requests += usage.Requests
		report.Totals.PromptTokens += usage.PromptTokens
		report.Totals.CompletionTokens += usage.CompletionTokens
		report.Totals.TotalTokens += usage.TotalTokens
		report.Totals.CachedInputTokens += usage.CachedInputTokens
		report.Totals.CacheCreationTokens += usage.CacheCreationTokens
		report.Totals.ReasoningTokens += usage.ReasoningTokens
		report.Totals.EstimatedCostUSD += usage.EstimatedCostUSD
	}
	if err := rows.Err(); err != nil {
		return UsersUsageReport{}, fmt.Errorf("iterate users usage report: %w", err)
	}

	result := UsersUsageReport{Users: make([]UserUsageReport, 0, len(ordered)), From: from, To: to, AsOf: time.Now().UTC()}
	for _, userID := range ordered {
		result.Users = append(result.Users, *reports[userID])
	}
	return result, nil
}

// GetUserUsageReport returns a read-only month-to-date usage report. It does
// not invoke entitlement logic or record quota denials. If PriceTag has linked
// multiple MaaS logins to one person, those logins are aggregated together,
// matching the dollar-quota spend calculation.
func (s *Store) GetUserUsageReport(ctx context.Context, username string) (UserUsageReport, error) {
	logins := []string{username}
	person, err := s.GetPersonByUsername(ctx, username)
	switch {
	case err == nil:
		linked, linkedErr := s.PersonUsernames(ctx, person.Slug)
		if linkedErr != nil {
			return UserUsageReport{}, fmt.Errorf("resolve linked usernames: %w", linkedErr)
		}
		if len(linked) > 0 {
			logins = linked
		}
	case err != sql.ErrNoRows:
		return UserUsageReport{}, fmt.Errorf("resolve usage identity: %w", err)
	}

	report := UserUsageReport{
		Username: username,
		Models:   []ModelUsageReport{},
	}
	if profile, profileErr := s.GetUserProfile(ctx, username); profileErr == nil {
		report.UserID = profile.UserID
		report.Tags = profile.Tags
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT to_char(date_trunc('month', NOW()), 'YYYY-MM'), NOW()
	`).Scan(&report.Month, &report.AsOf); err != nil {
		return UserUsageReport{}, fmt.Errorf("read report period: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(e.provider, ''), COALESCE(e.model, ''),
			COUNT(*)::bigint,
			COALESCE(SUM(e.prompt_tokens), 0)::bigint,
			COALESCE(SUM(e.completion_tokens), 0)::bigint,
			COALESCE(SUM(e.total_tokens), 0)::bigint,
			COALESCE(SUM(e.cached_input_tokens), 0)::bigint,
			COALESCE(SUM(e.cache_creation_tokens), 0)::bigint,
			COALESCE(SUM(e.reasoning_tokens), 0)::bigint,
			COALESCE(SUM(%s), 0)
		FROM usage_events e
		LEFT JOIN model_pricing p ON e.model = p.model
		WHERE e.username = ANY($1)
			AND e.timestamp >= date_trunc('month', NOW())
		GROUP BY COALESCE(e.provider, ''), COALESCE(e.model, '')
		ORDER BY COALESCE(e.provider, ''), COALESCE(e.model, '')`, costUSDExpr), logins)
	if err != nil {
		return UserUsageReport{}, fmt.Errorf("query user usage report: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var model ModelUsageReport
		if err := rows.Scan(
			&model.Provider, &model.Model,
			&model.Requests, &model.PromptTokens, &model.CompletionTokens,
			&model.TotalTokens, &model.CachedInputTokens, &model.CacheCreationTokens,
			&model.ReasoningTokens, &model.EstimatedCostUSD,
		); err != nil {
			return UserUsageReport{}, fmt.Errorf("scan user usage report: %w", err)
		}
		report.Models = append(report.Models, model)
		report.Totals.Requests += model.Requests
		report.Totals.PromptTokens += model.PromptTokens
		report.Totals.CompletionTokens += model.CompletionTokens
		report.Totals.TotalTokens += model.TotalTokens
		report.Totals.CachedInputTokens += model.CachedInputTokens
		report.Totals.CacheCreationTokens += model.CacheCreationTokens
		report.Totals.ReasoningTokens += model.ReasoningTokens
		report.Totals.EstimatedCostUSD += model.EstimatedCostUSD
	}
	if err := rows.Err(); err != nil {
		return UserUsageReport{}, fmt.Errorf("iterate user usage report: %w", err)
	}
	return report, nil
}
