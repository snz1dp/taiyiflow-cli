package agentloop

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const (
	ObserverMaxFailures       = 3
	ObserverStableConnection  = 30 * time.Second
	ObserverInitialBackoff    = time.Second
	ObserverMaximumBackoff    = 30 * time.Second
	ObserverPollingInterval   = 5 * time.Second
	ObserverClientIDMaxPrefix = 96
)

func ObserverClientID(origin string) string {
	origin = strings.TrimSpace(origin)
	if len(origin) > ObserverClientIDMaxPrefix {
		origin = origin[:ObserverClientIDMaxPrefix]
	}
	return origin + ":observer"
}

func (s *Store) observerPath(sessionID string) string {
	return s.path("observers", stateKey(sessionID)+".json")
}

func (s *Store) LoadObserver(sessionID string) (ObserverState, error) {
	state := ObserverState{Version: StateVersion, SessionID: strings.TrimSpace(sessionID)}
	if state.SessionID == "" {
		return state, fmt.Errorf("session_id is required")
	}
	if err := readJSON(s.observerPath(sessionID), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ObserverState{Version: StateVersion, SessionID: state.SessionID}, nil
		}
		return ObserverState{}, err
	}
	if err := validateVersion(state.Version); err != nil {
		return ObserverState{}, err
	}
	return state, nil
}

func (s *Store) UpdateObserver(sessionID string, update func(*ObserverState) error) (ObserverState, error) {
	unlock := s.lock("observer:" + strings.TrimSpace(sessionID))
	defer unlock()
	state, err := s.LoadObserver(sessionID)
	if err != nil {
		return ObserverState{}, err
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = s.now().UTC()
	}
	if err := update(&state); err != nil {
		return ObserverState{}, err
	}
	state.Version = StateVersion
	state.SessionID = strings.TrimSpace(sessionID)
	state.UpdatedAt = s.now().UTC()
	if state.FlowID == "" {
		return ObserverState{}, fmt.Errorf("flow_id is required")
	}
	if err := writeJSONAtomic(s.observerPath(sessionID), state); err != nil {
		return ObserverState{}, err
	}
	return state, nil
}

func (s *Store) AdvanceObserver(sessionID string, event provider.StreamEvent) (ObserverState, error) {
	return s.UpdateObserver(sessionID, func(state *ObserverState) error {
		if eventID := event.EventID(); eventID != "" {
			state.AfterEventID = eventID
		}
		if state.AfterEventID == "" {
			state.AfterEventID = stringValue(event.Data, "current_event_id")
		}
		return nil
	})
}

func (s *Store) ResetObserverCursor(sessionID string) (ObserverState, error) {
	return s.UpdateObserver(sessionID, func(state *ObserverState) error {
		state.AfterEventID = ""
		state.SubscriptionID = ""
		state.NeedsSnapshot = true
		return nil
	})
}

func (s *Store) CloseObserver(sessionID string) (ObserverState, error) {
	return s.UpdateObserver(sessionID, func(state *ObserverState) error {
		state.Closed = true
		state.AfterEventID = ""
		state.SubscriptionID = ""
		return nil
	})
}

func ObserverBackoff(failures int) time.Duration {
	if failures <= 1 {
		return ObserverInitialBackoff
	}
	backoff := ObserverInitialBackoff
	for index := 1; index < failures && backoff < ObserverMaximumBackoff; index++ {
		backoff *= 2
	}
	if backoff > ObserverMaximumBackoff {
		return ObserverMaximumBackoff
	}
	return backoff
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func IsObserverProtocolViolation(event provider.StreamEvent) bool {
	switch event.Type {
	case provider.EventClientTool, provider.EventToolCall, provider.EventBuiltIn, provider.EventMCPTool:
		return true
	}
	return false
}
