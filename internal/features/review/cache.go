package review

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
)

// cacheKey is SDD §8.10: the input hash (a section hash, or the bundle hash for a doc-scope
// step), the check or stage, the profile version, the model fingerprint, and the prompt
// version. Anything else a step depends on goes in extra.
type cacheKey struct {
	Step          string `json:"step"`
	InputHash     string `json:"input"`
	ProfileVer    int64  `json:"profile_version"`
	Fingerprint   string `json:"model"`
	PromptVersion string `json:"prompt"`
	Extra         string `json:"extra,omitempty"`
}

func (k cacheKey) hash() string {
	b, _ := json.Marshal(k)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// cached reads a step result. ok is false when there is none.
func (s *Service) cached(ctx context.Context, k cacheKey, out any) (bool, error) {
	raw, err := s.DB.Queries().GetCache(ctx, k.hash())
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// The entry stays while reviews read it (docs/specs/hosted-operations.md). A failed touch
	// costs at most an early prune, so it does not fail the review.
	now := time.Now().UTC()
	_ = s.DB.Queries().TouchCache(ctx, pgdb.TouchCacheParams{KeyHash: k.hash(), Now: now, Stale: now.Add(-24 * time.Hour)})
	return json.Unmarshal(raw, out) == nil, nil
}

// CacheDays is how long a cache entry that no review reads stays.
const CacheDays = 90

// JobKindPruneCache is the daily job that deletes the cache entries no review read for
// CacheDays.
const JobKindPruneCache = "prune_cache"

// PruneCache deletes the cache entries no review read for CacheDays.
func (s *Service) PruneCache(ctx context.Context, _ []byte) error {
	n, err := s.DB.Queries().PruneCache(ctx, time.Now().UTC().AddDate(0, 0, -CacheDays))
	if err == nil && n > 0 {
		slog.InfoContext(ctx, "pruned the review cache", "entries", n)
	}
	return err
}

func (s *Service) putCache(ctx context.Context, k cacheKey, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.DB.Queries().PutCache(ctx, pgdb.PutCacheParams{KeyHash: k.hash(), Result: dbtype.JSON(raw), CreatedAt: time.Now().UTC()})
}

// hashOf returns a hex SHA-256 of the parts.
func hashOf(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
