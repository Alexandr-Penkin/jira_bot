package telegram

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStateManager_SetAndGet(t *testing.T) {
	sm := newStateManager()

	data := map[string]string{"key": "value"}
	sm.Set(456, 123, "step1", data)

	step, got := sm.Get(456, 123)
	assert.Equal(t, "step1", step)
	assert.Equal(t, "value", got["key"])
}

func TestStateManager_GetNonExistent(t *testing.T) {
	sm := newStateManager()

	step, data := sm.Get(456, 999)
	assert.Equal(t, "", step)
	assert.Nil(t, data)
}

func TestStateManager_Clear(t *testing.T) {
	sm := newStateManager()

	sm.Set(456, 123, "step1", nil)
	sm.Clear(456, 123)

	step, data := sm.Get(456, 123)
	assert.Equal(t, "", step)
	assert.Nil(t, data)
}

func TestStateManager_ClearNonExistent(t *testing.T) {
	sm := newStateManager()
	// Should not panic
	sm.Clear(456, 999)
}

func TestStateManager_OverwriteState(t *testing.T) {
	sm := newStateManager()

	sm.Set(456, 123, "step1", map[string]string{"a": "1"})
	sm.Set(456, 123, "step2", map[string]string{"b": "2"})

	step, data := sm.Get(456, 123)
	assert.Equal(t, "step2", step)
	assert.Equal(t, "2", data["b"])
	_, hasA := data["a"]
	assert.False(t, hasA)
}

func TestStateManager_MultipleUsers(t *testing.T) {
	sm := newStateManager()

	sm.Set(456, 1, "step_a", map[string]string{"x": "1"})
	sm.Set(456, 2, "step_b", map[string]string{"y": "2"})

	step1, data1 := sm.Get(456, 1)
	assert.Equal(t, "step_a", step1)
	assert.Equal(t, "1", data1["x"])

	step2, data2 := sm.Get(456, 2)
	assert.Equal(t, "step_b", step2)
	assert.Equal(t, "2", data2["y"])
}

func TestStateManager_SameUserDifferentChats(t *testing.T) {
	// Group-chat isolation: the same user holding state in two
	// different chats must not see them collapse.
	sm := newStateManager()

	sm.Set(100, 42, "private_step", map[string]string{"v": "priv"})
	sm.Set(200, 42, "group_step", map[string]string{"v": "grp"})

	stepP, dataP := sm.Get(100, 42)
	assert.Equal(t, "private_step", stepP)
	assert.Equal(t, "priv", dataP["v"])

	stepG, dataG := sm.Get(200, 42)
	assert.Equal(t, "group_step", stepG)
	assert.Equal(t, "grp", dataG["v"])
}

func TestStateManager_NilData(t *testing.T) {
	sm := newStateManager()

	sm.Set(456, 123, "waiting", nil)

	step, data := sm.Get(456, 123)
	assert.Equal(t, "waiting", step)
	assert.Nil(t, data)
}

func TestStateManager_Concurrent(t *testing.T) {
	sm := newStateManager()
	var wg sync.WaitGroup

	for i := int64(0); i < 100; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			sm.Set(456, id, "step", nil)
			sm.Get(456, id)
			sm.Clear(456, id)
		}(i)
	}

	wg.Wait()
}
