package telegram

import "sync"

// botState owns the mutable per-session fields that the periodic checker,
// callback handlers, and UI mutators race against. The mutex is unexported
// and never held by callers — every read/write goes through a method below.
type botState struct {
	mu sync.Mutex

	installedVer        string
	latestVer           string
	updateAvailable     bool
	selfLatest          string
	selfUpdateAvailable bool
	menuMID             int
}

func newBotState() *botState { return &botState{} }

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

// updateReady reports whether a podkop update is currently advertised.
func (s *botState) updateReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateAvailable
}

// setPodkopFetch records a successful podkop refresh result.
func (s *botState) setPodkopFetch(installed, latest string, available bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installedVer = installed
	s.latestVer = latest
	s.updateAvailable = available
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

// setMenuID overwrites the tracked menu id.
func (s *botState) setMenuID(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.menuMID = id
}

// adoptMenuID atomically replaces the tracked menu id and returns the
// previous one. If clickedID already matches the tracked id, returns
// (clickedID, false) — caller should skip the orphan-cleanup step.
func (s *botState) adoptMenuID(clickedID int) (oldID int, changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.menuMID == clickedID {
		return clickedID, false
	}
	oldID = s.menuMID
	s.menuMID = clickedID
	return oldID, true
}
