package hub

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/stolasapp/chat/internal/match"
	"github.com/stolasapp/chat/internal/view"
)

// idleState tracks the inactivity timers for a client in an active
// chat session. Both timers run in separate goroutines via
// time.AfterFunc and push tokens onto hub channels for processing
// in the run loop. Timer callbacks do not acquire locks or perform
// complex work; if one panics, the process crashes (preferred over
// a hidden deadlock).
type idleState struct {
	warnTimer       *time.Timer
	disconnectTimer *time.Timer
}

// warnAfter returns the duration before the idle warning fires.
func (h *Hub) warnAfter() time.Duration {
	return h.IdleTimeout - h.IdleWarning
}

// newWarnTimer creates a timer that pushes token onto the
// idleWarning channel after warnAfter elapses.
func (h *Hub) newWarnTimer(token match.Token) *time.Timer {
	return time.AfterFunc(h.warnAfter(), func() {
		select {
		case h.idleWarning <- token:
		default:
		}
	})
}

// startIdleTimer creates a warning timer for the given token.
// Stops any existing timer for the same token first. Called when a
// session starts or after reconnect recovery. Only called from the
// run goroutine.
func (h *Hub) startIdleTimer(token match.Token) {
	h.stopIdleTimer(token)
	h.idle[token] = &idleState{
		warnTimer: h.newWarnTimer(token),
	}
}

// resetIdleTimer stops both timers and restarts the warning timer.
// Called on message send and typing indicator. Only called from the
// run goroutine.
func (h *Hub) resetIdleTimer(token match.Token) {
	state, ok := h.idle[token]
	if !ok {
		return
	}
	state.warnTimer.Stop()
	if state.disconnectTimer != nil {
		state.disconnectTimer.Stop()
		state.disconnectTimer = nil
	}
	state.warnTimer = h.newWarnTimer(token)
}

// stopIdleTimer stops both timers and removes the entry. Called
// from endSession to centralize cleanup. Only called from the run
// goroutine.
func (h *Hub) stopIdleTimer(token match.Token) {
	state, ok := h.idle[token]
	if !ok {
		return
	}
	state.warnTimer.Stop()
	if state.disconnectTimer != nil {
		state.disconnectTimer.Stop()
	}
	delete(h.idle, token)
}

// handleIdleWarning sends an inactivity warning to the idle client
// and starts the disconnect timer.
func (h *Hub) handleIdleWarning(ctx context.Context, token match.Token) {
	state, ok := h.idle[token]
	if !ok {
		return
	}
	// guard against stale timer callbacks: if the disconnect timer
	// is already running, this is a duplicate warning delivery
	if state.disconnectTimer != nil {
		return
	}

	client := h.ClientByToken(token)
	if client == nil {
		return
	}

	msg := fmt.Sprintf(
		"You will be disconnected for inactivity in %d seconds.",
		int(h.IdleWarning.Seconds()),
	)
	_ = client.SendComponent(ctx, view.Notify(msg))

	state.disconnectTimer = time.AfterFunc(h.IdleWarning, func() {
		select {
		case h.idleDisconn <- token:
		default:
		}
	})
}

// handleIdleDisconnect ends the session for the idle client and
// notifies the partner.
func (h *Hub) handleIdleDisconnect(ctx context.Context, token match.Token) {
	h.mu.Lock()
	client := h.tokens[token]
	if client == nil {
		h.mu.Unlock()
		return
	}
	partner := h.endSession(token)
	h.mu.Unlock()

	// endSession calls stopIdleTimer for both partners, so the
	// idle map entry is already cleaned up at this point

	if partner != nil {
		_ = partner.SendComponent(ctx, view.SessionEndComponents(view.MsgPartnerLeft, true)...)
	}
	_ = client.SendComponent(ctx, view.SessionEndComponents(view.MsgIdleKicked, true)...)

	slog.Info("client idle disconnected")
}
