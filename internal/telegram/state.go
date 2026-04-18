package telegram

import (
	"context"
	"sync"
	"time"
)

const stateMaxAge = 15 * time.Minute

// stateStore is the backend the state manager delegates to. The
// in-memory impl is the default; the Mongo impl (state_mongo.go)
// persists conversation FSM across restarts and — once telegram-svc
// runs with more than one replica — across processes.
type stateStore interface {
	Set(userID int64, step string, data map[string]string)
	Get(userID int64) (step string, data map[string]string)
	Clear(userID int64)
	StartCleanup(ctx context.Context)
}

type userState struct {
	step      string
	data      map[string]string
	createdAt time.Time
}

// stateManager wraps a stateStore so handler code stays impl-agnostic.
// 106+ call sites use h.states.{Set,Get,Clear} — preserving the
// facade means no refactor there.
type stateManager struct {
	store stateStore
}

func newStateManager() *stateManager {
	return &stateManager{store: newMemoryStateStore()}
}

func newStateManagerWithStore(store stateStore) *stateManager {
	return &stateManager{store: store}
}

func (sm *stateManager) StartCleanup(ctx context.Context) { sm.store.StartCleanup(ctx) }
func (sm *stateManager) Set(userID int64, step string, data map[string]string) {
	sm.store.Set(userID, step, data)
}
func (sm *stateManager) Get(userID int64) (string, map[string]string) { return sm.store.Get(userID) }
func (sm *stateManager) Clear(userID int64)                           { sm.store.Clear(userID) }

// memoryStateStore is the original in-process map. Values expire
// after stateMaxAge; a periodic goroutine started by StartCleanup
// evicts stale entries.
type memoryStateStore struct {
	mu     sync.RWMutex
	states map[int64]*userState
}

func newMemoryStateStore() *memoryStateStore {
	return &memoryStateStore{states: make(map[int64]*userState)}
}

func (s *memoryStateStore) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.cleanExpired()
			}
		}
	}()
}

func (s *memoryStateStore) cleanExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, st := range s.states {
		if now.Sub(st.createdAt) > stateMaxAge {
			delete(s.states, id)
		}
	}
}

func (s *memoryStateStore) Set(userID int64, step string, data map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[userID] = &userState{step: step, data: data, createdAt: time.Now()}
}

func (s *memoryStateStore) Get(userID int64) (string, map[string]string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.states[userID]
	if !ok {
		return "", nil
	}
	if time.Since(st.createdAt) > stateMaxAge {
		return "", nil
	}
	var dataCopy map[string]string
	if st.data != nil {
		dataCopy = make(map[string]string, len(st.data))
		for k, v := range st.data {
			dataCopy[k] = v
		}
	}
	return st.step, dataCopy
}

func (s *memoryStateStore) Clear(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, userID)
}
