package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// PermissionBackup represents a backup entry for the chat_permissions table.
type PermissionBackup struct {
	Phone   string `json:"phone"`
	Name    string `json:"name"`
	Allowed int    `json:"allowed"`
}

// GetWhitelistDefault queries the global whitelist default flag (0 = denied by default, 1 = allowed).
func (s *Store) GetWhitelistDefault() (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var def int
	err := s.db.QueryRow(`SELECT allowed FROM whitelist_default WHERE id = 1`).Scan(&def)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("store: get whitelist default: %w", err)
	}
	return def, nil
}

// SetWhitelistDefault updates or inserts the global whitelist default flag.
func (s *Store) SetWhitelistDefault(allowed int) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO whitelist_default (id, allowed) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET allowed = excluded.allowed
	`, allowed)
	return err
}

// GetAllowedPermissions returns a map of phone -> name for contacts with allowed = 1.
func (s *Store) GetAllowedPermissions() (map[string]string, error) {
	if s == nil || s.db == nil {
		return map[string]string{}, nil
	}
	rows, err := s.db.Query(`SELECT phone, name FROM chat_permissions WHERE allowed = 1`)
	if err != nil {
		return nil, fmt.Errorf("store: query allowed permissions: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var phone, name string
		if err := rows.Scan(&phone, &name); err == nil && phone != "" {
			out[phone] = name
		}
	}
	return out, rows.Err()
}

// GetPermission returns the allowed state and whether a row exists for the given phone.
func (s *Store) GetPermission(phone string) (allowed int, exists bool, err error) {
	if s == nil || s.db == nil || phone == "" {
		return 0, false, nil
	}
	err = s.db.QueryRow(`SELECT allowed FROM chat_permissions WHERE phone = ?`, phone).Scan(&allowed)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	return allowed, true, nil
}

// SetPermission inserts or updates the permission for a phone.
func (s *Store) SetPermission(phone, name string, allowed int) error {
	if s == nil || s.db == nil || phone == "" {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO chat_permissions (phone, name, allowed) VALUES (?, ?, ?)
		ON CONFLICT(phone) DO UPDATE SET allowed = excluded.allowed,
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE chat_permissions.name END
	`, phone, name, allowed)
	return err
}

// BackupPermissions reads all rows from chat_permissions.
func (s *Store) BackupPermissions() ([]PermissionBackup, bool) {
	if s == nil || s.db == nil {
		return nil, false
	}
	rows, err := s.db.Query(`SELECT phone, name, allowed FROM chat_permissions`)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	var backups []PermissionBackup
	for rows.Next() {
		var b PermissionBackup
		if err := rows.Scan(&b.Phone, &b.Name, &b.Allowed); err == nil {
			backups = append(backups, b)
		}
	}
	return backups, true
}

// RestorePermissions populates chat_permissions from a backup slice.
func (s *Store) RestorePermissions(backups []PermissionBackup) error {
	if s == nil || s.db == nil || len(backups) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO chat_permissions (phone, name, allowed) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, b := range backups {
		if _, err := stmt.Exec(b.Phone, b.Name, b.Allowed); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

// PurgeContactsWithName removes un-stored push-name contacts matching a given name.
func (s *Store) PurgeContactsWithName(name string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, nil
	}
	res, err := s.db.Exec(`DELETE FROM contacts WHERE notify = ? AND stored = 0`, name)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgePermissionName clears chat_permissions.name rows that match the given name.
func (s *Store) PurgePermissionName(name string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, nil
	}
	res, err := s.db.Exec(`UPDATE chat_permissions SET name = '' WHERE name = ?`, name)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
