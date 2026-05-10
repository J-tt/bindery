package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vavallee/bindery/internal/models"
)

type ABSMetadataConflictRepo struct {
	db *sql.DB
}

func NewABSMetadataConflictRepo(db *sql.DB) *ABSMetadataConflictRepo {
	return &ABSMetadataConflictRepo{db: db}
}

func (r *ABSMetadataConflictRepo) GetByID(ctx context.Context, id int64) (*models.ABSMetadataConflict, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, source_id, library_id, item_id, entity_type, local_id, field_name,
		       abs_value, upstream_value, applied_source, preferred_source, resolution_status,
		       created_at, updated_at
		FROM abs_metadata_conflicts
		WHERE id = ?`, id)
	return scanABSMetadataConflict(row, "get abs metadata conflict")
}

func (r *ABSMetadataConflictRepo) GetByEntityField(ctx context.Context, entityType string, localID int64, fieldName string) (*models.ABSMetadataConflict, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, source_id, library_id, item_id, entity_type, local_id, field_name,
		       abs_value, upstream_value, applied_source, preferred_source, resolution_status,
		       created_at, updated_at
		FROM abs_metadata_conflicts
		WHERE entity_type = ? AND local_id = ? AND field_name = ?`,
		entityType, localID, fieldName)
	return scanABSMetadataConflict(row, "get abs metadata conflict by entity+field")
}

func (r *ABSMetadataConflictRepo) List(ctx context.Context) ([]models.ABSMetadataConflict, error) {
	items, _, err := r.ListPaginated(ctx, 0, 0)
	return items, err
}

func (r *ABSMetadataConflictRepo) ListPaginated(ctx context.Context, limit, offset int) ([]models.ABSMetadataConflict, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM abs_metadata_conflicts`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count abs metadata conflicts: %w", err)
	}

	query := `
		SELECT id, source_id, library_id, item_id, entity_type, local_id, field_name,
		       abs_value, upstream_value, applied_source, preferred_source, resolution_status,
		       created_at, updated_at
		FROM abs_metadata_conflicts
		ORDER BY CASE resolution_status WHEN 'unresolved' THEN 0 ELSE 1 END, updated_at DESC, id DESC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
		if offset > 0 {
			query += ` OFFSET ?`
			args = append(args, offset)
		}
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list abs metadata conflicts: %w", err)
	}
	defer rows.Close()

	var out []models.ABSMetadataConflict
	for rows.Next() {
		item, err := scanABSMetadataConflict(rows, "scan abs metadata conflict")
		if err != nil {
			return nil, 0, err
		}
		if item != nil {
			out = append(out, *item)
		}
	}
	return out, total, rows.Err()
}

// Claim atomically transitions a conflict from "pending" to "resolving" so
// that concurrent Resolve calls cannot both apply conflicting entity writes.
// Returns true if the claim succeeded (this caller owns the conflict), false if
// another caller already claimed or resolved it.
func (r *ABSMetadataConflictRepo) Claim(ctx context.Context, id int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE abs_metadata_conflicts SET resolution_status = 'resolving', updated_at = ? WHERE id = ? AND resolution_status IN ('pending', 'unresolved')`,
		time.Now().UTC(), id)
	if err != nil {
		return false, fmt.Errorf("claim abs metadata conflict %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim abs metadata conflict %d rows affected: %w", id, err)
	}
	return n == 1, nil
}

// Unclaim reverts a conflict from "resolving" back to "unresolved". Called when
// the entity update inside Resolve fails after a successful Claim.
func (r *ABSMetadataConflictRepo) Unclaim(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE abs_metadata_conflicts SET resolution_status = 'unresolved', updated_at = ? WHERE id = ? AND resolution_status = 'resolving'`,
		time.Now().UTC(), id)
	return err
}

func (r *ABSMetadataConflictRepo) Upsert(ctx context.Context, c *models.ABSMetadataConflict) error {
	now := time.Now().UTC()
	if c.SourceID == "" {
		c.SourceID = "default"
	}
	if c.ResolutionStatus == "" {
		c.ResolutionStatus = "unresolved"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO abs_metadata_conflicts (
			source_id, library_id, item_id, entity_type, local_id, field_name,
			abs_value, upstream_value, applied_source, preferred_source, resolution_status,
			created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(entity_type, local_id, field_name) DO UPDATE SET
			source_id         = excluded.source_id,
			library_id        = excluded.library_id,
			item_id           = excluded.item_id,
			abs_value         = excluded.abs_value,
			upstream_value    = excluded.upstream_value,
			applied_source    = excluded.applied_source,
			preferred_source  = excluded.preferred_source,
			resolution_status = excluded.resolution_status,
			updated_at        = excluded.updated_at`,
		c.SourceID, c.LibraryID, c.ItemID, c.EntityType, c.LocalID, c.FieldName,
		c.ABSValue, c.UpstreamValue, c.AppliedSource, c.PreferredSource, c.ResolutionStatus,
		now, now)
	if err != nil {
		return fmt.Errorf("upsert abs metadata conflict %s/%d/%s: %w", c.EntityType, c.LocalID, c.FieldName, err)
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT id, created_at, updated_at
		FROM abs_metadata_conflicts
		WHERE entity_type = ? AND local_id = ? AND field_name = ?`,
		c.EntityType, c.LocalID, c.FieldName)
	if err := row.Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return fmt.Errorf("reload abs metadata conflict %s/%d/%s: %w", c.EntityType, c.LocalID, c.FieldName, err)
	}
	return nil
}

type absMetadataConflictScanner interface {
	Scan(dest ...any) error
}

func scanABSMetadataConflict(scanner absMetadataConflictScanner, caller string) (*models.ABSMetadataConflict, error) {
	var item models.ABSMetadataConflict
	if err := scanner.Scan(
		&item.ID, &item.SourceID, &item.LibraryID, &item.ItemID, &item.EntityType, &item.LocalID, &item.FieldName,
		&item.ABSValue, &item.UpstreamValue, &item.AppliedSource, &item.PreferredSource, &item.ResolutionStatus,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	return &item, nil
}
