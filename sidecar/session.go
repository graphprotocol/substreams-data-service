package sidecar

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/graphprotocol/substreams-data-service/horizon"
	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	"github.com/streamingfast/eth-go"
)

// SessionState represents the state of a payment session
type SessionState int

const (
	SessionStateActive SessionState = iota
	SessionStatePaused
	SessionStateEnded
)

// Session represents an active payment session
type Session struct {
	mu sync.RWMutex

	ID        string
	State     SessionState
	CreatedAt time.Time
	UpdatedAt time.Time
	EndedAt   *time.Time
	EndReason commonv1.EndReason

	// Escrow account details
	Payer       eth.Address
	Receiver    eth.Address // Service provider
	DataService eth.Address

	// Current RAV state
	CurrentRAV *horizon.SignedRAV

	// Usage tracking
	BlocksProcessed  uint64
	BytesTransferred uint64
	Requests         uint64
	TotalCost        *big.Int

	// Price configuration (set by provider)
	PricePerBlock *big.Int
}

// NewSession creates a new session with a generated ID
func NewSession(payer, receiver, dataService eth.Address) *Session {
	return &Session{
		ID:            uuid.New().String(),
		State:         SessionStateActive,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Payer:         payer,
		Receiver:      receiver,
		DataService:   dataService,
		TotalCost:     big.NewInt(0),
		PricePerBlock: big.NewInt(0),
	}
}

// AddUsage adds usage to the session and returns the updated total cost
func (s *Session) AddUsage(blocks, bytes, requests uint64, cost *big.Int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.BlocksProcessed += blocks
	s.BytesTransferred += bytes
	s.Requests += requests
	if cost != nil {
		s.TotalCost = new(big.Int).Add(s.TotalCost, cost)
	}
	s.UpdatedAt = time.Now()
}

// GetUsage returns a copy of the current usage
func (s *Session) GetUsage() *commonv1.Usage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return &commonv1.Usage{
		BlocksProcessed:  s.BlocksProcessed,
		BytesTransferred: s.BytesTransferred,
		Requests:         s.Requests,
		Cost:             commonv1.BigIntFromNative(s.TotalCost),
	}
}

// SetRAV updates the current RAV
func (s *Session) SetRAV(rav *horizon.SignedRAV) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.CurrentRAV = rav
	s.UpdatedAt = time.Now()
}

// GetRAV returns the current RAV
func (s *Session) GetRAV() *horizon.SignedRAV {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.CurrentRAV
}

// End marks the session as ended
func (s *Session) End(reason commonv1.EndReason) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.State = SessionStateEnded
	s.EndedAt = &now
	s.EndReason = reason
	s.UpdatedAt = now
}

// IsActive returns true if the session is active
func (s *Session) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.State == SessionStateActive
}

// ToSessionInfo converts to proto SessionInfo
func (s *Session) ToSessionInfo() *commonv1.SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return &commonv1.SessionInfo{
		SessionId: s.ID,
		EscrowAccount: &commonv1.EscrowAccount{
			Payer:       commonv1.AddressFromEth(s.Payer),
			Receiver:    commonv1.AddressFromEth(s.Receiver),
			DataService: commonv1.AddressFromEth(s.DataService),
		},
		CurrentRav:       HorizonSignedRAVToProto(s.CurrentRAV),
		AccumulatedUsage: s.GetUsage(),
	}
}

// SessionManager manages active sessions
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionManager creates a new session manager
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// Create creates and stores a new session
func (sm *SessionManager) Create(payer, receiver, dataService eth.Address) *Session {
	session := NewSession(payer, receiver, dataService)

	sm.mu.Lock()
	sm.sessions[session.ID] = session
	sm.mu.Unlock()

	return session
}

// Get retrieves a session by ID
func (sm *SessionManager) Get(id string) (*Session, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return session, nil
}

// Delete removes a session
func (sm *SessionManager) Delete(id string) {
	sm.mu.Lock()
	delete(sm.sessions, id)
	sm.mu.Unlock()
}

// GetActive returns all active sessions
func (sm *SessionManager) GetActive() []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var active []*Session
	for _, s := range sm.sessions {
		if s.IsActive() {
			active = append(active, s)
		}
	}
	return active
}

// Count returns the number of sessions
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return len(sm.sessions)
}
