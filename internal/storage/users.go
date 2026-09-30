package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// User is the SSO-facing identity stored by the metering service. user_id is
// immutable and is deliberately separate from the MaaS username (normally the
// email tag). Tags are strings so callers can add organization-specific
// attributes without a schema migration.
type User struct {
	UserID    string            `json:"user_id"`
	Tags      map[string]string `json:"tags"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`

	// username is the current MaaS identity used to join usage_events. It is
	// intentionally not serialized: partner callers use tags.email instead.
	username string
}

func normalizeUserTags(tags map[string]string) map[string]string {
	if tags == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(tags))
	for key, value := range tags {
		result[strings.TrimSpace(key)] = value
	}
	return result
}

func encodeUserTags(tags map[string]string) ([]byte, error) {
	normalized := normalizeUserTags(tags)
	for key := range normalized {
		if key == "" {
			return nil, fmt.Errorf("tag names must not be empty")
		}
	}
	return json.Marshal(normalized)
}

func decodeUserTags(raw string, username, firstName, lastName string) (map[string]string, error) {
	tags := map[string]string{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &tags); err != nil {
			return nil, fmt.Errorf("decode user tags: %w", err)
		}
	}
	// These defaults make old username/first_name/last_name rows appear as
	// fully tagged users until the migration has been backfilled everywhere.
	if _, ok := tags["email"]; !ok && username != "" {
		tags["email"] = username
	}
	if _, ok := tags["first_name"]; !ok && firstName != "" {
		tags["first_name"] = firstName
	}
	if _, ok := tags["last_name"]; !ok && lastName != "" {
		tags["last_name"] = lastName
	}
	return tags, nil
}

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var user User
	var rawTags string
	var username, firstName, lastName string
	if err := row.Scan(&user.UserID, &username, &rawTags, &firstName, &lastName, &user.CreatedAt, &user.UpdatedAt); err != nil {
		return User{}, err
	}
	user.username = username
	var err error
	user.Tags, err = decodeUserTags(rawTags, username, firstName, lastName)
	if err != nil {
		return User{}, err
	}
	if user.UserID == "" {
		user.UserID = username
	}
	return user, nil
}

const userSelect = `
	SELECT COALESCE(user_id, ''), username, COALESCE(tags, '{}'::jsonb)::text,
		first_name, last_name, created_at, updated_at
	FROM user_profiles`

// GetUser returns one SSO user by its immutable ID.
func (s *Store) GetUser(ctx context.Context, userID string) (User, error) {
	if strings.TrimSpace(userID) == "" {
		return User{}, sql.ErrNoRows
	}
	return scanUser(s.db.QueryRowContext(ctx, userSelect+` WHERE user_id = $1`, userID))
}

// ListUsers returns users ordered by stable ID. tagFilters are exact matches;
// search is a case-insensitive search over the serialized tag object.
func (s *Store) ListUsers(ctx context.Context, tagFilters map[string]string, search string) ([]User, error) {
	query := userSelect
	args := []any{}
	conditions := []string{}
	for key, value := range tagFilters {
		encoded, err := json.Marshal(map[string]string{key: value})
		if err != nil {
			return nil, err
		}
		args = append(args, string(encoded))
		conditions = append(conditions, fmt.Sprintf("tags @> $%d::jsonb", len(args)))
	}
	if search = strings.TrimSpace(search); search != "" {
		args = append(args, "%"+search+"%")
		conditions = append(conditions, fmt.Sprintf("tags::text ILIKE $%d", len(args)))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY user_id"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// CreateUser creates an SSO identity. The handler validates required tags;
// this layer also enforces the immutable ID and MaaS email join key.
func (s *Store) CreateUser(ctx context.Context, userID string, tags map[string]string) (User, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return User{}, fmt.Errorf("user_id is required")
	}
	tags = normalizeUserTags(tags)
	email := strings.TrimSpace(tags["email"])
	if email == "" {
		return User{}, fmt.Errorf("tags.email is required")
	}
	encoded, err := encodeUserTags(tags)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("create user transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // committed or already rolled back
	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_profiles (username, user_id, tags, first_name, last_name)
		VALUES ($1, $2, $3::jsonb, $4, $5)`,
		email, userID, string(encoded), tags["first_name"], tags["last_name"])
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	// Events may have arrived before Atlas created the directory record. Tie
	// those legacy email-keyed rows to the stable ID now so a future email
	// change cannot orphan their usage.
	if _, err := tx.ExecContext(ctx, `
		UPDATE usage_events SET user_id = $2
		WHERE user_id IS NULL AND username = $1`, email, userID); err != nil {
		return User{}, fmt.Errorf("backfill user events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit user: %w", err)
	}
	return s.GetUser(ctx, userID)
}

// UpdateUser replaces all tags while retaining the stable user ID.
func (s *Store) UpdateUser(ctx context.Context, userID string, tags map[string]string) (User, error) {
	userID = strings.TrimSpace(userID)
	tags = normalizeUserTags(tags)
	email := strings.TrimSpace(tags["email"])
	if userID == "" || email == "" {
		return User{}, fmt.Errorf("user_id and tags.email are required")
	}
	encoded, err := encodeUserTags(tags)
	if err != nil {
		return User{}, err
	}
	current, err := s.GetUser(ctx, userID)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("update user transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // committed or already rolled back
	result, err := tx.ExecContext(ctx, `
		UPDATE user_profiles
		SET username = $2, tags = $3::jsonb, first_name = $4, last_name = $5, updated_at = NOW()
		WHERE user_id = $1`, userID, email, string(encoded), tags["first_name"], tags["last_name"])
	if err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return User{}, sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE usage_events SET user_id = $1
		WHERE user_id IS NULL AND username = ANY($2)`, userID, pq.Array([]string{current.username, email})); err != nil {
		return User{}, fmt.Errorf("backfill updated user events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit user update: %w", err)
	}
	return s.GetUser(ctx, userID)
}

// PatchUser merges additional tags into the existing user.
func (s *Store) PatchUser(ctx context.Context, userID string, additional map[string]string) (User, error) {
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return User{}, err
	}
	for key, value := range additional {
		user.Tags[key] = value
	}
	return s.UpdateUser(ctx, userID, user.Tags)
}

// DeleteUser removes the user directory record. Usage events are retained;
// deletion is intentionally not a destructive ledger operation.
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM user_profiles WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetUsersByIDs returns only existing users. The report layer uses the caller's
// order when constructing its response and turns a missing identity into 404
// rather than silently returning a partial billing report.
func (s *Store) GetUsersByIDs(ctx context.Context, userIDs []string) (map[string]User, error) {
	if len(userIDs) == 0 {
		return map[string]User{}, nil
	}
	rows, err := s.db.QueryContext(ctx, userSelect+` WHERE user_id = ANY($1)`, pq.Array(userIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make(map[string]User, len(userIDs))
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users[user.UserID] = user
	}
	return users, rows.Err()
}
