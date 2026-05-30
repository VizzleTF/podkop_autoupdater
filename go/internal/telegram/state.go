package telegram

import (
	"sync"
	"time"
)

// botState owns the mutable per-session fields that the periodic checker,
// callback handlers, and UI mutators race against. The mutex is unexported
// and never held by callers — every read/write goes through a method below.
type botState struct {
	mu sync.Mutex

	installedVer        string
	latestVer           string
	latestTag           string // raw upstream tag for the latest podkop release
	updateAvailable     bool
	selfLatest          string
	selfUpdateAvailable bool
	menuMID             int
	lastCheck           time.Time // wall-clock of the most recent successful podkop refresh

	dnsIP      string // last fakeip probe result (ip or error string)
	dnsOK      bool   // whether the last fakeip probe landed in range
	dnsChecked bool   // whether a probe has run at least once this session

	// rollbackTarget/Tag hold the version the user is about to roll back to,
	// staged by the rollback-prompt step (or by a broken update) and consumed
	// by the confirm step.
	rollbackTarget string
	rollbackTag    string

	// restoreTarget holds the backup id the user is about to restore
	// (config-only, no package change); deleteTarget holds the backup id the
	// user is about to delete. Both are staged by their prompt step and
	// consumed by the matching confirm step.
	restoreTarget string
	deleteTarget  string

	// awaiting marks that the settings menu is expecting the next plain text
	// message as input (router label or admin id).
	awaiting awaitKind

	// busy guards long-running side effects (restart/update/self-update) so a
	// double-click or an overlapping auto-update can't run two install.sh
	// invocations as root concurrently.
	busy bool

	// persistMID is invoked outside the mutex whenever menuMID changes to
	// a new value, so the next daemon start can edit the same message
	// instead of posting a fresh menu. nil disables persistence.
	persistMID func(int)
}

// newBotState seeds the state with the previously persisted menu id (0 if
// none) and a persistence callback that records subsequent changes.
func newBotState(initialMenuMID int, persistMID func(int)) *botState {
	return &botState{
		menuMID:    initialMenuMID,
		persistMID: persistMID,
	}
}

// snapshotPodkop returns the cached podkop fields and the tracked menu id
// under a single lock acquisition.
func (s *botState) snapshotPodkop() (installed, latest string, available bool, menuMID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installedVer, s.latestVer, s.updateAvailable, s.menuMID
}

// snapshotSelf returns the cached self-update fields.
func (s *botState) snapshotSelf() (latest string, available bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selfLatest, s.selfUpdateAvailable
}

// installed returns the cached installed podkop version (for menu titles).
func (s *botState) installed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installedVer
}

// installedAndLatest returns the pair used by the "update available" prompt.
func (s *botState) installedAndLatest() (installed, latest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installedVer, s.latestVer
}

// latest returns the cached latest podkop version (used as update target).
func (s *botState) latest() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latestVer
}

// latestAndTag returns the cached latest podkop version and its raw upstream
// tag, used to pin the install.sh download.
func (s *botState) latestAndTag() (version, tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latestVer, s.latestTag
}

// lastCheckTime returns the wall-clock of the most recent successful refresh
// (zero if none yet).
func (s *botState) lastCheckTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCheck
}

// tryBusy atomically marks the state busy, returning false if it was already
// busy. The caller must call clearBusy when done iff this returned true.
func (s *botState) tryBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return false
	}
	s.busy = true
	return true
}

// clearBusy releases the busy guard.
func (s *botState) clearBusy() {
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

// setDNS records the latest fakeip probe result for the dashboard card.
func (s *botState) setDNS(ip string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dnsIP = ip
	s.dnsOK = ok
	s.dnsChecked = true
}

// dnsSnapshot returns the cached fakeip probe result.
func (s *botState) dnsSnapshot() (ip string, ok, checked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dnsIP, s.dnsOK, s.dnsChecked
}

// setRollback stages the rollback target consumed by the confirm step.
func (s *botState) setRollback(version, tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollbackTarget = version
	s.rollbackTag = tag
}

// rollback returns the staged rollback target.
func (s *botState) rollback() (version, tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rollbackTarget, s.rollbackTag
}

// setRestore stages the config-restore target.
func (s *botState) setRestore(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restoreTarget = version
}

// restore returns the staged config-restore target.
func (s *botState) restore() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restoreTarget
}

// awaitKind enumerates the pending text-input modes of the settings menu.
type awaitKind int

const (
	awaitNone awaitKind = iota
	awaitLabel
	awaitAdminAdd
)

// setAwait marks the bot as waiting for a text reply of the given kind.
func (s *botState) setAwait(k awaitKind) {
	s.mu.Lock()
	s.awaiting = k
	s.mu.Unlock()
}

// await returns the pending text-input kind.
func (s *botState) await() awaitKind {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.awaiting
}

// clearAwait resets the pending text-input kind.
func (s *botState) clearAwait() {
	s.mu.Lock()
	s.awaiting = awaitNone
	s.mu.Unlock()
}

// setDelete stages the backup id to delete.
func (s *botState) setDelete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteTarget = id
}

// delete returns the staged delete target.
func (s *botState) deleteID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteTarget
}

// updateReady reports whether a podkop update is currently advertised.
func (s *botState) updateReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateAvailable
}

// setPodkopFetch records a successful podkop refresh result.
func (s *botState) setPodkopFetch(installed, latest, tag string, available bool, when time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installedVer = installed
	s.latestVer = latest
	s.latestTag = tag
	s.updateAvailable = available
	s.lastCheck = when
}

// setPodkopFetchError records a failed podkop refresh: keep the installed
// version (still valid locally), clear latest, mark unavailable.
func (s *botState) setPodkopFetchError(installed string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installedVer = installed
	s.latestVer = ""
	s.updateAvailable = false
}

// setSelfFetch records a successful self refresh.
func (s *botState) setSelfFetch(latest string, available bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selfLatest = latest
	s.selfUpdateAvailable = available
}

// setSelfFetchError clears the self-update fields on fetch error.
func (s *botState) setSelfFetchError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selfLatest = ""
	s.selfUpdateAvailable = false
}

// menuID returns the tracked menu message id (0 if none yet).
func (s *botState) menuID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.menuMID
}

// setMenuID overwrites the tracked menu id. No-op (and no persist call)
// when id matches the cached value — avoids redundant UCI writes when
// sendOrEdit succeeds and returns the same message_id.
func (s *botState) setMenuID(id int) {
	s.mu.Lock()
	if s.menuMID == id {
		s.mu.Unlock()
		return
	}
	s.menuMID = id
	s.mu.Unlock()
	if s.persistMID != nil {
		s.persistMID(id)
	}
}

// adoptMenuID atomically replaces the tracked menu id and returns the
// previous one. If clickedID already matches the tracked id, returns
// (clickedID, false) — caller should skip the orphan-cleanup step.
func (s *botState) adoptMenuID(clickedID int) (oldID int, changed bool) {
	s.mu.Lock()
	if s.menuMID == clickedID {
		s.mu.Unlock()
		return clickedID, false
	}
	oldID = s.menuMID
	s.menuMID = clickedID
	s.mu.Unlock()
	if s.persistMID != nil {
		s.persistMID(clickedID)
	}
	return oldID, true
}
