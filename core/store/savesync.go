package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Save-sync custody: worlds, sessions and versions
// (docs/save-sync-architecture.md). The active session row IS the lock —
// the sync_sessions_one_active partial index makes the database refuse a
// second checkout, so custody survives console restarts and never lives
// in process memory the way the backup runner's busy flag does.

// Sync session statuses. "Expired" is deliberately not among them: an
// active session past its expiry stays active (the holder may still check
// in normally); expiry only makes the hold claimable by someone else.
const (
	SyncActive    = "active"
	SyncReturned  = "returned"  // checked back in by its holder
	SyncReclaimed = "reclaimed" // taken over after expiry
	SyncReleased  = "released"  // force-released by an admin
)

// Sync version kinds.
const (
	SyncKindCheckin    = "checkin"
	SyncKindCheckpoint = "checkpoint" // mid-session safety push; never the head
	SyncKindImport     = "import"     // seeded outside a session (portal upload, server pull)
)

// ErrWorldHeld is the unique-active-session index refusing a second
// checkout.
var ErrWorldHeld = errors.New("this world is already checked out")

type SyncWorld struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Game metadata, reported by the companion app that discovered the
	// game: a display title, where the save lived on the reporter's
	// machine (a setup hint for the next player, never followed blindly),
	// and free-form JSON the reporter shaped (Steam app id, install
	// name). The server stores and shows these; it never interprets them.
	GameTitle string `json:"gameTitle"`
	SaveHint  string `json:"saveHint"`
	GameMeta  string `json:"gameMeta"`
	// SavePath is the folder this world lives in, relative to each
	// player's own save root — the opaque leaf an Unreal game generates
	// ("K2hAc0p_LH74aymwOemkgg"), which every player of the world shares
	// and none of them can retype. Slash-separated, always relative;
	// empty when the save root is itself the world's folder. A joining
	// player picks their root and their companion recreates this beneath
	// it (migration 0027).
	SavePath string `json:"savePath"`
	// The sidecar agent that can also hold this world (the give/take
	// flows), when the game has a dedicated server. The token is
	// decrypted here, encrypted at rest, and never serialized.
	AgentURL   string `json:"agentUrl"`
	AgentToken string `json:"-"`
	// HasAgentToken is what the UI may know about the credential.
	HasAgentToken bool   `json:"hasAgentToken"`
	LeaseHours    int    `json:"leaseHours"`
	MaxBytes      int64  `json:"maxBytes"`
	KeepVersions  int    `json:"keepVersions"`
	Checkpoints   bool   `json:"checkpoints"`
	WebhookURL    string `json:"webhookUrl"`
	// HeadVersion is the canonical current version; nil until something
	// is checked in or imported.
	HeadVersion *int64 `json:"headVersion,omitempty"`
	// NextHolder is the claim-next queue of exactly one.
	NextHolder *int64    `json:"nextHolder,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type SyncSession struct {
	ID           int64      `json:"id"`
	WorldID      int64      `json:"worldId"`
	HolderID     int64      `json:"holderId"`
	ServerHeld   bool       `json:"serverHeld"`
	BaseVersion  *int64     `json:"baseVersion,omitempty"`
	Status       string     `json:"status"`
	CheckedOutAt time.Time  `json:"checkedOutAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	WarnedAt     *time.Time `json:"warnedAt,omitempty"`
	EndedAt      *time.Time `json:"endedAt,omitempty"`
	EndedBy      *int64     `json:"endedBy,omitempty"`
	// An admin's standing request that this holder's companion hand the
	// world back: "checkpoint" (capture, keep the hold) or "checkin"
	// (capture and end it). Empty means nothing is pending. The
	// companion sees it on its next poll — it cannot be reached any
	// other way (migration 0028).
	RequestedKind string     `json:"requestedKind,omitempty"`
	RequestedAt   *time.Time `json:"requestedAt,omitempty"`
	RequestedBy   *int64     `json:"requestedBy,omitempty"`
}

// Expired reports whether the hold is past its lease — claimable, not
// ended.
func (s *SyncSession) Expired(now time.Time) bool {
	return s.Status == SyncActive && now.After(s.ExpiresAt)
}

type SyncVersion struct {
	ID         int64     `json:"id"`
	WorldID    int64     `json:"worldId"`
	SessionID  *int64    `json:"sessionId,omitempty"`
	ParentID   *int64    `json:"parentId,omitempty"`
	Kind       string    `json:"kind"`
	Conflict   bool      `json:"conflict"`
	Bytes      int64     `json:"bytes"`
	SHA256     string    `json:"sha256"`
	UploaderID *int64    `json:"uploaderId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// syncTime is the stored timestamp format — RFC3339 UTC, written
// explicitly by every insert so the column never mixes formats.
func syncTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseSyncTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseSyncTimePtr(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := parseSyncTime(s.String)
	return &t
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// --- worlds ---

// sync_worlds.server_id predates the standalone service and is no
// longer read: a world's dedicated server is its own agent link now.
const syncWorldColumns = `id, name, game_title, save_hint, game_meta, save_path, agent_url, agent_token_enc, lease_hours, max_bytes, keep_versions, checkpoints, webhook_url, head_version, next_holder, created_at`

func (s *Store) scanSyncWorld(scan func(...any) error) (*SyncWorld, error) {
	var w SyncWorld
	var head, next sql.NullInt64
	var checkpoints int
	var created, tokenEnc string
	if err := scan(&w.ID, &w.Name, &w.GameTitle, &w.SaveHint, &w.GameMeta, &w.SavePath, &w.AgentURL, &tokenEnc, &w.LeaseHours, &w.MaxBytes, &w.KeepVersions, &checkpoints, &w.WebhookURL, &head, &next, &created); err != nil {
		return nil, err
	}
	if tokenEnc != "" {
		token, err := s.box.Decrypt(tokenEnc)
		if err != nil {
			return nil, err
		}
		w.AgentToken = token
	}
	w.HasAgentToken = w.AgentToken != ""
	if head.Valid {
		w.HeadVersion = &head.Int64
	}
	if next.Valid {
		w.NextHolder = &next.Int64
	}
	w.Checkpoints = checkpoints != 0
	w.CreatedAt = parseSyncTime(created)
	return &w, nil
}

func (s *Store) CreateSyncWorld(ctx context.Context, name string, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sync_worlds (name, created_at) VALUES (?, ?)`, name, syncTime(now))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, fmt.Errorf("a world named %q already exists", name)
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetSyncWorld(ctx context.Context, id int64) (*SyncWorld, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+syncWorldColumns+` FROM sync_worlds WHERE id = ?`, id)
	w, err := s.scanSyncWorld(row.Scan)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return w, err
}

func (s *Store) ListSyncWorlds(ctx context.Context) ([]*SyncWorld, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+syncWorldColumns+` FROM sync_worlds ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SyncWorld{}
	for rows.Next() {
		w, err := s.scanSyncWorld(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// UpdateSyncWorldSettings changes the policy knobs and the agent link.
// Custody state (head, next holder) is deliberately not here — a
// settings form save must never move the head. An empty agentToken keeps
// the stored one (the password-update pattern); clearing the URL clears
// the credential with it.
func (s *Store) UpdateSyncWorldSettings(ctx context.Context, id int64, name string, leaseHours int, maxBytes int64, keepVersions int, checkpoints bool, webhookURL, agentURL, agentToken string) error {
	tokenSQL := ""
	args := []any{name, leaseHours, maxBytes, keepVersions, boolToInt(checkpoints), webhookURL, agentURL}
	switch {
	case agentURL == "":
		tokenSQL = ", agent_token_enc = ''"
	case agentToken != "":
		enc, err := s.box.Encrypt(agentToken)
		if err != nil {
			return err
		}
		tokenSQL = ", agent_token_enc = ?"
		args = append(args, enc)
	}
	args = append(args, id)
	res, err := s.db.ExecContext(ctx,
		`UPDATE sync_worlds SET name = ?, lease_hours = ?, max_bytes = ?, keep_versions = ?, checkpoints = ?, webhook_url = ?, agent_url = ?`+tokenSQL+` WHERE id = ?`,
		args...)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("a world named %q already exists", name)
		}
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// SetSyncWorldGameInfo stores what a companion reported about the game
// behind a world. Metadata only — custody and policy stay untouched, so
// a companion can never move a head or widen a size cap.
func (s *Store) SetSyncWorldGameInfo(ctx context.Context, id int64, title, saveHint, meta, savePath string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sync_worlds SET game_title = ?, save_hint = ?, game_meta = ?, save_path = ? WHERE id = ?`,
		title, saveHint, meta, savePath, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// RenameSyncWorld changes only a world's name — the one setting a
// player's own sync token may touch through the meta endpoint, everything
// else there (lease, size, checkpoints, the agent link) stays an admin's
// job through UpdateSyncWorldSettings.
func (s *Store) RenameSyncWorld(ctx context.Context, id int64, name string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE sync_worlds SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("a world named %q already exists", name)
		}
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) DeleteSyncWorld(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sync_worlds WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// SetSyncWorldHead moves the canonical head pointer, the only mutation a
// fast-forward check-in or an explicit human resolve performs.
func (s *Store) SetSyncWorldHead(ctx context.Context, worldID, versionID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_worlds SET head_version = ? WHERE id = ?`, versionID, worldID)
	return err
}

// SetSyncWorldNextHolder queues (or clears, with nil) the claim-next
// holder.
func (s *Store) SetSyncWorldNextHolder(ctx context.Context, worldID int64, userID *int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_worlds SET next_holder = ? WHERE id = ?`, nullInt(userID), worldID)
	return err
}

// --- sessions ---

const syncSessionColumns = `id, world_id, holder_id, server_held, base_version, status, checked_out_at, expires_at, warned_at, ended_at, ended_by, requested_kind, requested_at, requested_by`

func scanSyncSession(scan func(...any) error) (*SyncSession, error) {
	var ss SyncSession
	var serverHeld int
	var base, endedBy, requestedBy sql.NullInt64
	var checkedOut, expires string
	var warned, ended, requestedAt sql.NullString
	if err := scan(&ss.ID, &ss.WorldID, &ss.HolderID, &serverHeld, &base, &ss.Status, &checkedOut, &expires, &warned, &ended, &endedBy, &ss.RequestedKind, &requestedAt, &requestedBy); err != nil {
		return nil, err
	}
	if requestedBy.Valid {
		ss.RequestedBy = &requestedBy.Int64
	}
	ss.RequestedAt = parseSyncTimePtr(requestedAt)
	ss.ServerHeld = serverHeld != 0
	if base.Valid {
		ss.BaseVersion = &base.Int64
	}
	if endedBy.Valid {
		ss.EndedBy = &endedBy.Int64
	}
	ss.CheckedOutAt = parseSyncTime(checkedOut)
	ss.ExpiresAt = parseSyncTime(expires)
	ss.WarnedAt = parseSyncTimePtr(warned)
	ss.EndedAt = parseSyncTimePtr(ended)
	return &ss, nil
}

// CreateSyncSession acquires the lock: the partial unique index refuses a
// second active session, surfaced as ErrWorldHeld. baseVersion is the
// head this checkout delivered.
func (s *Store) CreateSyncSession(ctx context.Context, worldID, holderID int64, serverHeld bool, baseVersion *int64, now, expiresAt time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sync_sessions (world_id, holder_id, server_held, base_version, status, checked_out_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		worldID, holderID, boolToInt(serverHeld), nullInt(baseVersion), SyncActive, syncTime(now), syncTime(expiresAt))
	if err != nil {
		if strings.Contains(err.Error(), "sync_sessions") && strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrWorldHeld
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetSyncSession(ctx context.Context, id int64) (*SyncSession, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+syncSessionColumns+` FROM sync_sessions WHERE id = ?`, id)
	ss, err := scanSyncSession(row.Scan)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return ss, err
}

// RequestSyncHandback records an admin's standing ask on an active hold:
// "checkpoint" to capture the holder's save, "checkin" to capture it and
// end the hold. Passing an empty kind withdraws the request.
//
// It only ever touches an active session. A hold that has already ended
// has nothing to ask of, and letting the flag survive would mean a
// companion coming back online days later acting on an instruction that
// stopped making sense.
func (s *Store) RequestSyncHandback(ctx context.Context, sessionID int64, kind string, by int64, now time.Time) error {
	var at any
	var byID any
	if kind != "" {
		at, byID = syncTime(now), by
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE sync_sessions SET requested_kind = ?, requested_at = ?, requested_by = ?
		 WHERE id = ? AND status = ?`, kind, at, byID, sessionID, SyncActive)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// ClearSyncHandback drops a pending request once the companion has
// answered it. Separate from ending the session so a checkpoint — which
// keeps the hold — clears its own request too.
func (s *Store) ClearSyncHandback(ctx context.Context, sessionID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sync_sessions SET requested_kind = '', requested_at = NULL, requested_by = NULL WHERE id = ?`, sessionID)
	return err
}

// ActiveSyncSession returns the world's current hold, or ErrNotFound when
// the world is free.
func (s *Store) ActiveSyncSession(ctx context.Context, worldID int64) (*SyncSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+syncSessionColumns+` FROM sync_sessions WHERE world_id = ? AND status = ?`, worldID, SyncActive)
	ss, err := scanSyncSession(row.Scan)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return ss, err
}

// ActiveSyncSessions returns every live hold — the expiry sweeper's
// worklist. Small by nature: one row per held world.
func (s *Store) ActiveSyncSessions(ctx context.Context) ([]*SyncSession, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+syncSessionColumns+` FROM sync_sessions WHERE status = ? ORDER BY id`, SyncActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SyncSession{}
	for rows.Next() {
		ss, err := scanSyncSession(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// RenewSyncSession extends an active hold and re-arms its expiry warning.
func (s *Store) RenewSyncSession(ctx context.Context, id int64, expiresAt time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sync_sessions SET expires_at = ?, warned_at = NULL WHERE id = ? AND status = ?`,
		syncTime(expiresAt), id, SyncActive)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) MarkSyncSessionWarned(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sync_sessions SET warned_at = ? WHERE id = ?`, syncTime(at), id)
	return err
}

// EndSyncSession closes a hold with its outcome; the guard on status
// makes ending idempotent and keeps two racing enders from both counting.
func (s *Store) EndSyncSession(ctx context.Context, id int64, status string, endedBy int64, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sync_sessions SET status = ?, ended_at = ?, ended_by = ? WHERE id = ? AND status = ?`,
		status, syncTime(at), endedBy, id, SyncActive)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// --- versions ---

const syncVersionColumns = `id, world_id, session_id, parent_id, kind, conflict, bytes, sha256, uploader_id, created_at`

func scanSyncVersion(scan func(...any) error) (*SyncVersion, error) {
	var v SyncVersion
	var session, parent, uploader sql.NullInt64
	var conflict int
	var created string
	if err := scan(&v.ID, &v.WorldID, &session, &parent, &v.Kind, &conflict, &v.Bytes, &v.SHA256, &uploader, &created); err != nil {
		return nil, err
	}
	if session.Valid {
		v.SessionID = &session.Int64
	}
	if parent.Valid {
		v.ParentID = &parent.Int64
	}
	if uploader.Valid {
		v.UploaderID = &uploader.Int64
	}
	v.Conflict = conflict != 0
	v.CreatedAt = parseSyncTime(created)
	return &v, nil
}

func (s *Store) CreateSyncVersion(ctx context.Context, v *SyncVersion, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sync_versions (world_id, session_id, parent_id, kind, conflict, bytes, sha256, uploader_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.WorldID, nullInt(v.SessionID), nullInt(v.ParentID), v.Kind, boolToInt(v.Conflict), v.Bytes, v.SHA256, nullInt(v.UploaderID), syncTime(now))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetSyncVersion(ctx context.Context, id int64) (*SyncVersion, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+syncVersionColumns+` FROM sync_versions WHERE id = ?`, id)
	v, err := scanSyncVersion(row.Scan)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return v, err
}

// ListSyncVersions returns a world's versions, newest first.
func (s *Store) ListSyncVersions(ctx context.Context, worldID int64) ([]*SyncVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+syncVersionColumns+` FROM sync_versions WHERE world_id = ? ORDER BY id DESC`, worldID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SyncVersion{}
	for rows.Next() {
		v, err := scanSyncVersion(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) DeleteSyncVersion(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sync_versions WHERE id = ?`, id)
	return err
}

// ClearSyncConflicts unflags a world's conflict versions — called when a
// human has picked a head, which is what the flag was waiting for. The
// versions themselves stay until retention takes them.
func (s *Store) ClearSyncConflicts(ctx context.Context, worldID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_versions SET conflict = 0 WHERE world_id = ?`, worldID)
	return err
}

// --- per-player companion tokens ---

// SetUserSyncToken sets or clears a user's companion credential.
func (s *Store) SetUserSyncToken(ctx context.Context, userID int64, token string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET sync_token = ? WHERE id = ?`, token, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// GetUserSyncToken reads a user's token ("" = none minted). Kept out of
// the User struct so the credential never rides along on user listings.
func (s *Store) GetUserSyncToken(ctx context.Context, userID int64) (string, error) {
	var token string
	err := s.db.QueryRowContext(ctx, `SELECT sync_token FROM users WHERE id = ?`, userID).Scan(&token)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	return token, err
}

// GetUserBySyncToken resolves a companion credential. An empty token
// never matches — it is the "none minted" value on every row.
func (s *Store) GetUserBySyncToken(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE sync_token = ?`, token)
	u, err := scanUser(row.Scan)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return u, err
}
