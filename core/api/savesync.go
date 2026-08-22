package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/savesync"
	"github.com/safwyls/artificer/core/store"
)

// Save-sync custody (docs/save-sync-architecture.md): worlds, holds and
// versions over core/savesync. Two trust tiers speak the same verbs:
// the session cookie (browser, gated per-route below) and the
// per-player sync token (the companion app, /api/public/sync/{token}).
//
// World management is admin territory; holding and moving the save is
// PermSync — the grant that says "this person is in the rotation".
// Reading custody state is open to any signed-in user, like dashboards.

// maxSyncUpload is the transport ceiling for save-bundle uploads; the
// real bound is each world's max_bytes, enforced while staging. This
// only exists so the API-wide 1 MiB body cap doesn't apply.
const maxSyncUpload = 2<<30 + 1<<20

// syncWorldStatus is one world's custody state, shaped for both the
// console page and the companion's status poll.
type syncWorldStatus struct {
	World *store.SyncWorld `json:"world"`
	// Holder is the active hold, nil when the world is free.
	Holder *syncHolder `json:"holder,omitempty"`
	// ClaimedBy names the queued next holder.
	ClaimedBy string `json:"claimedBy,omitempty"`
	// Head describes the canonical current version.
	Head *store.SyncVersion `json:"head,omitempty"`
}

type syncHolder struct {
	SessionID  int64     `json:"sessionId"`
	UserID     int64     `json:"userId"`
	Username   string    `json:"username"`
	ServerHeld bool      `json:"serverHeld"`
	ExpiresAt  time.Time `json:"expiresAt"`
	// A standing request for this holder's companion, waiting to be
	// picked up on its next poll: "checkpoint" or "checkin".
	RequestedKind string     `json:"requestedKind,omitempty"`
	RequestedAt   *time.Time `json:"requestedAt,omitempty"`
	// Claimable: the hold is past its lease, so a takeover checkout
	// would succeed.
	Claimable bool `json:"claimable"`
}

func (s *Server) syncStatus(r *http.Request, w *store.SyncWorld) *syncWorldStatus {
	out := &syncWorldStatus{World: w}
	if ss, err := s.store.ActiveSyncSession(r.Context(), w.ID); err == nil {
		holder := &syncHolder{
			SessionID: ss.ID, UserID: ss.HolderID, ServerHeld: ss.ServerHeld,
			ExpiresAt: ss.ExpiresAt, Claimable: ss.Expired(time.Now()),
			RequestedKind: ss.RequestedKind, RequestedAt: ss.RequestedAt,
		}
		if u, err := s.store.GetUser(r.Context(), ss.HolderID); err == nil {
			holder.Username = u.Username
		}
		out.Holder = holder
	}
	if w.NextHolder != nil {
		if u, err := s.store.GetUser(r.Context(), *w.NextHolder); err == nil {
			out.ClaimedBy = u.Username
		}
	}
	if w.HeadVersion != nil {
		if v, err := s.store.GetSyncVersion(r.Context(), *w.HeadVersion); err == nil {
			out.Head = v
		}
	}
	return out
}

// writeSyncError maps the engine's refusals onto responses: custody
// refusals are 409s carrying enough for the client to render the state
// (who holds it, whether a takeover would work), refused uploads are the
// uploader's fault, and the rest is a plain error.
func writeSyncError(w http.ResponseWriter, err error) {
	var held *savesync.HeldError
	var upload *savesync.UploadError
	switch {
	case errors.As(err, &held):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     held.Error(),
			"holder":    held.Holder,
			"expiresAt": held.Session.ExpiresAt,
			"claimable": held.Claimable,
		})
	case errors.As(err, &upload):
		writeError(w, http.StatusBadRequest, upload.Msg)
	case errors.Is(err, savesync.ErrReserved), errors.Is(err, savesync.ErrWorldFree), errors.Is(err, store.ErrWorldHeld):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// syncAudit logs custody actions. Worlds are not servers, so there is no
// per-server audit page for these to land on; the session and version
// history is itself the custody record, and the log carries the rest.
func (s *Server) syncAudit(r *http.Request, w *store.SyncWorld, action, detail string) {
	username := "unknown"
	if user, ok := userFromContext(r.Context()); ok {
		username = user.Username
	}
	s.logger.Info("savesync: "+action, "world", w.Name, "user", username, "detail", detail)
}

func (s *Server) loadSyncWorld(w http.ResponseWriter, r *http.Request) (*store.SyncWorld, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "worldID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid world id")
		return nil, false
	}
	world, err := s.store.GetSyncWorld(r.Context(), id)
	if err != nil {
		writeSyncError(w, err)
		return nil, false
	}
	return world, true
}

func (s *Server) loadSyncSession(w http.ResponseWriter, r *http.Request) (*store.SyncSession, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "sessionID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return nil, false
	}
	ss, err := s.store.GetSyncSession(r.Context(), id)
	if err != nil {
		writeSyncError(w, err)
		return nil, false
	}
	return ss, true
}

// requireSyncHolder: the session's holder may act on it; an admin may
// too (that is how a hold whose holder vanished gets checked in from a
// downloaded copy).
func requireSyncHolder(w http.ResponseWriter, r *http.Request, ss *store.SyncSession) (*store.User, bool) {
	user, ok := userFromContext(r.Context())
	if !ok || (user.ID != ss.HolderID && !user.IsAdmin()) {
		writeError(w, http.StatusForbidden, "this hold belongs to someone else")
		return nil, false
	}
	return user, true
}

// --- world management and custody state (session-cookie tier) ---

func (s *Server) handleListSyncWorlds(w http.ResponseWriter, r *http.Request) {
	if s.SaveSync == nil {
		writeError(w, http.StatusNotFound, "save sync is not enabled on this console")
		return
	}
	worlds, err := s.store.ListSyncWorlds(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list worlds")
		return
	}
	out := make([]*syncWorldStatus, 0, len(worlds))
	for _, world := range worlds {
		out = append(out, s.syncStatus(r, world))
	}
	writeJSON(w, http.StatusOK, map[string]any{"worlds": out})
}

// handleCreateSyncWorld makes a world. PermSync rather than admin: the
// companion's "link this installed game" flow creates worlds, and the
// custody grant is the trust boundary — an admin can always delete.
// Game metadata may ride along at creation (the companion's discovery
// report); size caps below match the meta endpoint's.
func (s *Server) handleCreateSyncWorld(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		GameTitle string `json:"gameTitle"`
		SaveHint  string `json:"saveHint"`
		GameMeta  string `json:"gameMeta"`
		SavePath  string `json:"savePath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeError(w, http.StatusBadRequest, "a world needs a name")
		return
	}
	if err := validSyncGameInfo(in.GameTitle, in.SaveHint, in.GameMeta, in.SavePath); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.store.CreateSyncWorld(r.Context(), in.Name, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.GameTitle != "" || in.SaveHint != "" || in.GameMeta != "" || in.SavePath != "" {
		if err := s.store.SetSyncWorldGameInfo(r.Context(), id, in.GameTitle, in.SaveHint, in.GameMeta, in.SavePath); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	world, err := s.store.GetSyncWorld(r.Context(), id)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	s.syncAudit(r, world, "sync-world-create", in.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"accepted": true, "status": s.syncStatus(r, world)})
}

// validSyncGameInfo bounds companion-reported metadata: stored and shown
// verbatim, so the only rule is that it stays small and, for the meta
// blob, is actually JSON.
func validSyncGameInfo(title, hint, meta, savePath string) error {
	if len(title) > 200 || len(hint) > 500 {
		return errors.New("game title or save hint is unreasonably long")
	}
	if len(meta) > 4096 {
		return errors.New("game metadata is capped at 4 KB")
	}
	if meta != "" && !json.Valid([]byte(meta)) {
		return errors.New("game metadata must be JSON")
	}
	return validSavePath(savePath)
}

// validSavePath guards the one piece of metadata that becomes a real
// filesystem path on someone else's machine.
//
// Every other field here is displayed; this one is joined onto a folder
// the player chose and then created. So it is checked the way a path
// from a stranger has to be: relative, no traversal, no root, no drive
// letter, no UNC — the companion is careful too, but a rule enforced in
// one place only is a rule waiting to be routed around.
func validSavePath(savePath string) error {
	if savePath == "" {
		return nil
	}
	if len(savePath) > 300 {
		return errors.New("the world's save path is unreasonably long")
	}
	if strings.ContainsAny(savePath, "\\") {
		return errors.New(`the world's save path must use "/" separators`)
	}
	if strings.HasPrefix(savePath, "/") {
		return errors.New("the world's save path must be relative to a player's save folder, not absolute")
	}
	// "C:", "\\server" and friends: anything that could escape the root
	// a player picks.
	if len(savePath) > 1 && savePath[1] == ':' {
		return errors.New("the world's save path must be relative, not a drive path")
	}
	for _, part := range strings.Split(savePath, "/") {
		switch part {
		case "", ".", "..":
			return errors.New(`the world's save path must not contain empty, "." or ".." segments`)
		}
		if strings.ContainsAny(part, "\x00:*?\"<>|") {
			return errors.New("the world's save path contains characters a folder name cannot hold")
		}
	}
	return nil
}

// handleSyncWorldMeta stores a companion's discovery report about the
// game behind a world. Metadata only — custody and policy are out of its
// reach by construction (SetSyncWorldGameInfo touches nothing else).
func (s *Server) handleSyncWorldMeta(w http.ResponseWriter, r *http.Request) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	var in struct {
		GameTitle string `json:"gameTitle"`
		SaveHint  string `json:"saveHint"`
		GameMeta  string `json:"gameMeta"`
		SavePath  string `json:"savePath"`
		// Name is the one setting beyond game metadata a player's own
		// sync token may touch here — everything else (lease, size,
		// checkpoints, the agent link) stays an admin's job through the
		// settings form. Absent or blank leaves the name untouched.
		Name string `json:"name,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validSyncGameInfo(in.GameTitle, in.SaveHint, in.GameMeta, in.SavePath); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Name != "" {
		if err := s.store.RenameSyncWorld(r.Context(), world.ID, in.Name); err != nil {
			writeSyncError(w, err)
			return
		}
	}
	// A pure rename carries none of the game-info fields — skip the game
	// info write entirely rather than blanking gameTitle/saveHint/gameMeta
	// with empties the request never meant to set.
	if in.GameTitle != "" || in.SaveHint != "" || in.GameMeta != "" || in.SavePath != "" {
		// The save path is the world's own, not the reporter's: the first
		// companion to record it settles where the world lives for
		// everyone, and a later joiner reporting its own metadata must
		// not overwrite it. Clearing it is an admin's job, through the
		// settings form.
		savePath := world.SavePath
		if savePath == "" {
			savePath = in.SavePath
		}
		if err := s.store.SetSyncWorldGameInfo(r.Context(), world.ID, in.GameTitle, in.SaveHint, in.GameMeta, savePath); err != nil {
			writeSyncError(w, err)
			return
		}
	}
	s.syncAudit(r, world, "sync-world-meta", in.GameTitle)
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func (s *Server) handleGetSyncWorld(w http.ResponseWriter, r *http.Request) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	versions, err := s.store.ListSyncVersions(r.Context(), world.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list versions")
		return
	}
	// Uploader names for the history table, resolved once per user.
	names := map[int64]string{}
	for _, v := range versions {
		if v.UploaderID == nil {
			continue
		}
		if _, ok := names[*v.UploaderID]; !ok {
			if u, err := s.store.GetUser(r.Context(), *v.UploaderID); err == nil {
				names[*v.UploaderID] = u.Username
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    s.syncStatus(r, world),
		"versions":  versions,
		"uploaders": names,
	})
}

func (s *Server) handleUpdateSyncWorld(w http.ResponseWriter, r *http.Request) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	var in struct {
		Name         string `json:"name"`
		LeaseHours   int    `json:"leaseHours"`
		MaxBytes     int64  `json:"maxBytes"`
		KeepVersions int    `json:"keepVersions"`
		Checkpoints  bool   `json:"checkpoints"`
		WebhookURL   string `json:"webhookUrl"`
		// The optional dedicated-server agent for the give/take flows.
		// An empty token keeps the stored one; clearing the URL clears
		// the credential with it.
		AgentURL   string `json:"agentUrl"`
		AgentToken string `json:"agentToken"`
		// The world's folder beneath each player's save root. Editable
		// here because the meta endpoint deliberately will not overwrite
		// it — the first companion to record it settles it, and only an
		// admin can correct a mistake.
		SavePath string `json:"savePath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "a world needs a name")
		return
	}
	if in.LeaseHours < 1 || in.LeaseHours > 24*30 {
		writeError(w, http.StatusBadRequest, "lease must be 1 hour to 30 days")
		return
	}
	if in.MaxBytes < 1<<20 || in.MaxBytes > 2<<30 {
		writeError(w, http.StatusBadRequest, "max size must be 1 MiB to 2 GiB")
		return
	}
	if in.KeepVersions < 1 || in.KeepVersions > 100 {
		writeError(w, http.StatusBadRequest, "keep must be 1 to 100 versions")
		return
	}
	if in.WebhookURL != "" {
		if err := notify.ValidateWebhookURL(in.WebhookURL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validSavePath(in.SavePath); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.UpdateSyncWorldSettings(r.Context(), world.ID, in.Name, in.LeaseHours, in.MaxBytes, in.KeepVersions, in.Checkpoints, in.WebhookURL, in.AgentURL, in.AgentToken); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.SavePath != world.SavePath {
		if err := s.store.SetSyncWorldGameInfo(r.Context(), world.ID, world.GameTitle, world.SaveHint, world.GameMeta, in.SavePath); err != nil {
			writeSyncError(w, err)
			return
		}
	}
	world, err := s.store.GetSyncWorld(r.Context(), world.ID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	s.syncAudit(r, world, "sync-world-settings", in.Name)
	writeJSON(w, http.StatusOK, s.syncStatus(r, world))
}

func (s *Server) handleDeleteSyncWorld(w http.ResponseWriter, r *http.Request) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	if err := s.SaveSync.DeleteWorld(r.Context(), world.ID); err != nil {
		writeSyncError(w, err)
		return
	}
	s.syncAudit(r, world, "sync-world-delete", world.Name)
	w.WriteHeader(http.StatusNoContent)
}

// --- custody verbs (shared by both tiers via the user resolved on the
// request) ---

func (s *Server) syncCheckout(w http.ResponseWriter, r *http.Request, user *store.User) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	var in struct {
		Takeover bool `json:"takeover"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&in) // an empty body is a plain checkout
	}
	ss, err := s.SaveSync.Checkout(r.Context(), world.ID, user, false, in.Takeover)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	action := "sync-checkout"
	if in.Takeover {
		action = "sync-takeover"
	}
	s.syncAudit(r, world, action, fmt.Sprintf("session %d", ss.ID))
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted": true,
		"session":  ss,
		"world":    world.Name,
	})
}

func (s *Server) syncClaim(w http.ResponseWriter, r *http.Request, user *store.User) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	if err := s.SaveSync.Claim(r.Context(), world.ID, user); err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "world": world.Name})
}

func (s *Server) syncUnclaim(w http.ResponseWriter, r *http.Request, user *store.User) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	if err := s.SaveSync.Unclaim(r.Context(), world.ID, user); err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "world": world.Name})
}

func (s *Server) syncRenew(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.loadSyncSession(w, r)
	if !ok {
		return
	}
	if _, ok := requireSyncHolder(w, r, ss); !ok {
		return
	}
	until, err := s.SaveSync.Renew(r.Context(), ss)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "expiresAt": until})
}

func (s *Server) syncCheckin(w http.ResponseWriter, r *http.Request, kind string) {
	ss, ok := s.loadSyncSession(w, r)
	if !ok {
		return
	}
	user, ok := requireSyncHolder(w, r, ss)
	if !ok {
		return
	}
	v, err := s.SaveSync.Checkin(r.Context(), ss, user, r.Body, kind)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	if world, werr := s.store.GetSyncWorld(r.Context(), ss.WorldID); werr == nil && kind == store.SyncKindCheckin {
		s.syncAudit(r, world, "sync-checkin", fmt.Sprintf("version %d, conflict=%v", v.ID, v.Conflict))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "version": v})
}

func (s *Server) syncDownload(w http.ResponseWriter, r *http.Request) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	versionID, err := strconv.ParseInt(chi.URLParam(r, "versionID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid version id")
		return
	}
	path, v, err := s.SaveSync.VersionPath(r.Context(), world.ID, versionID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("%s-v%d.tar", world.Name, v.ID)))
	w.Header().Set("ETag", `"`+v.SHA256+`"`)
	http.ServeFile(w, r, path)
}

func (s *Server) handleSyncImport(w http.ResponseWriter, r *http.Request) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	user, uok := userFromContext(r.Context())
	if !uok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	v, err := s.SaveSync.Import(r.Context(), world.ID, user, r.Body)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	s.syncAudit(r, world, "sync-import", fmt.Sprintf("version %d", v.ID))
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "version": v})
}

// handleSyncRequestHandback asks the current holder's companion to hand
// the world back on its next poll.
//
// Nothing here reaches into a companion: it lives on someone's home
// machine behind a router, and it polls. So the ask is a flag on the
// session, and the answer arrives as an ordinary upload up to a poll
// interval later. That is also why this cannot fail loudly when the
// holder's machine is asleep — the request simply stands, and the page
// says it is still standing.
//
// "checkin" is the verb that answers the case this exists for: a holder
// who went to bed mid-session. A checkpoint captures their save but
// never moves the head, so on its own it would leave the next player
// checking out a version from before the session began.
func (s *Server) handleSyncRequestHandback(w http.ResponseWriter, r *http.Request) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	var in struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch in.Kind {
	case store.SyncKindCheckin, store.SyncKindCheckpoint, "":
	default:
		writeError(w, http.StatusBadRequest, `ask for "checkin", "checkpoint", or "" to withdraw`)
		return
	}
	ss, err := s.store.ActiveSyncSession(r.Context(), world.ID)
	if err != nil {
		writeError(w, http.StatusConflict, "nobody is holding this world")
		return
	}
	if ss.ServerHeld {
		writeError(w, http.StatusConflict,
			"the dedicated server holds this world — take it back from the server instead; there is no companion to ask")
		return
	}
	if in.Kind == store.SyncKindCheckpoint && !world.Checkpoints {
		writeError(w, http.StatusBadRequest, "checkpoints are off for this world — ask for a check-in, or turn them on in settings")
		return
	}
	user, _ := userFromContext(r.Context())
	var by int64
	if user != nil {
		by = user.ID
	}
	if err := s.store.RequestSyncHandback(r.Context(), ss.ID, in.Kind, by, time.Now()); err != nil {
		writeSyncError(w, err)
		return
	}
	s.syncAudit(r, world, "sync-world-request", in.Kind)
	writeJSON(w, http.StatusOK, s.syncStatus(r, world))
}

func (s *Server) handleSyncRelease(w http.ResponseWriter, r *http.Request) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	user, uok := userFromContext(r.Context())
	if !uok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	ss, err := s.store.ActiveSyncSession(r.Context(), world.ID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	if err := s.SaveSync.Release(r.Context(), ss, user); err != nil {
		writeSyncError(w, err)
		return
	}
	s.syncAudit(r, world, "sync-release", fmt.Sprintf("session %d", ss.ID))
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func (s *Server) handleSyncSetHead(w http.ResponseWriter, r *http.Request) {
	world, ok := s.loadSyncWorld(w, r)
	if !ok {
		return
	}
	var in struct {
		VersionID int64 `json:"versionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.SaveSync.SetHead(r.Context(), world.ID, in.VersionID); err != nil {
		writeSyncError(w, err)
		return
	}
	s.syncAudit(r, world, "sync-set-head", fmt.Sprintf("version %d", in.VersionID))
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

// --- the per-player companion token ---

// handleMySyncToken mints (POST), shows (GET) or revokes (DELETE) the
// caller's own companion credential. Minting rotates: the old token
// stops working the moment a new one exists.
func (s *Server) handleMySyncToken(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	switch r.Method {
	case http.MethodGet:
		token, err := s.store.GetUserSyncToken(r.Context(), user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read token")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": token})
	case http.MethodPost:
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate token")
			return
		}
		token := hex.EncodeToString(buf)
		if err := s.store.SetUserSyncToken(r.Context(), user.ID, token); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save token")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": token})
	case http.MethodDelete:
		if err := s.store.SetUserSyncToken(r.Context(), user.ID, ""); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to revoke token")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- the companion tier: /api/public/sync/{token} ---

// syncTokenUser resolves the token in the path to a user who may sync.
// A miss is a 404 with no hint of which part was wrong, like the other
// token tiers; a revoked grant is indistinguishable from a bad token on
// purpose.
func (s *Server) syncTokenUser(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	if s.SaveSync == nil {
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	user, err := s.store.GetUserBySyncToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil || !user.Can(store.PermSync) {
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	return user, true
}

// withSyncTokenUser adapts the cookie-tier handlers to the token tier by
// placing the resolved user on the context, so both tiers run the same
// code and cannot drift.
func (s *Server) withSyncTokenUser(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.syncTokenUser(w, r)
		if !ok {
			return
		}
		fn(w, r.WithContext(contextWithUser(r.Context(), user)))
	}
}

func (s *Server) handlePublicSyncStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := s.syncTokenUser(w, r)
	if !ok {
		return
	}
	worlds, err := s.store.ListSyncWorlds(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list worlds")
		return
	}
	out := make([]*syncWorldStatus, 0, len(worlds))
	for _, world := range worlds {
		out = append(out, s.syncStatus(r, world))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted": true,
		"username": user.Username,
		"worlds":   out,
		// The companion shows this beside its own build, so a mismatched
		// pair is visible without asking anyone to check a container.
		"serverVersion": s.version(),
	})
}

// mountSyncRoutes registers the custody surface on both tiers. Called
// from Routes() — the small-verb group and the upload group carry
// different body caps, so the routes arrive in two pieces.
func (s *Server) mountSyncSmall(r chi.Router) {
	// Reading custody state: any signed-in user. Holding, creating and
	// annotating worlds: PermSync — the custody grant is the trust
	// boundary, and the companion's link-a-game flow creates worlds.
	// Settings, deletion and the destructive verbs: admin.
	r.Get("/sync/worlds", s.handleListSyncWorlds)
	r.With(s.requirePermission(store.PermSync)).Post("/sync/worlds", s.handleCreateSyncWorld)
	r.Get("/sync/worlds/{worldID}", s.handleGetSyncWorld)
	r.With(s.requireAdmin).Put("/sync/worlds/{worldID}", s.handleUpdateSyncWorld)
	r.With(s.requirePermission(store.PermSync)).Put("/sync/worlds/{worldID}/meta", s.handleSyncWorldMeta)
	r.With(s.requireAdmin).Delete("/sync/worlds/{worldID}", s.handleDeleteSyncWorld)
	r.With(s.requirePermission(store.PermSync)).Post("/sync/worlds/{worldID}/checkout", s.asUser(s.syncCheckout))
	r.With(s.requirePermission(store.PermSync)).Post("/sync/worlds/{worldID}/claim", s.asUser(s.syncClaim))
	r.With(s.requirePermission(store.PermSync)).Delete("/sync/worlds/{worldID}/claim", s.asUser(s.syncUnclaim))
	r.With(s.requirePermission(store.PermSync)).Get("/sync/worlds/{worldID}/versions/{versionID}/download", s.syncDownload)
	r.With(s.requireAdmin).Post("/sync/worlds/{worldID}/request", s.handleSyncRequestHandback)
	r.With(s.requireAdmin).Post("/sync/worlds/{worldID}/release", s.handleSyncRelease)
	r.With(s.requireAdmin).Post("/sync/worlds/{worldID}/head", s.handleSyncSetHead)
	// The dedicated server as a holder (savesync_server.go): give the
	// world to the linked server, take it back. Admin — these move a
	// server's live save.
	r.With(s.requireAdmin).Post("/sync/worlds/{worldID}/server/give", s.handleSyncServerGive)
	r.With(s.requireAdmin).Post("/sync/worlds/{worldID}/server/take", s.handleSyncServerTake)
	r.With(s.requirePermission(store.PermSync)).Post("/sync/sessions/{sessionID}/renew", s.syncRenew)
	r.With(s.requirePermission(store.PermSync)).HandleFunc("/me/sync-token", s.handleMySyncToken)
	// Live custody updates, and cover art for the game shelf
	// (savesync_live.go).
	r.Get("/sync/events", s.handleSyncEvents)
	r.Post("/sync/artwork", s.handleSyncArtwork)
	// The IGDB credential pair and its diagnostics (artwork.go). Admin:
	// it is a shared credential, and its status names the deployment's
	// own configuration.
	r.With(s.requireAdmin).Get("/sync/artwork/settings", s.handleArtworkSettings)
	r.With(s.requireAdmin).Put("/sync/artwork/settings", s.handleSetArtworkSettings)
	r.With(s.requireAdmin).Delete("/sync/artwork/settings", s.handleDeleteArtworkSettings)
	r.With(s.requireAdmin).Post("/sync/artwork/test", s.handleTestArtwork)
	// Save locations from the Ludusavi manifest (savedirs.go). Reading
	// is PermSync like the rest of the companion's surface; refreshing
	// the catalogue is admin.
	r.With(s.requirePermission(store.PermSync)).Post("/sync/savehints", s.handleSyncSaveHints)
	r.With(s.requireAdmin).Get("/sync/savehints/status", s.handleSaveHintsStatus)
	r.With(s.requireAdmin).Post("/sync/savehints/refresh", s.handleRefreshSaveHints)
}

// asUser lifts a handler that takes the acting user out of the context.
func (s *Server) asUser(fn func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := userFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		fn(w, r, user)
	}
}

func (s *Server) mountSyncUploads(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.requireAuth)
		r.With(s.requirePermission(store.PermSync)).Post("/sync/sessions/{sessionID}/checkin", func(w http.ResponseWriter, r *http.Request) {
			s.syncCheckin(w, r, store.SyncKindCheckin)
		})
		r.With(s.requirePermission(store.PermSync)).Post("/sync/sessions/{sessionID}/checkpoint", func(w http.ResponseWriter, r *http.Request) {
			s.syncCheckin(w, r, store.SyncKindCheckpoint)
		})
		// Import is PermSync, not admin: seeding a fresh world with the
		// current save is the second half of the companion's link flow,
		// and it is already refused while anyone holds the world.
		r.With(s.requirePermission(store.PermSync)).Post("/sync/worlds/{worldID}/import", s.handleSyncImport)
	})

	// The companion tier: token in the path is the whole credential.
	// Same handlers as the cookie tier, user resolved from the token —
	// including the downloads and status, so the companion needs exactly
	// one base URL.
	r.Route("/public/sync/{token}", func(r chi.Router) {
		r.Get("/", s.handlePublicSyncStatus)
		r.Get("/companion/download", s.withSyncTokenUser(s.handleSyncCompanionDownload))
		r.Post("/artwork", s.withSyncTokenUser(s.handleSyncArtwork))
		r.Post("/savehints", s.withSyncTokenUser(s.handleSyncSaveHints))
		r.Post("/worlds", s.withSyncTokenUser(s.handleCreateSyncWorld))
		r.Put("/worlds/{worldID}/meta", s.withSyncTokenUser(s.handleSyncWorldMeta))
		r.Post("/worlds/{worldID}/import", s.withSyncTokenUser(s.handleSyncImport))
		r.Post("/worlds/{worldID}/checkout", s.withSyncTokenUser(func(w http.ResponseWriter, r *http.Request) {
			s.asUser(s.syncCheckout)(w, r)
		}))
		r.Post("/worlds/{worldID}/claim", s.withSyncTokenUser(func(w http.ResponseWriter, r *http.Request) {
			s.asUser(s.syncClaim)(w, r)
		}))
		r.Delete("/worlds/{worldID}/claim", s.withSyncTokenUser(func(w http.ResponseWriter, r *http.Request) {
			s.asUser(s.syncUnclaim)(w, r)
		}))
		r.Get("/worlds/{worldID}/versions/{versionID}/download", s.withSyncTokenUser(s.syncDownload))
		r.Post("/sessions/{sessionID}/renew", s.withSyncTokenUser(s.syncRenew))
		r.Post("/sessions/{sessionID}/checkin", s.withSyncTokenUser(func(w http.ResponseWriter, r *http.Request) {
			s.syncCheckin(w, r, store.SyncKindCheckin)
		}))
		r.Post("/sessions/{sessionID}/checkpoint", s.withSyncTokenUser(func(w http.ResponseWriter, r *http.Request) {
			s.syncCheckin(w, r, store.SyncKindCheckpoint)
		}))
	})
}
