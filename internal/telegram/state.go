package telegram

import (
	"context"
	"sync"
	"time"
)

const stateMaxAge = 15 * time.Minute

type userState struct {
	step      string
	data      map[string]string
	createdAt time.Time
}

type stateManager struct {
	mu     sync.RWMutex
	states map[int64]*userState
}

func newStateManager() *stateManager {
	return &stateManager{
		states: make(map[int64]*userState),
	}
}

// StartCleanup runs a background goroutine that removes expired states.
func (sm *stateManager) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sm.cleanExpired()
			}
		}
	}()
}

func (sm *stateManager) cleanExpired() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	for id, s := range sm.states {
		if now.Sub(s.createdAt) > stateMaxAge {
			delete(sm.states, id)
		}
	}
}

func (sm *stateManager) Set(userID int64, step string, data map[string]string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.states[userID] = &userState{step: step, data: data, createdAt: time.Now()}
}

func (sm *stateManager) Get(userID int64) (step string, data map[string]string) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.states[userID]
	if !ok {
		return "", nil
	}
	if time.Since(s.createdAt) > stateMaxAge {
		return "", nil
	}
	return s.step, s.data
}

func (sm *stateManager) Clear(userID int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.states, userID)
}
