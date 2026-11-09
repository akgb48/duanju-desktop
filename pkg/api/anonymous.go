package api

import "net/http"

// NewAnonymousSession shares connection configuration with this session without
// inheriting its account, device, headers, query parameters or signer. A separate
// client also prevents an account cookie jar or redirect callback from escaping
// into an anonymous device registration.
func (s *Session) NewAnonymousSession() *Session {
	n := NewSession()
	n.retainPlaybackState = true
	if s == nil {
		return n
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n.registry, n.scheme, n.logf = s.registry, s.scheme, s.logf
	if s.client != nil {
		n.client = &http.Client{Transport: s.client.Transport, Timeout: s.client.Timeout}
		n.anonymousConnectionSource = s.client
	}
	return n
}

// HTTPClient exposes the session's configured client for device registration.
// Use it on a NewAnonymousSession when registering an anonymous playback device.
func (s *Session) HTTPClient() *http.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// SyncAnonymousConnectionFrom follows proxy/port changes without inheriting an
// account cookie jar, redirect callback, credentials or device state.
func (s *Session) SyncAnonymousConnectionFrom(source *Session) {
	if source == nil {
		return
	}
	source.mu.RLock()
	client := source.client
	source.mu.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if client != nil && s.anonymousConnectionSource != client {
		s.client = &http.Client{Transport: client.Transport, Timeout: client.Timeout}
		s.anonymousConnectionSource = client
	}
}
