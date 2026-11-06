package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// ListNotificationChannels returns all channels.
func (s *Store) ListNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	rows, err := s.db.QueryContext(ctx, notifyCols+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotificationChannel{}
	for rows.Next() {
		c, err := s.scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetNotificationChannel returns one channel.
func (s *Store) GetNotificationChannel(ctx context.Context, id string) (*NotificationChannel, error) {
	return s.scanChannel(s.db.QueryRowContext(ctx, notifyCols+` WHERE id=?`, id))
}

// CreateNotificationChannel inserts a channel.
func (s *Store) CreateNotificationChannel(ctx context.Context, c *NotificationChannel) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO notification_channels
		(id,name,type,config_enc,key_version,events_json,enabled,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Name, c.Type, c.ConfigEnc, c.KeyVersion, c.EventsJSON, boolInt(c.Enabled), c.CreatedAt, c.UpdatedAt)
	return err
}

// UpdateNotificationChannel updates a channel.
func (s *Store) UpdateNotificationChannel(ctx context.Context, c *NotificationChannel) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notification_channels SET name=?,type=?,config_enc=?,key_version=?,events_json=?,enabled=?,updated_at=? WHERE id=?`,
		c.Name, c.Type, c.ConfigEnc, c.KeyVersion, c.EventsJSON, boolInt(c.Enabled), c.UpdatedAt, c.ID)
	return err
}

// DeleteNotificationChannel removes a channel.
func (s *Store) DeleteNotificationChannel(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notification_channels WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// EnabledChannelsForEvent filters enabled channels subscribed to event.
func (s *Store) EnabledChannelsForEvent(ctx context.Context, event string) ([]NotificationChannel, error) {
	all, err := s.ListNotificationChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := []NotificationChannel{}
	for _, c := range all {
		if !c.Enabled {
			continue
		}
		var events []string
		if err := json.Unmarshal([]byte(c.EventsJSON), &events); err != nil {
			continue
		}
		for _, e := range events {
			if e == event || e == "*" {
				out = append(out, c)
				break
			}
		}
	}
	return out, nil
}

// ClaimDelivery inserts a dedupe row. It returns false when an identical
// (channel,event_key) delivery already exists.
func (s *Store) ClaimDelivery(ctx context.Context, id, channelID, eventType, eventKey string, now int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO notification_deliveries
		(id,channel_id,event_type,event_key,status,attempts,created_at) VALUES (?,?,?,?, 'pending',0,?)`,
		id, channelID, eventType, eventKey, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// FinishDelivery records delivery outcome.
func (s *Store) FinishDelivery(ctx context.Context, id, status string, attempts int, lastErr *string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notification_deliveries SET status=?,attempts=?,last_error=?,sent_at=? WHERE id=?`, status, attempts, lastErr, now, id)
	return err
}

const notifyCols = `SELECT id,name,type,config_enc,key_version,events_json,enabled,created_at,updated_at FROM notification_channels`

func (s *Store) scanChannel(row interface{ Scan(...any) error }) (*NotificationChannel, error) {
	var c NotificationChannel
	var enabled int
	if err := row.Scan(&c.ID, &c.Name, &c.Type, &c.ConfigEnc, &c.KeyVersion, &c.EventsJSON, &enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	c.Enabled = enabled != 0
	return &c, nil
}

// InsertAudit appends an audit entry.
func (s *Store) InsertAudit(ctx context.Context, a *AuditLog) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_logs
		(id,actor_id,action,resource_type,resource_id,ip,user_agent,request_id,result,detail_json,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.ActorID, a.Action, a.ResourceType, a.ResourceID, a.IP, a.UserAgent, a.RequestID, a.Result, a.DetailJSON, a.CreatedAt)
	return err
}

// ListAudit returns audit entries newest first with a composite cursor.
func (s *Store) ListAudit(ctx context.Context, limit int, cursor int64, cursorID string) ([]AuditLog, error) {
	q := `SELECT id,actor_id,action,resource_type,resource_id,ip,user_agent,request_id,result,detail_json,created_at FROM audit_logs`
	var args []any
	if cursor > 0 {
		if cursorID == "" {
			q += ` WHERE created_at < ?`
			args = append(args, cursor)
		} else {
			q += ` WHERE (created_at < ? OR (created_at = ? AND id < ?))`
			args = append(args, cursor, cursor, cursorID)
		}
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditLog{}
	for rows.Next() {
		var a AuditLog
		var actor, resID, ip, ua, req, detail sql.NullString
		if err := rows.Scan(&a.ID, &actor, &a.Action, &a.ResourceType, &resID, &ip, &ua, &req, &a.Result, &detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.ActorID, a.ResourceID, a.IP, a.UserAgent, a.RequestID, a.DetailJSON = nullPtr(actor), nullPtr(resID), nullPtr(ip), nullPtr(ua), nullPtr(req), nullPtr(detail)
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetSetting returns a raw setting row.
func (s *Store) GetSetting(ctx context.Context, key string) (valueJSON, valueEnc *string, isSecret bool, keyVersion int, updatedAt int64, err error) {
	var vj, ve sql.NullString
	var secret int
	err = s.db.QueryRowContext(ctx, `SELECT value_json,value_enc,is_secret,key_version,updated_at FROM settings WHERE key=?`, key).
		Scan(&vj, &ve, &secret, &keyVersion, &updatedAt)
	if err != nil {
		return nil, nil, false, 0, 0, notFound(err)
	}
	return nullPtr(vj), nullPtr(ve), secret != 0, keyVersion, updatedAt, nil
}

// SetSetting inserts or replaces a setting.
func (s *Store) SetSetting(ctx context.Context, key string, valueJSON, valueEnc *string, isSecret bool, keyVersion int, now int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key,value_json,value_enc,is_secret,key_version,updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,value_enc=excluded.value_enc,is_secret=excluded.is_secret,key_version=excluded.key_version,updated_at=excluded.updated_at`,
		key, valueJSON, valueEnc, boolInt(isSecret), keyVersion, now)
	return err
}

// ListSettingsKeys returns keys (never values) for the settings screen.
func (s *Store) ListSettingsKeys(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key,is_secret FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		var secret int
		if err := rows.Scan(&k, &secret); err != nil {
			return nil, err
		}
		out[k] = secret != 0
	}
	return out, rows.Err()
}

// CreateRecoveryCode stores a hashed TOTP recovery code.
func (s *Store) CreateRecoveryCode(ctx context.Context, id, userID string, hash []byte, now int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO totp_recovery_codes (id,user_id,code_hash,created_at) VALUES (?,?,?,?)`, id, userID, hash, now)
	return err
}

// ConsumeRecoveryCode marks a matching unused hash as used.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID string, hash []byte, now int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE totp_recovery_codes SET used_at=? WHERE user_id=? AND code_hash=? AND used_at IS NULL`, now, userID, hash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DeleteRecoveryCodes removes all recovery codes for a user.
func (s *Store) DeleteRecoveryCodes(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM totp_recovery_codes WHERE user_id=?`, userID)
	return err
}
